# CD Pipeline — Design Spec (OIDC + GitHub Environments)

> Subproyecto transversal (tarea #23): completa el CI/CD. GitHub Actions despliega los stacks CDK-Go a AWS
> vía OIDC (sin access keys de larga vida) con approval manual a producción.
> **Worktree:** `cd-pipeline` (rama `worktree-cd-pipeline`, base `42567c1` = HEAD de develop con SP-4 + METHODOLOGY).
> **Fecha:** 2026-06-24. **Cuenta:** ErickSA `367707589526` / `us-east-1`. Repo PÚBLICO con Git Flow.
> **Patrón NUEVO de seguridad** (4º principal con poder de mutación = GitHub Actions) → auditoría adversarial tras el spec.

---

## 1. Objetivo

Automatizar lo que hoy se hace a mano (`cdk deploy` out-of-band con SSO Admin) **sin debilitar el gate de aprobación
humana**, y AÑADIR un preview (`cdk diff`) en cada PR. El humano sigue aprobando toda mutación a prod; solo cambia
QUIÉN ejecuta el comando (GitHub Actions vía OIDC, credenciales temporales) y se gana visibilidad del cambio.

**Por qué importa:** cierra el "completar el CI/CD" pedido. Ya hay 4 stacks (Foundation/Sending/MailStorage/Receiving)
+ el cliente; el CD los automatiza manteniendo el gate. Es el cambio de seguridad más grande desde el repo público.

---

## 2. Ground truth verificado (no asumido)

Datos de la investigación contra doc oficial viva (`15-cd-research-findings.md`) + del sistema real (2026-06-24):

| Hecho | Valor verificado |
|---|---|
| Repo | público `esaldgut/erickaldama-mail`, Git Flow main/rc/develop protegidos, default=develop |
| Boundary `erickaldama-boundary` (v4 live) | Deny statements: `route53domains/ec2/rds/organizations:*`, los `Put*PermissionsBoundary`, y (excepción SP-4) `iam:CreateUser`/`CreateAccessKey` con NotResource. **NO deniega `iam:CreateRole`** → el CdStack puede crear los 2 roles sin tocar el boundary |
| OIDC provider URL | `https://token.actions.githubusercontent.com`, audience `sts.amazonaws.com`. ThumbprintList opcional (IAM lo autocompleta vía CA confiable) |
| `configure-aws-credentials` | `@v6` (v6.1.0 actual). OIDC vía `role-to-assume` + `permissions: {id-token: write}` |
| `cdk deploy --require-approval` sin TTY | NO funciona (confirmado vivo en SP-4): default `broadening` → error o no-op exit 0. CI usa `--require-approval never`; approval va a GitHub Environments |
| GitHub Environment con required reviewers | en repo PÚBLICO disponible sin Pro. Job con `environment:` queda "Waiting" hasta approval. "Prevent self-review" DESACTIVADO para single-dev |
| Bootstrap version-skew | pinear `aws-cdk@2.258.x` cerca de la lib `awscdk/v2 v2.258.1` para evitar el CLI-vs-lib skew (hallazgo SP-1) |
| CDK-Go en CI | runner necesita setup-go + setup-node + cdk CLI; `app: "go mod download && go run ./cmd/cdk"` funciona (ya en cdk.json) |
| Bootstrap roles | `cdk-hnb659fds-{deploy,file-publishing,image-publishing,lookup}-role-367707589526-us-east-1` — asumidos por la CLI durante deploy |
| Lo que la doc NO cubre | single-account + Git Flow → el diseño es **adaptación informada**, no receta oficial (el sample AWS es multi-account + workflow_dispatch) |

---

## 3. Arquitectura

**Principio rector:** el CD automatiza el comando, NO el juicio. El gate humano se preserva en **dos capas**:
(1) GitHub Environment pausa el job hasta tu approval; (2) el trust OIDC solo emite credenciales de deploy para el
contexto `environment:production`. Aunque alguien evadiera el runner, AWS no le daría credenciales de mutación.

```
┌─ Git Flow ─────────────────────────────────────────────────────────┐
│  feature/* ─PR─► develop ─PR─► rc ─PR─► main (protegido)            │
│                    │           │          │ (merge del PR)          │
│           (cada PR)▼  (cada PR)▼          ▼                          │
└────────────────────┼───────────┼─────────┼────────────────────────┘
        ┌────────────▼───────────▼┐   ┌─────▼──────────────────────┐
        │ JOB: cdk-diff (preview) │   │ JOB: cdk-deploy            │
        │ on: pull_request        │   │ on: push → main            │
        │ OIDC → mail-cd-diff     │   │ environment: production    │
        │ (read-only: lookup-role)│   │ ⏸ APPROVAL HUMANO (Waiting)│
        │ cdk diff --all          │   │ OIDC → mail-cd-deploy      │
        │ → COMENTA en el PR      │   │ cdk deploy --all           │
        └─────────────────────────┘   │   --require-approval never │
                                       └────┬───────────────────────┘
                                            ▼ (asume los cdk-* roles)
                              AWS 367707589526 — los 4 stacks
                          (cfn-exec-role con boundary muta los recursos)
```

**Dos planos, dos principales** (separación read/write, como SP-4 mail-client-read/mail-sender):
- **Preview (cualquier PR):** `mail-cd-diff` asume solo el `cdk-*-lookup-role` (read-only). No puede mutar. El diff
  se comenta en el PR.
- **Deploy (merge a main + approval):** `mail-cd-deploy` asume los 4 roles `cdk-*`. Trust exige `sub=environment:production`.

---

## 4. Componentes

### 4.1 `internal/infra/cd_stack.go` — el 5º stack CDK-Go
Crea la infra IAM del CD, versionada y auditable. `NewCdStack(scope, id, props) awscdk.Stack`. Helpers:
- **`addOidcProvider`** — `awsiam.NewOpenIdConnectProvider`: url `https://token.actions.githubusercontent.com`,
  clientIds `["sts.amazonaws.com"]`. (Thumbprint omitido — IAM lo autocompleta.)
- **`addDiffRole`** (`mail-cd-diff`) — `NewRole` con `OpenIdConnectPrincipal` (el provider) + Conditions
  `StringEquals { ...:aud = sts.amazonaws.com }` y `StringLike { ...:sub = repo:esaldgut/erickaldama-mail:* }`
  (read-only puede ser laxo — solo lee; pero **el spec lo deja explícito como decisión de la auditoría** si conviene
  estrechar a `pull_request`). Permiso: `sts:AssumeRole` solo sobre el `cdk-*-lookup-role`. PermissionsBoundary adjunto.
- **`addDeployRole`** (`mail-cd-deploy`) — `NewRole` con Conditions **`StringEquals`** (no StringLike):
  `...:aud = sts.amazonaws.com` Y `...:sub = repo:esaldgut/erickaldama-mail:environment:production`. Permiso:
  `sts:AssumeRole` sobre los 4 roles `cdk-*`. PermissionsBoundary adjunto. NI un PR NI otra rama lo asumen.
- Constantes en `naming.go`: `OidcProviderUrl`, `GithubRepo`, `DiffRoleName`, `DeployRoleName`, los 4 ARNs de los
  roles `cdk-*`, el ARN del lookup-role.

**Sutileza gallina-huevo:** el PRIMER deploy del CdStack lo hace el humano out-of-band (el CD aún no existe para
auto-desplegarse). Después el CdStack es un stack más que el propio CD gestiona.

**Hallazgo del boundary (verificado):** v4 NO deniega `iam:CreateRole` → el CdStack crea los 2 roles sin cambiar el
boundary. PENDIENTE para la auditoría: confirmar que `iam:CreateRole`/`CreateOpenIDConnectProvider` están en la
**exec-policy** del cfn-exec-role (allow) — si no, hay que ampliarla (no el boundary).

### 4.2 `.github/workflows/cd.yml` — el workflow
- **Job `diff`** (`on: pull_request` a develop/rc/main):
  `permissions: { id-token: write, contents: read, pull-requests: write }`,
  `actions/checkout` + `actions/setup-go@v5` (go-version-file: go.mod, cache) + `actions/setup-node@v4` +
  `npm install -g aws-cdk@2.258.x`, `aws-actions/configure-aws-credentials@v6` con `role-to-assume: <mail-cd-diff ARN>`
  + `aws-region: us-east-1`, `cdk diff --all 2>&1 | tee diff.txt`, **comenta el diff en el PR** (upsert con marcador
  `<!-- cdk-diff-bot -->`). Aplica skill `pr-as-auditable-evidence`.
- **Job `deploy`** (`on: push` a main):
  `environment: production` (el GATE), `concurrency: { group: deploy-production, cancel-in-progress: false }`,
  `permissions: { id-token: write, contents: read }`, mismo setup con `role-to-assume: <mail-cd-deploy ARN>`,
  `cdk deploy --all --require-approval never`. Verifica en logs que hubo cambios reales (no "no changes" silencioso).

### 4.3 Config de GitHub (a mano — out-of-band, en el runbook)
Environment `production`: required reviewers = tú (1 basta), **"Prevent self-review" DESACTIVADO** (single-dev),
deployment branches = "Selected branches and tags" → `main`.

### 4.4 Documentación (evidencia de portafolio — fase F6 de la metodología)
- **`docs/CD-DEPLOY.md`** (nuevo) — runbook: el bootstrap out-of-band del CdStack, la config manual del environment,
  verificación OIDC end-to-end, kill-switch (revocar el rol), y los deploy findings reales (los habrá).
- **`iam/cd-diff-trust.json` + `iam/cd-deploy-trust.json`** — los 2 trust policies, auditables (como mail-send-policy.json).
- **`CHANGELOG.md`** — entrada del CD.
- **`docs/architecture.md` + diagrama** — añadir el plano de CD.
- **Memoria** — el patrón OIDC seguro (StringEquals no wildcard, repo público) como canónico reutilizable.

---

## 5. Manejo de errores y riesgos

| Riesgo | Mitigación |
|---|---|
| `cdk deploy` no-op exit 0 sin TTY (job verde sin desplegar) | `--require-approval never` + verificar en logs que hubo cambios reales |
| Bootstrap version-skew (CLI vs lib) | Pinear `aws-cdk@2.258.x` en el runner |
| Dos deploys en paralelo → estado CFN inconsistente | `concurrency: { group: deploy-production, cancel-in-progress: false }` |
| Deploy parcial (un stack falla) | CFN hace rollback automático por stack (visto en SP-4); el job reporta el fallo |
| `iam:CreateRole`/`CreateOpenIDConnectProvider` no en la exec-policy | Verificación de la auditoría; ampliar exec-policy (no boundary) si falta |

---

## 6. Seguridad (el eje crítico — principal con poder de mutación a prod)

- **Trust scopeado `StringEquals`** — `mail-cd-deploy` solo asumible con `sub=environment:production` + `aud=sts.amazonaws.com`.
  Cero wildcard (CATASTRÓFICO en repo público: un PR de un fork podría asumir el rol de deploy).
- **Separación read/write** — `mail-cd-diff` (lookup-role, read-only) jamás muta; un PR no toca el rol de deploy.
- **Sin permisos de escalación** — los roles OIDC solo `sts:AssumeRole` sobre los `cdk-*`; cero `iam:Create*`/`cfn:*`
  directos. Toda mutación pasa por el cfn-exec-role con boundary. El boundary anti-escalación queda intacto.
- **Boundary adjunto a ambos roles OIDC** — defensa en profundidad.
- **Doble gate de approval** — GitHub Environment pausa + trust solo emite credenciales para `environment:production`.
- **`permissions` mínimos por job** — diff: `{id-token, contents, pull-requests:write}`; deploy: `{id-token, contents}`.
- **`pull-requests: write` es del GITHUB_TOKEN, NO del rol OIDC** — credenciales separadas (skill `pr-as-auditable-evidence`).
- **Kill-switch** — `aws iam delete-role` o quitar el trust corta el CD de inmediato (runbook).
- **Repo público** — gate NDA sobre workflows + CdStack + runbook (367707589526 publicable; verificar cero marcas prohibidas).

---

## 7. Testing

- **`cd_stack_test.go`** — template-asserts (como SP-1/2/3/4): el OIDC provider existe (url + clientId correctos),
  los 2 roles existen, los trust policies tienen el `sub` exacto con el operador correcto (deploy=`StringEquals`
  `environment:production`; diff scopeado al repo), boundary adjunto a ambos, permisos = solo `sts:AssumeRole` sobre
  los `cdk-*` esperados (deploy: 4 roles; diff: solo lookup).
- **`cd.yml`** — validación de sintaxis (`actionlint` si disponible; al menos que parsee).
- **Verificación e2e humana (post-bootstrap):** abrir un PR de prueba → el job `diff` corre, asume `mail-cd-diff`,
  comenta el diff. Mergear a main → el job `deploy` **PAUSA** pidiendo approval (el gate funciona), aprobar, despliega.
- **Smoke de seguridad:** `mail-cd-diff` NO puede mutar (assume + intentar deploy → AccessDenied), como verificamos
  que `mail-client-read` no enviaba en SP-4.

---

## 8. Definition of Done

1. `go build ./... && go test ./...` verde (incluye los template-asserts del CdStack).
2. CdStack synth + diff read-only OK (canario: solo crea el OIDC provider + 2 roles, no toca los 4 stacks existentes).
3. CdStack desplegado (humano, out-of-band) + `cd.yml` en su lugar + Environment `production` configurado.
4. Verificación e2e: un PR comenta su `cdk diff`; un merge a main PAUSA pidiendo approval; tras aprobar, despliega.
5. Smoke de seguridad: `mail-cd-diff` no puede mutar (AccessDenied empírico).
6. Runbook + CHANGELOG + diagrama + IAM JSON + memoria. Gate NDA limpio.
7. PR a develop con CI verde (Git Flow).

---

## 9. Non-goals (v0.1)

- Sin múltiples ambientes (dev/staging/prod) — cuenta única; `environment:production` es el único gate.
- Sin auto-bootstrap en el CD (`cdk bootstrap` es recurso de plataforma, out-of-band).
- Sin `workflow_dispatch` manual en v0.1 (on-merge a main + approval cubre el caso; dispatch es un posible v0.2).
- Sin rollback automatizado más allá del rollback-por-stack nativo de CFN.
- Sin notificaciones (Slack/email) del deploy — los logs de Actions + el comment del PR bastan para v0.1.

---

## 10. Disciplinas aplicables

`subproject-delivery-canonical` (este es su primer fogueo) · `adversarial-audit-before-new-pattern` (OIDC es patrón
nuevo → auditoría tras este spec) · `aws-cli-pre-flight-canonical` · `pr-as-auditable-evidence` (el diff comentado) ·
`engineering-audit-6-axes` (gate por tarea) · `control-subagents-in-worktrees-canonical` · gate NDA (repo público) ·
`feedback_cdk_permissions_boundary_intersects` (el boundary; verificado que NO deniega CreateRole, pero la exec-policy sí debe permitirlo).
