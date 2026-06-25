# Changelog

Historia de la construcción del sistema de correo `erickaldama.com` — envío y recepción sobre AWS SES,
provisionado **íntegramente con AWS CDK en Go**, consumido por un cliente terminal-native Go (CLI + TUI + agente AI).

Este changelog es **orgánico**: narra la evolución real, las decisiones de arquitectura, y los hallazgos que solo
aparecieron al desplegar contra AWS de verdad — no un volcado de commits. El proyecto se construyó como cadena de
subproyectos (SP-0 → SP-4), cada uno con su ciclo **brainstorm → spec → auditoría adversarial → plan → ejecución
subagent-driven → deploy verificado en vivo**. El formato se inspira en [Keep a Changelog](https://keepachangelog.com).

Cuenta AWS `367707589526` · región `us-east-1` · repo público con Git Flow (`main`/`rc`/`develop` protegidos).

---

## [mail tmux] — Integración tmux del cliente (cierra deuda SP-4 §5.3) — 2026-06-25

El spec de SP-4 (§5.3) diseñó una glue de tmux para el cliente que v0.1 dejó sin implementar. Se cierra esa deuda
para que la integración apunte a un subcomando real, no a un atajo.

### Added
- **`mail tmux popup`** — lanza `mail-tui` en un `tmux display-popup` (overlay flotante 90%×90%), forwarding
  `--read-profile`/`--mailbox`. Guarda contra el env `TMUX` (error claro si se corre fuera de tmux). El argv lo
  construye `tmuxPopupArgs()` (función pura, testeada) — un slice de argv, NUNCA un string de shell → sin inyección.
- **`mail tmux status`** — imprime el conteo de mensajes (`📬 N`) para el `status-right` de tmux. Read-only; no toca
  los caminos de AI ni de envío.
- **Test** `TestTmuxPopupArgs` — verifica el forward de flags + que los valores son elementos de argv independientes
  (no concatenación de shell). El paquete `cmd/mail` pasa de tener solo tests de `renderList` a cubrir el subcomando.

### Integración del entorno (bindings sin colisión, verificados contra la config real)
- **tmux** (`~/.tmux.conf`, prefix `C-a`): `bind e display-popup -E -w 90% -h 90% "mail-tui"` → `prefix+e` abre el
  cliente en un popup. Tecla `e` verificada libre.
- **nvim** (`~/.config/nvim/lua/config/keymaps.lua`, leader `\`): `<leader>ml` (TUI), `<leader>ms` (list),
  `<leader>mc` (compose), `<leader>ma` (AI agent). Prefijo `<leader>m` verificado libre.

### Notas
- Ambos binarios (`mail`, `mail-tui`) instalados en `~/go/bin` (en PATH) — consumibles globalmente, no temporales.
- Menor privilegio verificado en vivo: `mail-client-read` autoriza `Query` (el `mail ls`/`tmux status` funcionan)
  pero niega `s3:ListBucket`/`Scan` (AccessDenied) — la policy es exactamente Query+GetItem + GetObject scoped.

---

## [CD] — Pipeline de CI/CD con OIDC — 2026-06-24

Automatización del `cdk deploy` vía GitHub Actions con credenciales temporales OIDC — sin access keys de
larga vida en el repo, con doble gate de aprobación humana independiente.

### Added
- **CdStack** (CDK Go, `internal/infra/cd_stack.go`) — 3 recursos desplegados en vivo en 48s, 7/7 CREATE_COMPLETE:
  - `AWS::IAM::OIDCProvider` — `GithubOidc` (`token.actions.githubusercontent.com`), L1 `CfnOIDCProvider` nativo
    (0 Lambda, 0 custom-resource — el L2 `NewOpenIdConnectProvider` añadiría un tercer rol sin boundary).
  - `AWS::IAM::Role` `mail-cd-diff` — trust `sub=repo:esaldgut/erickaldama-mail:pull_request`, permisos solo
    `sts:AssumeRole` sobre `cdk-hnb659fds-lookup-role` (read-only, preview de cambios en PRs).
  - `AWS::IAM::Role` `mail-cd-deploy` — trust `StringEquals sub=repo:esaldgut/erickaldama-mail:environment:production`
    (NUNCA wildcard), `sts:AssumeRole` sobre los 4 roles `cdk-hnb659fds-*`. Ambos roles con boundary
    `erickaldama-boundary`. Stack ARN `arn:aws:cloudformation:us-east-1:367707589526:stack/CdStack/a8f59b10`.
- **Workflow `.github/workflows/cd.yml`** — dos jobs:
  - `diff` (on: pull_request): asume `mail-cd-diff` → `cdk diff` → comenta el resultado en el PR.
    Fork-guard: `if: github.event.pull_request.head.repo.full_name == github.repository` (forks externos no
    obtienen OIDC ni `pull-requests:write` en repos públicos — correcto por diseño, no un bug).
  - `deploy` (on: push → main): PAUSA en estado "Waiting" por el Environment gate → tras aprobación humana,
    asume `mail-cd-deploy` → `cdk deploy --all --require-approval never`.
  - `concurrency: group: deploy-production, cancel-in-progress: false` — el deploy en curso termina; solo el
    pending intermedio se cancela si llega un 3er push.
- **Environment `production` configurado** — `required_reviewers: [esaldgut]`, branch policy `main`,
  `can_admins_bypass: true` (single-dev; self-review habilitado). Verificado por API (`gh api`).
- **Runbook** `docs/CD-DEPLOY.md` — pre-flight, canario, bootstrap out-of-band, gate CRÍTICO del environment,
  verificación OIDC e2e, smoke de seguridad, kill-switch (4 opciones), notas de seguridad. 665 líneas.

### Doble gate — diseño de seguridad

El gate humano tiene dos capas independientes:

1. **GitHub Environment `production`** — el job `deploy` queda en "Waiting" hasta aprobación en la UI de Actions.
   **CRÍTICO:** GitHub auto-crea el environment cuando el workflow lo referencia por primera vez, pero lo crea
   **vacío** — sin reviewers, sin branch rules — y el job corre inmediatamente. La configuración del environment
   debe hacerse **antes** de cualquier push a main, no después.

2. **Trust OIDC `StringEquals`** — AWS solo emite credenciales si `sub=environment:production` exacto. Un PR
   (sub=`pull_request`) no puede asumir el rol de deploy, aunque alguien eludiera la capa 1.

Si se desactiva la capa 1, la capa 2 sigue activa. Si se elimina el rol (kill-switch), la capa 1 ya no importa.

### Verificación e2e en vivo

El CD se verificó end-to-end **en su propio PR #6** antes de mergearse a develop (95ce3a3): el job `diff`
asumió OIDC `mail-cd-diff`, corrió `cdk diff`, y publicó el comentario del diff directamente en el PR.
Evidencia binaria visible en el PR — no declarado operativo hasta que el flujo OIDC completo funcionó en vivo.

### Deploy findings — los 4 hallazgos reales del CD

El CdStack es el stack que más ha exigido del boundary `erickaldama-boundary` — 2 versiones nuevas (v5, v6)
en un solo subproyecto. Los findings #3 y #4 son del CLI y el diff flag, cazados por el CD en vivo en su
propio PR. Son la 4ª y 5ª instancia del patrón "boundary intersecta" en el proyecto (SP-1 ssm,
SP-2 events, SP-4 iam:CreateUser, CD v5, CD v6).

**Finding B4 — boundary v5: `iam:PutRolePermissionsBoundary` (anticipado en la auditoría, confirmado en vivo)**

El boundary tenía un `Deny iam:PutRolePermissionsBoundary` como anti-escalación (ningún rol bajo el boundary
puede adjuntar boundaries arbitrarios a otros roles). El cfn-exec-role tiene `iam:*` en su exec-policy, pero el
boundary INTERSECTA: el Deny explícito gana. Al crear `mail-cd-diff` y `mail-cd-deploy` con `PermissionsBoundary`
adjunto, CloudFormation llama `CreateRole` + `PutRolePermissionsBoundary` → AccessDenied.

Fix: boundary v5 con la excepción scoped `StringNotEqualsIfExists` — permite `PutRolePermissionsBoundary` SOLO
cuando el boundary adjunto es `erickaldama-boundary` mismo. El anti-escalación se preserva para cualquier otro
boundary. Confirmado: con v5 activo, el deploy creó los 2 roles con boundary sin error. Sin Task 0 (aplicar v5
antes del deploy), el CdStack habría fallado con AccessDenied en el primer recurso.

**Finding A1 — boundary v6: `sts:AssumeRole` (cazado por el smoke, NO anticipado)**

El smoke de seguridad post-deploy (`simulate-principal-policy`) reveló que el boundary v5 no incluía
`sts:AssumeRole`. Como effective perms = identity policy ∩ boundary, y el boundary no tenía `sts:AssumeRole`
Allow, los roles OIDC no podían asumir los roles cdk-* en runtime — aunque la identity policy los permitiera.

Síntoma sin el smoke: el CD habría fallado en el primer run real con `AccessDenied sts:AssumeRole` en el step
`configure-aws-credentials` de GitHub Actions. Difícil de diagnosticar desde los logs de Actions porque el
error vendría de AWS STS. El smoke lo cazó **antes del runtime** — `PermissionsBoundaryDecision.AllowedByPermissionsBoundary=false`.

Fix: boundary v6 (commit `75c647d`) con `sts:AssumeRole` scoped exactamente a los 4 ARNs `cdk-hnb659fds-*`
— no `sts:*`, no `Resource:*`. Menor privilegio: cualquier otro `sts:AssumeRole` sigue bloqueado. Smoke
re-ejecutado post-v6: los 4 roles cdk-* → `allowed`; diff-role → deploy-role → `implicitDeny`. Pass.

**Finding #3 — CLI version skew: `aws-cdk@2.258.1` no existe en npm**

- **Evento:** primer run del CD (job `diff`) en GitHub Actions — `npm install -g aws-cdk@2.258.1`
- **Error:** `npm error notarget No matching version found for aws-cdk@2.258.1`
- **Causa raíz:** el CDK CLI npm y la librería Go `awscdk/v2` usan **esquemas de versión distintos**. La lib
  Go es `v2.258.1`; el CLI npm sigue el esquema `2.1xxx.x` (actualmente `2.1128.1`). `aws-cdk@2.258.1` y
  `aws-cdk@2.258.x` no existen en npm. Confundir ambos esquemas reproduce el hallazgo SP-1 "CLI vs lib
  version skew" pero con un síntoma diferente (notarget en npm en vez de "not bootstrapped" en cdk).
- **Fix:** `CDK_VERSION=2.1128.1` en el workflow (commit `faae10d`). Cazado por el CD en vivo en su propio
  primer run — nunca fue visible en un entorno local donde el CLI ya estaba instalado.
- **Resultado:** workflow corrige la versión y el `npm install -g aws-cdk@2.1128.1` pasa en Actions.
- **Status:** RESUELTO (finding #3 — ver `docs/CD-DEPLOY.md` §11)

**Finding #4 — `cdk diff --all` flag inválido en CLI 2.1xxx**

- **Evento:** job `diff` del CD — el step `cdk diff --all` corrió pero su output incluía una advertencia
- **Error/Síntoma:** el comentario del diff publicado en el PR mostraba `Unknown option(s): --all. These will
  be ignored` al inicio del output
- **Causa raíz:** en CLI 2.1xxx, `cdk diff` no acepta `--all` — el default ya difea todos los stacks del
  app. En contraste, `cdk deploy --all` SÍ es flag válido (el subcomando `deploy` lo acepta). El flag `--all`
  sobre `diff` es simplemente ignorado con advertencia, pero queda en el output como ruido y como señal falsa.
- **Fix:** quitar `--all` del step `diff` en el workflow (commit `af98821`). `cdk deploy --all` sin cambios —
  es correcto y válido. Cazado por el comentario del diff en el propio PR #6.
- **Resultado:** el diff output queda limpio (sin advertencia); el deploy sigue usando `--all` correctamente.
- **Status:** RESUELTO (finding #4 — ver `docs/CD-DEPLOY.md` §11)

### Smoke de seguridad empírico (DoD #5 — PASS en vivo)

Separación read/write verificada empíricamente via `aws iam simulate-principal-policy`:
- `mail-cd-diff` → `cdk-hnb659fds-deploy-role`: **implicitDeny** (no puede escalar a deploy) ✓
- `mail-cd-diff` → `cdk-hnb659fds-file-publishing-role`: **implicitDeny** ✓
- `mail-cd-diff` → `cdk-hnb659fds-image-publishing-role`: **implicitDeny** ✓
- `mail-cd-diff` → `cdk-hnb659fds-lookup-role`: **allowed** (solo lo que necesita) ✓
- `mail-cd-deploy` → los 4 roles `cdk-hnb659fds-*`: **allowed** ✓

Evidencia binaria: la separación no es solo en papel. El smoke es el gate que cierra el DoD antes de declarar
el CD operativo — obligatorio en cualquier arquitectura con boundary estricto, donde los permisos efectivos
(identity ∩ boundary) no son obvios al leer solo la identity policy.

### Decisions
- **L1 `CfnOIDCProvider` sobre el L2 `NewOpenIdConnectProvider`** — el L2 crea un custom resource (Lambda
  nodejs22.x) que añade un tercer rol de ejecución sin boundary. Con un boundary estricto ese rol quedaría
  fuera del control del boundary. El L1 es nativo de CloudFormation: 0 Lambda, 0 custom-resource.
- **`can_admins_bypass: true`** — single-dev; con 1 reviewer y self-review habilitado, la única forma de
  aprobar el deploy propio es que can_admins_bypass esté activo. Trade-off aceptado para el proyecto personal.
- **Job `diff` NO marcado como required status check** — un PR de fork (sub=`pull_request`) hace skip por el
  fork-guard; GitHub trata `skipped` distinto a `success` y bloquearía PRs de colaboradores externos. No es
  un bug — es la barrera de seguridad funcionando. Solo no debe convertirse en un bloqueo de PR innecesario.
- **`cancel-in-progress: false` en concurrency** — cancelar el deploy en curso deja el stack en rollback parcial;
  con `false`, solo el pending intermedio se cancela; el deploy activo siempre termina limpio.

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
