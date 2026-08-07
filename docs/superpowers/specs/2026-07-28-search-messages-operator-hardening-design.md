# v0.7.1 — Endurecer el `Description` de `search_messages` contra operadores estilo Gmail (spec de diseño)

**Estado:** aprobado, listo para plan.
**Versión asignada:** `v0.7.1` (fast-follow patch de v0.7, análogo a cómo v0.5.1 lo fue de v0.5). Verificado libre
contra `CHANGELOG.md` y `docs/ROADMAP.md` — la última entrada es v0.7 (PR #18), no existe un v0.7.1 tomado.
**Alcance:** cambio puramente declarativo (dos literales de string). **Cero** cambio de comportamiento en runtime.

---

## 1. Motivación — el deploy finding de v0.7 (§7 de `docs/v0.7-AI-FTS5.md`)

El smoke E2E de v0.7 contra un LLM real (`qwen2.5:7b` sobre Ollama) reveló una clase de defecto que ninguna
auditoría estática puede predecir: **comportamiento de modelo**. Al pedirle al agente que buscara correo por
remitente, qwen invocó `search_messages` con `query="from:amazon"` — inventando un operador de campo estilo
Gmail (`from:`) que la tool no soporta ni documenta. FTS5 buscó el token literal → 0 hits.

No fue un bug de código: el invariante **AR-1** ("nunca ciego, nunca mal-disparado") se sostuvo correctamente
(0 resultados **sin error** es una respuesta válida; no hubo fallback espurio; el agente reportó honestamente
"sin coincidencias"). Y re-ejecutar con `query="amazon"` devolvió los 3 hits reales. El código funciona — el
modelo asumió una sintaxis que ninguna instrucción de la tool prometía.

La deuda quedó registrada en `docs/v0.7-AI-FTS5.md` §8(c) y `docs/ROADMAP.md` (fast-follows v0.7) con dos
mitigaciones candidatas. Este frente entrega **la primera**: endurecer el `Description`.

## 2. Decisión de alcance — Description-only (Approach A), NO strip determinístico

Se evaluaron **dos** approaches accionables (**A** = endurecer el `Description`; **B** = strip determinístico de
operadores en el wire); un tercer candidato, `input_examples`, se descartó por infra (§3.3, es Claude-only y el
tipo `ToolSpec` es compartido). Se elige **A** por evidencia, no por defecto — tres argumentos:

1. **AR-1 ya hace el trabajo pesado.** El sistema ya es correcto frente a este input — el peor caso hoy es "0
   resultados honestos", nunca una respuesta incorrecta ni un crash. Lo que falta es guiar mejor al modelo, no
   blindar más el código.
2. **El strip determinístico (Approach B) añade superficie de defecto para matar un síntoma ya contenido.**
   Introduciría un mini-parser de operadores-para-negar-operadores con un modo de fallo semántico real —
   mutilar queries legítimas cuyo texto contenga `from:`/`to:` (p.ej. un subject "News from: Berlin") — y una
   cola infinita de operadores por perseguir (otro modelo inventará `sender:`, `has:`). Cambiar runtime que ya
   es correcto, para un peor-caso que ya es benigno, es mala relación riesgo/beneficio.
3. **A es lo que la deuda §8(c) lista primero**; el strip está explícitamente diferido como alternativa "y/o".

**Approach B queda registrado como fast-follow condicionado** a evidencia de un smoke futuro donde un modelo
siga inventando operadores *a pesar* del Description endurecido — no se blinda contra un fallo no observado
como persistente (disciplina del proyecto).

## 3. Investigación F0 — vara externa recuperada de doc oficial viva

Tres frentes de investigación con evidencia binaria (quote verbatim + URL / `file:line`). Resúmenes:

### 3.1 Anthropic — cómo redactar un tool `description`
Fuente viva: `https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools` (el dominio migró de
`docs.anthropic.com` a `platform.claude.com`). Reglas que aplican, citadas verbatim en la investigación:
- *"Provide extremely detailed descriptions. This is by far the most important factor in tool performance."*
- *"Aim for at least 3-4 sentences for each tool description, more if the tool is complex."*
- La lista requerida incluye *"Any important caveats or limitations"*, y el ejemplo "bueno" canónico cierra
  con un límite negativo (*"It will not provide any other information about the stock or company."*).
- Framing (de `.../prompt-engineering/claude-prompting-best-practices`): *"Tell Claude what to do instead of
  what not to do"* → **positivo primero**; si se incluye el negativo, con su razón.

**Consecuencia de diseño:** el texto lidera con qué SÍ hacer ("usa palabras clave simples"), y el límite
negativo va acompañado de su razón ("se buscan como texto literal, no como filtros").

### 3.2 SQLite FTS5 — qué le pasa de verdad a `from:amazon` / `subject:amazon`
Fuente viva: `https://www.sqlite.org/fts5.html` (§3.1 strings, §3.2 phrases, §3.6 column filters, §4.3.1
unicode61). Hallazgo decisivo, cruzado con el código real (`internal/cache/escape.go`, `internal/cache/query.go`):

- El repo **nunca** manda `from:amazon` crudo. `EscapeFTS` envuelve todo el input entre comillas dobles →
  `"from:amazon"*`. Con las comillas, el `:` queda **dentro de un string literal**, nunca llega al parser de
  column-filter; el tokenizer unicode61 lo trata como **separador** (es puntuación, categoría Unicode Po).
- Resultado: `"from:amazon"*` tokeniza como frase de dos tokens `from` + prefijo `amazon*`, sobre ambas columnas.
- **Crítico:** `subject:amazon` se comporta **idéntico** a `from:amazon` en esta tool — se vuelve `subject` +
  `amazon*`, **NO** un column-filter — precisamente porque el escaping lo mete entre comillas. En FTS5 crudo,
  `subject : amazon` SÍ sería un column-filter válido (`subject` es columna real del índice `fts5(sender,
  subject)`), pero el escaping lo neutraliza.

**Consecuencia de diseño:** la afirmación del Description debe ir **atada a esta tool** ("esta búsqueda trata
`from:`/`subject:` como texto literal"), NO al motor en abstracto — decir "FTS5 no tiene operadores de campo"
sería falso (§3.6 documenta uno real). El texto elegido es verdadero-verificado para esta tool.

### 3.3 Contrato downstream — el `Description` llega al modelo en AMBOS backends
Confirmado contra doc oficial viva + SDK pineado + código real:

- **Ollama** (`POST /api/chat`, `internal/aiassist/ollama/ollama.go:81`): la tool se marshala como
  `{"type":"function","function":{"name","description","parameters"}}` (`ollama.go:34-42,77-78`) — byte-por-byte
  el schema de `https://docs.ollama.com/capabilities/tool-calling` y `github.com/ollama/ollama/blob/main/docs/api.md`.
  `ToolSpec.Description` → `function.description`; `Parameters` (incl. `properties.query.description`) →
  `function.parameters`. El texto entra al prompt vía el nodo `{{ .Tools }}` del chat template (qwen es
  tool-trained). **`input_examples` NO existe en Ollama** (3 fuentes oficiales lo confirman).
- **Claude API** (`v1/messages`, SDK `anthropic-sdk-go v1.51.1`): `ToolSpec.Description` →
  `anthropic.ToolParam.Description` (`internal/aiassist/claude/claude.go:58`) → tool object top-level
  `description`. Per-param descriptions → `input_schema.properties.<prop>.description`.
- **`input_examples`** SÍ es un campo real y actual del schema de Claude (verificado en la página viva + SDK
  `message.go:6650`), PERO `ToolSpec` (`internal/aiassist/provider.go:16-20`) es el **tipo neutral compartido
  por ambos backends** y no tiene campo de ejemplos. Como Ollama —el backend donde ocurrió el finding— no lo
  soporta, `input_examples` sería Claude-only y asimétrico.

**Consecuencia de diseño:** endurecer el `Description` (+ `parameters.query.description`) es el **único lever
soportado en ambas rutas** y no requiere cambios de tipo/plumbing. Los ejemplos van como **prosa** dentro del
texto. `input_examples` se descarta de esta versión (registrado como fast-follow Claude-only, §7).

## 4. El cambio — superficie exacta

Dos literales inline en `internal/aiassist/tools.go`. Confirmado por la investigación interna: **no existe
golden/contract test** que fije estos strings (los 4 tests de `search_messages` en `tools_test.go` asertan solo
comportamiento de `execTool`, nunca el texto), y **no hay convención de strings-catalog** (`list_messages` /
`read_message` son literales inline en el mismo archivo). Reescribir inline es consistente con el patrón del repo.

### 4.1 `tools.go:21` — el `Description` de la tool

**Antes:**
```
"Search messages by sender or subject (full-text, ranked best-first, prefix match). Returns matching headers as JSON."
```

**Después** (positivo → contrato → límite-que-describe-el-MECANISMO, ~4 oraciones, **ASCII puro**):
```
"Search messages by sender or subject and return matching headers as JSON, ranked best-first with prefix matching on the last word. Use plain search keywords, for example \"amazon\", \"payment receipt\", or a person's name. This tool does not support field operators like \"from:\" or \"subject:\": a query such as \"from:amazon\" is searched as the two literal words \"from\" and \"amazon\", not as a sender filter, so use plain keywords like \"amazon\" instead. Pass the terms you want to appear anywhere in the sender or subject."
```

> **Auditoría adversarial F2 — dos correcciones aplicadas a este string** (ambas MAJOR, cazadas por agentes que
> verificaron contra el código y la doc oficial viva; ver §10):
> - **Sin em-dash U+2014.** La versión anterior usaba `—` (U+2014) — un char no-ASCII que viaja a ambos backends y
>   toca la clase de defecto documentada `downstream-write-api-stricter-than-template-generator` (SSO rechazó un
>   `description` con em-dash contra su regex ASCII). Reemplazado por comas/frases ASCII. Un cambio que se declara
>   "cero runtime, puramente declarativo" no debe introducir una dependencia no-ASCII innecesaria.
> - **No afirma un RESULTADO corpus-dependiente ("matches nothing"), describe el MECANISMO.** La versión anterior
>   decía `"from:amazon" matches nothing` — un absoluto FALSO: `EscapeFTS` produce la frase `"from:amazon"*` →
>   tokens `from` + `amazon*`, y FTS5 (§3.2 doc oficial: una frase matchea una sub-secuencia de tokens **adyacentes
>   y en orden**) SÍ matchearía un subject como "Order confirmation **from Amazon**". Era true solo para el corpus
>   del deploy finding (`orders@amazon.com`, donde `@` separa). Hornear un resultado corpus-específico en un string
>   permanente es exactamente la clase de defecto que este frente busca eliminar: una instrucción que la tool no
>   honra siempre. El texto nuevo afirma el mecanismo ("se busca como las dos palabras literales `from` y `amazon`")
>   — verdadero SIEMPRE y respaldado por §3.2/§4.3.1.

### 4.2 `tools.go:23` — el `description` del parámetro `query`

**Antes:**
```
"Search terms (sender or subject)."
```

**Después** (per-param description redundante con el tool-desc — la doc de Anthropic pide describir cada parámetro;
**ASCII puro**, framing positivo + mecanismo):
```
"Plain search keywords matched against sender and subject, e.g. \"amazon\" or \"invoice march\". Field-operator prefixes such as \"from:\" or \"subject:\" are searched as literal words, not as filters."
```
> **Nota F2:** también aquí se quitó el em-dash U+2014. El framing se volvió positivo (describe qué hace con esos
> prefijos) en vez de "Do not use", alineado con la guía de Anthropic "tell Claude what to do instead of what not
> to do" (§3.1). No hay `matches nothing` en este string (el frente B lo confirmó localizado solo al tool-desc).

**No se toca** el `description` de `limit` (`tools.go:24`), ni ningún otro archivo. `search.go`, el wire, y todo
lo demás quedan intactos.

## 5. Invariantes preservados

- **AR-1** ("nunca ciego, nunca mal-disparado") — intacto: no se toca `search.go` ni el wire. El compuesto
  `fallbackSearcher` y su regla (0 resultados sin error = respuesta válida) no cambian.
- **Redacción de `read_message`** (`TestReadMessageRedactsBody`) — intacto: no se toca esa ruta.
- **Default limit = 50** (`tools.go:76`, aserido por `TestSearchMessagesDefaultLimit` en `tools_test.go:74`) —
  intacto: no se toca el número.
- **Cero regresión de comportamiento en runtime** — el cambio solo altera el texto que el modelo lee al decidir
  cómo llamar la tool; la ejecución de `execTool`/`Searcher` es idéntica byte-por-byte.

## 6. Verificación (no `mutation-test-must-have-teeth` — no hay guard nuevo)

Al ser un cambio puramente declarativo **no se introduce ningún guard de código nuevo que mutar**, así que la
disciplina de test-de-mutación-con-dientes no aplica (no hay invariante de código que revertir). La verificación
correcta es de dos capas, con **roles distintos** (corrección F2-Finding 8 — antes el smoke se llamaba "el juez",
lo que sobre-afirmaba una sonda inconclusa-por-diseño):

- **Capa que BLOQUEA EL MERGE — estática:** `go build ./...` + `go vet ./...` + `go test -count=1 ./...` en verde.
  Confirma que el texto nuevo compila (comillas escapadas correctas, ASCII puro) y que los 4 tests behaviorales de
  `search_messages` siguen pasando (no tocan el string, así que deben seguir verdes; si alguno falla, el cambio
  tocó algo que no debía). **Este es el gate del PR.**
- **Capa que ENRUTA el fast-follow (NO bloquea) — smoke E2E real, "la sonda del frente":** re-correr
  `mail ai agent --agent-model qwen2.5:7b --mailbox <mb>` con el mismo goal "buscar correo por remitente" del §7
  de v0.7, **instrumentando la query enviada** a stderr. El punto de instrumentación correcto es el
  `case "search_messages"` de `execTool` (`tools.go:71`), porque ahí llega `call.Args["query"]` — literalmente el
  string que el modelo decidió enviar en la tool-call, antes de cualquier escaping o bifurcación FTS/substring.
  Loguear `query`/`limit`/hits a stderr, y revertir la instrumentación con `git checkout` al terminar — nunca
  commitearla. La observación binaria: ¿qwen sigue mandando `query="from:amazon"`, o ahora manda `query="amazon"`?
  Es la única capa que puede *observar* si el Description endurecido cambió el comportamiento del modelo —
  exactamente la lección de v0.7 (una clase de defecto de comportamiento de modelo solo la revela un LLM real).
  - **Regla de decisión explícita (ningún outcome bloquea el merge; el merge ya lo gatea la capa estática):**
    un modelo pequeño (`qwen2.5:7b`) es probabilístico; una sola corrida **no es prueba**. Si manda la query limpia
    (`amazon`) → evidencia de SOPORTE (no prueba absoluta) de que la mitigación funciona. Si sigue inventando el
    operador (`from:amazon`) pese al Description → NO es un fallo del cambio (AR-1 ya garantiza "0 resultados
    honestos") — es la SEÑAL que justifica escalar al Approach B (strip) en un fast-follow, y así se registra.
    En ambos casos el resultado se documenta con la query verbatim capturada. La sonda enruta trabajo futuro; no
    es un gate PASS/FAIL de esta entrega.

## 7. Fast-follows registrados (no bloquean, condicionados a evidencia)

- **Approach B — strip determinístico de operadores en el wire** — despojar prefijos `from:`/`subject:`/`to:`/
  `cc:` antes de bifurcar a FTS/substring. Diferido; se justifica solo con evidencia de un smoke donde un modelo
  siga inventando operadores a pesar del Description endurecido. Si se implementa: el strip debe vivir **antes**
  de la bifurcación a ambos backends (paridad — la lección de F5 de v0.7), y exige test-con-dientes real.
- **`input_examples` (Claude-only)** — añadir un campo `InputExamples []map[string]any` a `ToolSpec`
  (`provider.go`) + una línea en `toAnthropicTools` (`claude.go`) para pasar ejemplos de queries bien-formadas.
  Es real en el schema de Claude y soportado por el SDK pineado, pero Claude-only (Ollama no tiene el concepto).
  Diferido: asimétrico entre backends, y no ayuda al backend (Ollama) donde ocurrió el finding.

## 8. Limpieza pendiente (de la fase de investigación)

El agente de investigación FTS5 creó un artefacto diagnóstico sin correr: `internal/cache/fts5probe_test.go`.
La disciplina canónica prohíbe dejar instrumentación sin commitear en el árbol. **Debe eliminarse antes del PR**
(`rm internal/cache/fts5probe_test.go`), o —opcional— correrse UNA vez (`go test -run TestFTS5ColonProbe -v
./internal/cache/`) para cerrar el único punto NO VERIFICADO de la investigación FTS5 (comportamiento de un
column-filter con columna inexistente `from` en el motor real, sin escaping — moot para la ruta real de la tool
porque el escaping lo previene) y **luego** borrarse.

**Nota de PR (corrección F2-Finding 6):** este borrado vive en `internal/cache/`, un paquete que el cambio de
v0.7.1 NO toca (el diff real es solo `internal/aiassist/tools.go`). Es un huérfano de la fase de investigación F0,
no de este frente. Etiquetar la eliminación en el cuerpo del PR como **"orphan cleanup de la fase F0 de research"**
para que la deleción cross-package se lea como intencional y no confunda el blast-radius del PR. El archivo es un
diagnóstico sin aserciones que nunca corrió — su borrado no puede regresar nada.

## 9. Referencias

- Deuda que origina este frente: `docs/v0.7-AI-FTS5.md` §7 (deploy finding) + §8(c) (mitigaciones) +
  `docs/ROADMAP.md` (fast-follows v0.7).
- Contrato de la tool: `internal/aiassist/tools.go` (spec + `execTool`), `internal/aiassist/provider.go`
  (`ToolSpec`), `internal/aiassist/ollama/ollama.go` + `internal/aiassist/claude/claude.go` (serialización por
  backend), `internal/aiassist/search.go` (AR-1, intacto).
- Vara externa: `https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools`,
  `https://www.sqlite.org/fts5.html`, `https://docs.ollama.com/capabilities/tool-calling`.

## 10. Auditoría adversarial F2 (registro) — qué cazó y cómo se corrigió

El spec pasó por una **auditoría adversarial de 3 frentes** (agentes que verifican contra el código real + la doc
oficial viva + coherencia interna; cada hallazgo con `file:line`/URL). Registro de lo cazado y aplicado:

| Hallazgo | Frente | Sev | Corrección aplicada |
|---|---|---|---|
| **em-dash U+2014** en ambos strings (§4.1/§4.2) → clase `downstream-write-api-stricter-than-template-generator` | A (vs código) | MAJOR | Reescritos en **ASCII puro** |
| **`"from:amazon" matches nothing`** = absoluto FALSO (FTS5 §3.2: frase = tokens adyacentes; matchearía "from Amazon") | B (vs doc FTS5) | MAJOR | Description reescrito para afirmar el **MECANISMO** (dos tokens literales), no el resultado corpus-dependiente |
| smoke llamado "el juez" pero no bloquea merge | C (coherencia) | MINOR | Re-etiquetado **"la sonda"**; capa estática = gate de merge, smoke solo enruta el fast-follow B (§6) |
| "tres approaches" (solo hay dos accionables) | C | MINOR | Corregido a "dos"; `input_examples` explicado como descarte por infra (§2) |
| `rm` del probe acopla paquete no-tocado | C | MINOR | Etiquetar como "orphan cleanup F0" en el PR body (§8) |
| cita "3-4 sentences" (hyphen ASCII vs en-dash del source) | B | MINOR | Cosmético — la cita conserva el sentido; no se altera |
| asimetría `from`(no-columna)/`subject`(columna real) sin-escape | B | MINOR | No cambia conclusión (escaping neutraliza ambos); documentado aquí |

**Verificado TRUE por los tres frentes (no defectos):** `v0.7.1` genuinamente libre (CHANGELOG/ROADMAP/go.mod/refs
limpios, precedente estructural v0.5.1); NO existe golden/contract test que fije los strings (rewording no rompe la
suite); superficie del cambio exactamente `tools.go:21` + `tools.go:23`; AR-1 intacto (`search.go` sin tocar); el
claim sutil (c) —"decir 'FTS5 no tiene operadores de campo' sería falso porque §3.6 documenta un column-filter
real"— es **correcto**; default limit=50 sin tocar. **Cero CRIT, cero merge-blocker.** Los dos MAJOR eran defectos
que YO introduje al redactar y que ninguna auto-revisión inline vio — la evidencia de por qué la capa adversarial
caza una clase distinta de defecto (el punto ciego del autor), consistente con `layered-audit-catches-distinct-
defect-classes`.
