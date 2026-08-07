# v0.7.1 — Endurecer el `Description` de `search_messages` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reescribir dos literales de string en `internal/aiassist/tools.go` (el `Description` de la tool
`search_messages` y el `description` de su parámetro `query`) para que el agente AI deje de inventar operadores de
campo estilo Gmail (`from:`/`subject:`), guiando al modelo con texto que describe el MECANISMO de búsqueda; y
limpiar el artefacto de investigación `internal/cache/fts5probe_test.go`.

**Architecture:** Cambio **puramente declarativo, cero runtime**. La query fluye `execTool` → `Searcher` →
FTS/substring; NO se toca ese camino. Solo cambia el texto que ambos backends (Ollama `/api/chat`, Claude
Messages API) entregan al modelo al decidir cómo llamar la tool. El texto endurecido describe el mecanismo
("`from:amazon` se busca como las dos palabras literales `from` y `amazon`"), afirmación verdadera SIEMPRE y
respaldada por la doc oficial de SQLite FTS5 (§3.2 phrases + §4.3.1 unicode61), en vez de un resultado
corpus-dependiente ("matches nothing") que sería falso para un buzón con "from Amazon" adyacente.

**Tech Stack:** Go (stdlib), `internal/aiassist` (tool specs + agent loop), `modernc.org/sqlite` (el cache FTS5,
solo tocado para BORRAR el probe). Backends: Ollama local (`qwen3:32b` default) + Claude API (`claude-opus-4-8`).

**Spec:** `docs/superpowers/specs/2026-07-28-search-messages-operator-hardening-design.md` (auditado F2, §10).

## Global Constraints

- **Restricción operativa del proyecto (SP-0):** el hook `.claude/hooks/cdk-go-guard.sh` DENIEGA `git`/`go` al
  agente por diseño. **TODOS los comandos `git`/`go`/`gh` de este plan los ejecuta el HUMANO** (out-of-band, con
  `!` en el prompt o a mano); el agente redacta los edits y VERIFICA el output binario que el humano pega de vuelta.
  Esto es la disciplina canónica "toda mutación es human-executed", no una limitación a sortear.
- **ASCII puro** en ambos strings nuevos — cero caracteres no-ASCII (NADA de em-dash U+2014). Clase de defecto
  documentada: `downstream-write-api-stricter-than-template-generator` (un `description` con em-dash fue rechazado
  por un API downstream contra su regex ASCII). Verificar con un grep de no-ASCII antes de commitear (Task 1 Step 4).
- **Cero cambio de runtime.** La superficie del diff de producción es EXACTAMENTE dos strings en
  `internal/aiassist/tools.go` (líneas 21 y 23) + el `rm` del probe. NO se toca `search.go`, el wire, ni ningún
  otro archivo. AR-1 (`search.go`, "0 resultados sin error = respuesta válida") queda intacto.
- **No `mutation-test-must-have-teeth`:** no se introduce ningún guard de código nuevo → no hay invariante que
  mutar. La verificación es estática (`go build/vet/test` = gate de merge) + un smoke E2E que ENRUTA (no bloquea).
- **Versión:** `v0.7.1` (verificado libre en F2). Rama: `feature/v0.7.1-search-messages-operator-hardening`.
- **Los 4 tests behaviorales de `search_messages`** (`internal/aiassist/tools_test.go:54-95`) NO tocan el texto
  del Description — deben seguir verdes tras el cambio. NO existe golden test que fije los strings (verificado F2).

---

### Task 1: Reescribir los dos literales de `search_messages` en `tools.go`

**Files:**
- Modify: `internal/aiassist/tools.go:21` (tool `Description`) y `internal/aiassist/tools.go:23` (param `query`
  `description`).
- Test (existentes, NO se modifican): `internal/aiassist/tools_test.go:54-95` (deben seguir verdes).

**Interfaces:**
- Consumes: nada nuevo. El struct `ToolSpec{Name, Description string, Parameters map[string]any}`
  (`internal/aiassist/provider.go:16-20`) es el tipo compartido por ambos backends; solo cambia el CONTENIDO de
  dos strings, no la forma.
- Produces: nada nuevo para tasks posteriores. El texto viaja tal cual a `ollama.go:78`
  (`Description: ts.Description`) y `claude.go:58` (`anthropic.String(s.Description)`).

- [ ] **Step 1: Aplicar el edit del tool `Description` (`tools.go:21`).**

Reemplazar EXACTAMENTE el string actual:
```go
			Description: "Search messages by sender or subject (full-text, ranked best-first, prefix match). Returns matching headers as JSON.",
```
por (ASCII puro, describe el mecanismo — NO un resultado corpus-dependiente):
```go
			Description: "Search messages by sender or subject and return matching headers as JSON, ranked best-first with prefix matching on the last word. Use plain search keywords, for example \"amazon\", \"payment receipt\", or a person's name. This tool does not support field operators like \"from:\" or \"subject:\": a query such as \"from:amazon\" is searched as the two literal words \"from\" and \"amazon\", not as a sender filter, so use plain keywords like \"amazon\" instead. Pass the terms you want to appear anywhere in the sender or subject.",
```

- [ ] **Step 2: Aplicar el edit del param `query` (`tools.go:23`).**

Reemplazar EXACTAMENTE:
```go
					"query": map[string]any{"type": "string", "description": "Search terms (sender or subject)."},
```
por (ASCII puro, framing positivo + mecanismo):
```go
					"query": map[string]any{"type": "string", "description": "Plain search keywords matched against sender and subject, e.g. \"amazon\" or \"invoice march\". Field-operator prefixes such as \"from:\" or \"subject:\" are searched as literal words, not as filters."},
```

- [ ] **Step 3 (HUMANO): compilar + vet + test.**

El agente NO puede correr `go` (hook SP-0). El humano ejecuta y pega el output:
```
go build ./...
go vet ./...
go test -count=1 ./internal/aiassist/...
```
Expected: los tres en VERDE. En particular `TestSearchMessagesUsesSearcher`, `TestSearchMessagesDefaultLimit`,
`TestSearchMessagesSearcherError`, `TestSearchMessagesEmptyReturnsJSONArray` (tools_test.go) PASAN — no tocan el
texto del Description, así que si alguno falla el edit rompió algo que no debía. Si `go build` falla por comillas
mal escapadas, revisar que los `\"` internos estén correctos (son escapes Go válidos dentro de un literal `"..."`).

- [ ] **Step 4 (HUMANO): verificar ASCII puro — sin em-dash ni no-ASCII.**

El humano ejecuta y pega el output (debe ser VACÍO):
```
LC_ALL=C grep -nP '[^\x00-\x7F]' internal/aiassist/tools.go
```
Expected: **sin salida** (exit 1 en grep = ningún match = ASCII puro). Si aparece cualquier línea, hay un char
no-ASCII (probablemente un em-dash `—` que se coló) → corregir a ASCII antes de continuar. Esta es la red contra la
clase `downstream-write-api-stricter-than-template-generator`.

- [ ] **Step 5 (HUMANO): ver el diff — confirmar superficie exacta.**

```
git diff internal/aiassist/tools.go
```
Expected: el diff toca EXACTAMENTE las líneas 21 y 23 (las dos strings), nada más. El agente verifica el diff pegado:
cero cambios fuera de esos dos literales.

- [ ] **Step 6 (HUMANO): commit.**

```
git add internal/aiassist/tools.go
```
```
git commit -m "feat(v0.7.1): endurecer Description de search_messages contra operadores estilo Gmail"
```
(Los comandos van de a uno: el hook SP-0 deniega compound commands con `&&`/`;`.)

---

### Task 2: Eliminar el artefacto de investigación `fts5probe_test.go`

**Files:**
- Delete: `internal/cache/fts5probe_test.go` (diagnóstico F0 sin correr, sin aserciones).

**Interfaces:**
- Consumes/Produces: nada. Es un huérfano de la fase de investigación; su borrado no puede regresar nada (nunca
  se ejecutó, no tiene aserciones, y `internal/cache/` no es tocado por el cambio de v0.7.1).

- [ ] **Step 1 (OPCIONAL, HUMANO): correr el probe UNA vez antes de borrarlo.**

Solo si se quiere cerrar el único punto NO VERIFICADO de la investigación FTS5 (comportamiento de un column-filter
con columna inexistente `from`, sin escaping — moot para la ruta real porque el escaping lo previene):
```
go test -run TestFTS5ColonProbe -v ./internal/cache/
```
El humano pega el output; el agente lo interpreta y lo anota como evidencia de motor real. **Esto es opcional** —
la doc oficial ya cubre el caso de la ruta real; el probe solo cerraría el edge sin-escape.

- [ ] **Step 2 (HUMANO): borrar el archivo.**

```
git rm internal/cache/fts5probe_test.go
```
(Usar `git rm` — el archivo NO está trackeado aún, así que si `git rm` falla con "did not match any files", usar
`rm internal/cache/fts5probe_test.go` a secas.)

- [ ] **Step 3 (HUMANO): confirmar que el paquete cache sigue compilando/testeando sin el probe.**

```
go test -count=1 ./internal/cache/
```
Expected: VERDE. El probe era diagnóstico aislado; su ausencia no afecta la suite del paquete.

- [ ] **Step 4 (HUMANO): commit.**

```
git add -A
```
```
git commit -m "chore(v0.7.1): eliminar fts5probe_test.go (orphan cleanup de la fase F0 de research)"
```

---

### Task 3: Smoke E2E que ENRUTA el fast-follow (NO bloquea el merge)

**Files:**
- Instrumentación TEMPORAL, revertida antes de commitear: `internal/aiassist/tools.go` (el `case
  "search_messages"` de `execTool`, `tools.go:71`).

**Interfaces:**
- Consumes: el binario `mail` construido con el Description endurecido (Task 1).
- Produces: una línea de evidencia (la query verbatim que qwen envió) que se documenta en el runbook. NO produce
  código para tasks posteriores.

> **Rol de este task (corrección F2):** la capa que BLOQUEA el merge es la estática (Task 1 Step 3). Este smoke
> solo OBSERVA si el modelo cambió de comportamiento y ENRUTA el fast-follow Approach B. Ningún outcome del smoke
> bloquea la entrega.

- [ ] **Step 1: Añadir instrumentación temporal a `execTool` (agente edita).**

En el `case "search_messages":` de `internal/aiassist/tools.go` (justo después de extraer `q`), añadir una línea
a stderr que loguee lo que el MODELO envió (antes de cualquier escaping/bifurcación):
```go
		q, _ := call.Args["query"].(string)
		fmt.Fprintf(os.Stderr, "SMOKE-INSTRUMENT search_messages query=%q\n", q) // TEMPORAL — revertir antes de commit
```
(Requiere `os` en imports; `fmt` ya está importado. Si `os` no está, añadirlo temporalmente.)

- [ ] **Step 2 (HUMANO): construir el binario instrumentado.**
```
go build -o /tmp/mail-smoke ./cmd/mail
```
Expected: compila. Si falla por import `os` faltante, añadirlo.

- [ ] **Step 3 (HUMANO): prerrequisitos del smoke — Ollama + cache tibio.**
```
ollama serve
```
```
ollama pull qwen2.5:7b
```
```
/tmp/mail-smoke search --mailbox <mb-real> "example"
```
(El último calienta el cache FTS5. Sustituir `<mb-real>` por un mailbox real de `config.toml`.)

- [ ] **Step 4 (HUMANO): correr el agente con el goal del deploy finding de v0.7.**
```
/tmp/mail-smoke ai agent --agent-model qwen2.5:7b --mailbox <mb-real> "Find any message from a sender containing 'amazon' and tell me the subject"
```
El humano pega el stderr. El agente busca la línea `SMOKE-INSTRUMENT search_messages query=...`.

- [ ] **Step 5: interpretar (regla de decisión explícita — NINGÚN outcome bloquea el merge).**

| Query que envió qwen | Interpretación | Acción |
|---|---|---|
| `query="amazon"` (limpia) | Evidencia de SOPORTE (no prueba absoluta) de que el Description endurecido funcionó | Documentar en runbook; NO se necesita Approach B por ahora |
| `query="from:amazon"` (sigue inventando) | NO es fallo del cambio — AR-1 ya da "0 resultados honestos". SEÑAL que justifica Approach B | Registrar el fast-follow B con esta evidencia; el merge de v0.7.1 procede igual |

- [ ] **Step 6 (HUMANO): revertir la instrumentación — CRÍTICO, nunca commitearla.**
```
git checkout internal/aiassist/tools.go
```
```
git diff internal/aiassist/tools.go
```
Expected del segundo comando: **VACÍO** (el archivo volvió a su estado commiteado en Task 1, con el Description
endurecido pero SIN la línea de instrumentación). El agente confirma diff vacío. Borrar el binario temporal:
```
rm /tmp/mail-smoke
```

---

### Task 4: Bitácora — CHANGELOG + ROADMAP + runbook v0.7

**Files:**
- Modify: `CHANGELOG.md` (nueva entrada `[mail v0.7.1]` al tope, bajo el header, sobre `[mail v0.7]`).
- Modify: `docs/ROADMAP.md` (mover el fast-follow "endurecer Description" de v0.7 a la tabla "Entregado" como
  v0.7.1; dejar registrado el Approach B / `input_examples` como candidatos si el smoke lo justifica).
- Modify: `docs/v0.7-AI-FTS5.md` §8(c) (anotar que la primera de las dos mitigaciones quedó entregada en v0.7.1).

**Interfaces:** documentación; no produce código.

> **Disciplina `git-flow-promotion-and-bitacora-canonical`:** la bitácora se committea INMEDIATO, en su propio
> commit, no se deja sin commitear mientras el PR se prepara.

- [ ] **Step 1: Redactar la entrada de CHANGELOG (agente edita `CHANGELOG.md`).**

Añadir al tope (bajo la línea de intro, sobre `## [mail v0.7]`), narrativa orgánica: qué cambió (dos literales),
por qué (deploy finding §7 de v0.7), la decisión Approach A (no strip), la corrección de la auditoría F2 (mecanismo
no resultado; ASCII puro contra la clase em-dash), y el resultado del smoke (Task 3 Step 5) con la query verbatim.
Fecha: la fecha real del merge.

- [ ] **Step 2: Actualizar `docs/ROADMAP.md` — mover a "Entregado".**

Añadir fila `| v0.7.1 | Endurecer Description de search_messages contra operadores estilo Gmail | <SHA-merge> | <PR#> |`
a la tabla "Entregado"; en "Fast-follows v0.7", marcar la primera mitigación de §8(c) como RESUELTA por v0.7.1 y
dejar Approach B (strip) + `input_examples` como candidatos condicionados a evidencia.

- [ ] **Step 3: Anotar `docs/v0.7-AI-FTS5.md` §8(c).**

Marcar que "endurecer el Description" quedó entregado en v0.7.1; el strip determinístico y `input_examples` siguen
diferidos (con el link al spec de v0.7.1).

- [ ] **Step 4 (HUMANO): commit de la bitácora (su propio commit).**
```
git add CHANGELOG.md docs/ROADMAP.md docs/v0.7-AI-FTS5.md
```
```
git commit -m "docs(v0.7.1): bitácora — CHANGELOG + ROADMAP (fast-follow entregado) + runbook §8(c)"
```

---

### Task 5: Review whole-branch (F5) + PR a develop (F6)

**Files:** ninguno nuevo; revisión del branch completo + apertura del PR.

- [ ] **Step 1: Review whole-branch (agente, ojos frescos sobre el diff completo).**

Con el humano pegando `git diff develop...feature/v0.7.1-search-messages-operator-hardening`, el agente revisa el
diff COMPLETO buscando lo que solo emerge cross-file: que el diff de producción sea EXACTAMENTE los dos strings +
el borrado del probe (sin instrumentación filtrada, sin em-dash, sin tocar `search.go`/wire). Aplica la rúbrica
6-ejes donde tenga tracción (es un cambio declarativo, así que Robustez/Seguridad/Deuda son los ejes vivos).

- [ ] **Step 2 (HUMANO): confirmar el gate estático final en el branch.**
```
go build ./...
```
```
go vet ./...
```
```
go test -count=1 ./...
```
Expected: TODO verde (no solo `./internal/aiassist/...`). Este es el gate de merge.

- [ ] **Step 3 (HUMANO): abrir el PR a develop.**
```
gh pr create --base develop --head feature/v0.7.1-search-messages-operator-hardening --title "feat(v0.7.1): endurecer Description de search_messages contra operadores estilo Gmail" --body "<cuerpo>"
```
El `<cuerpo>` del PR (que el agente redacta) DEBE incluir, como evidencia auditable (`pr-as-auditable-evidence`):
- El diff de superficie exacta (2 strings + rm del probe).
- El registro de la auditoría adversarial F2 (§10 del spec): los 2 MAJOR cazados (em-dash, "matches nothing") y
  cómo se corrigieron.
- El resultado del smoke (Task 3) con la query verbatim que envió qwen.
- La nota "orphan cleanup de la fase F0" para la deleción cross-package del probe (corrección F2-Finding 6).

- [ ] **Step 4: External PR review (F6, agente fresco que NO construyó).**

Tras abrir el PR, una pasada 6-ejes final sobre el `gh pr diff` terminado, con un revisor independiente (modelo
capaz, sin el contexto de haberlo escrito) — la red contra el sesgo de constructor. Los hallazgos van como
comentario al PR.

---

## Self-Review (checklist del autor del plan)

**1. Cobertura del spec:**
- §4.1 (Description) → Task 1 Step 1. ✅
- §4.2 (param query) → Task 1 Step 2. ✅
- §5 (invariantes: AR-1, limit 50, search.go intacto) → verificado por Task 1 Step 5 (diff exacto) + Step 3 (tests). ✅
- §6 (verificación 2 capas: estática = gate, smoke = enruta) → Task 1 Step 3 (estática) + Task 3 (smoke). ✅
- §8 (limpiar probe) → Task 2. ✅
- §10 (auditoría F2, ASCII puro, mecanismo no resultado) → Global Constraints + Task 1 Step 4 (grep no-ASCII). ✅
- Bitácora (git-flow-canonical) → Task 4. ✅
- PR como evidencia + external review → Task 5. ✅

**2. Placeholder scan:** los `<mb-real>`, `<SHA-merge>`, `<PR#>`, `<cuerpo>` son sustituciones de operador claras
(mailbox real, SHA del merge, número de PR, cuerpo redactado en el momento), no TODOs. Sin "TBD"/"implement later".

**3. Consistencia de tipos:** `ToolSpec{Name, Description, Parameters}` (provider.go:16-20) referenciado igual en
Task 1 Interfaces y en el spec §3.3. `execTool` `case "search_messages"` (tools.go:71) es el mismo punto en Task 1
y Task 3. Los strings de Task 1 son byte-idénticos a los del spec §4.1/§4.2 corregido (ASCII puro).

**4. Restricción del ejecutor:** cada Step de `git`/`go`/`gh` está marcado (HUMANO) — coherente con el hook SP-0.
