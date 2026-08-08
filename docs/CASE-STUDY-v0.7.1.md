# Case study — cuando un cambio de dos strings recorre seis capas de auditoría (v0.7.1)

> Evidencia de ingeniería AI-native. Un frente real del cliente `erickaldama-mail`: endurecer el `Description` de
> una tool que un LLM invoca, para que un modelo local dejara de inventar sintaxis que la tool no soporta. El diff
> de producción fueron **dos strings**. El proceso que los rodeó es la tesis de este documento.

## TL;DR

El agente AI del cliente de correo (`mail ai agent`, backend Ollama local por defecto) inventaba un operador de
campo estilo Gmail — mandaba `query="from:amazon"` a la tool `search_messages`, un operador que la tool no soporta
ni documentaba. La búsqueda FTS5 buscaba el token literal y devolvía 0 resultados. **No era un bug de código** (el
invariante AR-1 ya garantizaba "0 resultados honestos, nunca una respuesta inventada"); era una brecha de
*comportamiento de modelo*: nada en la tool le decía al modelo que usara términos simples.

El fix (v0.7.1, PR #21, merge `b22449f`, 2026-08-06) reescribió el `Description` de la tool y el de su parámetro
`query` — dos literales de string, cero cambio de runtime. Y aun así, ese diff de dos strings pasó por **seis
capas de auditoría**, dos de las cuales cazaron defectos MAJOR que el autor había introducido al redactar. La
lección: un `description` que un LLM lee es **input funcional**, no prosa — y se audita como código.

## El problema, en detalle

`erickaldama-mail` es un cliente de correo terminal-native en Go sobre un backend serverless AWS (SES/S3/Lambda/
DynamoDB, todo provisionado con AWS CDK en Go). Su agente AI tiene una tool de búsqueda, `search_messages`, que
rutea por un cache local SQLite+FTS5 (bm25-ranked, prefix match) con un fallback a substring sobre DynamoDB en
vivo — el invariante **AR-1**: *nunca ciego, nunca mal-disparado* (0 filas sin error es una respuesta válida; el
fallback dispara solo ante un fallo real de infraestructura).

El deploy finding original (v0.7) solo lo pudo revelar un smoke E2E contra un LLM real: `qwen2.5:7b`, al pedírsele
buscar correo por remitente, **inventó** `query="from:amazon"`. FTS5 (que no interpreta `:` como operador cuando el
query va escapado entre comillas) buscó el token literal `from:amazon` → 0 hits. AR-1 se comportó impecable — el
agente reportó honestamente "sin coincidencias", no inventó nada. Pero el usuario no encontró su correo. La causa
no vivía en el código: vivía en cómo un modelo concreto interpreta una `Description`.

## La decisión de diseño (con base, no por defecto)

La deuda registrada ofrecía dos mitigaciones "y/o":

| Approach | Qué | Veredicto |
|---|---|---|
| **A — endurecer el `Description`** | Decirle al modelo, en el texto que lee, que use keywords simples y qué hace la tool con `from:` | **ELEGIDO** |
| **B — strip de operadores en el wire** | Despojar prefijos `from:`/`subject:` de la query antes de buscar | **Diferido** |

Approach A ganó por evidencia, no por ser el más simple: **AR-1 ya garantiza que el peor caso es "0 resultados
honestos"** — el sistema ya es correcto, lo que falta es guiar mejor al modelo. Approach B introduciría un
mini-parser de operadores-para-negar-operadores con un modo de fallo semántico real (mutilar una query legítima
cuyo texto contenga `from:`/`to:`, p.ej. un subject "News from: Berlin") y una cola infinita de operadores por
perseguir (otro modelo inventará `sender:`, `has:`). Cambiar runtime que ya es correcto, para un peor-caso ya
benigno, es mala relación riesgo/beneficio. B quedó como fast-follow **condicionado a evidencia** de un smoke donde
un modelo siga fallando pese al Description — no se blinda contra un fallo no observado como persistente.

Un tercer candidato, `input_examples` (un campo del schema de tools de Claude que muestra ejemplos válidos), se
descartó con fundamento tras verificar el contrato de **ambos** backends contra su doc oficial viva: es un campo
real de Claude, pero **no existe en Ollama**, y el tipo `ToolSpec` es compartido por los dos backends. Añadirlo
sería Claude-only y asimétrico — y el deploy finding ocurrió en Ollama (qwen). Los ejemplos van como prosa dentro
del `Description`, el único lever soportado en todas las rutas.

## Las seis capas de auditoría — y qué cazó cada una

El diff de producción fueron dos strings. Aquí está lo que cada capa vio que las otras, estructuralmente, no
podían ver — la tesis de `layered-audit-catches-distinct-defect-classes` aplicada al texto de una tool:

| Capa | Qué SOLO ella ve | Hallazgo |
|---|---|---|
| **F0 · investigación** (5 agentes vs doc oficial viva: Anthropic, SQLite FTS5, Ollama/Claude tool-schema) | supuestos falsos sobre el contrato del backend | `input_examples` no existe en Ollama → descartado con fundamento |
| **F2 · spec-audit adversarial** (verifica el texto contra el código real) | un char no-ASCII que el compilador acepta pero un consumidor rechaza | **MAJOR: em-dash U+2014** en ambos strings — la clase `downstream-write-api-stricter-than-template-generator` aplicada a un backend LLM |
| **F2 · spec-audit adversarial** (verifica el texto contra la doc del motor) | una afirmación verdadera para el corpus de hoy, FALSA en general | **MAJOR: "matches nothing"** — `EscapeFTS` produce `"from:amazon"*` → frase adyacente `from`+`amazon*`, que FTS5 SÍ matchearía en "Order confirmation **from Amazon**" |
| **F5 · whole-branch** (rama completa, gate estático) | fugas cross-file, superficie del diff | ✅ diff = exactamente 2 strings; `go build/vet/test ./...` verde |
| **F6 · external PR review** (agente que NO construyó, ojos frescos) | el punto ciego del autor recién salido de corregir el hallazgo anterior | **NIT: "anywhere"** — el param decía que los términos matchean "anywhere", pero `EscapeFTS` da una frase ordenada adyacente |
| **Auditoría 7-ejes** (código propio, rigor de owner) | veracidad de cada afirmación contra el mecanismo real | ✅ "prefix on last word" y "adjacent phrase, in order" verificados verdaderos contra `EscapeFTS` |

**Los dos MAJOR los introdujo el autor al redactar.** Ese es el dato nuevo — el "8º fogueo" de la metodología del
proyecto (`docs/METHODOLOGY.md`): las siete corridas anteriores auditaron lógica de código; esta probó que el
mismo aparato es necesario para un artefacto "que no es código ejecutable" — un `description` que un LLM lee y que
altera su comportamiento. Los tres defectos (em-dash, over-claim, under-claim) no son de estilo, son de
corrección: encoding que un consumidor downstream rechaza, y afirmaciones falsas sobre el propio motor.

## El smoke E2E — la única prueba de que funcionó, y un segundo hallazgo gratis

Un `Description` endurecido cambia comportamiento de MODELO — una clase de defecto que ninguna auditoría estática
puede predecir. La única evidencia de que el texto nuevo cambió la conducta fue un smoke E2E contra el modelo real
(`qwen2.5:7b`), instrumentando la tool-call para loguear a stderr el argumento que el modelo enviaba, y revirtiendo
la instrumentación con `git checkout` (nunca commiteada):

- **Resultado primario:** en **4 corridas consecutivas** del mismo goal que antes producía `from:amazon`, el modelo
  mandó `query="amazon"` — keyword limpia. La mitigación funcionó (evidencia de soporte; un modelo pequeño es
  probabilístico, no prueba absoluta).
- **Resultado colateral (gratis):** el agente reportaba "sin coincidencias" pese a haber 5 correos de "Amazon Web
  Services" en el buzón. El smoke destapó un deploy finding **ortogonal e independiente** del cambio de v0.7.1: el
  wire del agente (`cmd/mail/main.go:511`) busca en un solo mailbox, mientras `ls`/`search` caen a todos los del
  config. Confirmado con evidencia binaria (`mail search --json` → los correos viven en un mailbox distinto del
  default). AR-1 se comportó exactamente como se diseñó. Registrado como fast-follow **AGENT-MB-1**, no como
  bloqueante — el objetivo del frente se cumplió; la arquitectura del wire es su propio frente.

Es `smoke-e2e-reveals-architecture-not-just-bugs` en su variante más elegante: el smoke que iba a probar UNA cosa
destapó DOS clases a la vez, y la colateral no contaminó la primaria.

## Lo que se interiorizó (para no repetirlo)

El frente produjo conocimiento reutilizable, persistido para que la próxima iteración no lo re-descubra:

- **Skill nuevo `llm-tool-description-mechanism-not-result`** — tres reglas duras para redactar un tool
  `description`: (1) describe el MECANISMO, no un resultado corpus-dependiente; (2) ASCII puro (viaja al backend
  LLM); (3) verifica la veracidad contra el motor real.
- **Skill extendido `downstream-write-api-stricter-than-template-generator`** — la clase "el consumidor downstream
  valida más estricto que el generador" NO es solo AWS: un em-dash en un tool `description` que viaja a un LLM es
  el mismo defecto que un em-dash rechazado por el regex de AWS SSO. Caso 4.
- **Skill reforzado `smoke-e2e-reveals-architecture-not-just-bugs`** — la variante "el smoke destapa arquitectura
  ortogonal a lo que iba a probar".
- **8º fogueo en `docs/METHODOLOGY.md`** — la capa adversarial caza defectos que el AUTOR introduce al redactar.
- **Primer set de memorias del proyecto** — estado, el hook SP-0, v0.7.1, AGENT-MB-1.

## Por qué esto es evidencia de portafolio

Un cambio de dos strings es la clase de trabajo que "cualquiera aprueba de un vistazo". Este documento es la prueba
de que un vistazo no basta: el diff más pequeño imaginable contenía dos defectos de corrección que solo una
auditoría en capas, con investigación contra doc oficial viva y un smoke contra un modelo real, podía cazar. La
disciplina no escala con el tamaño del diff — escala con las consecuencias, y un tool `description` que un LLM lee
tiene consecuencias en cada request.

Este frente encaja en el ecosistema del repo (ver el README, sección "Ecosystem"): las lecciones generalizables
—las clases de defecto, no los detalles del cliente de correo— están destiladas como skills reutilizables,
candidatas a graduar hacia
[`ai-native-engineering-workspace`](https://github.com/esaldgut/ai-native-engineering-workspace), y son
exactamente la clase de lección durable que [`lessongate`](https://github.com/esaldgut/lessongate) está construido
para detectar y publicar upstream.

## Referencias

- Spec de diseño (con el registro de la auditoría F2 en §10):
  [`docs/superpowers/specs/2026-07-28-search-messages-operator-hardening-design.md`](superpowers/specs/2026-07-28-search-messages-operator-hardening-design.md)
- Plan ejecutable: [`docs/superpowers/plans/2026-07-30-search-messages-operator-hardening.md`](superpowers/plans/2026-07-30-search-messages-operator-hardening.md)
- Runbook v0.7 (deploy finding original §7, nota v0.7.1 §8c): [`docs/v0.7-AI-FTS5.md`](v0.7-AI-FTS5.md)
- Metodología (8º fogueo): [`docs/METHODOLOGY.md`](METHODOLOGY.md)
- Arquitectura (diagrama del flujo del agente AI): [`docs/architecture.md`](architecture.md)
- CHANGELOG: [`CHANGELOG.md`](../CHANGELOG.md) §`[mail v0.7.1]` · ROADMAP: [`docs/ROADMAP.md`](ROADMAP.md)
