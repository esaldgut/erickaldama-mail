# Metodología — cómo se construyó este sistema

Este documento explica **cómo** se construyó el sistema de correo `erickaldama.com`, no solo qué. Cada subproyecto
(SP-0 … SP-4, y el CD) siguió el mismo flujo de fases gated. Es la parte del repo que demuestra el *proceso* de
ingeniería — la metodología es tan parte del entregable como el código.

> El sistema completo está vivo y verificado end-to-end: un correo enviado por el cliente Go recorre
> SES → S3 → Lambda → DynamoDB y se lee de vuelta. Ver [CHANGELOG.md](../CHANGELOG.md) y los runbooks
> `docs/SP-*-DEPLOY.md`.

---

## El flujo de un subproyecto (6 fases, cada una un gate)

```
F0 Investigación → F1 Brainstorm → F2 Spec+Audit → F3 Plan+Audit → F4 Build+6ejes → F5 Review+Deploy+Smoke → F6 Evidencia+PR
   (doc oficial      (worktree       (auditoría      (plan-audit:    (subagente/      (whole-branch       (runbook +
    viva, citar       aislado,        adversarial     gaps, bugs,     tarea +          + gate humano       CHANGELOG +
    verbatim,         decisiones      que COMPILA     base)          auditoría        + smoke REAL →      IAM JSON +
    NO VERIFICADO)    cerradas)       vs libs vivas)                 6-ejes/tarea)    deploy findings)    memoria + PR)
```

### F0 — Investigación antes de implementar
Cuando hay un patrón nuevo (primera cuenta SES, primer OIDC) o una API que no se domina: agentes investigan contra
**documentación oficial viva**, citan verbatim con URL, y marcan **NO VERIFICADO** lo que la doc no cubre. Los
hechos + los gaps se consolidan en un documento durable. Esto ancla el diseño en realidad, no en supuestos.

### F1 — Worktree aislado + brainstorm
Cada subproyecto vive en un worktree git aislado (protege la rama base). Brainstorm guiado: las decisiones de
alcance se cierran una a una, con la investigación como ancla. Hard gate: no se implementa nada sin diseño aprobado.

### F2 — Spec + auditoría adversarial (compilando)
El spec formal pasa por una **auditoría adversarial**: 3-4 agentes que verifican contra doc oficial Y **compilan el
código propuesto contra las librerías en sus versiones reales**. El objetivo es cazar las trampas que rompen el
deploy ANTES de tocar AWS — firmas de API inventadas, una API no soportada en el contexto asumido, un recurso que
el IAM no cubre.

### F3 — Plan ejecutable + su auditoría
El plan (tareas bite-sized, TDD, código completo) se audita TAMBIÉN — el plan-audit caza lo que el spec-audit no
ve: promesas del spec sin tarea, bugs en el código del plan, bloqueantes de base.

### F4 — Subagent-driven-development con auditoría 6-ejes por tarea
Subagente fresco por tarea. Por cada una: implementer (TDD) → **el reporte se valida contra el código real**
(`go test -count=1` + `vet` + `build` + `go mod verify` — los reportes mienten) → spec + quality review →
**auditoría de 6 ejes** (robustez · eficiencia · patrones de diseño · mantenibilidad · seguridad · deuda técnica,
cada hallazgo con evidencia `archivo:línea`) como gate → aplicar hallazgos → marcar completa. Cualquier eje puede
bloquear el merge.

### F5 — Review final whole-branch + deploy + smoke real
La rama completa se revisa junta (para lo que solo emerge cross-package). Las mutaciones a AWS las ejecuta el
humano out-of-band (SSO Admin); el agente verifica post-deploy con menor privilegio empírico. **El smoke
end-to-end real** contra el sistema desplegado es donde aparecen los **deploy findings** — los hallazgos que ningún
code review puede cazar porque viven en el boundary/la cuenta/el config real.

### F6 — Evidencia en repo + PR + review final consolidado
Runbook con los deploy findings (causa raíz + fix), CHANGELOG orgánico, los JSON de IAM auditables en `iam/`,
memoria de patrones canónicos, y el PR a develop con CI verde (Git Flow). Los artefactos revisables van al PR como
comentarios.

Tras abrir el PR, una **pasada de auditoría 6-ejes final sobre el PR ya en GitHub, con un revisor independiente**
(agente fresco, modelo más capaz, que NO construyó el código). Es distinta del review de F5: F5 mira la rama en el
worktree *durante* la construcción y caza lo cross-package; este mira el `gh pr diff` *terminado* como lo vería un
revisor externo, y elimina el **sesgo de constructor** — la última red antes del merge la pone alguien sin el contexto
de haberlo escrito. Las cinco capas de auditoría del flujo (spec-audit, plan-audit, 6-ejes por tarea, whole-branch F5,
y este review final sobre el PR) no son redundantes: cada una caza una clase distinta de defecto — una API inventada,
un bug en el código del plan, un defecto por tarea, una incoherencia cross-package, y el sesgo de quien lo construyó.

---

## Por qué los "deploy findings" son la parte más valiosa

Un diagrama de arquitectura bonito puede describir algo que nunca tocó AWS. Lo que prueba que un sistema se desplegó
**de verdad** son los hallazgos que solo aparecen al desplegar — porque viven en capas que el código no ve:

| Subproyecto | Deploy finding real | Lección |
|---|---|---|
| SP-1 | El permissions boundary INTERSECTA la exec-policy (no une); `ssm:GetParameters` debía estar en ambas | El boundary es una 2ª compuerta, no un duplicado |
| SP-2 | El boundary también necesita `events:*`, no solo la exec-policy | Al añadir un servicio nuevo, tocar AMBAS capas |
| SP-3 | El read-only del agente no podía leer logs Lambda ni el estado de la suscripción SNS | La observabilidad es parte del límite, no un extra |
| SP-4 | El boundary DENIEGA `iam:CreateUser` (anti-escalación); la policy de envío necesitaba el config-set ARN; una access key impresa en chat → rotación segura | El primer recurso de un tipo nuevo topa con denies deliberados; SES aplica el default config-set a todo send |

Cada uno se documentó con su causa raíz y su fix en el runbook correspondiente. Eso es ingeniería real, no una foto.

---

## La auditoría en capas caza clases DISTINTAS de defecto (caso v0.6, MERGEADA PR #16)

Los deploy findings prueban el valor de las capas en infra. **v0.6 (render de enlaces del correo) probó el mismo
principio en código puro**, sin AWS de por medio: 8 clases distintas de defecto, cada una cazada por una capa de
review diferente e **invisible a las demás**. No es redundancia — es que cada clase de defecto requiere un punto de
observación distinto. Es la mejor demostración del proyecto de por qué las capas no se pueden colapsar:

| Capa de review | Qué SOLO ella puede ver | Hallazgo v0.6 |
|---|---|---|
| **Spec-audit** (F2, compila contra libs vivas) | supuestos FALSOS sobre el provider/API | "mailto no sobrevive bluemonday" (era falso; sí sobrevive en `<a href>`) |
| **Plan-audit** (F3, audita el código del plan) | bugs en el código concreto que el plan manda escribir | `strings.Fields` colapsaba la estructura del cuerpo (8 newlines → 0) |
| **6-ejes por-tarea** (F4) | defectos locales de una unidad | **O(n²) DoS** en el scan de sentinel; **test de mutación tautológico** |
| **Whole-branch** (F5) | defectos de **COMPOSICIÓN** | **forja OSC 8**: fixes por-tarea correctos cuya interacción abría el vector `ESC]` (`sanitizeTerminalOutput` preserva OSC por diseño → no distingue la forjada) |
| **External PR review** (F6, agente que NO construyó) | el **punto ciego del constructor** | **sink de plain-text**: `TextHTML=="" → return TextPlain` se saltaba todo el saneo — el constructor no lo vio porque "mi sanitizer es el paso final" |

Los dos hallazgos más caros — la forja OSC 8 (composición, F5) y el sink de plain-text (punto ciego, F6) — habrían
llegado a producción sin su capa específica, porque **ninguna otra capa podía verlos**. El per-task review de la
tarea del materializador no podía ver la forja porque solo miraba su tarea; el constructor no podía ver el sink
porque su modelo mental se lo ocultaba. No es que un reviewer fuera flojo: cada clase de defecto vive en una capa
distinta. Skills reutilizables derivados: `layered-audit-catches-distinct-defect-classes` y `mutation-test-must-have-teeth`.

**Sub-lección — validar, no confiar TAMBIÉN al revisor.** El reviewer que caza un defecto puede prescribir un fix
sutilmente falso. En v0.6, DOS prescripciones lo eran: `collapseInternalWrap` (F5) re-unía el wrap de glamour →
overflow; y `sanitizeTerminalOutput(TextPlain)` (F6) preserva secuencias OSC/CSI → dejaba pasar el ataque (el
correcto era el strip agresivo `stripDangerousControls`, porque en el plain-path no hay ANSI legítimo que
preservar). Ambas se cazaron **ejecutando el fix propuesto** antes de aceptarlo.

**Sub-lección — un test de mutación de seguridad DEBE tener dientes.** `TestMutationForgedSentinelStripped` decía
cubrir el strip de sentinel forjado, pero su input (`"xy"`) no contenía ningún sentinel forjado → pasaba con el
guard revertido. Un test tautológico da confianza falsa, peor que no tener test. La regla: verificar SIEMPRE con
un experimento de mutación (revertir el guard → el test debe FALLAR → `git checkout` restaurar → confirmar `git
diff` vacío y `hash-object` == el blob de HEAD, para nunca commitear un guard revertido).

---

## La auditoría en capas, 7º fogueo (caso v0.7, MERGEADA PR #18) — dos hallazgos nuevos que el patrón aún no tenía

v0.7 (FTS5 en las tools del agente AI) es el **séptimo fogueo** de la misma metodología, y no se limita a repetir
la lección de v0.6 — **la extiende con dos datos nuevos y potentes** que ninguna corrida anterior había producido:
el auditor de una capa puede él mismo prescribir un fix equivocado, y una clase entera de defecto — el
comportamiento de un modelo — solo la revela un smoke E2E contra un LLM real, nunca una auditoría estática.

| Capa de review | Qué SOLO ella puede ver | Hallazgo v0.7 |
|---|---|---|
| **Plan-audit** (F3, audita el código del plan) | bugs en el código concreto que el plan manda escribir | **CRIT shadowing**: el parámetro `mailbox` sombreaba el paquete `mailbox`, rompiendo `mailbox.Header` dentro del método; **MAYOR** overflow de `int32(limit)` si el LLM manda un `limit` enorme sin clamp |
| **6-ejes por-tarea** (F4, el wire) | defectos locales de una unidad, incluido un fix que asume mal el propio estado del sistema | **MAYOR H8-SYNC-N-1**: `cache.Sync` devuelve `len(hs)` (filas traídas de DynamoDB en ESTA llamada), no el tamaño del índice. El guard de cold-start (`serr != nil && n == 0 → substring`) descartaba un índice FTS tibio y usable cuando un `r.List` fallaba en una invocación posterior a una sincronización exitosa. Fix: probar el estado REAL del índice con `ca.List(mb, 1)` |
| **Whole-branch** (F5, ojos frescos) | defectos de **composición/contrato** entre piezas que cada una por sí sola pasa su propio review | **IMPORTANT cross-package**: los dos backends de `Searcher` no coincidían en qué significa "buscar" — FTS indexa sender+subject (y la tool promete "sender or subject"), pero `substringSearcher` matcheaba solo Subject (nunca From); y una query vacía hacía que FTS no matcheara nada mientras substring matcheaba TODO. Raíz: el rename `search_subject`→`search_messages` amplió el contrato, solo el lado FTS lo cumplía |
| **External PR review** (F6, agente que NO construyó) | el punto ciego del constructor, y la fragilidad de sus propios tests | **NIT-1**: el guard de query vacía comparaba `== ""` exacto, así que una query solo-espacios se colaba. Fix con `strings.TrimSpace` — y el test que en teoría lo cubría era TAUTOLÓGICO (los subjects de fixture no tenían espacios, pasaba con o sin el guard); se reconstruyó con subjects que sí los tienen |

Las capas siguen cazando clases distintas — nada de esto es nuevo como *patrón*. Lo nuevo son las dos sub-lecciones
que siguen, cada una con evidencia que v0.6 no había producido.

**Sub-lección — "validar, no confiar" aplica también al AUDITOR de la capa, no solo al revisor externo.** En v0.6
la lección fue que un *revisor* (F5/F6) puede prescribir un fix sutilmente falso. v0.7 la extiende un nivel más
adentro: en F4, el mismo agente de auditoría 6-ejes que **encontró** H8-SYNC-N-1 también **propuso** un fix —
"fail-open a FTS ante cualquier error de sync". Ejecutar esa sugerencia contra el código real mostró que
reintroducía exactamente la ceguera de cold-start que el guard original existía para prevenir: un índice
genuinamente vacío (primer arranque) más un sync fallido habría dejado un índice FTS vacío, y el agente AI vería
"sin coincidencias" pese a que sí hay correo en DynamoDB. El fix correcto — probar el índice real con
`ca.List(mb, 1)` en vez de confiar en `n` — salió de **rechazar la propia sugerencia del auditor**. La lección: el
agente que ENCUENTRA un defecto no es automáticamente el que tiene razón sobre el FIX. Verificar la corrección con
el mismo rigor que el hallazgo, incluso cuando es tu propio agente de auditoría el que la propone.

**Sub-lección — el smoke E2E con un LLM REAL caza una clase de defecto que ninguna auditoría estática puede ver:
comportamiento de modelo.** El deploy finding: `qwen2.5:7b`, al pedírsele buscar correo por remitente, **inventó**
un operador de campo estilo Gmail — mandó `query="from:amazon"` a `search_messages`. FTS5 buscó el token literal
`"from:amazon"` → 0 hits. El invariante **AR-1** ("nunca ciego, nunca mal-disparado") se sostuvo correctamente: 0
resultados sin error es una respuesta válida, no hubo fallback espurio a substring, y el agente reportó
honestamente "sin coincidencias" en vez de inventar una respuesta. Y esto NO es un bug de código: re-ejecutando el
mismo goal con `query="amazon"` (sin el prefijo inventado) el mismo índice devolvió 3 hits correctos. Es una clase
de defecto distinta — "el LLM usa la tool de una forma que la tool no soporta" — que ningún spec-audit, plan-audit
ni code review puede predecir, porque no vive en el código: vive en cómo un modelo concreto interpreta una
`Description`. Solo un smoke E2E real contra un modelo real la revela. Técnica de instrumentación usada: loguear a
stderr los argumentos reales de la tool-call (`query`/`limit`/hits devueltos) durante el smoke, y revertir esa
instrumentación con `git checkout` al terminar — nunca comitearla.

---

## Disciplinas transversales

- **Validar, no confiar** — cada reporte de subagente (y cada prescripción de fix de un revisor) se verifica
  contra el código/sistema real, ejecutándolo, antes de aceptarlo.
- **Un test de mutación de seguridad DEBE tener dientes** — verificar SIEMPRE con un experimento de mutación
  (revertir el guard → el test debe FALLAR → `git checkout` restaurar → confirmar hash == HEAD). Un test
  tautológico da confianza falsa, peor que ninguno.
- **Gate humano out-of-band** — el agente nunca ejecuta writes a sistemas reales; entrega los comandos exactos.
- **Menor privilegio verificado empíricamente** — no se asume; se prueba (`mail-client-read` puede leer pero NO
  enviar → AccessDenied real).
- **Secrets nunca en chat/binario/git/logs** — si una credencial toca un canal observable, se revoca y rota
  (patrón `create-access-key` capturado a variable + `aws configure set`, la secret nunca se imprime).
- **NDA / repo público** — gate sobre todo el output antes de exponer.

---

## La metodología es reutilizable

Este flujo está codificado como skills de agente reutilizables (`subproject-delivery-canonical` orquesta las fases;
`pr-as-auditable-evidence` y `engineering-audit-6-axes` son piezas). Este documento es la vitrina pública de esa
metodología — la fuente vive en la configuración del agente, esta página la hace legible para quien lee el repo.
