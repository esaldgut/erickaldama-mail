# CD Pipeline — Deploy Runbook

> Subproyecto transversal (tarea #23): el CD que automatiza `cdk deploy` a AWS vía OIDC sin access keys
> de larga vida, con approval manual a producción.
> **Cuenta** ErickSA `367707589526` / **región** `us-east-1`. **Repo** `esaldgut/erickaldama-mail` (público, Git Flow).
> **Worktree:** `worktree-cd-pipeline` / rama `worktree-cd-pipeline`.
> **Runbook documentado:** 2026-06-24. Canario verificado previo al primer deploy.

---

## Resumen del CD

El CD automatiza el comando `cdk deploy`, NO el juicio. Lo que hoy se hace a mano (el humano corre `cdk deploy` con
SSO Admin) lo ejecuta GitHub Actions con credenciales temporales OIDC — **sin access keys de larga vida en el repo**.
El gate de aprobación humana se preserva en **dos capas independientes**:

1. **GitHub Environment `production`:** el job `deploy` queda en estado "Waiting" hasta que el humano aprueba en
   la UI de Actions (o la API).
2. **Trust OIDC `StringEquals`:** AWS solo emite credenciales de deploy si el token OIDC tiene
   `sub=repo:esaldgut/erickaldama-mail:environment:production`. Aunque alguien evadiera GitHub, AWS rechaza la
   emisión si el contexto no coincide exactamente — cero wildcard.

Ninguna de las dos capas depende de la otra para sostener el gate: si GitHub Environment se desactiva, el trust OIDC
sigue siendo la segunda barrera; si el rol se elimina (kill-switch), el Environment gate ya no importa.

### Qué automatiza

| Evento | Job | Principal OIDC | Acción |
|---|---|---|---|
| PR abierto / actualizado | `diff` (on: pull_request) | `mail-cd-diff` (lookup-role, read-only) | `cdk diff` → comenta en el PR |
| Merge a `main` + approval | `deploy` (on: push → main) | `mail-cd-deploy` (4 roles cdk-*) | `cdk deploy --all --require-approval never` |

El job `diff` es un **preview no-bloqueante**: muestra qué cambiaría en los stacks antes del merge, como evidencia
de auditoría (skill `pr-as-auditable-evidence`). Nunca muta.

El job `deploy` **PAUSA** (estado "Waiting") tras el merge a main y solo corre tras la aprobación humana explícita.
Este es el 4º principal con poder de mutación a producción en el proyecto (junto a Admin SSO, cfn-exec-role y, en
scope limitado, el SES sender de SP-4).

### Stacks que gestiona el CD (5 en total tras este subproyecto)

| Stack | Contenido | Primera vez desplegado |
|---|---|---|
| `FoundationStack` | DynamoDB mail-index, S3 raw, usuarios IAM cliente | SP-4 (manual) |
| `SendingStack` | SES identity, config-set, mail-sender | SP-4 (manual) |
| `MailStorageStack` | Retention policy S3, TTL DynamoDB | SP-2 (manual) |
| `ReceivingStack` | SES receipt rule, Lambda, SNS | SP-3 (manual) |
| `CdStack` | OIDC provider, mail-cd-diff, mail-cd-deploy | **este subproyecto — bootstrap out-of-band** |

---

## 0. Pre-flight

**Antes de cualquier `aws`/`cdk` que toque recursos:** verificar identity y sesión SSO.

Disciplina: `aws-cli-pre-flight-canonical`.

```bash
aws sts get-caller-identity --profile AdministratorAccess-367707589526 --output json
```

Salida esperada:

```json
{
    "UserId": "AROA...",
    "Account": "367707589526",
    "Arn": "arn:aws:sts::367707589526:assumed-role/AWSReservedSSO_AdministratorAccess_.../admin@esaldgut"
}
```

Si la sesión SSO expiró (`InvalidClientTokenId` / `no credentials have been configured`):

```bash
aws sso login --profile AdministratorAccess-367707589526
```

Volver a verificar con `get-caller-identity` antes de continuar.

**Condiciones de abort:** si `Account` no es `367707589526`, detener. El deploy en la cuenta equivocada es
irreversible para varios recursos.

---

## 1. Canario pre-deploy (verificado — no repetir salvo rollback)

El canario fue ejecutado por el controller antes de la fase de deploy. Resultados:

### Suite completa

```bash
go build ./... && go test -count=1 ./...
```

Resultado (VERDE, 5 paquetes):

```
ok      github.com/esaldgut/erickaldama-mail/internal/infra     0.XXXs
ok      github.com/esaldgut/erickaldama-mail/internal/mailbox   0.XXXs
ok      github.com/esaldgut/erickaldama-mail/internal/message   0.XXXs
ok      github.com/esaldgut/erickaldama-mail/internal/redact    0.XXXs
ok      github.com/esaldgut/erickaldama-mail/test/hook          0.XXXs
```

`go vet ./...` — 0 issues. `gofmt -l internal/ cmd/` — 0 archivos con formato incorrecto.

### cdk diff (stacks existentes — canario de no-regresión)

```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk diff FoundationStack SendingStack MailStorageStack ReceivingStack
```

Resultado: **"There were no differences"** en los 4 stacks existentes. El CdStack NO los toca — canario
correcto: el diff del nuevo stack está aislado.

### cdk diff CdStack (preview del deploy)

```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk diff CdStack
```

Recursos que **se crearán** (exactamente estos, nada más):

```
+ AWS::IAM::OIDCProvider  GithubOidc
+ AWS::IAM::Role          DiffRole    (mail-cd-diff)
+ AWS::IAM::Role          DeployRole  (mail-cd-deploy)
+ AWS::IAM::ManagedPolicy DiffRoleDefaultPolicy
+ AWS::IAM::ManagedPolicy DeployRoleDefaultPolicy
```

**Verificación clave:** exactamente 2 `AWS::IAM::Role` (el L1 `CfnOIDCProvider` nativo, 0 Lambda, 0 custom-resource).
El L2 `NewOpenIdConnectProvider` daría un 3er rol y una función Lambda nodejs22.x sin boundary — por eso se usa L1.

---

## 2. Task 0 — Boundary v5 → v6 (dos deploy findings de boundary)

**TAREA DEL HUMANO — out-of-band — antes del primer `cdk deploy CdStack`.**

Esta sección documenta **dos hallazgos reales** que requirieron dos versiones sucesivas del boundary (`v5` y `v6`).
El archivo `iam/erickaldama-boundary.json` en disco contiene **ambos** statements aplicados. Al crear una nueva
versión del boundary en un entorno limpio, se obtendrá la siguiente versión secuencial disponible (no
necesariamente "v5" o "v6" — depende del historial de versiones de esa cuenta).

---

### Finding #1 (B4) — `iam:PutRolePermissionsBoundary` denegado (→ boundary v5)

**Causa:** el boundary v4 original tiene el statement `DenyEscalationAndOutOfScope` que deniega
`iam:PutRolePermissionsBoundary`. Al crear roles CON `PermissionsBoundary` adjunto (como hacen `mail-cd-diff`
y `mail-cd-deploy`), CloudFormation llama a `CreateRole` + `PutRolePermissionsBoundary`. Aunque el cfn-exec-role
tiene `iam:*` Allow en su exec-policy, el boundary **intersecta** (no une) — el Deny de la acción prevalece.

**Error observado en el primer `cdk deploy CdStack`:**

```
User: arn:aws:sts::367707589526:assumed-role/cdk-hnb659fds-cfn-exec-role-... is not authorized to perform:
iam:PutRolePermissionsBoundary on resource: arn:aws:iam::367707589526:role/mail-cd-diff
with an explicit deny in a permissions boundary: arn:aws:iam::367707589526:policy/erickaldama-boundary
```

**Fix — boundary v5:** añadir el statement `DenyPutRoleBoundaryExceptErickaldamaBoundary` que convierte el Deny
global en un Deny condicional: solo deniega `PutRolePermissionsBoundary` si el boundary que se adjunta **no es**
`erickaldama-boundary` (vía `StringNotEqualsIfExists`). Esto permite que el cfn-exec-role adjunte el propio
boundary al crear los roles.

**Resultado:** re-deploy con v5 live → **7/7 CREATE_COMPLETE** (los roles con boundary se crearon OK → B4 confirmado).

---

### Finding #2 (A1) — `sts:AssumeRole` denegado por el boundary (→ boundary v6)

**Detectado por:** el smoke de seguridad (`simulate-principal-policy`, §6) tras el deploy exitoso del CdStack.
Evidencia: `PermissionsBoundaryDecision.AllowedByPermissionsBoundary=false` para `sts:AssumeRole` en los 4 roles
`cdk-hnb659fds-*`.

**Causa:** el boundary v5 no contenía ningún `Allow` explícito para `sts:AssumeRole`. La lógica
"boundary intersecta" implica que el effective permission = policy ∩ boundary. Aunque `mail-cd-deploy` tiene
`sts:AssumeRole` en su inline policy (hacia los 4 roles cdk-*), el boundary v5 no permitía esa acción →
effective = denegado. Consecuencia: el job `deploy` habría fallado en runtime al intentar asumir los roles CDK
bootstrap, incluso con el deploy del CdStack exitoso.

**Fix — boundary v6:** añadir el statement `AllowAssumeCdkBootstrapRoles`: `sts:AssumeRole` con `Allow`,
scoped exactamente a los 4 ARNs `cdk-hnb659fds-*` (menor privilegio — no wildcard en Resource):

```json
{
  "Sid": "AllowAssumeCdkBootstrapRoles",
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": [
    "arn:aws:iam::367707589526:role/cdk-hnb659fds-deploy-role-367707589526-us-east-1",
    "arn:aws:iam::367707589526:role/cdk-hnb659fds-file-publishing-role-367707589526-us-east-1",
    "arn:aws:iam::367707589526:role/cdk-hnb659fds-image-publishing-role-367707589526-us-east-1",
    "arn:aws:iam::367707589526:role/cdk-hnb659fds-lookup-role-367707589526-us-east-1"
  ]
}
```

**Nota sobre el límite de versiones IAM:** IAM permite máximo 5 versiones por policy. Si ya hay 5 versiones,
hay que borrar la más vieja no-default antes de crear la nueva:

```bash
# Borrar la versión más vieja no-default (ejemplo: v1)
aws iam delete-policy-version \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --version-id v1 \
  --profile AdministratorAccess-367707589526
```

**Resultado tras v6:** el smoke pasó — `mail-cd-deploy` puede asumir los 4 roles cdk-* (`allowed`);
`mail-cd-diff` solo puede asumir el lookup (`allowed`); `mail-cd-diff` NO puede asumir deploy ni publishing
(`implicitDeny`). Separación read/write probada en vivo (ver §6).

---

### Aplicar el boundary actual (contiene ambos statements)

El archivo `iam/erickaldama-boundary.json` en disco ya contiene tanto `DenyPutRoleBoundaryExceptErickaldamaBoundary`
(finding #1) como `AllowAssumeCdkBootstrapRoles` (finding #2). Aplicarlo en un entorno limpio:

```bash
# 1. Verificar cuántas versiones hay (límite 5; si hay 5, borrar la más vieja no-default)
aws iam list-policy-versions \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --profile AdministratorAccess-367707589526 \
  --query 'Versions[].{V:VersionId,Default:IsDefaultVersion}' \
  --output table

# 2. Crear la nueva versión con ambos statements y hacerla default
aws iam create-policy-version \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --policy-document file://iam/erickaldama-boundary.json \
  --set-as-default \
  --profile AdministratorAccess-367707589526
```

Salida esperada (la versión resultante depende del historial; será la siguiente secuencial disponible):

```json
{ "PolicyVersion": { "VersionId": "vN", "IsDefaultVersion": true, "CreateDate": "..." } }
```

**Verificación read-only antes del deploy (validar, no asumir):**

```bash
aws iam get-policy-version \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --version-id vN \
  --profile AdministratorAccess-367707589526 \
  --query 'PolicyVersion.Document.Statement[*].Sid' \
  --output json
# Debe incluir AMBOS:
# "DenyPutRoleBoundaryExceptErickaldamaBoundary"
# "AllowAssumeCdkBootstrapRoles"
```

Tras confirmar ambos Sids presentes, el deploy del CdStack puede proceder. El smoke de seguridad (§6) verifica
empíricamente la separación read/write — ejecutarlo siempre después del bootstrap.

---

## 3. Bootstrap out-of-band del CdStack

**TAREA DEL HUMANO — el CD no puede auto-desplegarse (aún no existe).**

Este es el único deploy del CdStack que se hace a mano. Después del bootstrap exitoso, el propio CD gestiona el
CdStack como un stack más en el `cdk deploy --all`.

### Sutileza gallina-huevo

El job `deploy` del CD necesita que `mail-cd-deploy` exista. El rol `mail-cd-deploy` lo crea el CdStack. El
CdStack lo despliega el humano la primera vez. Es el patrón bootstrap canónico del proyecto (cf. SP-1 boot).

### Comando de bootstrap

```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk deploy CdStack --require-approval never
```

Por qué `--require-approval never`: el CDK con el flag default `broadening` exige un TTY para confirmar cambios
de seguridad. En operaciones no interactivas (y como prevención del incidente #1 de SP-4) se usa `never` cuando
el diff ya fue auditado previamente (el canario de §1 lo auditó).

### Salida esperada

```
CdStack | CREATE_IN_PROGRESS | AWS::IAM::OIDCProvider | GithubOidc
CdStack | CREATE_COMPLETE    | AWS::IAM::OIDCProvider | GithubOidc
CdStack | CREATE_IN_PROGRESS | AWS::IAM::Role          | DiffRole
CdStack | CREATE_IN_PROGRESS | AWS::IAM::Role          | DeployRole
CdStack | CREATE_COMPLETE    | AWS::IAM::Role          | DiffRole
CdStack | CREATE_COMPLETE    | AWS::IAM::Role          | DeployRole
 ✅  CdStack
```

### Verificación post-bootstrap

```bash
# El OIDC provider existe
aws iam list-open-id-connect-providers \
  --profile AdministratorAccess-367707589526 \
  --query 'OpenIDConnectProviderList[].Arn'
# Debe incluir: arn:aws:iam::367707589526:oidc-provider/token.actions.githubusercontent.com

# Los roles existen
aws iam get-role --role-name mail-cd-diff \
  --profile AdministratorAccess-367707589526 \
  --query 'Role.Arn'
# → arn:aws:iam::367707589526:role/mail-cd-diff

aws iam get-role --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526 \
  --query 'Role.Arn'
# → arn:aws:iam::367707589526:role/mail-cd-deploy

# Los trust policies son los esperados (auditables en iam/)
aws iam get-role --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526 \
  --query 'Role.AssumeRolePolicyDocument' \
  --output json
# Debe coincidir con iam/cd-deploy-trust.json: StringEquals environment:production
```

### Deploy Findings — resumen del bootstrap real

El bootstrap del CdStack (2026-06-24) generó 4 deploy findings, los 2 de boundary durante el deploy
(`CREATE_COMPLETE 7/7`) y los 2 del CLI/diff-flag cazados por el primer run del CD en PR #6.
Los hallazgos completos (evento/error/causa/fix/resultado) están documentados en **§11** de este mismo runbook.

---

## 4. GATE CRÍTICO del Environment `production`

**TAREA DEL HUMANO — before any push to main.**

Este es el gate más crítico del runbook. Si se omite o se configura mal, el deploy corre **sin aprobación humana**
— el principal objetivo de seguridad del CD queda anulado.

### Por qué es crítico

GitHub auto-crea el environment `production` cuando el workflow referencia `environment: production` **si no existe
pre-configurado**. El environment auto-creado viene **vacío**: sin required reviewers, sin deployment branch rules.
El job `deploy` corre inmediatamente tras el merge a main, sin pausa.

`cdk deploy` en el workflow usa `--require-approval never` (necesario en CI — cf. incidente #1 SP-4). Si el
Environment está vacío, **no hay ningún gate humano entre el merge y la mutación a producción**.

### Configuración paso a paso (UI de GitHub)

1. Ir a `https://github.com/esaldgut/erickaldama-mail/settings/environments`
2. Crear (o editar si fue auto-creado) el environment **`production`**
3. En **"Required reviewers"**: añadir tu usuario (`esaldgut`) — 1 reviewer basta
4. **"Prevent self-review"**: **DESACTIVADO** (single-dev; con 1 reviewer y self-review habilitado nunca se podría aprobar)
5. En **"Deployment branches and tags"**: seleccionar "Selected branches and tags" → añadir `main`
6. Guardar

### Verificación por API (antes de cualquier push a main)

```bash
gh api repos/esaldgut/erickaldama-mail/environments/production \
  --jq '.protection_rules'
```

Salida esperada (required_reviewers NO vacío):

```json
[
  {
    "id": 123456,
    "type": "required_reviewers",
    "reviewers": [
      {
        "type": "User",
        "reviewer": { "login": "esaldgut" }
      }
    ]
  }
]
```

Si la salida es `[]` o el campo `required_reviewers` está vacío: **NO hacer push a main**. Configurar el
environment primero.

### Verificación adicional — deployment branch rule

```bash
gh api repos/esaldgut/erickaldama-mail/environments/production \
  --jq '.deployment_branch_policy'
```

Debe indicar `"protected_branches": false, "custom_branch_policies": true` (o el equivalente con main protegido).
Si es `null`, el environment acepta deploy desde cualquier rama — configurar la regla.

---

## 5. Verificación OIDC end-to-end

Esta sección documenta la verificación funcional completa del CD tras el bootstrap del CdStack y la configuración
del Environment.

### 5a. Job `diff` — PR de prueba

Abrir un PR de cualquier feature branch a `develop` (o a `main` directamente para la prueba):

```
# El job diff corre automáticamente (on: pull_request)
# Revisar Actions tab → job "diff":
# - checkout OK
# - configure-aws-credentials: asume mail-cd-diff (role-to-assume)
# - cdk diff: muestra los stacks y sus cambios (o "no differences")
# - github-script: crea/actualiza el comment con el diff
```

Evidencia esperada: el PR recibe un comment del bot con el cdk diff, marcado con `<!-- cdk-diff-bot -->`.

**Nota sobre PRs de fork:** el job `diff` tiene un fork guard:

```yaml
if: github.event.pull_request.head.repo.full_name == github.repository
```

Los PRs de forks externos no obtienen OIDC ni `pull-requests:write` en repos públicos — el job hace skip.
Esto es la barrera de seguridad funcionando correctamente, no un bug. **Por esta razón el job `diff` NO debe
marcarse como "required status check"** en las branch protection rules de develop/main: un PR de fork lo dejaría
en estado `skipped`, que bloquearía el PR indefinidamente.

### 5b. Job `deploy` — merge a main + approval

Después de que el PR se aprueba y se mergea a `main`:

```
# El push a main dispara el job "deploy"
# En Actions tab: el job queda en estado "Waiting" (Environment gate activo)
# Aparece el botón "Review deployments" o "Approve"

# APROBAR en la UI de GitHub Actions (o API):
gh api repos/esaldgut/erickaldama-mail/actions/runs/<RUN_ID>/pending_deployments \
  -X POST \
  -f environment_ids[]='<ENV_ID>' \
  -f state='approved' \
  -f comment='Approved by human reviewer — deployment verified'
```

Tras la aprobación, el job reanuda:

```
deploy | configure-aws-credentials: asumiendo mail-cd-deploy
deploy | cdk deploy --all --require-approval never
deploy | FoundationStack: no changes
deploy | SendingStack: no changes
deploy | MailStorageStack: no changes
deploy | ReceivingStack: no changes
deploy | CdStack: no changes (o los cambios del PR)
deploy | ✅ Deployment complete
```

---

## 6. Smoke de seguridad empírico

Verificar que `mail-cd-diff` **no puede mutar** y que `mail-cd-deploy` **no es asumible desde un PR**.

Disciplina: los permisos correctos se verifican empíricamente, no solo en el papel (cf. SP-4 §5).

### 6a. `mail-cd-diff` no puede asumir el deploy-role

```bash
# Simular que mail-cd-diff intenta asumir mail-cd-deploy
aws iam simulate-principal-policy \
  --policy-source-arn arn:aws:iam::367707589526:role/mail-cd-diff \
  --action-names sts:AssumeRole \
  --resource-arns arn:aws:iam::367707589526:role/mail-cd-deploy \
  --profile AdministratorAccess-367707589526 \
  --query 'EvaluationResults[].{Action:EvalActionName,Decision:EvalDecision}' \
  --output table
```

Resultado esperado: `implicitDeny` (o `explicitDeny`). Si dice `allowed`: revisar la inline policy del rol diff.

### 6b. `mail-cd-diff` puede asumir el lookup-role

```bash
aws iam simulate-principal-policy \
  --policy-source-arn arn:aws:iam::367707589526:role/mail-cd-diff \
  --action-names sts:AssumeRole \
  --resource-arns arn:aws:iam::367707589526:role/cdk-hnb659fds-lookup-role-367707589526-us-east-1 \
  --profile AdministratorAccess-367707589526 \
  --query 'EvaluationResults[].{Action:EvalActionName,Decision:EvalDecision}' \
  --output table
```

Resultado esperado: `allowed`.

### 6c. `mail-cd-deploy` puede asumir los 4 roles cdk-*

```bash
for ROLE in deploy-role-367707589526-us-east-1 file-publishing-role-367707589526-us-east-1 \
            image-publishing-role-367707589526-us-east-1 lookup-role-367707589526-us-east-1; do
  aws iam simulate-principal-policy \
    --policy-source-arn arn:aws:iam::367707589526:role/mail-cd-deploy \
    --action-names sts:AssumeRole \
    --resource-arns "arn:aws:iam::367707589526:role/cdk-hnb659fds-${ROLE}" \
    --profile AdministratorAccess-367707589526 \
    --query "EvaluationResults[].{Role:'${ROLE}',Decision:EvalDecision}" \
    --output table
done
```

Resultado esperado: `allowed` para los 4.

### 6d. Verificar que el trust del deploy-role rechaza tokens sin `environment:production`

El trust de `mail-cd-deploy` usa `StringEquals` (no `StringLike`) en el `sub`. Un PR (sub=`pull_request`)
**no puede** asumir el rol de deploy — verificar que el trust policy en AWS coincide con `iam/cd-deploy-trust.json`:

```bash
aws iam get-role --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526 \
  --query 'Role.AssumeRolePolicyDocument.Statement[0].Condition' \
  --output json
```

Salida esperada:

```json
{
    "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
        "token.actions.githubusercontent.com:sub": "repo:esaldgut/erickaldama-mail:environment:production"
    }
}
```

Si hay un `StringLike` o wildcard en el `sub`: **STOP — riesgo crítico**. Cualquier PR podría obtener credenciales
de deploy. Corregir el trust policy antes de continuar.

---

## 7. Matiz de concurrency

El workflow tiene:

```yaml
concurrency:
  group: deploy-production
  cancel-in-progress: false
```

**Por qué `cancel-in-progress: false`:** cancela el job **en espera** (el siguiente que llegó), NO el que está
corriendo. Si se cancelara el deploy en curso, el stack quedaría en estado inconsistente (CloudFormation en
rollback parcial). Con `false`, el deploy en curso termina; solo el pending intermedio se cancela.

**Consecuencia práctica:** si se mergean 2 PRs a `main` mientras hay un deploy activo:

```
PR A mergea → deploy A corre
PR B mergea → deploy B encola (pending)
PR C mergea → deploy C cancela a B (solo 1 pending permitido); C encola
deploy A termina → deploy C corre
```

El estado final converge (cada deploy es `--all` desde HEAD), pero el commit de PR B no tiene su run verde propio
en Actions. Esto es aceptable para single-dev en v0.1 — documentar si se convierte en problema.

---

## 8. Kill-switch

Si se detecta un compromiso del CD o se necesita revocar el acceso de GitHub Actions a AWS de forma inmediata:

### Opción 1 (rápida) — Eliminar el trust del rol de deploy

```bash
# Reemplazar el trust policy por uno vacío (no asumible)
aws iam update-assume-role-policy \
  --role-name mail-cd-deploy \
  --policy-document '{"Version":"2012-10-17","Statement":[]}' \
  --profile AdministratorAccess-367707589526

# Verificar que el trust quedó vacío
aws iam get-role --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526 \
  --query 'Role.AssumeRolePolicyDocument.Statement'
# → []
```

El CD no puede emitir credenciales de deploy de inmediato. El job fallará en el step `configure-aws-credentials`.

### Opción 2 (irreversible) — Eliminar el rol

```bash
# Primero, desadjuntar las policies inline
aws iam list-role-policies --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526

aws iam delete-role-policy --role-name mail-cd-deploy \
  --policy-name <inline-policy-name> \
  --profile AdministratorAccess-367707589526

# Luego borrar el rol
aws iam delete-role --role-name mail-cd-deploy \
  --profile AdministratorAccess-367707589526
```

Para recuperarlo: `cdk deploy CdStack` (out-of-band, como en §3).

### Opción 3 (GitHub-side) — Deshabilitar el Environment

En `https://github.com/esaldgut/erickaldama-mail/settings/environments`, eliminar o deshabilitar `production`.
El job deploy queda bloqueado aunque tenga credenciales AWS. Es la barrera de la capa 1.

### Opción 4 (GitHub-side) — Deshabilitar el workflow

```bash
gh workflow disable cd.yml --repo esaldgut/erickaldama-mail
```

El job no se disparará aunque haya un push a main.

**Recomendación:** en una emergencia real, combinar Opción 1 (AWS) + Opción 4 (GitHub) — corte inmediato en ambas
capas. La Opción 2 se usa si se sospecha que el rol fue comprometido y se quiere evidencia forense antes de
recrearlo.

---

## 9. Notas de seguridad adicionales

### El job `diff` NO debe ser required status check

**Importante:** no marcar el job `diff` como "required status check" en las branch protection rules de `develop`
o `main`.

PRs de forks externos tienen el fork guard activo:

```yaml
if: github.event.pull_request.head.repo.full_name == github.repository
```

En repos públicos, un PR de fork no puede obtener OIDC ni `pull-requests:write`. El job hace skip (`skipped`).
GitHub trata `skipped` distinto a `success` — un required check en `skipped` **bloquea el PR**. El PR de un
colaborador externo quedaría bloqueado indefinidamente aunque su código sea correcto.

Esto no es un bug: es la barrera de seguridad funcionando. Los forks no obtienen credenciales AWS — exactamente
el diseño correcto. Solo es importante no convertirlo en un bloqueo de PR innecesario.

### `pull-requests:write` es del GITHUB_TOKEN, no del rol OIDC

Los dos jobs usan credenciales separadas:

- **OIDC (`id-token: write`):** para asumir roles AWS. Las credenciales AWS temporales van a `configure-aws-credentials`.
- **GITHUB_TOKEN (`pull-requests:write` en el job diff):** para comentar en el PR. Es el token de la Actions app,
  no tiene permisos AWS.

Esta separación es intencional (skill `pr-as-auditable-evidence`): el comentario del diff en el PR es una acción
de GitHub, no de AWS. El rol `mail-cd-diff` no tiene ni necesita permisos de GitHub API.

### Node 22 — requerido para CDK v2

```yaml
- uses: actions/setup-node@v6
  with:
    node-version: 22
```

CDK v2 requiere Node 22+ (verificado en doc oficial). Sin esto el runner podría tener Node 20 o anterior y fallar
en el `cdk diff` / `cdk deploy` con un error latente difícil de diagnosticar.

### Version pinning del CDK CLI

```bash
npm install -g aws-cdk@2.1128.1
```

**IMPORTANTE:** el CDK CLI npm y la librería Go `awscdk/v2` **usan esquemas de versión distintos**. La lib Go
está en `v2.258.1` (esquema `2.X.Y`); el CLI npm sigue el esquema `2.1xxx.x` (actualmente `2.1128.1`).
Instalar el CLI npm con la versión de la lib Go (ej. `@2.258.1`) genera `npm error notarget No matching version
found` — esa versión no existe en el registro npm (deploy finding #3, cazado por el CD en su primer run).

El pin correcto para el workflow es `CDK_VERSION=2.1128.1`. Actualizar el CLI npm cuando la versión de la lib Go
suba: verificar la última versión disponible con `npm view aws-cdk versions --json | tail -5`. El skew CLI < lib
genera el error "stack not bootstrapped" (hallazgo SP-1) — engañoso porque el bootstrap sí existe, pero el CLI
no reconoce el schema de la lib más nueva.

---

## 10. Quick Reference

| Acción | Comando |
|---|---|
| Pre-flight identity | `aws sts get-caller-identity --profile AdministratorAccess-367707589526` |
| Sesión SSO expirada | `aws sso login --profile AdministratorAccess-367707589526` |
| Aplicar boundary (v5→v6, ambos findings) | `aws iam create-policy-version --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary --policy-document file://iam/erickaldama-boundary.json --set-as-default --profile AdministratorAccess-367707589526` |
| Ver versiones del boundary | `aws iam list-policy-versions --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary --profile AdministratorAccess-367707589526` |
| Bootstrap CdStack | `AWS_PROFILE=AdministratorAccess-367707589526 cdk deploy CdStack --require-approval never` |
| Verificar Environment gate | `gh api repos/esaldgut/erickaldama-mail/environments/production --jq '.protection_rules'` |
| Verificar OIDC provider | `aws iam list-open-id-connect-providers --profile AdministratorAccess-367707589526` |
| Smoke diff-role no puede deploy | `aws iam simulate-principal-policy --policy-source-arn arn:aws:iam::367707589526:role/mail-cd-diff --action-names sts:AssumeRole --resource-arns arn:aws:iam::367707589526:role/mail-cd-deploy --profile AdministratorAccess-367707589526` |
| Kill-switch rápido | `aws iam update-assume-role-policy --role-name mail-cd-deploy --policy-document '{"Version":"2012-10-17","Statement":[]}' --profile AdministratorAccess-367707589526` |
| Kill-switch rol completo | `aws iam delete-role --role-name mail-cd-deploy --profile AdministratorAccess-367707589526` |
| Deshabilitar workflow | `gh workflow disable cd.yml --repo esaldgut/erickaldama-mail` |

**Cuenta** `367707589526` · **región** `us-east-1` · **boundary** v6 live (2 deploy findings: v5=B4/PutRolePermissionsBoundary, v6=A1/sts:AssumeRole; ver §2) · **repo** `esaldgut/erickaldama-mail` (público).

---

## 11. Deploy Findings

> El patrón de documentación sigue el de SP-4-DEPLOY.md §3: evento, error exacto, causa raíz, fix, resultado.
> Ambos findings ocurrieron durante el bootstrap real del CdStack (2026-06-24).

### Finding B4 — `iam:PutRolePermissionsBoundary` AccessDenied (→ boundary v5)

- **Evento:** primer `cdk deploy CdStack` con boundary v4 live
- **Error:** `User: ...assumed-role/cdk-hnb659fds-cfn-exec-role-... is not authorized to perform: iam:PutRolePermissionsBoundary` (explicit deny in permissions boundary)
- **Causa raíz:** el boundary v4 denegaba `iam:PutRolePermissionsBoundary` globalmente; al crear roles con `PermissionsBoundary`, CloudFormation necesita esa acción. El boundary intersecta (no une) con la exec-policy.
- **Fix:** boundary v5 — statement `DenyPutRoleBoundaryExceptErickaldamaBoundary` con `StringNotEqualsIfExists`: convierte el Deny global en Deny condicional (solo si el boundary adjunto no es `erickaldama-boundary`).
- **Resultado:** re-deploy con v5 → **7/7 CREATE_COMPLETE** (DiffRole + DeployRole creados con boundary). Finding B4 confirmado en vivo.
- **Status:** RESUELTO (anticipado en la auditoría del spec)

### Finding A1 — `sts:AssumeRole` denegado por el boundary (→ boundary v6)

- **Evento:** smoke de seguridad post-deploy (§6c) — `simulate-principal-policy` sobre `mail-cd-deploy` intentando asumir los 4 roles `cdk-hnb659fds-*`
- **Síntoma:** `PermissionsBoundaryDecision.AllowedByPermissionsBoundary=false` para `sts:AssumeRole`
- **Causa raíz:** el boundary v5 no incluía ningún `Allow` para `sts:AssumeRole`. Effective permission = policy ∩ boundary → aunque `mail-cd-deploy` tiene `sts:AssumeRole` en su inline policy, el boundary lo filtraba. El job `deploy` habría fallado en runtime al intentar asumir los roles CDK bootstrap.
- **Fix:** boundary v6 — statement `AllowAssumeCdkBootstrapRoles`: `sts:AssumeRole` con `Allow`, scoped a exactamente los 4 ARNs `cdk-hnb659fds-*`. Aplicado out-of-band; requirió borrar la versión más vieja (límite de 5 versiones IAM).
- **Resultado tras v6:** smoke §6 pasó completamente — `mail-cd-deploy` asume los 4 cdk-* (`allowed`); `mail-cd-diff` solo asume lookup (`allowed`); `mail-cd-diff` no puede asumir deploy ni publishing (`implicitDeny`). Separación read/write verificada en vivo.
- **Status:** RESUELTO (detectado por smoke; no habría sido evidente sin `simulate-principal-policy`)
- **Lección:** el smoke de seguridad (§6) es obligatorio antes de declarar el CD operativo — el boundary puede filtrar acciones que la inline policy sí permite.

### Finding #3 — CLI version skew: versión de la lib Go no existe en npm como CLI

- **Evento:** primer run del CD en GitHub Actions (PR #6) — step `npm install -g aws-cdk@$CDK_VERSION` con `CDK_VERSION` seteado a la versión de la lib Go (`v2.258.1`)
- **Error:** `npm error notarget No matching version found for aws-cdk@<versión-lib-go>`
- **Causa raíz:** el CDK CLI npm usa el esquema de versión `2.1xxx.x` (ej. `2.1128.1`), **completamente distinto** del esquema de la lib Go `awscdk/v2` (que es `v2.258.1`). Los dos esquemas no comparten números — la versión de la lib Go no es una versión válida del CLI npm. Este es un nuevo modo de fallo del hallazgo SP-1 "CLI vs lib version skew": en SP-1 el síntoma era "stack not bootstrapped"; aquí es un `notarget` de npm, más obvio pero igual de engañoso si se asume que los esquemas son equivalentes.
- **Fix:** `CDK_VERSION=2.1128.1` en `cd.yml` (commit `faae10d`). Verificar la versión disponible con `npm view aws-cdk versions --json`. El pin correcto es siempre del esquema `2.1xxx.x`, nunca la versión de la lib Go.
- **Resultado:** `npm install -g aws-cdk@2.1128.1` pasa en Actions; el workflow continúa.
- **Status:** RESUELTO. Cazado por el CD en su propio primer run — nunca visible en local donde el CLI ya estaba instalado con la versión correcta.

### Finding #4 — `cdk diff --all` flag inválido en CLI 2.1xxx

- **Evento:** job `diff` del CD (PR #6) — el step `cdk diff --all` corrió y publicó un comentario en el PR
- **Síntoma:** el comentario del diff incluía `Unknown option(s): --all. These will be ignored` al inicio del output — el flag fue ignorado silenciosamente, el diff corrió igual pero con ruido en la salida
- **Causa raíz:** en CLI 2.1xxx, `cdk diff` no acepta `--all` — el subcomando `diff` ya difea todos los stacks del app por defecto. El flag `--all` es específico de `cdk deploy` (donde sí es válido y necesario). Usar `--all` en `diff` no falla el comando pero añade una advertencia que contamina el output del PR comment.
- **Fix:** quitar `--all` del step `cdk diff` en `cd.yml` (commit `af98821`). `cdk deploy --all` permanece sin cambios — es correcto y necesario.
- **Resultado:** el PR comment del diff queda limpio; `cdk deploy --all` sigue funcionando.
- **Status:** RESUELTO. Cazado por el output del comment del diff en el propio PR #6.

---

> **Repo público** · Cuenta `367707589526` publicable · Sin secrets ni marcas internas.
> Documentado 2026-06-24 con los resultados reales del canario pre-deploy.
> Patrón de referencia: `SP-4-DEPLOY.md` (mismo proyecto, mismo estilo).
