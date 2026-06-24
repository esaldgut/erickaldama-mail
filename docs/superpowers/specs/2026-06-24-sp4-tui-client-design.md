# SP-4 — Cliente TUI/CLI/AI Go para erickaldama.com — Design Spec

> **Subproyecto:** SP-4 (tarea #18). Cierra el lazo end-to-end del sistema de correo: el primer
> componente que **consume** (no aprovisiona) la infra de SP-1/2/3.
> **Worktree:** `sp-4-tui-client` (rama `worktree-sp-4-tui-client`, base `d9c5a0b` = HEAD de develop).
> **Fecha:** 2026-06-24. **Cuenta:** ErickSA `367707589526` / `us-east-1`.

---

## 1. Objetivo

Un cliente de correo **terminal-native en Go** para `erickaldama.com` que **lee** el correo recibido
(DynamoDB `mail-index` + cuerpos MIME en S3 `erickaldama-mail-raw`) y **envía/responde** vía SES
(`SendRawEmail`), con un **agente AI de doble backend** (Ollama local + API de Claude) que asiste
lectura (resumen/triage) y composición de respuestas (draft). Integrado al flujo keyboard-first del
usuario (Vim-motions, tmux, nvim). Publicable en `erickaldama.com` como **evidencia de portafolio de
AI-agents**.

**Por qué importa:** SP-1/2/3 construyeron el backend (DNS, envío, recepción). SP-4 es el producto que
un humano usa. Y el agente AI de doble backend es la pieza diferenciadora: demuestra ingeniería de
AI-agents real (loop propio, tool-use, dos providers intercambiables, postura de privacidad por diseño),
no una llamada-a-librería.

**Premisa de gobernanza (crítica):** SP-4 es un **cliente runtime**, NO aprovisiona infra. El hook
PreToolUse de CDK-Go **no aplica al código del cliente** (no hay `cdk deploy` en runtime; usa el AWS SDK
Go v2 directo). La **única** pieza de infra de SP-4 es un mini-cambio CDK-Go previo (Task 0: el user
`mail-sender`), que el humano deploya out-of-band como en SP-1/2/3.

---

## 2. Ground truth verificado (no asumido)

Datos extraídos del sistema y de AWS el 2026-06-24, no de supuestos:

| Hecho | Valor verificado |
|---|---|
| Go toolchain | `go 1.26.4` (go.mod del repo) |
| AWS SDK Go v2 ya en go.sum | `aws-sdk-go-v2 v1.42.0`, `service/dynamodb v1.59.0`, `service/s3 v1.104.0`, `config v1.32.25`, `credentials v1.19.24`, `feature/dynamodb/attributevalue v1.20.48` (indirectas hoy → directas en SP-4) |
| Deps SES a AÑADIR (binario nuevo, NO hereda del lambda; A7) | `service/ses v1.35.2` (envío SendRawEmail) + `service/sesv2 v1.62.4` (DetectSandbox vía GetAccount). Versiones verificadas vivas 2026-06-24 |
| Schema `mail-index` (fuente de verdad) | `cmd/lambda/receive/main.go`: `PK="mailbox#<addr-lower>"`, `SK="ts#<RFC3339-UTC>#<RFC5322-MessageID>"`, attrs `messageId, s3Key, from, subject, date` |
| S3 key del cuerpo | `inbound/<Mail.MessageID>` (SES id). El cuerpo MIME crudo NO está en DynamoDB |
| `mail-readonly` (gobernanza) | User `arn:aws:iam::367707589526:user/mail-readonly`, profile en `~/.aws/config`. **DENIEGA explícitamente `s3:GetObject`** (Sid `HardDenyMutationReconAndCredentialMinting`, `foundation_stack.go:90-95`) + `ses:Send*`/`sts:AssumeRole*`/`iam:*`. Diseñado como verificador puro: lee **metadata**, NO contenido. **NO sirve como principal de lectura del cliente** (el cliente debe leer cuerpos S3) |
| Envío | policy `mail-send` (solo `ses:SendEmail/SendRawEmail`, identity `erickaldama.com`, Condition `From=erick@erickaldama.com`) attachada a **role** `mail-sender-role` (asumible por `:root`). NO hay user/profile que envíe hoy |
| Principales del cliente (Task 0) | NO existen aún → se crean en CDK: `mail-client-read` (dynamodb:Query/GetItem `mail-index` + s3:GetObject `inbound/*`) y `mail-sender` (policy `mail-send` directa) |
| SES estado | **SANDBOX** (`ProductionAccessEnabled=false`), `SendingEnabled=true`, 200/24h, 1/s. Identity `erickaldama.com` verificada para envío (SP-2) |
| Ollama | instalado `0.30.10`, modelo `llama3.2` descargado (store 1.9G). Daemon on-demand en `:11434` (no corre por default) |
| Claude API | modelo objetivo `claude-opus-4-8`, adaptive thinking. SDK `anthropic-sdk-go` |
| tmux | `prefix=C-a`. Teclas `e/E/m/M` libres. NO usa `display-popup`. Plugins: tpm, sensible, resurrect, continuum (no reclaman teclas) |
| nvim | `<leader>=\`. `<leader>m`/`<leader>M` libres. `<leader>e` (LSP diag) y `<leader>c` (code actions) ocupados → evitar. which-key descubre grupos dinámicamente |

---

## 3. Arquitectura

**Principio rector:** núcleo de dominio sin UI; TUI, CLI y el agente AI son adaptadores delgados que lo
consumen por igual. Ningún frontend reimplementa lógica; ninguno acopla el dominio a su framework.

```
┌─────────────────────────────────────────────────────────────────┐
│  FRONTENDS (delgados, intercambiables)                            │
│   cmd/mail (CLI Cobra)    cmd/mail-tui (Bubble Tea)   tmux glue    │
│   ls·read·send·reply·ai      pantalla completa,        popup/      │
│   (stdout componible)        Vim-motions, glamour      status      │
└───────────────┬───────────────────┬──────────────────────────────┘
                ▼                    ▼
┌─────────────────────────────────────────────────────────────────┐
│  NÚCLEO DE DOMINIO (internal/, sin dependencia de UI)             │
│   mailbox/  → Reader (DynamoDB Query + S3 GetObject)              │
│             → Sender (MIME build + SES SendRawEmail)              │
│   message/  → parse MIME (enmime), threading, render (glamour)    │
│   aiassist/ → LLMProvider interface + agent loop + tools          │
│       ├─ ollama/  (local, DEFAULT seguro — el cuerpo no sale)     │
│       └─ claude/  (API Opus 4.8, opt-in explícito)                │
│   awsconf/  → carga profiles read/send, sandbox detection         │
└───────────────┬───────────────────────────────────┬─────────────┘
                ▼                                     ▼
        AWS (us-east-1)                         LLM backends
   DynamoDB mail-index (mail-client-read)   Ollama :11434 (local)
   S3 erickaldama-mail-raw (mail-client-read) api.anthropic.com (opt-in)
   SES SendRawEmail (mail-sender)
```

**Tres propiedades clave:**
1. **Frontends delgados, núcleo gordo** — toda la lógica vive en `internal/`, testeable sin terminal/red.
2. **Dos planos de lectura** — listar = DynamoDB Query (barato); abrir = S3 GetObject + parse (on-demand).
3. **El AI es un componente del núcleo, no un frontend** — CLI y TUI invocan las mismas funciones de `aiassist`.

---

## 4. Componentes del núcleo (`internal/`)

### 4.1 `internal/mailbox/` — plano de datos

**`Reader`** (profile `mail-client-read` — NO `mail-readonly`, que deniega `s3:GetObject`):
- `List(ctx, mailbox string, n int, cursor PageToken) ([]Header, PageToken, error)` — DynamoDB Query:
  `KeyConditionExpression "PK = :pk"`, `ScanIndexForward: aws.Bool(false)` (es `*bool`; SK descendente = recientes
  primero), `Limit *int32`, paginación por `LastEvaluatedKey`. Mapea con `attributevalue.UnmarshalListOfMaps`. NO toca S3.
- `Open(ctx, s3Key string) (io.ReadCloser, error)` — S3 GetObject del MIME crudo (sin parsear). **El consumidor
  (`message.Parse`) DEBE `defer body.Close()`** — no es explícito en la doc del SDK pero es obligatorio (leak si la TUI abre muchos, A3).

`Header` espeja el schema exacto del handler (`PK,SK,messageId,s3Key,from,subject,date`).

**`Sender`** (profile `mail-sender`):
- `Send(ctx, raw []byte) (messageID string, error)` — SES v1 **`SendRawEmail`** (`service/ses`, NO sesv2 — sesv2 no
  tiene SendRawEmail; A1): `SendRawEmailInput{RawMessage:&types.RawMessage{Data: raw}}`. El MIME lo arma `message.Build`.
- `DetectSandbox(ctx) (bool, error)` — **vive aquí o en awsconf; usa `sesv2.GetAccount`** (`ProductionAccessEnabled`
  NO existe en SES v1; A5). Lee `out.ProductionAccessEnabled == false`. Requiere dep `service/sesv2` solo para esto.

Ambos clients AWS detrás de **interfaces mínimas** (las ops que se usan) para testear con fakes.

### 4.2 `internal/message/` — MIME (puro, sin red, máxima densidad de tests)

- **Parse** (`github.com/jhillyerd/enmime/v2` — v2, go directive 1.25.0, compatible con Go 1.26): `enmime.ReadEnvelope(r)` → `Parsed{Headers, TextPlain (env.Text), TextHTML (env.HTML), Attachments (env.Attachments []*Part)}`. Decodifica quoted-printable/base64/charsets automáticamente (verificado C1).
- **Render** (C4 — corrección): glamour renderiza **markdown, NO HTML**. Pipeline TUI enriquecido:
  `env.HTML → html-to-markdown (github.com/JohannesKaufmann/html-to-markdown) → glamour.Render → ANSI`.
  El CLI usa `env.Text` directo (enmime ya hace HTML→texto-plano cuando solo hay HTML; sin lib extra para el CLI).
- **Build** (C2/C6 — corrección): construir saliente con **`enmime.Builder()`** (mismo paquete), NO MIME a mano.
  `Builder().From().To().Subject().Text().AddFileAttachment().Header("In-Reply-To",…).Header("References",…).Header("Message-ID",…).Build()`.
  **Message-ID se genera a mano** (ni stdlib ni el Builder lo crean): `<unixnano.randhex@erickaldama.com>`, pasado vía `Header()`.
- **ReplyHeaders** (función pura): dado el **mensaje PARSEADO del original** (no solo el `Header` de DynamoDB — el
  `References` completo del hilo vive en los headers del MIME en S3, C3), devuelve `{InReplyTo: <Message-ID del padre>,
  References: <References-del-padre> + <Message-ID-del-padre>, Subject:"Re: …"}` para threading RFC 5322 §3.6.4 + cita.

### 4.3 `internal/awsconf/` — credenciales y entorno

- Carga dos `aws.Config` segregados: `--read-profile` (default `mail-client-read`), `--send-profile`
  (default `mail-sender`). El client de lectura físicamente no porta credenciales de envío.
- `DetectSandbox(ctx) (bool, error)` para avisos proactivos antes de enviar.
- Región fija `us-east-1` (constante).

### 4.4 `internal/aiassist/` — el agente con doble backend

**Interfaz neutral** (abstrae el backend; dos implementaciones intercambiables):
```go
type LLMProvider interface {
    Chat(ctx, msgs []Message, tools []ToolSpec) (Response, error) // texto O tool-calls
    Name() string // "ollama:llama3.2" | "claude:claude-opus-4-8"
}
```
- **`ollama/`** → HTTP directo a `http://localhost:11434/api/chat` (cliente `net/http` propio; no hay SDK Go oficial necesario). DEFAULT seguro: el cuerpo del correo nunca sale del Mac.
  - **Forma verificada de la API (auditoría B3)**: tools con wrapper `{"type":"function","function":{name,description,parameters}}`; `stream:false` explícito (default es streaming); en la respuesta `message.tool_calls[].function.arguments` es un **objeto JSON** (deserializar a `map[string]any`/`json.RawMessage`, NUNCA como string); el tool result se devuelve como `{"role":"tool","tool_name":<name>,"content":<string>}` — **Ollama NO usa tool_call_id**, correlaciona por `tool_name`+orden.
- **`claude/`** → SDK `github.com/anthropics/anthropic-sdk-go` (pin `v1.51.x`), modelo `claude-opus-4-8`, adaptive thinking. Opt-in explícito.
  - **Verificado (B1/B2)**: `Tools` es `[]anthropic.ToolUnionParam` (envolver cada tool con `OfTool`), no `[]ToolParam`. Adaptive vía `ThinkingConfigParamUnion{OfAdaptive:…}` (sin helper). **NO** setear `Temperature`/`TopP`/`budget_tokens` (Opus 4.8 → 400). Key desde `ANTHROPIC_API_KEY` o `option.WithAPIKey(keyFromKeychain)`.

**Modelos por capacidad (auditoría B4 + investigación DeepSeek 2026-06-24 — corrección crítica):** `llama3.2` NO
figura en la lista oficial de tool-support de Ollama y arrastra bugs de fiabilidad de tool-calling (#7824/#8337/#9947).
Por tanto:
- **Summarize/Draft** (sin tools) → cualquier modelo local, default `llama3.2` (ya descargado).
- **Agent** (tool-use) → **`qwen3:32b` local (20GB, cabe holgado en 48GB)** — Ollama lo usa como SU PROPIA referencia
  de tool-calling y lo describe como agéntico (`docs.ollama.com/capabilities/tool-calling`, `ollama.com/library/qwen3`).
  Alternativa estable: `qwen2.5:32b`. NO usar `deepseek-r1:32b` para tools (gap de propagación de template #10935; es
  destilación, no R1 nativo). Cambio de constante de modelo (el `Name()` neutral lo permite), no de arquitectura.
  El cliente verifica que el modelo de Agent esté descargado al arrancar; si no, lo indica.

**Backends `:cloud` (incl. `deepseek-v4-pro:cloud`) — DESCARTADOS para v0.1 (investigación verificada):** los modelos
`:cloud` de Ollama corren en datacenter remoto (`docs.ollama.com/cloud` verbatim: "offloaded to Ollama's cloud service")
→ el cuerpo del correo CRUZA la red bajo cuenta identificada. Ollama afirma no-train/no-retain (`ollama.com/privacy`),
PERO sin DPA firmado, sin SOC2, y sin respuesta oficial a si los requests `:cloud` se enrutan a APIs de terceros (issue
#14279 cerrado sin respuesta de maintainer). **No NDA-safe sin DPA verificable.** Posible provider extra en v0.2 SOLO si
se obtiene DPA — la interfaz `LLMProvider` lo admite sin refactor (sería categoría "cruza red → opt-in con aviso", junto a Claude).

**Agent loop propio** (no `BetaToolRunner` del SDK — para que ambos backends compartan loop):
```
build prompt → provider.Chat(msgs, tools) → {texto final → return | tool-calls → ejecuta → acumula → loop}
```
Con tope de iteraciones (anti-runaway), idéntico para ambos providers; cada provider traduce a/desde los tipos
neutrales `ToolSpec`/`ToolCall`. **Impedancia manejada (B5):** `ToolCall` lleva `ID string` (opcional) + `Name string`
— el adaptador Anthropic correlaciona por `ID` (devuelve `tool_result` con el `tool_use_id` exacto, o 400), el de
Ollama por `Name`+orden. Reglas Anthropic que el loop respeta: (a) TODOS los `tool_result` de parallel-tool-use van en
**un solo** user message; (b) chequear `StopReason` (`"tool_use"` continúa, `"refusal"` se maneja sin crashear índice).
No-streaming en ambos backends para v0.1 (B7; si `max_tokens` crece >~16k, streamear el lado Claude).

**Tres capacidades:**
1. `Summarize(ctx, parsed)` → resumen + acción requerida + urgencia (una vuelta, sin tools).
2. `Draft(ctx, thread, instruction)` → borrador de respuesta. Texto → `$EDITOR`/composer → confirmas → SES. **Nunca envía solo.**
3. `Agent(ctx, goal)` → tools **read-only** sobre el buzón: `list_messages`, `read_message`, `search_subject`. **Sin tool de envío** — el blast radius del agente queda acotado por diseño; enviar es siempre acción humana fuera del loop.

---

## 5. Frontends

### 5.1 CLI — `cmd/mail` (Cobra, binario ligero sin Bubble Tea)
```
mail ls   [--mailbox …] [-n 50] [--json]              # DynamoDB Query
mail read <id|sk> [--raw|--html|--plain]              # S3 + parse + render
mail send --to … --subject … [--body-file f] [--attach f]   # SES SendRawEmail
mail reply <id> [--instruction "…"]                   # threading + $EDITOR + confirmación
mail ai summarize <id> [--backend ollama|claude]
mail ai draft <id> --instruction "…"
mail ai agent "encuentra correos de X esta semana"    # tool-use read-only
```
`--json` en `ls`/`read` para componer (fzf/jq/tmux). Defaults desde `~/.config/erickaldama-mail/config.toml`
(profile/mailbox/backend), overridable por flag/env. Sin config: `mail-client-read` + Ollama.

### 5.2 TUI — `cmd/mail-tui` (Bubble Tea + Bubbles + Glamour + Lipgloss, binario separado)
Tres vistas, Vim-motions en todas:
- **List**: `j/k` mover, `gg/G` extremos, `/` buscar (subject/from), `Enter` abrir, `r` responder, `s` resumen AI, `a` agente, `q` salir.
- **Reader**: HTML render con glamour; `J/K` scroll, adjuntos listados, `s` resumen inline, `r` responder, `Esc` vuelve.
- **Composer**: campos to/subject/body; `Ctrl-E` abre `$EDITOR` (nvim); responder pre-rellena threading + cita + (si pediste) draft AI; **`Ctrl-S` = enviar exige confirmación `y/n`** antes de tocar SES.

Estado del modelo Bubble Tea explícito → testeable vía `Update(msg)` con keypresses simulados.

### 5.3 Integración tmux (glue documentado, no plugin formal)
- **`mail tmux popup`** → TUI en `tmux display-popup` (overlay flotante).
- **Status**: `mail ls -n0 --count` → conteo para `status-right`.
- **Bindings sugeridos** (en runbook, copy-paste, NO impuestos — anclados a config real, cero colisión):
  - tmux: `bind e display-popup -E "mail-tui"` (prefix+e libre).
  - nvim: `<leader>ml` listar, `<leader>mc` componer, `<leader>ms` buscar, `<leader>ma` agente (`<leader>m` libre; encaja en convención "un prefijo = un dominio").

---

## 6. Manejo de errores (clasificado, sin string-match silenciado)

Disciplina `avoid-string-match-error-silencing`: errores tipados del SDK, no match contra `.Error()`.

| Caso | Manejo |
|---|---|
| SES sandbox rechaza destinatario | **AWS NO tipa este caso** (A6): llega como `*types.MessageRejected` genérico — NO existe `RecipientNotVerified` (el tipo `FromEmailAddressNotVerifiedException` es para el REMITENTE). Manejo compatible con `avoid-string-match-error-silencing`: `errors.As(err, new(*types.MessageRejected))` clasifica "envío rechazado" (tipado); para inferir el sub-motivo "sandbox" NO se string-matchea el mensaje, se combina con `DetectSandbox()` previo (si sandbox + MessageRejected → presentar como causa probable: "SES en sandbox: verifica el destinatario o usa el Mailbox Simulator success@simulator.amazonses.com"). El `mr.ErrorMessage()` se muestra como **contexto** al usuario, nunca como predicado de control de flujo. Límite del provider declarado (verify-provider-api-supports-property). |
| Buzón vacío / mensaje no existe | estado limpio, no error |
| S3 GetObject NoSuchKey | error claro con el s3Key (índice apunta a objeto ausente) |
| Ollama `:11434` caído | detectar → ofrecer arrancar o degradar: "backend AI no disponible; lectura/envío siguen". El AI nunca bloquea el correo |
| Credenciales AWS expiradas/SSO | mensaje que apunta a refrescar el profile correcto |

---

## 7. Seguridad y postura NDA

- **Cero secretos en binario/logs/git**: AWS desde profiles; API key Claude desde env/keychain.
- **Separación read/send física**: el client de lectura no porta credenciales de envío y viceversa.
- **Agente AI sin tool de envío**: solo lee; enviar es siempre acción humana confirmada.
- **Default privacidad = Ollama local** (el correo no cruza la red). **Claude = opt-in con aviso** una vez:
  "el cuerpo cruzará a api.anthropic.com (no se entrena por default; ZDR recomendado)" — coherente con el
  gate de tarea #9.
- **Redacción previa opcional** (defensa en profundidad): paso determinista que enmascara patrones
  sensibles (emails de terceros, tokens AKIA/ghp_) antes de mandar a CUALQUIER backend.
- **Adjuntos**: sanitizar nombres al guardar (anti path-traversal); respetar límites SES (raw ≤10MB).
- **Repo público** → gate NDA sobre todo el output antes del PR.

---

## 8. Mini-cambio CDK previo (Task 0 — humano deploya out-of-band)

La única infra de SP-4. Es CDK-Go (el hook aplica aquí). **Dos principales nuevos del cliente** — `mail-readonly`
NO se reusa ni se modifica (deniega `s3:GetObject` por diseño de gobernanza; SP-4 separa "verificador" de
"cliente de producto"):

**(a) User `mail-client-read`** — el principal de LECTURA del cliente. Allowlist propia scoped:
- `dynamodb:Query` + `dynamodb:GetItem` sobre la tabla `mail-index` (ARN scoped).
- `s3:GetObject` sobre `arn:aws:s3:::erickaldama-mail-raw/inbound/*` (solo el prefijo de correo entrante).
- Condition regional `us-east-1`. NO envío, NO mutación. Constante `ClientReadUserName = "mail-client-read"`.

**(b) User `mail-sender`** — el principal de ENVÍO. Policy `mail-send` existente attachada directa.
  Constante `SenderUserName = "mail-sender"`.

- **Sin access keys en el stack** (el humano las genera fuera de CDK con `aws iam create-access-key`; nunca en código/git).
- En qué stack viven: ambos users encajan naturalmente en `SendingStack`/`FoundationStack` — el plan decide
  el stack exacto (probablemente `mail-client-read` en FoundationStack junto a `mail-readonly`, `mail-sender`
  en SendingStack junto a `mail-sender-role`). Lo resuelve la auditoría/plan.
- Flujo humano: `cdk deploy <stacks>` → `aws iam create-access-key --user-name mail-client-read` +
  `--user-name mail-sender` → guardar en `~/.aws/credentials` profiles `mail-client-read` y `mail-sender`.
  El agente verifica read-only después (assume-as cada profile, prueba empírica como en el redeploy del Hallazgo #8).

**Trade-off aceptado (documentado):** users con policy directa implican **access keys de larga vida**
(peor postura que STS temporal). Mitigación: ambas policies fuertemente scopeadas — `mail-client-read` solo
lee `mail-index` + `inbound/*` (no puede enviar ni mutar); `mail-send` solo SendEmail/SendRawEmail desde la
identidad verificada con From=erick@ → blast radius mínimo si se filtra. Rotación documentada en el runbook.

**Por qué NO tocar `mail-readonly`:** el self-review (2026-06-24) confirmó que `mail-readonly` deniega
`s3:GetObject` deliberadamente (es un verificador de gobernanza, lee metadata no contenido). Ensancharlo para
que el cliente lea cuerpos contaminaría su propósito. Mantener la separación gobernanza/producto es la decisión
correcta — mismo principio que separó SSO-Admin-deploya / agente-verifica en todo el proyecto.

---

## 9. Layout del módulo

```
cmd/mail/main.go              # CLI Cobra (ls/read/send/reply/ai)
cmd/mail-tui/main.go          # TUI Bubble Tea
internal/mailbox/             # Reader + Sender (+ interfaces AWS para fakes)
internal/message/             # parse/build/render/threading (puro) + testdata/ MIME fixtures
internal/aiassist/            # LLMProvider + agent loop + tools
internal/aiassist/ollama/     # provider local
internal/aiassist/claude/     # provider API
internal/awsconf/             # carga profiles read/send + sandbox detect
internal/infra/naming.go      # +SenderUserName (Task 0)
internal/infra/sending_stack.go  # +user mail-sender (Task 0)
docs/SP-4-*.md, README, runbook
```

---

## 10. Testing

- **`internal/message/`** (corazón puro): fixtures MIME reales en `testdata/` → parse, threading, build round-trip, render. Sin red.
- **`internal/aiassist/`**: agent loop con `LLMProvider` **fake** (tool-calls scripteados) → valida loop, cap de iteraciones, ejecución de tools. Tools read-only contra `Reader` fake.
- **`internal/mailbox/`**: clients AWS fake (interfaces sobre ops del SDK) → Query/GetObject/SendRawEmail sin AWS.
- **CLI**: subcomandos con stdout capturado + núcleo fake → salida determinista.
- **TUI**: `Update(msg)` con mensajes simulados → transiciones de estado sin terminal.
- **CI**: entra al `ci.yml` existente (build/vet/test-race/gofmt). El cliente NO toca el hook CDK.

---

## 11. Definition of Done (v0.1)

1. `go build ./... && go test ./...` verde (fixtures MIME + fake LLM + fake AWS).
2. `mail ls/read/send/reply` funcionan contra recursos reales (lectura `mail-client-read`, envío `mail-sender` al Mailbox Simulator).
3. TUI navegable con Vim-motions, render HTML, composer con confirmación de envío.
4. AI: `summarize` + `draft` con **Ollama `llama3.2`**; `agent` (tool-use) con **`qwen3:32b` local** (B4 — llama3.2 no es confiable para tools; qwen3 es la referencia de tool-calling de Ollama); backend Claude (`claude-opus-4-8`) por flag. Backends `:cloud` descartados (no NDA-safe sin DPA).
5. Smoke end-to-end real: enviar a `test@erickaldama.com` con el cliente → aparece en `mail ls` (cierra SP-2↔SP-3↔SP-4).
6. Runbook + README publicables (arquitectura, doble backend AI, bindings tmux/nvim sugeridos, seguridad). Diagrama actualizado.
7. Gate NDA sobre todo el output (repo público).

---

## 12. Non-goals (v0.1)

- Sin envío autónomo del agente (humano-en-el-loop estricto).
- Sin render HTML→imágenes ni tablas complejas (glamour cubre negritas/links/listas; HTML real exótico degrada a texto).
- Sin múltiples cuentas/dominios (solo erickaldama.com).
- Sin IMAP/POP (el backend es DynamoDB+S3, no un mailserver clásico).
- Sin auto-arranque del daemon Ollama como servicio (se detecta/ofrece arrancar, no se gestiona).
- Sin backends `:cloud` de Ollama (deepseek-v4-pro:cloud etc.) — no NDA-safe sin DPA verificable; diferido a v0.2 condicionado a DPA.
- Production access SES (sigue en sandbox; el cliente lo maneja con gracia, no lo resuelve).

---

## 13. Disciplinas aplicables

`aws-cli-pre-flight-canonical` (verify identity antes de cada AWS) · `modern-go-guidelines` (Go 1.26) ·
`avoid-string-match-error-silencing` (errores tipados) · `adversarial-audit-before-new-pattern` (el agent
loop + doble backend + tool-use es patrón NUEVO → auditoría adversarial antes del plan) · `claude-api`
(SDK anthropic-sdk-go, model id `claude-opus-4-8`, adaptive thinking) · `control-subagents-in-worktrees-canonical`
(subagent-driven-development en este worktree) · gate NDA (repo público).
