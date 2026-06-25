# Changelog

Historia de la construcción del sistema de correo `erickaldama.com` — envío y recepción sobre AWS SES,
provisionado **íntegramente con AWS CDK en Go**, consumido por un cliente terminal-native Go (CLI + TUI + agente AI).

Este changelog es **orgánico**: narra la evolución real, las decisiones de arquitectura, y los hallazgos que solo
aparecieron al desplegar contra AWS de verdad — no un volcado de commits. El proyecto se construyó como cadena de
subproyectos (SP-0 → SP-4), cada uno con su ciclo **brainstorm → spec → auditoría adversarial → plan → ejecución
subagent-driven → deploy verificado en vivo**. El formato se inspira en [Keep a Changelog](https://keepachangelog.com).

Cuenta AWS `367707589526` · región `us-east-1` · repo público con Git Flow (`main`/`rc`/`develop` protegidos).

---

## [SP-4] — Cliente TUI/CLI/AI Go — 2026-06-24

El primer componente que **consume** la infra en vez de aprovisionarla: cierra el lazo end-to-end del producto.

### Added
- **Cliente de correo terminal-native** en dos binarios sobre un núcleo de dominio compartido:
  - `cmd/mail` — CLI Cobra (`ls`/`read`/`send`/`reply`/`ai`), stdout componible (`--json` para pipes/tmux/fzf).
  - `cmd/mail-tui` — TUI Bubble Tea (list/reader/composer) con Vim-motions (`j/k/gg/G`), render HTML enriquecido.
- **Agente AI de doble backend** sobre una interfaz `LLMProvider` neutral con un **agent-loop propio** (no el del SDK):
  - `ollama` (local, **default seguro** — el correo nunca sale del Mac) — modelo de tool-use `qwen3:32b`.
  - `claude` (API Opus 4.8, **opt-in con aviso** — el cuerpo cruza la red) — adaptive thinking.
  - Capacidades: resumen/triage, draft de respuestas (humano-en-el-loop), y agente con tools **read-only** (sin tool de envío).
- **`internal/redact`** — máscara NDA determinista (tokens secret-shaped + emails de terceros) antes de cualquier backend que cruza red, con golden corpus + canary.
- **Núcleo de dominio puro**: `internal/message` (parse/build MIME via enmime/v2, threading RFC 5322, render html-to-markdown→glamour), `internal/mailbox` (Reader DynamoDB+S3 / Sender SES), `internal/awsconf`, `internal/wire` (punto único de instanciación — DRY).
- **Dos principales IAM del cliente** (CDK, menor privilegio disjunto): `mail-client-read` (Query+GetObject scoped) y `mail-sender` (SendRawEmail scoped). `mail-readonly` (verificador de gobernanza) queda intacto.
- Runbook `docs/SP-4-DEPLOY.md` con comandos AWS CLI reales + los 3 incidentes de deploy.

### Engineering discipline
- Plan ejecutado vía **subagent-driven-development**: un subagente fresco por tarea, con **auditoría de ingeniería de 6 ejes** (robustez · eficiencia · patrones · mantenibilidad · seguridad · deuda) como gate antes de cerrar cada tarea — cada hallazgo con evidencia binaria (archivo:línea), no opinión.
- La auditoría cazó y corrigió **3 hallazgos MAYOR bloqueantes** antes del merge: un error de `DetectSandbox` silenciado (anti-silencing), una type-assertion frágil que descartaba campos `required` venidos de JSON, y un `reply` que impersonaba al remitente original. Más una variante de doble-envío SES en la TUI y un editor que corrompía el AltScreen (resueltos con `tea.ExecProcess`).
- Stack verificado **compilando contra las librerías vivas** durante la auditoría del plan: anthropic-sdk-go v1.51.1, enmime/v2, aws-sdk-go-v2 (ses v1 SendRawEmail / sesv2 GetAccount), bubbletea v1.3.10, cobra, glamour.

### Deploy findings (lo que solo apareció contra AWS real)
- **El permissions boundary deniega `iam:CreateUser`** (anti-escalación, diseño SP-1). SP-4 es el primer stack que crea un `AWS::IAM::User` → primera intersección. Fix: ampliar el boundary (v4) con una excepción `NotResource` scoped a los 2 ARNs del cliente, preservando el deny para cualquier otro user.
- **La policy de envío necesitaba el config-set además de la identidad**: `mail-config` es el config-set por defecto de la identidad, así que SES lo aplica a todo envío y exige `ses:SendRawEmail` sobre **ambos** recursos. Fix: `Resources: [identity, configuration-set]`.
- **Una access key impresa en chat → rotación segura**: revocar + regenerar con `aws configure set` (la secret nunca toca la pantalla). Patrón documentado en el runbook.

### Decisions
- **Backends `:cloud` de Ollama descartados** (investigados): `deepseek-v4-pro:cloud` es inferencia remota sin DPA verificable → no NDA-safe. El modelo de tool-use local pasó a `qwen3:32b` (la referencia de tool-calling de Ollama), no `llama3.2` (no figura en la lista oficial de tool-support).
- **Users con access keys de larga vida** (no STS temporal) por simplicidad de runtime — trade-off aceptado, mitigado por policies fuertemente scopeadas + rotación documentada.

---

## [SP-3] — Pipeline de recepción — 2026-06-18

Recepción de correo entrante, end-to-end y verificada con un correo real.

### Added
- **MailStorageStack** — bucket S3 `erickaldama-mail-raw` (SSE-S3, BLOCK_ALL, EnforceSSL, lifecycle IA@90d, RETAIN).
- **ReceivingStack** — SES v1 receipt rule set `erickaldama-inbound` (catch-all, TLS Require, ScanEnabled) → S3 → Go Lambda `mail-receive` (arm64, provided.al2023) → DynamoDB `mail-index` (on-demand) + SQS DLQ (SSE) vía OnFailure destination.
- Rule set **activado por custom resource** (`SES.setActiveReceiptRuleSet` — no hay campo declarativo "active" en la API v1).
- Apex MX, DMARC `rua` re-apuntado a `dmarc-reports@erickaldama.com` (dogfood, mismo dominio → sin autorización cross-domain), suscripción SNS al operador.
- Handler idempotente: un item por destinatario del dominio (`Receipt.Recipients`, envelope autoritativo), idempotencia por el Message-ID RFC 5322 (`errors.As` + `ConditionalCheckFailedException` → continue).

### Deploy findings
- **Hallazgo #8 (3ª vez del patrón "observabilidad es parte del límite del agente")**: el `mail-readonly` no podía leer los logs del Lambda ni `sns:GetSubscriptionAttributes` — justo las 2 señales para diagnosticar un pipeline async. Ampliado con `logs:*` (read) + `sns:GetSubscriptionAttributes`.
- **Dos "síntomas que no eran bugs"**: el email SNS no llegó (era `PendingConfirmation`, faltaba un clic); el correo de prueba "no indexó" (sí lo hizo, era falta de visibilidad del read-only). Diagnosticados con 3 agentes read-only.

### Resolved by adversarial audit (antes de tocar AWS)
- Ciclo cross-stack bucket↔rule (la resource policy del bucket importado cicla sobre el token del rule-ARN) → `NewBucketPolicy` en el stack consumidor.
- La invocación SES→Lambda necesita `fn.AddPermission` explícito; el cuerpo NO viene en el evento (S3 se escribe primero) — el `Mail.MessageID` es la S3 key, distinto del `CommonHeaders.MessageID` (RFC 5322, clave de idempotencia).

---

## [SP-2] — Identidad SES + envío — 2026-06-16

Capacidad de **enviar** correo firmado desde el dominio.

### Added
- **SendingStack** — `EmailIdentity erickaldama.com` (Easy DKIM RSA-2048, verificado vivo; MAIL FROM `mail.erickaldama.com`) → auto-publica 6 record sets (3 DKIM CNAME + MAIL FROM MX + SPF TXT; sin SPF duplicado — canario de la auditoría).
- ConfigurationSet `mail-config` (FeedbackForwarding OFF) + EventBridge event destination (BOUNCE+COMPLAINT) → Rule → SNS al operador.
- 2 alarmas de reputación (BounceRate ≥0.02, ComplaintRate ≥0.0005, treatMissingData IGNORE).
- ManagedPolicy `mail-send` (solo `ses:SendEmail/SendRawEmail`, identidad verificada, Condition `From=erick@`) + `mail-sender-role` asumible.
- Smoke al Mailbox Simulator: 3× OK.

### Deploy findings
- **El boundary necesita `events:*`** además de la exec-policy (INTERSECTA — la lección de SP-1 en un 2º servicio): el deploy falló en `SesEventRule` (5/18) hasta ampliar ambos.
- **El read-only del agente no podía leer SNS/EventBridge** para verificar lo que despliega → ampliado con `sns:*`/`events:*` reads. "La observabilidad es parte del límite, no un extra."

### Decisions
- DMARC quedó `p=none` sin `rua` en SP-2 (un `rua` cross-domain a Gmail es no-funcional por RFC 7489 — el receptor debe publicar autorización; Gmail no la publica). El `rua` a un buzón propio se añadió en SP-3.

---

## [SP-1] — Fundación DNS + cuenta — 2026-06-10

El primer subproyecto que aprovisiona infra real — el examen de la gobernanza SP-0.

### Added
- **FoundationStack** — PublicHostedZone `erickaldama.com` (+ CAA Amazon-only) + ManagedPolicy `mail-readonly` (allowlist puro, 4 statements verificados contra el Service Authorization Reference, con hard-deny de mutación + recon).
- Re-delegación del registrar, gate post-deploy (identity + simulate 13/13).
- El **permissions boundary `erickaldama-boundary`** se trata como artefacto de **bootstrap** (out-of-band), NO como recurso del stack.

### Deploy findings (los 5 hallazgos del primer deploy real)
1. **CLI-vs-lib version skew** — el `cdk` CLI debe ser ≥ el schema de la librería; síntoma engañoso "not bootstrapped".
2. La exec-policy necesita `ssm:GetParameters`.
3. El **boundary también** (INTERSECTA: effective = exec-policy ∩ boundary).
4. El boundary da **409 AlreadyExists** si el stack intenta crearlo — es bootstrap-owned, no stack-owned.
5. `ListHostedZonesByName` cae fuera del allowlist del read-only (el límite funcionando, no roto).

---

## [SP-0] — Gobernanza CDK-Go — 2026-06-08

La premisa imperativa: **todo aprovisionamiento AWS vía AWS CDK en Go**, garantizado mecánicamente.

### Added
- **Hook `PreToolUse`** (`.claude/hooks/cdk-go-guard.sh`) — bloquea mecánicamente cualquier write AWS fuera de CDK-Go (no depende de juicio del agente → idempotente cada sesión).
- **Plugin `cdk-go-aws-plugin`** — skill-receta CDK-Go (verifica la versión viva de aws-cdk-go, no hardcodea), skill-receta SES (patrón 4-fases verify-before-act), agente cdk-verifier, eval golden.
- Límite IAM allowlist-puro `mail-readonly` verificado en vivo (gate 8/8 + simulate 13/13). Bootstrap del principal en la cuenta real (t=0).
- Journal triple-capa: checkboxes en el plan + `EXECUTION-LOG.md` + framework de tareas.

---

## Infraestructura transversal

### CI/CD
- **2026-06-17** — repo público con **Git Flow**: `main`/`rc`/`develop` protegidos (PR obligatorio, 2 required status checks strict, no force-push/delete). Default `develop`. CI `.github/workflows/ci.yml` (gofmt + vet + build + test-race + shellcheck). El cierre de cada subproyecto es **PR a develop con CI verde**, no merge local.
- Gate NDA sobre **todo el historial** antes del primer push público: `git-filter-repo` reescribió 89 commits para purgar marcas de material de ejemplo de SP-0.

### Convenciones
- Cada subproyecto vive su ciclo completo en un **worktree aislado** y cierra con su runbook `docs/SP-N-DEPLOY.md`.
- Los artefactos IAM (policies, boundary, gates) se persisten como JSON en `iam/` — evidencia auditable.
- Toda credencial fuera del binario/git/logs; los deploys mutantes los ejecuta el humano out-of-band con SSO Admin, mientras el agente trabaja read-only y verifica.

---

_Documentado de forma orgánica conforme el proyecto se construyó. Cada "deploy finding" es real — apareció al
desplegar contra AWS, no en code review — y por eso es la parte más valiosa de esta historia._
