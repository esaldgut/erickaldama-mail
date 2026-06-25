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
| Boundary `erickaldama-boundary` (v4 live) | Deny statements: `route53domains/ec2/rds/organizations:*`, los `Put*PermissionsBoundary` (incl. **`PutRolePermissionsBoundary`**), y (excepción SP-4) `iam:CreateUser`/`CreateAccessKey` con NotResource. NO deniega `iam:CreateRole` PERO SÍ deniega `iam:PutRolePermissionsBoundary` → crear un rol CON boundary FALLA (hallazgo B4). Necesita **boundary v5** con excepción scoped antes del primer deploy del CdStack |
| OIDC provider en la cuenta | **NO existe** (verificado: `list-open-id-connect-providers` vacío) → crear, no importar |
| Construct OIDC | usar **L1 `CfnOIDCProvider`** (nativo, 2 roles) NO el L2 `NewOpenIdConnectProvider` (custom-resource + Lambda + 3er rol) |
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
Crea la infra IAM del CD, versionada y auditable. `NewCdStack(scope, id, props) awscdk.Stack`. Firmas **verificadas
compilando contra awscdk v2.258.1** (auditoría A). Helpers:
- **`addOidcProvider`** — usar el **L1 `awsiam.NewCfnOIDCProvider(stack, "GithubOidc", &awsiam.CfnOIDCProviderProps{
  Url: jsii.String("https://token.actions.githubusercontent.com"), ClientIdList: jsii.Strings("sts.amazonaws.com")})`**.
  **CRÍTICO (hallazgo A1):** el L2 `NewOpenIdConnectProvider` NO crea un provider nativo — sintetiza un custom-resource
  con un Lambda nodejs22.x + un 3er rol IAM (sin boundary). El L1 produce un `AWS::IAM::OIDCProvider` nativo, cero Lambda,
  exactamente 2 roles. ThumbprintList omitible (IAM autocompleta). Verificado vs AWS vivo: NO existe ya un provider
  (`list-open-id-connect-providers` vacío) → crear, no importar.
- **`addDiffRole`** (`mail-cd-diff`) — `NewRole` con `AssumedBy: awsiam.NewFederatedPrincipal(cfnOidc.AttrArn(),
  &conditions, jsii.String("sts:AssumeRoleWithWebIdentity"))` (el L1 expone `AttrArn()`, no `IOIDCProviderRef`, así que
  va `FederatedPrincipal` no `OpenIdConnectPrincipal` — trade-off del L1). Conditions: `StringEquals { ...:aud =
  sts.amazonaws.com }` y `StringLike { ...:sub = repo:esaldgut/erickaldama-mail:pull_request }` (**estrechado a
  pull_request por la auditoría B1** — la defensa que no depende de un toggle de admin). Permiso: `sts:AssumeRole` solo
  sobre el `cdk-*-lookup-role` vía `role.AddToPolicy(...)`. PermissionsBoundary adjunto.
- **`addDeployRole`** (`mail-cd-deploy`) — igual, con Conditions **`StringEquals`** (no StringLike):
  `...:aud = sts.amazonaws.com` Y `...:sub = repo:esaldgut/erickaldama-mail:environment:production`. Permiso:
  `sts:AssumeRole` sobre los 4 roles `cdk-*`. PermissionsBoundary adjunto. NI un PR NI otra rama lo asumen.
- **Boundary a nivel STACK** (defensa en profundidad, A2): además del `PermissionsBoundary` por rol, aplicar
  `awscdk.PermissionsBoundary_Of(stack).Apply(...)` / `StackProps.PermissionsBoundary` para cubrir todo el scope.
- El boundary se importa con `awsiam.ManagedPolicy_FromManagedPolicyName(stack, "Boundary", BoundaryManagedPolicyName)`.
- Constantes en `naming.go`: `OidcProviderUrl`, `GithubRepo`, `DiffRoleName`, `DeployRoleName`, los 4 ARNs de los
  roles `cdk-*`, el ARN del lookup-role.

**Sutileza gallina-huevo:** el PRIMER deploy del CdStack lo hace el humano out-of-band (el CD aún no existe para
auto-desplegarse). Después el CdStack es un stack más que el propio CD gestiona.

**🔴 DEPLOY FINDING ANTICIPADO (hallazgo B4) — el boundary v4 bloqueará el primer deploy:** crear un rol CON
`PermissionsBoundary` requiere `iam:PutRolePermissionsBoundary` en el cfn-exec-role (verbatim AWS Prescriptive Guidance
+ IAM User Guide: `CreateRole` + `PutRolePermissionsBoundary` van juntos). El `erickaldama-boundary` v4 DENIEGA
`PutRolePermissionsBoundary` (anti-escalación) → el deploy del CdStack fallará con AccessDenied al crear los roles con
boundary. La afirmación previa "v4 no deniega CreateRole → se crean sin tocar el boundary" es cierta SOLO para roles SIN
boundary. **FIX out-of-band (boundary es bootstrap-owned, como SP-4):** crear boundary **v5** con una excepción scoped —
`Allow iam:PutRolePermissionsBoundary` condicionado a `iam:PermissionsBoundary = arn:...:policy/erickaldama-boundary`
(solo se puede adjuntar ESE boundary, no escala). Tarea 0 del plan = aplicar el boundary v5 antes del primer deploy.
**La exec-policy NO necesita cambios** (ya tiene `iam:*` Allow que cubre CreateRole/CreateOpenIDConnectProvider — resuelto).

### 4.2 `.github/workflows/cd.yml` — el workflow (versiones de actions verificadas vivas, auditoría C)
- **Job `diff`** (`on: pull_request` a develop/rc/main):
  **`if: github.event.pull_request.head.repo.full_name == github.repository`** (fork guard, C2/C3 — los PRs de fork no
  obtienen OIDC ni pull-requests:write en repo público; sin el guard el check queda en rojo permanente. Es la barrera de
  seguridad funcionando, no un bug — un fork jamás obtiene credenciales AWS).
  `permissions: { id-token: write, contents: read, pull-requests: write }`,
  `actions/checkout@v4` + `actions/setup-go@v5` (go-version-file: go.mod, cache) + `actions/setup-node@v6` con
  **`node-version: 22`** (CDK v2 REQUIERE Node 22+, verbatim doc — C5; sin esto fallo latente) +
  `npm install -g aws-cdk@2.258.x`, `aws-actions/configure-aws-credentials@v6` con `role-to-assume: <mail-cd-diff ARN>`
  + `aws-region: us-east-1`, `cdk diff --all 2>&1 | tee diff.txt`, **comenta el diff en el PR** vía
  `actions/github-script@v9` (upsert: list comments → find marker `<!-- cdk-diff-bot -->` → update/create).
  Aplica skill `pr-as-auditable-evidence`.
- **Job `deploy`** (`on: push` a main — un merge a main, incl. squash/rebase, dispara push y activa este job, C1):
  `environment: production` (el GATE), `concurrency: { group: deploy-production, cancel-in-progress: false }`,
  `permissions: { id-token: write, contents: read }`, `checkout@v4` + `setup-go@v5` + `setup-node@v6` con
  `node-version: 22` + `npm install -g aws-cdk@2.258.x`, `configure-aws-credentials@v6` con
  `role-to-assume: <mail-cd-deploy ARN>`, `cdk deploy --all --require-approval never` (respeta dependencias
  Receiving→MailStorage, no necesita synth previo ni `--ci`, rollback-por-stack activo por default — C6).
  Verifica en logs que hubo cambios reales (no "no changes" silencioso).
- **Matiz de concurrency (C4, documentar en runbook):** `cancel-in-progress: false` deja terminar el deploy en curso;
  la cola retiene solo 1 pending — si se mergean 2 PRs a main durante un deploy, el pending intermedio se cancela. El
  estado final converge (cada deploy es `--all` del HEAD), solo ese commit no tuvo su run verde propio.

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
| **El boundary v4 deniega `PutRolePermissionsBoundary` → crear roles con boundary falla (B4)** | **Boundary v5 con excepción scoped (Task 0, out-of-band) ANTES del primer deploy del CdStack** |
| L2 OIDC provider crea Lambda + 3er rol sin boundary (A1) | Usar L1 `CfnOIDCProvider` (nativo, 2 roles) — ya en §4.1 |
| Node <22 en el runner | `node-version: 22` explícito (CDK v2 lo requiere, C5) |
| PRs de fork → check diff en rojo | fork guard `if:` en el job diff (C2/C3) |
| exec-policy: `CreateRole`/`CreateOpenIDConnectProvider` | **Resuelto** — la exec-policy ya tiene `iam:*` Allow; NO ampliarla |

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

- **`cd_stack_test.go`** — template-asserts (como SP-1/2/3/4): el `AWS::IAM::OIDCProvider` nativo existe (url +
  clientId correctos — con el L1 NO hay Lambda ni custom-resource), **exactamente 2 `AWS::IAM::Role`** (con el L1; el
  L2 daría 3 — verificado), los trust policies tienen el `sub` exacto con el operador correcto (deploy=`StringEquals`
  `environment:production`; diff=`StringLike` `pull_request`), boundary adjunto a ambos roles, permisos = solo
  `sts:AssumeRole` sobre los `cdk-*` esperados (deploy: 4 roles; diff: solo lookup). El `AssumeRolePolicyDocument` se
  asserta con `Match_ObjectLike` anidado (verificado igual que sending_stack_test.go).
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
