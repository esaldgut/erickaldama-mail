# CD Pipeline — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan
> task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GitHub Actions despliega los stacks CDK-Go a AWS vía OIDC (sin access keys de larga vida) con approval
manual a producción, y comenta el `cdk diff` en cada PR.

**Architecture:** Un 5º stack CDK-Go (CdStack) crea un OIDC provider nativo + 2 roles (mail-cd-diff read-only /
mail-cd-deploy scopeado a environment:production). Un workflow `cd.yml` con 2 jobs: `diff` (en PRs, comenta el
preview) y `deploy` (en merge a main, con gate de approval del GitHub Environment). Doble gate de seguridad:
el Environment pausa el job + el trust OIDC solo emite credenciales para environment:production.

**Tech Stack:** Go 1.26.4 · CDK-Go `awscdk/v2 v2.258.1` · GitHub Actions (checkout@v4, setup-go@v5, setup-node@v6,
configure-aws-credentials@v6, github-script@v9) · aws-cdk CLI @2.258.x · Node 22.

## Global Constraints

- **Go 1.26.4** · CDK-Go `awscdk/v2 v2.258.1`. Aplicar modern-go-guidelines (`any` no `interface{}`, etc.).
- **Cuenta AWS `367707589526`, región `us-east-1`** (constantes, ver `env()` en cmd/cdk/main.go).
- **Repo PÚBLICO con Git Flow:** cierre = PR a develop con CI verde, NO merge local. Gate NDA sobre todo el output.
- **OIDC provider: usar el L1 `awsiam.NewCfnOIDCProvider`** (nativo, 2 roles) — NO el L2 `NewOpenIdConnectProvider`
  (custom-resource + Lambda + 3er rol sin boundary). Verificado compilando.
- **El L1 expone `AttrArn()`, no `IOIDCProviderRef`** → el trust va con `awsiam.NewFederatedPrincipal(arn, &conditions,
  jsii.String("sts:AssumeRoleWithWebIdentity"))`, NO `NewOpenIdConnectPrincipal`.
- **OIDC provider NO existe en la cuenta** (verificado vivo) → crear, no importar.
- **El boundary v4 DENIEGA `iam:PutRolePermissionsBoundary`** → crear un rol CON boundary falla. Boundary v5 con
  excepción scoped es Task 0 (out-of-band). La exec-policy YA cubre CreateRole/CreateOIDCProvider (`iam:*`) → NO ampliarla.
- **Node 22+** es requisito de CDK v2 (verbatim). Versiones de actions: setup-node@v6, github-script@v9.
- **subagent-driven en worktree:** todo commit con `git -C "$WT"`, validar rama = worktree-cd-pipeline (≠ develop/main),
  validar cada SHA. `go mod tidy` tras tocar deps (no debería añadir deps — todo awscdk ya presente).
- **Disciplinas:** aws-cli-pre-flight-canonical, pr-as-auditable-evidence (el diff comentado), engineering-audit-6-axes
  (gate por tarea), control-subagents-in-worktrees-canonical. Es el primer fogueo de subproject-delivery-canonical.

**Constantes reales (verificadas):** `BoundaryManagedPolicyName = "erickaldama-boundary"` (en naming.go:16).
Roles bootstrap: `cdk-hnb659fds-{deploy,file-publishing,image-publishing,lookup}-role-367707589526-us-east-1` (los 4 existen).

---

## File Structure

| Archivo | Responsabilidad |
|---|---|
| `iam/erickaldama-boundary.json` (mod → v5) | Excepción scoped para `iam:PutRolePermissionsBoundary` (Task 0) |
| `internal/infra/naming.go` (mod) | +constantes CD (OIDC url, repo, role names, ARNs cdk-*) |
| `internal/infra/cd_stack.go` (new) | CdStack: OIDC provider L1 + mail-cd-diff + mail-cd-deploy + boundary |
| `internal/infra/cd_stack_test.go` (new) | template-asserts (provider nativo, 2 roles, trust sub exacto, boundary) |
| `cmd/cdk/main.go` (mod) | Registrar CdStack |
| `.github/workflows/cd.yml` (new) | Jobs diff (comenta PR) + deploy (gate environment) |
| `iam/cd-diff-trust.json` + `iam/cd-deploy-trust.json` (new) | Los 2 trust policies, auditables |
| `docs/CD-DEPLOY.md` (new) | Runbook: boundary v5, bootstrap CdStack, config environment, kill-switch, deploy findings |
| `CHANGELOG.md`, `docs/architecture.md`, `docs/diagrams/` (mod) | Entrada CD + plano + diagrama |

---

## Task 0: Boundary v5 — excepción scoped para PutRolePermissionsBoundary (GATE HUMANO)

**El agente escribe + valida el JSON, entrega el comando exacto. El HUMANO aplica el boundary v5 out-of-band
(SSO Admin) — el deploy del CdStack lo necesita ANTES de crear los roles con boundary.**

**Files:**
- Modify: `iam/erickaldama-boundary.json`

**Interfaces:**
- Produces: boundary v5 vivo que permite `iam:PutRolePermissionsBoundary` SOLO con `iam:PermissionsBoundary = arn erickaldama-boundary`.

- [ ] **Step 1: Añadir el statement de excepción al boundary**

En `iam/erickaldama-boundary.json`, añadir un 4º statement (tras `DenyCreateUserExceptMailClientPrincipals`).
**Importante:** un Deny gana sobre Allow en un boundary; pero `PutRolePermissionsBoundary` NO está en ningún Deny
(solo los `Put*PermissionsBoundary` del statement `DenyEscalationAndOutOfScope` — hay que SACARLO de ahí Y añadir el Allow
scoped). Editar así:
```json
{
  "Sid": "DenyEscalationAndOutOfScope",
  "Effect": "Deny",
  "Action": [
    "route53domains:*", "ec2:*", "rds:*", "organizations:*",
    "iam:PutUserPermissionsBoundary",
    "iam:DeleteUserPermissionsBoundary", "iam:DeleteRolePermissionsBoundary"
  ],
  "Resource": "*"
}
```
(se quitó `iam:PutRolePermissionsBoundary` del Deny). Y añadir un nuevo statement Deny scoped que lo permite SOLO para
el boundary correcto (mismo patrón que `DenyCreateUserExceptMailClientPrincipals`):
```json
{
  "Sid": "DenyPutRoleBoundaryExceptErickaldamaBoundary",
  "Effect": "Deny",
  "Action": "iam:PutRolePermissionsBoundary",
  "Resource": "*",
  "Condition": {
    "StringNotEquals": {
      "iam:PermissionsBoundary": "arn:aws:iam::367707589526:policy/erickaldama-boundary"
    }
  }
}
```
Esto deniega adjuntar CUALQUIER boundary distinto de erickaldama-boundary (anti-escalación preservado) pero permite
adjuntar erickaldama-boundary al crear un rol. NO toca el `DenyUserExcept...` ni los demás.

- [ ] **Step 2: Validar el JSON (sin claves IAM inválidas)**

Run:
```bash
python3 -c "import json; b=json.load(open('iam/erickaldama-boundary.json')); allowed={'Sid','Effect','Action','Resource','NotResource','Condition','Principal'}; [print('BAD',s.get('Sid'),set(s)-allowed) for s in b['Statement'] if set(s)-allowed]; print('JSON OK')"
```
Expected: `JSON OK` (sin líneas BAD — recordar: IAM no acepta `Comment` en statements).

- [ ] **Step 3: Commit del JSON**

```bash
git add iam/erickaldama-boundary.json
git commit -m "feat(cd): boundary v5 — scoped exception for PutRolePermissionsBoundary"
```

- [ ] **Step 4: GATE HUMANO — entregar el comando exacto**

El usuario aplica out-of-band (SSO Admin). Primero verificar cuántas versiones hay (límite 5; borrar la no-default
más vieja si hace falta):
```bash
aws iam list-policy-versions --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --profile AdministratorAccess-367707589526 --query 'Versions[].{V:VersionId,Default:IsDefaultVersion}' --output table
# crear v5 y hacerla default:
aws iam create-policy-version --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --policy-document file://iam/erickaldama-boundary.json --set-as-default \
  --profile AdministratorAccess-367707589526
```
**El agente NO ejecuta esto** (hook bloquea writes). Espera confirmación del humano.

- [ ] **Step 5: Verificación read-only (agente, tras el humano)**

```bash
aws iam get-policy-version --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --version-id v5 --profile AdministratorAccess-367707589526 \
  --query 'PolicyVersion.Document.Statement[?Sid==`DenyPutRoleBoundaryExceptErickaldamaBoundary`]' --output json
```
Expected: el statement con la Condition StringNotEquals. Confirma que `PutRolePermissionsBoundary` ya NO está en el
Deny general. Cierra el gate del hallazgo B4.

---

## Task 1: naming.go + CdStack (OIDC provider L1 + 2 roles) con template-asserts

**Files:**
- Modify: `internal/infra/naming.go`
- Create: `internal/infra/cd_stack.go` + `internal/infra/cd_stack_test.go`
- Modify: `cmd/cdk/main.go`

**Interfaces:**
- Produces: `NewCdStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack`.

- [ ] **Step 1: Añadir constantes CD a naming.go**

En `internal/infra/naming.go`, antes del `)` final del bloque `const`:
```go
	// CD pipeline — OIDC + GitHub Actions roles (created by CdStack).
	OidcProviderUrl   = "https://token.actions.githubusercontent.com"
	OidcAudience      = "sts.amazonaws.com"
	GithubRepo        = "esaldgut/erickaldama-mail"
	DiffRoleName      = "mail-cd-diff"
	DeployRoleName    = "mail-cd-deploy"
	// Bootstrap roles the CD roles may assume (verified to exist).
	CdkLookupRoleArn          = "arn:aws:iam::367707589526:role/cdk-hnb659fds-lookup-role-367707589526-us-east-1"
	CdkDeployRoleArn          = "arn:aws:iam::367707589526:role/cdk-hnb659fds-deploy-role-367707589526-us-east-1"
	CdkFilePublishingRoleArn  = "arn:aws:iam::367707589526:role/cdk-hnb659fds-file-publishing-role-367707589526-us-east-1"
	CdkImagePublishingRoleArn = "arn:aws:iam::367707589526:role/cdk-hnb659fds-image-publishing-role-367707589526-us-east-1"
```

- [ ] **Step 2: Escribir el template-assert que falla**

`internal/infra/cd_stack_test.go`:
```go
package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func synthCd(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NewCdStack(app, "CdStack", &awscdk.StackProps{
		Env: &awscdk.Environment{Account: jsii.String(Account), Region: jsii.String(Region)},
	})
	return assertions.Template_FromStack(stack, nil)
}

func TestCdStackOidcProviderIsNative(t *testing.T) {
	tpl := synthCd(t)
	// L1 CfnOIDCProvider → native resource, NOT a custom resource + Lambda.
	tpl.ResourceCountIs(jsii.String("AWS::IAM::OIDCProvider"), jsii.Number(1))
	tpl.ResourceCountIs(jsii.String("AWS::Lambda::Function"), jsii.Number(0)) // L2 would add one
	tpl.HasResourceProperties(jsii.String("AWS::IAM::OIDCProvider"), map[string]any{
		"Url":          OidcProviderUrl,
		"ClientIdList": []any{OidcAudience},
	})
}

func TestCdStackHasExactlyTwoRoles(t *testing.T) {
	tpl := synthCd(t)
	// L1 → exactly 2 roles (diff + deploy); L2 would add a 3rd (custom-resource provider role).
	tpl.ResourceCountIs(jsii.String("AWS::IAM::Role"), jsii.Number(2))
}

func TestCdDeployRoleTrustIsScopedToProductionEnv(t *testing.T) {
	tpl := synthCd(t)
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"RoleName": DeployRoleName,
		"AssumeRolePolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Action": "sts:AssumeRoleWithWebIdentity",
					"Condition": map[string]any{
						"StringEquals": map[string]any{
							"token.actions.githubusercontent.com:aud": OidcAudience,
							"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":environment:production",
						},
					},
				}),
			},
		}),
	}))
}

func TestCdDiffRoleTrustIsScopedToPullRequest(t *testing.T) {
	tpl := synthCd(t)
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"RoleName": DiffRoleName,
		"AssumeRolePolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Condition": map[string]any{
						"StringEquals": map[string]any{"token.actions.githubusercontent.com:aud": OidcAudience},
						"StringLike":   map[string]any{"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":pull_request"},
					},
				}),
			},
		}),
	}))
}

func TestCdRolesHaveBoundary(t *testing.T) {
	tpl := synthCd(t)
	tpl.AllResourcesProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"PermissionsBoundary": assertions.Match_AnyValue(),
	}))
}
```

- [ ] **Step 3: Run → FAIL** (NewCdStack no existe)

Run: `go test ./internal/infra/ -run TestCd`
Expected: FAIL (undefined: NewCdStack).

- [ ] **Step 4: Implementar cd_stack.go**

```go
package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewCdStack creates the GitHub Actions OIDC provider + two scoped deploy roles. Uses the L1 CfnOIDCProvider
// (native AWS::IAM::OIDCProvider) — the L2 NewOpenIdConnectProvider synthesizes a custom resource + Lambda + a
// 3rd role without boundary (audit A1). The trust is built with NewFederatedPrincipal(AttrArn,...) because the
// L1 exposes AttrArn(), not IOIDCProviderRef.
func NewCdStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	// Native OIDC provider (L1). ThumbprintList omitted → IAM autocompletes via trusted CA.
	oidc := awsiam.NewCfnOIDCProvider(stack, jsii.String("GithubOidc"), &awsiam.CfnOIDCProviderProps{
		Url:          jsii.String(OidcProviderUrl),
		ClientIdList: jsii.Strings(OidcAudience),
	})

	boundary := awsiam.ManagedPolicy_FromManagedPolicyName(stack, jsii.String("Boundary"),
		jsii.String(BoundaryManagedPolicyName))

	// diff role — read-only, any PR (sub scoped to pull_request).
	diffConditions := map[string]interface{}{
		"StringEquals": map[string]interface{}{
			"token.actions.githubusercontent.com:aud": OidcAudience,
		},
		"StringLike": map[string]interface{}{
			"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":pull_request",
		},
	}
	diffRole := awsiam.NewRole(stack, jsii.String("DiffRole"), &awsiam.RoleProps{
		RoleName: jsii.String(DiffRoleName),
		AssumedBy: awsiam.NewFederatedPrincipal(oidc.AttrArn(), &diffConditions,
			jsii.String("sts:AssumeRoleWithWebIdentity")),
		PermissionsBoundary: boundary,
	})
	diffRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect:    awsiam.Effect_ALLOW,
		Actions:   jsii.Strings("sts:AssumeRole"),
		Resources: jsii.Strings(CdkLookupRoleArn),
	}))

	// deploy role — only assumable from environment:production (StringEquals).
	deployConditions := map[string]interface{}{
		"StringEquals": map[string]interface{}{
			"token.actions.githubusercontent.com:aud": OidcAudience,
			"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":environment:production",
		},
	}
	deployRole := awsiam.NewRole(stack, jsii.String("DeployRole"), &awsiam.RoleProps{
		RoleName: jsii.String(DeployRoleName),
		AssumedBy: awsiam.NewFederatedPrincipal(oidc.AttrArn(), &deployConditions,
			jsii.String("sts:AssumeRoleWithWebIdentity")),
		PermissionsBoundary: boundary,
	})
	deployRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect:  awsiam.Effect_ALLOW,
		Actions: jsii.Strings("sts:AssumeRole"),
		Resources: jsii.Strings(CdkDeployRoleArn, CdkFilePublishingRoleArn,
			CdkImagePublishingRoleArn, CdkLookupRoleArn),
	}))

	return stack
}
```

- [ ] **Step 5: Run → PASS**

Run: `go test ./internal/infra/ -run TestCd`
Expected: PASS (5 tests). Si `AllResourcesProperties` o `Match_AnyValue` no existen con ese nombre exacto en v2.258.1,
verificar con `go doc github.com/aws/aws-cdk-go/awscdk/v2/assertions` y ajustar (el agente que auditó confirmó
`Template_FromStack` + `Match_ObjectLike`; `AllResourcesProperties`/`Match_AnyValue` son del mismo paquete).

- [ ] **Step 6: Registrar CdStack en cmd/cdk/main.go**

En `cmd/cdk/main.go`, tras el bloque de ReceivingStack (antes de `app.Synth(nil)`):
```go
	infra.NewCdStack(app, "CdStack", &awscdk.StackProps{
		Env: env(),
	})
```

- [ ] **Step 7: Run suite completa + synth**

Run:
```bash
go test -count=1 ./internal/infra/
AWS_PROFILE=AdministratorAccess-367707589526 cdk synth CdStack >/dev/null && echo "synth OK"
```
Expected: tests PASS; synth OK.

- [ ] **Step 8: Commit**

```bash
git add internal/infra/naming.go internal/infra/cd_stack.go internal/infra/cd_stack_test.go cmd/cdk/main.go
git commit -m "feat(cd): CdStack — native OIDC provider (L1) + scoped diff/deploy roles"
```

---

## Task 2: cd.yml workflow (diff job comenta el PR + deploy job con gate)

**Files:**
- Create: `.github/workflows/cd.yml`

**Interfaces:** N/A (workflow YAML).

- [ ] **Step 1: Escribir cd.yml**

`.github/workflows/cd.yml`:
```yaml
name: CD

on:
  pull_request:
    branches: [develop, rc, main]
  push:
    branches: [main]

jobs:
  diff:
    # Only run on PRs from the same repo — fork PRs get no OIDC token / no write token (audit C2/C3).
    if: github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: actions/setup-node@v6
        with:
          node-version: 22          # CDK v2 requires Node.js 22+ (audit C5)
      - run: npm install -g aws-cdk@2.258.0
      - uses: aws-actions/configure-aws-credentials@v6
        with:
          role-to-assume: arn:aws:iam::367707589526:role/mail-cd-diff
          aws-region: us-east-1
      - name: cdk diff
        run: cdk diff --all 2>&1 | tee /tmp/diff.txt
      - name: Comment diff on PR
        uses: actions/github-script@v9
        with:
          script: |
            const fs = require('fs');
            const marker = '<!-- cdk-diff-bot -->';
            const diff = fs.readFileSync('/tmp/diff.txt', 'utf8').slice(0, 60000);
            const body = `${marker}\n## cdk diff\n\`\`\`\n${diff}\n\`\`\``;
            const { data: comments } = await github.rest.issues.listComments({
              owner: context.repo.owner, repo: context.repo.repo, issue_number: context.issue.number,
            });
            const prev = comments.find(c => c.body.includes(marker));
            if (prev) {
              await github.rest.issues.updateComment({ owner: context.repo.owner, repo: context.repo.repo, comment_id: prev.id, body });
            } else {
              await github.rest.issues.createComment({ owner: context.repo.owner, repo: context.repo.repo, issue_number: context.issue.number, body });
            }

  deploy:
    if: github.event_name == 'push'
    runs-on: ubuntu-latest
    environment: production       # the approval gate — job waits for a required reviewer
    concurrency:
      group: deploy-production
      cancel-in-progress: false   # never cancel an in-flight deploy (CFN state safety)
    permissions:
      id-token: write
      contents: read
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - uses: actions/setup-node@v6
        with:
          node-version: 22
      - run: npm install -g aws-cdk@2.258.0
      - uses: aws-actions/configure-aws-credentials@v6
        with:
          role-to-assume: arn:aws:iam::367707589526:role/mail-cd-deploy
          aws-region: us-east-1
      - name: cdk deploy
        run: cdk deploy --all --require-approval never
```

- [ ] **Step 2: Validar la sintaxis YAML**

Run:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/cd.yml')); print('YAML OK')"
```
Expected: `YAML OK`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/cd.yml
git commit -m "feat(cd): cd.yml — diff job comments PR, deploy job gated by environment"
```

---

## Task 3: IAM trust JSONs (evidencia auditable)

**Files:**
- Create: `iam/cd-diff-trust.json`, `iam/cd-deploy-trust.json`

**Interfaces:** N/A (evidencia documental; espeja lo que el CdStack sintetiza).

- [ ] **Step 1: Escribir los 2 trust JSONs**

`iam/cd-diff-trust.json`:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Federated": "arn:aws:iam::367707589526:oidc-provider/token.actions.githubusercontent.com" },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": { "token.actions.githubusercontent.com:aud": "sts.amazonaws.com" },
        "StringLike": { "token.actions.githubusercontent.com:sub": "repo:esaldgut/erickaldama-mail:pull_request" }
      }
    }
  ]
}
```
`iam/cd-deploy-trust.json`:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Federated": "arn:aws:iam::367707589526:oidc-provider/token.actions.githubusercontent.com" },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
          "token.actions.githubusercontent.com:sub": "repo:esaldgut/erickaldama-mail:environment:production"
        }
      }
    }
  ]
}
```

- [ ] **Step 2: Validar JSON**

Run: `python3 -c "import json; [json.load(open(f)) for f in ['iam/cd-diff-trust.json','iam/cd-deploy-trust.json']]; print('JSON OK')"`
Expected: `JSON OK`.

- [ ] **Step 3: Commit**

```bash
git add iam/cd-diff-trust.json iam/cd-deploy-trust.json
git commit -m "docs(cd): auditable trust policy JSONs for the 2 CD roles"
```

---

## Task 4: Runbook + CHANGELOG + architecture (evidencia de portafolio) + bootstrap + verificación e2e

**Files:**
- Create: `docs/CD-DEPLOY.md`
- Modify: `CHANGELOG.md`, `docs/architecture.md`, `docs/diagrams/architecture_icons.py`

**Interfaces:** N/A (docs + verificación humana).

- [ ] **Step 1: Suite completa + synth + diff read-only (canario)**

Run:
```bash
go build ./... && go test -count=1 ./... && go vet ./... && gofmt -l internal/ cmd/
AWS_PROFILE=AdministratorAccess-367707589526 cdk diff CdStack 2>&1 | grep -E "AWS::IAM::Role|AWS::IAM::OIDCProvider" | head
```
Expected: todo verde; el diff muestra `+ OIDCProvider` + 2 `+ Role`, nada más (canario: no toca los 4 stacks existentes).

- [ ] **Step 2: Escribir docs/CD-DEPLOY.md (runbook)**

Contenido (estilo SP-4-DEPLOY.md, datos reales): pre-flight identity; Task 0 (boundary v5 — el comando create-policy-version
+ por qué B4); bootstrap out-of-band del CdStack (`cdk deploy CdStack --require-approval never` — el humano, el CD aún no
existe); config manual del GitHub Environment `production` (required reviewers=tú, "Prevent self-review" DESACTIVADO,
deployment branches=main); verificación OIDC e2e (PR de prueba → diff comenta; merge a main → deploy PAUSA pidiendo approval);
smoke de seguridad (mail-cd-diff no puede mutar → AccessDenied); kill-switch (`aws iam delete-role` o quitar el trust);
el matiz de concurrency (1 pending); y los deploy findings reales (los habrá). ~400+ líneas.

- [ ] **Step 3: GATE HUMANO — bootstrap del CdStack + config del Environment**

El usuario ejecuta out-of-band:
```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk deploy CdStack --require-approval never
```
(crea el OIDC provider + los 2 roles; el boundary v5 de Task 0 permite el PutRolePermissionsBoundary). Luego configura
el Environment `production` en GitHub Settings (a mano, documentado en el runbook).

- [ ] **Step 4: Verificación post-deploy (agente, read-only)**

```bash
aws iam list-open-id-connect-providers --profile AdministratorAccess-367707589526
aws iam get-role --role-name mail-cd-diff --profile AdministratorAccess-367707589526 --query 'Role.Arn'
aws iam get-role --role-name mail-cd-deploy --profile AdministratorAccess-367707589526 --query 'Role.Arn'
# smoke de seguridad: el trust de mail-cd-diff NO debe permitir asumirlo sin un token OIDC (no testeable read-only;
# se valida en la verificación e2e del runbook con un PR real).
```
Expected: el provider existe; los 2 roles existen con sus ARNs.

- [ ] **Step 5: Actualizar CHANGELOG + architecture + diagrama**

CHANGELOG: entrada `[CD]` (los 2 deploy findings anticipados B4/A1, las correcciones de la auditoría). architecture.md:
añadir el plano de CD (GitHub Actions → OIDC → AWS, doble gate). Regenerar el PNG con el nodo CD si el venv está disponible.

- [ ] **Step 6: Gate NDA + commit final**

Run: `grep -rIE "esagiosapp|yaan|roatech|MercadoPago|476114125529|288761749126|468227865963" .github/ internal/infra/cd_stack.go iam/cd-*.json docs/CD-DEPLOY.md CHANGELOG.md docs/architecture.md || echo "✅ NDA clean"`
Expected: NDA clean.
```bash
git add docs/CD-DEPLOY.md CHANGELOG.md docs/architecture.md docs/diagrams/
git commit -m "docs(cd): runbook + CHANGELOG + architecture (CD pipeline live)"
```

---

## Cierre

Tras Task 4: PR a develop con CI verde (Git Flow — NO merge local). `gh pr create --base develop`.
Recordar el quirk de `gh pr merge` (verificar state=MERGED, limpiar con `git -C` si falla la fase 2).
Persistencia triple-capa: checkboxes (este plan) + `docs/superpowers/EXECUTION-LOG.md` + task #23.
Es el primer fogueo de `subproject-delivery-canonical` — anotar si el skill tuvo algún gap.
