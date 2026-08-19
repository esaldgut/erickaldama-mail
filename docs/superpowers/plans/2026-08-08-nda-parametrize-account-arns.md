# Parametrización NDA (Account ID + ARNs) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Sacar el AWS Account ID `367707589526` y los ARNs con account embebido del source del repo público
(`internal/infra`, `.github/workflows/cd.yml`, `iam/`, `docs/`), inyectándolos vía un `Env` struct (approach #3)
resuelto de `CDK_DEFAULT_ACCOUNT`/`CDK_DEFAULT_REGION`, **sin cambiar el template sintetizado** (byte-identidad) ni
romper el CI/CD.

**Architecture:** Un `Env` struct (`internal/infra/env.go`) construido una vez con `NewEnv()`, con el account/region
de env vars (fallback a placeholder no-secreto `000000000000`/`us-east-1` para synth/test offline). Los ARN se
derivan de `Env.Account`/`Env.Region` con `fmt.Sprintf`, produciendo los MISMOS strings que los literales de hoy.
Inyectado a los 5 stacks. Los 2 role ARNs del CI → GitHub secrets. Docs + iam/*.json → placeholders. Regla
universal: skill `iac-inject-env-never-hardcode-account`.

**Tech Stack:** Go (CDK v2 `aws-cdk-go`), `internal/infra` (5 stacks + 4 tests), GitHub Actions, `iam/*.json`+`*.sh`.

**Spec:** `docs/superpowers/specs/2026-08-08-nda-parametrize-account-arns-design.md`.

## Global Constraints

- **Hook SP-0:** `.claude/hooks/cdk-go-guard.sh` DENIEGA `git`/`go`/`gh`/`cdk`(mutación)/`find` al agente. **TODO
  comando `go`/`git`/`gh`/`cdk` lo ejecuta el HUMANO** (con `!` o a mano); el agente redacta edits y VERIFICA el
  output binario que el humano pega. `cdk synth`/`cdk diff` (read-only) el hook los PERMITE, pero pasan por Bash
  del humano igual. De a UNO (el hook deniega compound `&&`/`;`/`|`).
- **BYTE-IDENTIDAD es el gate.** El objetivo es que `cdk synth` con `CDK_DEFAULT_ACCOUNT=367707589526`
  `CDK_DEFAULT_REGION=us-east-1` produzca un template IDÉNTICO al de HEAD. Cualquier diff ≠ 0 es un fallo del
  approach, no una mejora — PARAR y ajustar el `fmt.Sprintf` hasta que el string derivado == el literal.
- **El account real NUNCA en `.go`/`.yml`.** Solo puede quedar en docs redactadas como `<ACCOUNT_ID>`. Gate 3
  (grep) lo verifica.
- **El cliente Go NO se toca** — desacoplado de `internal/infra` (usa `internal/awsconf`). Cero blast radius ahí.
- **Los template-assertion tests pasan SIN cambiar sus expectativas** — template y assert derivan del mismo `Env`
  (con el placeholder en ambos lados cuando corren sin env).
- **Los 2 GitHub secrets se crean ANTES del PR** (acción humana en GitHub settings) o el `cdk-diff` job falla.
- **Rama:** `feature/nda-parametrize-account-arns` (NO develop directo — este frente toca infra real; el PR con el
  `cdk-diff` gate es esencial).

---

### Task 1: SPIKE — validar byte-identidad con 1 stack (SendingStack) + fijar el mecanismo de threading

**Files:**
- Create: `internal/infra/env.go` (el `Env` struct + resolvers — versión mínima para el spike: solo los campos que
  `SendingStack` usa: `Account`, `Region`, `IdentityArn`, `ConfigSetArn`).
- Modify: `internal/infra/sending_stack.go` (recibir `Env`, usar `env.IdentityArn`/`env.ConfigSetArn`).
- Modify: `internal/infra/sending_stack_test.go:93` (construir `Env` con `NewEnv()`).

**Interfaces:**
- Produces: `infra.Env` struct + `infra.NewEnv() Env`. **Mecanismo de threading DECIDIDO por la auditoría F2 del
  plan (NO es un "4º param"):** las 5 firmas de stack NO son uniformes — `NewReceivingStack(..., bucket
  awss3.IBucket)` ya tiene un 4º param, `NewMailStorageStack(...) (awscdk.Stack, awss3.Bucket)` retorna 2 valores,
  `NewCdStack` pasa `PermissionsBoundary` en sus props. Un 4º param `env` colisiona con el `bucket` de receiving y
  fuerza un param inútil en mail_storage. **El threading uniforme es un `StackProps` wrapper custom** que embebe
  el `Env`, del cual cada stack lo extrae:
  ```go
  // EnvStackProps wraps StackProps with the resolved Env so every stack reads the same source without a
  // per-signature positional param. Uniform across all 5 stacks.
  type EnvStackProps struct {
      *awscdk.StackProps
      Env Env
  }
  ```
  Cada `NewXxxStack` toma `props *EnvStackProps` (en vez de `*awscdk.StackProps`), pasa `props.StackProps` a
  `awscdk.NewStack`, y usa `props.Env.IdentityArn` etc. Esto conserva el 4º param `bucket` de receiving y el 2º
  return de mail_storage intactos — solo cambia el TIPO del param `props`, no el número de params. `main.go`
  construye un `&EnvStackProps{StackProps: &awscdk.StackProps{Env: ...}, Env: e}` una vez por stack.

- [ ] **Step 1: Capturar el baseline byte-identidad de TODOS los stacks (no solo SendingStack).** (HUMANO)

**Corrección F2-Finding 3:** Task 4 diffea los 5 stacks contra "HEAD antes del frente", pero para entonces HEAD ya
está mutado. Capturar el baseline COMPLETO **antes de cualquier edit**, con el account real:
```
git rev-parse HEAD
```
```
CDK_DEFAULT_ACCOUNT=367707589526 CDK_DEFAULT_REGION=us-east-1 cdk synth > /tmp/all-head.json
```
(Sin nombre de stack = los 5 stacks; el hook permite `cdk synth` read-only.) Guardar `/tmp/all-head.json` (baseline
de los 5) Y `/tmp/sending-head.json` (`cdk synth SendingStack > ...` para el gate del spike). El humano pega la
confirmación. Estos dos archivos son los baselines contra los que Task 1 Step 5 (spike) y Task 4 Step 3 (all-stacks)
diffean. El SHA de HEAD queda anotado por si hay que re-capturar desde un worktree limpio.

- [ ] **Step 2: Escribir `internal/infra/env.go` (versión spike).**

```go
package infra

import (
	"fmt"
	"os"
)

func awsAccount() string {
	if a := os.Getenv("CDK_DEFAULT_ACCOUNT"); a != "" {
		return a
	}
	return "000000000000" // non-secret placeholder — deterministic offline synth/test
}

func awsRegion() string {
	if r := os.Getenv("CDK_DEFAULT_REGION"); r != "" {
		return r
	}
	return "us-east-1"
}

// Env carries account/region-derived identifiers, built once and injected into the stacks.
type Env struct {
	Account, Region          string
	IdentityArn, ConfigSetArn string
	// (Task 2 adds: ReceiptRuleArn, the 4 Cdk*RoleArn, DynamoTableArn, BounceTopicArn.)
}

// EnvStackProps wraps StackProps with the resolved Env — the uniform threading vehicle across all 5
// stacks (F2-Finding 4: the 5 signatures are not homogeneous, so a positional param won't fit all).
type EnvStackProps struct {
	*awscdk.StackProps
	Env Env
}

func NewEnv() Env {
	a, r := awsAccount(), awsRegion()
	return Env{
		Account: a, Region: r,
		IdentityArn:  fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", r, a, DomainName),
		ConfigSetArn: fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", r, a, ConfigSetName),
	}
}
```
(El import de `awscdk` se añade a `env.go` para el `EnvStackProps`.)

- [ ] **Step 3: Threadear `Env` a `SendingStack` via el wrapper.** Cambiar `NewSendingStack` para tomar
  `props *EnvStackProps` (en vez de `*awscdk.StackProps`); pasar `props.StackProps` a `awscdk.NewStack`. Threadear
  `props.Env` a `addSendIam(stack, env)` (hoy `addSendIam(stack)` en `sending_stack.go:151`, llamada en `:30`).
  Migrar **AMBOS** consumidores de `Account`/ARN en `addSendIam` (F2-Finding 1 — el plan original omitió `:171`):
  - `sending_stack.go:161` — `jsii.Strings(IdentityArn, ConfigSetArn)` → `jsii.Strings(env.IdentityArn, env.ConfigSetArn)`.
  - `sending_stack.go:171` — `awsiam.NewAccountPrincipal(jsii.String(Account))` → `awsiam.NewAccountPrincipal(jsii.String(env.Account))`.
    (`addSendIam` DEBE recibir `env` para cubrir `:171`, no solo `:161` — este era el consumidor omitido.)
  NO borrar aún las constantes de `naming.go` (Task 2). Actualizar la llamada en `sending_stack_test.go:93` (pasar
  un `&EnvStackProps{StackProps: &awscdk.StackProps{Env: ...}, Env: NewEnv()}`) y en `cmd/cdk/main.go` (construir el
  wrapper). El spike PRUEBA el patrón wrapper completo en 1 stack antes de propagarlo.

- [ ] **Step 4: Compilar + test.** (HUMANO)
```
go build ./...
```
```
go vet ./...
```
```
go test -count=1 ./internal/infra/
```
Expected: verde. Los tests de SendingStack pasan (usan `NewEnv()` → placeholder en ambos lados si corren sin env).

- [ ] **Step 5: EL GATE DEL SPIKE — byte-identidad.** (HUMANO)
```
CDK_DEFAULT_ACCOUNT=367707589526 CDK_DEFAULT_REGION=us-east-1 cdk synth SendingStack > /tmp/sending-new.json
```
```
diff /tmp/sending-head.json /tmp/sending-new.json
```
Expected: **VACÍO** (byte-idéntico). El humano pega el resultado del `diff`. **Si NO es vacío**, el agente analiza
la diferencia (un espacio de más en `fmt.Sprintf`, un orden de campo, un token) y ajusta hasta diff=0. **Este es
el go/no-go de todo el frente:** si SendingStack no da byte-identidad, el approach #3 se re-evalúa ANTES de tocar
los otros 4 stacks.

- [ ] **Step 6: Documentar el mecanismo de threading elegido** (en este plan, inline) para que Tasks 2-5 lo
  repliquen sin re-decidir. Commit del spike. (HUMANO)
```
git add internal/infra/env.go internal/infra/sending_stack.go internal/infra/sending_stack_test.go cmd/cdk/main.go
```
```
git commit -m "spike(nda): Env struct inyectado en SendingStack — byte-identidad verificada (cdk diff vacío)"
```

---

### Task 2: Completar `Env` con todos los ARN + eliminar las constantes con-account de `naming.go`

**Files:**
- Modify: `internal/infra/env.go` (añadir `ReceiptRuleArn`, los 4 `Cdk*RoleArn`, `DynamoTableArn`, `BounceTopicArn`).
- Modify: `internal/infra/naming.go` (ELIMINAR `Account`, `Region`, `IdentityArn`, `ConfigSetArn`, `ReceiptRuleArn`,
  los 4 `Cdk*RoleArn` — pasan a `Env`; CONSERVAR todas las constantes de NOMBRE).

**Interfaces:**
- Consumes: el `Env` del spike. Produces: `Env` completo con los 10 campos derivados.

- [ ] **Step 1: Añadir los campos restantes a `Env` + `NewEnv`** (el código completo del §4.1 del spec):
```go
	ReceiptRuleArn: fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s:receipt-rule/%s", r, a, ReceiptRuleSetName, ReceiptRuleName),
	CdkLookupRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-lookup-role-%s-%s", a, a, r),
	CdkDeployRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-deploy-role-%s-%s", a, a, r),
	CdkFilePublishingRoleArn:  fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-file-publishing-role-%s-%s", a, a, r),
	CdkImagePublishingRoleArn: fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-image-publishing-role-%s-%s", a, a, r),
	DynamoTableArn: fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", r, a, MailIndexTableName),
	BounceTopicArn: fmt.Sprintf("arn:aws:sns:%s:%s:%s", r, a, BounceTopicName),
```
CRÍTICO: cada `fmt.Sprintf` debe producir EXACTAMENTE el string del literal que reemplaza. Verificar contra el
literal de `naming.go` antes de borrarlo (p.ej. `CdkLookupRoleArn` tiene el account DOS veces: en `iam::%s:role/`
y en `-%s-%s` — de ahí los 3 `%s` con `a,a,r`).

- [ ] **Step 2: Eliminar de `naming.go` las 8 constantes con-account** (`Account`, `Region`, `IdentityArn`,
  `ConfigSetArn`, `ReceiptRuleArn`, `CdkLookupRoleArn`, `CdkDeployRoleArn`, `CdkFilePublishingRoleArn`,
  `CdkImagePublishingRoleArn`). Conservar `DomainName`, `ConfigSetName`, `ReceiptRuleSetName`, `ReceiptRuleName`,
  `MailIndexTableName`, `BounceTopicName`, `HostedZoneID`, y todos los nombres de rol/recurso. Esto ROMPERÁ la
  compilación de los stacks/tests que aún referencian las constantes — se arregla en Tasks 3-4 (el compilador es
  la lista de call-sites a migrar).

- [ ] **Step 3: NO compilar aún** (romperá — es esperado). Continuar a Task 3.

---

### Task 3: Migrar los 4 stacks restantes a `Env` inyectado

**Files (call-sites EXACTOS del mapa + auditoría F2 — cada consumidor de una constante borrada, ninguno omitido):**
- `internal/infra/foundation_stack.go` — `Account` en `:71` (DynamoDB ARN concat) → `props.Env.DynamoTableArn`.
- `internal/infra/receiving_stack.go` — **5 consumidores** (F2-Finding 2 añadió `:142,:151`): `Account` en `:141`
  (`"AWS:SourceAccount": Account`) → `props.Env.Account`; `:150` (`SourceAccount: jsii.String(Account)`) →
  `jsii.String(props.Env.Account)`; `:196` (SNS ARN concat) → `props.Env.BounceTopicArn`; **`ReceiptRuleArn` en
  `:142`** (`"AWS:SourceArn": ReceiptRuleArn`) → `props.Env.ReceiptRuleArn`; **`:151`**
  (`SourceArn: jsii.String(ReceiptRuleArn)`) → `jsii.String(props.Env.ReceiptRuleArn)`.
- `internal/infra/cd_stack.go` — los 4 `Cdk*RoleArn` en `:44` (`jsii.Strings(CdkLookupRoleArn)`), `:63`
  (`CdkDeployRoleArn, CdkFilePublishingRoleArn`), `:64` (`CdkImagePublishingRoleArn, CdkLookupRoleArn`) →
  `props.Env.Cdk*RoleArn`.
- `internal/infra/mail_storage_stack.go` — **account-free** (F2 verificado: solo `RawBucketName`+enums+`sp3Tags`).
  NO consume ninguna constante borrada. Solo cambia el TIPO de su param a `*EnvStackProps` por uniformidad del
  wrapper (no usa `props.Env`), y su call-site.
- `cmd/cdk/main.go` — construir `NewEnv()` una vez; envolver cada `StackProps` en un `&EnvStackProps{...}`; cerrar
  la deuda de la 2ª fuente de verdad (`:44` re-hardcodea el account).

**Interfaces:**
- Consumes: `Env` completo (Task 2) + el `EnvStackProps` wrapper (Task 1). Cada `NewXxxStack` toma
  `props *EnvStackProps` (cambio de TIPO, no de número de params — conserva el `bucket` de receiving y el 2º
  return de mail_storage).

- [ ] **Step 1: `cmd/cdk/main.go`** — `env()` deja de hardcodear; `main()` construye `e := infra.NewEnv()` una vez,
  y por cada stack un `&infra.EnvStackProps{StackProps: &awscdk.StackProps{Env: &awscdk.Environment{Account:
  jsii.String(e.Account), Region: jsii.String(e.Region)}}, Env: e}`. Las 5 llamadas (F2 verbatim): FoundationStack
  (`:15`), SendingStack (`:19`), MailStorageStack (`:23`, retorna `storage, rawBucket`), ReceivingStack (`:28-30`,
  con `rawBucket` como trailing arg DESPUÉS del props — el wrapper no altera ese orden), CdStack (`:32`, que además
  pasa `PermissionsBoundary` — va DENTRO del `StackProps` embebido).

- [ ] **Step 2: Cada stack** — cambiar la firma a `props *EnvStackProps`, pasar `props.StackProps` a
  `awscdk.NewStack`, y reemplazar CADA referencia a una constante eliminada por el campo de `props.Env` (mapa
  COMPLETO de la sección Files, con los 2 consumidores de `ReceiptRuleArn` que la auditoría añadió):
  `foundation_stack.go:71` → `props.Env.DynamoTableArn`; `receiving_stack.go:141,150` → `props.Env.Account`, `:196`
  → `props.Env.BounceTopicArn`, **`:142,151` → `props.Env.ReceiptRuleArn`**; `cd_stack.go:44,64` →
  `props.Env.CdkLookupRoleArn`, `:63` → los otros 3. `sending_stack.go` ya migrado en el spike (Task 1). Para las
  helpers que usan constantes (`addSendIam`, `addEventRouting`, etc.), threadear `props.Env` como parámetro.

- [ ] **Step 3: Compilar.** (HUMANO)
```
go build ./...
```
Expected: verde (todos los call-sites migrados; si algo falla, es un call-site que Task 2 rompió y falta migrar —
el error dice cuál).

---

### Task 4: Migrar los 5 tests + verificar byte-identidad de TODOS los stacks

**Files (los 5 — F2-Finding: `mail_storage_stack_test.go` estaba omitido):**
- Modify: `internal/infra/{foundation,sending,receiving,cd}_stack_test.go` — construyen el `EnvStackProps` wrapper
  con `NewEnv()`; los asserts usan `e.CdkLookupRoleArn` etc.
- Modify: `internal/infra/mail_storage_stack_test.go` — NO tiene constante borrada, pero su call-site a
  `NewMailStorageStack` cambia de `*StackProps` a `*EnvStackProps` → actualizar o no compila. (También
  `receiving_stack_test.go` llama a `NewMailStorageStack` — verificar ambos call-sites.)

**Interfaces:** consume `Env` + `EnvStackProps`; los tests derivan template y assert del **MISMO `NewEnv()`**.

- [ ] **Step 1: Migrar los 5 tests — DISCIPLINA CRÍTICA (F2-Finding 6, trampa de consistencia).** En cada helper
  `synthXxx(t)` / los `env()` inline: construir **UN solo** `e := NewEnv()` por test y usarlo para AMBOS lados —
  el `EnvStackProps` que arma el stack Y los asserts. Ejemplo:
  ```go
  e := NewEnv()
  props := &EnvStackProps{StackProps: &awscdk.StackProps{Env: &awscdk.Environment{
      Account: jsii.String(e.Account), Region: jsii.String(e.Region)}}, Env: e}
  stack := NewCdStack(app, "CdStack", props)
  // ...asserts usan e.CdkLookupRoleArn, ":iam::" + e.Account + ":policy/" + BoundaryManagedPolicyName, etc.
  ```
  **Por qué es crítico:** los tests corren SIN env → `NewEnv()` devuelve el placeholder `000000000000`. Si el
  stack se arma con un `NewEnv()` y el assert compara contra otro (o contra un literal), divergen bajo el
  placeholder. UN `e` compartido garantiza que template y assert usan el mismo account en ambos lados. Además
  reescribir `cd_stack_test.go:80` (`boundaryArn := ":iam::" + Account + ":..."`) a `e.Account` — `Account` const
  ya no existe (F2 compile-dependency).

- [ ] **Step 2: Compilar + test completo.** (HUMANO)
```
go build ./...
```
```
go vet ./...
```
```
go test -count=1 ./...
```
Expected: TODO verde (infra + cliente). Los template-assertion tests pasan con el placeholder `000000000000` en
ambos lados (corren sin env).

- [ ] **Step 3: GATE 1 — byte-identidad de los 5 stacks contra el baseline capturado en Task 1 Step 1.** (HUMANO)
```
CDK_DEFAULT_ACCOUNT=367707589526 CDK_DEFAULT_REGION=us-east-1 cdk synth > /tmp/all-new.json
```
```
diff /tmp/all-head.json /tmp/all-new.json
```
Expected: **VACÍO** para los 5 stacks (F2-Finding 3: el baseline `/tmp/all-head.json` se capturó ANTES de cualquier
edit en Task 1 Step 1 — NO re-sintetizar HEAD ahora, que ya está mutado). Si algún stack difiere, ese `fmt.Sprintf`
no reproduce el literal — ajustar hasta diff=0. Este es el gate local de byte-identidad; el gate end-to-end es el
`cdk-diff` job del PR (Task 7 Step 4).

- [ ] **Step 4: GATE 3 — grep de fuga en código.** (HUMANO)
```
grep -rn 367707589526 internal/ cmd/
```
Expected: **cero hits** en `.go`. El account solo vive ahora en el runtime (env) y en docs (Task 6, redactadas).

- [ ] **Step 5: Commit del núcleo CDK.** (HUMANO)
```
git add internal/infra/ cmd/cdk/main.go
```
```
git commit -m "feat(nda): Env struct inyectado — account/ARNs desde CDK_DEFAULT_ACCOUNT, byte-identidad preservada"
```

---

### Task 5: `cd.yml` role ARNs → GitHub secrets + export EXPLÍCITO de `CDK_DEFAULT_ACCOUNT` (CRIT F2-Finding 1)

**Files:**
- Modify: `.github/workflows/cd.yml` — los 2 `role-to-assume` (`:39,94`) + un step nuevo antes de cada `cdk
  diff`/`cdk deploy`.

**Interfaces:** consume 2 GitHub secrets (creados out-of-band) + el account de las credenciales OIDC en runtime.

> **CRIT de la auditoría (F2-Finding 1+2):** post-cambio, el account del `cdk synth` viene de `CDK_DEFAULT_ACCOUNT`
> con fallback al placeholder `000000000000`. Pero `aws-actions/configure-aws-credentials` **NO** setea
> `CDK_DEFAULT_ACCOUNT` (verbatim de su README: solo `AWS_ACCESS_KEY_ID/SECRET/SESSION_TOKEN/AWS_REGION/
> AWS_DEFAULT_REGION`). La CDK CLI lo deriva vía STS `GetCallerIdentity` — pero es una dependencia IMPLÍCITA, con
> historial de regresión version-sensitive (aws-cdk#32757). Si por cualquier razón la CLI no lo puebla, el CI
> sintetiza con `000000000000` → **diff GIGANTE / deploy a la cuenta equivocada**. Convertir la apuesta implícita
> en garantía EXPLÍCITA: un step que exporta el account de las credenciales OIDC ya asumidas.

- [ ] **Step 1 (HUMANO, GitHub settings — ANTES de editar): crear los 2 secrets.**
  En repo Settings → Secrets and variables → Actions:
  - `AWS_CD_DIFF_ROLE_ARN` = `arn:aws:iam::367707589526:role/mail-cd-diff`
  - `AWS_CD_DEPLOY_ROLE_ARN` = `arn:aws:iam::367707589526:role/mail-cd-deploy` (idealmente como secret del
    Environment `production`, que el job ya usa como gate).
  El humano confirma que existen ANTES de continuar.

- [ ] **Step 2: Editar `cd.yml` — los 2 `role-to-assume`.** `:39` `role-to-assume: ${{ secrets.AWS_CD_DIFF_ROLE_ARN }}`;
  `:94` `role-to-assume: ${{ secrets.AWS_CD_DEPLOY_ROLE_ARN }}`. (Región: dejar `us-east-1` literal — no es fuga de
  account; opcional a `${{ vars.AWS_REGION }}`.)

- [ ] **Step 3: Añadir el export EXPLÍCITO de `CDK_DEFAULT_ACCOUNT` (el fix CRIT).** En cada job que corre `cdk`
  (el `diff` y el `deploy`), DESPUÉS del step `configure-aws-credentials` (que asume el rol OIDC) y ANTES del step
  `cdk diff`/`cdk deploy`, insertar:
  ```yaml
  - name: Export CDK account/region from assumed OIDC credentials
    run: |
      echo "CDK_DEFAULT_ACCOUNT=$(aws sts get-caller-identity --query Account --output text)" >> "$GITHUB_ENV"
      echo "CDK_DEFAULT_REGION=${AWS_REGION:-us-east-1}" >> "$GITHUB_ENV"
  ```
  Esto puebla `CDK_DEFAULT_ACCOUNT` de forma explícita y auto-evidente desde las credenciales ya asumidas — el
  `cdk synth` del CI SIEMPRE ve el account real (367707589526), nunca el placeholder. Convierte la dependencia
  implícita de la CDK CLI en una garantía leíble en el propio workflow. (`aws sts get-caller-identity` es una
  lectura — no muta nada; el rol OIDC ya tiene permiso implícito de leer su propia identidad.)

- [ ] **Step 4: Commit.** (HUMANO)
```
git add .github/workflows/cd.yml
```
```
git commit -m "feat(nda): cd.yml role-to-assume desde GitHub secrets + export explícito de CDK_DEFAULT_ACCOUNT"
```

---

### Task 6: Redactar docs + iam/*.json + iam/*.sh a placeholders

**Files (EXACTOS, del inventario — no genéricos):**
- `iam/*.json` — los **5 que contienen el account** (del inventario; NO todos tienen): `iam/erickaldama-boundary.json`
  (líneas 18-21,41-43,52), `iam/deploy-exec-policy.json` (27), `iam/mail-send-policy.json` (9-10),
  `iam/cd-deploy-trust.json` (6), `iam/cd-diff-trust.json` (6). (`iam/readonly-policy.json` NO tiene account — no
  se toca su account; verificar si tiene otros identificadores.)
- `iam/*.sh` — los **4 scripts**: `iam/bootstrap-gate.sh` (guard `367707589526` en 11,12,60 + role ARN),
  `iam/simulate-matrix.sh` (default de perfil `AdministratorAccess-367707589526` en :5),
  `iam/post-deploy-identity-check.sh` (default `arn:aws:iam::367707589526:user/mail-readonly` en :24),
  `iam/ses-dkim-wait.sh` (verificar — región no-secreta, quizá sin account).
- docs: `README.md`, `CHANGELOG.md`, `docs/architecture.md`, `docs/BOOTSTRAP.md`, `docs/SP-1-DEPLOY.md`,
  `docs/SP-2-DEPLOY.md`, `docs/SP-3-DEPLOY.md`, `docs/SP-4-DEPLOY.md`, `docs/CD-DEPLOY.md` (el más denso, ~40),
  `docs/ROADMAP.md`, `docs/METHODOLOGY.md`, `docs/superpowers/EXECUTION-LOG.md`, `docs/diagrams/architecture_icons.py`
  (labels), y los specs/plans bajo `docs/superpowers/` que narren el account (incl. ESTE plan y su spec — redactar
  el account de los ejemplos a `<ACCOUNT_ID>` salvo donde sea pedagógicamente necesario mostrarlo).

**Interfaces:** ninguno — sanitización de prosa/artefactos out-of-band. Cero consumo runtime (verificado: 0
`go:embed`/`os.ReadFile` de `iam/`). Los `.sh`/`.json` los aplica un HUMANO out-of-band, nunca el código ni el CI.

- [ ] **Step 1: `iam/*.json` (los 5 con account, nombrados arriba)** — reemplazar `367707589526` por `<ACCOUNT_ID>`
  en CADA ARN. Añadir al header de cada JSON (o a BOOTSTRAP.md) una nota: "sustituir `<ACCOUNT_ID>` por la cuenta
  real al aplicar con `aws iam ... file://`". Verificar también `iam/readonly-policy.json` por si tiene ARNs con
  account (el inventario dijo que su bucket-scope es `arn:aws:s3:::*erickaldama*`, sin account — pero confirmar).

- [ ] **Step 2: `iam/*.sh` (los 4, nombrados arriba)** — `bootstrap-gate.sh`: el guard
  `[[ "$acct" == "367707589526" ]]` → `[[ "$acct" == "${EXPECTED_ACCOUNT:?set EXPECTED_ACCOUNT}" ]]` y el role ARN
  del probe (`:60`) a placeholder/env. `simulate-matrix.sh:5` y `post-deploy-identity-check.sh:24`: los defaults de
  perfil/ARN `AdministratorAccess-367707589526` / `arn:aws:iam::367707589526:user/mail-readonly` →
  `AdministratorAccess-${EXPECTED_ACCOUNT}` / `${EXPECTED_ACCOUNT}`-derivado, con la env var documentada en el
  header del script. `ses-dkim-wait.sh`: verificar; probablemente solo región (no-secreta), sin cambio de account.

- [ ] **Step 3: docs `*.md` + diagramas** — redactar `367707589526` → `<ACCOUNT_ID>` y los ARN a forma placeholder,
  en README, CHANGELOG, architecture.md, todos los `*-DEPLOY.md`, BOOTSTRAP.md, CD-DEPLOY.md, EXECUTION-LOG.md, y
  los labels del `.py`. Sanitización pura — no rompe nada. (El PNG del diagrama se regenera si sus labels llevaban
  el account — verificar; probablemente no.)

- [ ] **Step 4: GATE 3 final — grep de fuga en TODO el repo.** (HUMANO)
```
grep -rn 367707589526 .
```
Expected: **cero hits** en cualquier archivo versionado (el account ya no vive en ninguna parte del source; solo
en el runtime env de CI/deploy). Si queda alguno, redactarlo.

- [ ] **Step 5: Commit.** (HUMANO)
```
git add -A
```
```
git commit -m "chore(nda): redactar Account ID + ARNs a placeholders en docs, iam/*.json y scripts out-of-band"
```

---

### Task 7: Bitácora + PR + gate CI real

**Files:**
- Modify: `CHANGELOG.md` (entrada del frente NDA), `docs/ROADMAP.md` (si aplica — es infra, no versión de cliente).
- Create/Modify: un runbook corto del frente (opcional) o la nota en BOOTSTRAP.md sobre los secrets + el
  `<ACCOUNT_ID>` placeholder.

- [ ] **Step 1: Bitácora.** Entrada en CHANGELOG narrando: el frente NDA, approach #3 (Env inyectado), el inventario
  (0 credenciales), byte-identidad como gate, los 2 GitHub secrets como prerrequisito de deploy. Referenciar el
  skill `iac-inject-env-never-hardcode-account`.

- [ ] **Step 2: Commit bitácora + push de la rama.** (HUMANO)
```
git add CHANGELOG.md docs/ROADMAP.md
```
```
git commit -m "docs(nda): bitácora del frente de parametrización de identificadores"
```
```
git push -u origin feature/nda-parametrize-account-arns
```

- [ ] **Step 3: Abrir el PR a develop.** (HUMANO)
```
gh pr create --base develop --head feature/nda-parametrize-account-arns --title "feat(nda): parametrizar Account ID + ARNs del repo público (Env struct inyectado)" --body-file <cuerpo>
```
El cuerpo (que el agente redacta) incluye: el inventario (0 credenciales), el approach #3 + por qué no #4, la
evidencia de byte-identidad (los `cdk diff` vacíos), el prerrequisito de los 2 secrets, y el skill universal.

- [ ] **Step 4: GATE 4 — el `cdk-diff` job del PR verde con diff vacío.** El job corre con el rol OIDC real
  (`mail-cd-diff` vía el secret nuevo) → prueba end-to-end que el CD funciona con los secrets + el account de la
  env, y que el template desplegado NO cambia. Este es el gate final del frente. Si el job comenta un diff ≠ 0,
  hay un stack cuyo `fmt.Sprintf` no reprodujo el literal — volver a Task 4 Step 3.

- [ ] **Step 5: Review whole-branch (F5) + external review (F6) + auditoría 6-ejes** sobre el PR — como todo frente
  de infra serio. Los hallazgos al PR (evidencia auditable).

---

## Self-Review (checklist del autor del plan)

**1. Cobertura del spec:** §3 approach #3 → Tasks 1-4. §4.1 Env struct → Task 1-2. §4.2 cd.yml secrets → Task 5.
§4.3 docs/iam → Task 6. §5 gates (byte-identidad, tests, grep, CI) → Task 4 Steps 3-4 + Task 6 Step 4 + Task 7
Step 4. §6 orden (spike primero) → Task 1 es el spike. ✅

**2. Placeholder scan:** los `<ACCOUNT_ID>`, `000000000000`, `<cuerpo>` son sustituciones intencionales (el diseño
mismo / redacción en el momento), no TODOs. Sin "implementar después".

**3. Consistencia de tipos:** `infra.Env` con los 10 campos referenciado igual en Tasks 1/2/3/4. `NewEnv()` la
factory. Los `fmt.Sprintf` de Task 2 deben producir byte-idéntico a los literales que Task 2 Step 2 elimina —
verificado contra `naming.go:29,33,58,71-74`.

**4. Restricción del ejecutor:** cada Step `go`/`git`/`gh`/`cdk`/`grep` marcado (HUMANO) — coherente con el hook
SP-0. El spike (Task 1) es el go/no-go: si SendingStack no da byte-identidad, se re-evalúa el approach ANTES de
propagar.

---

## Registro de la auditoría adversarial del plan (F2) — 7 hallazgos aplicados

El plan pasó una auditoría adversarial de 3 frentes (byte-identidad vs literales reales · blast-radius/call-sites ·
coherencia+CI+gates), cada uno contra el código real + doc oficial viva. Hallazgos cazados y corregidos:

| Hallazgo | Sev | Corrección aplicada |
|---|---|---|
| `sending_stack.go:171` (`NewAccountPrincipal(Account)`) consumidor omitido → compile break | **CRIT** | Task 1 Step 3 + Task 3 migran `addSendIam(stack, env)` cubriendo `:161` Y `:171` |
| `receiving_stack.go:142,151` (`ReceiptRuleArn`) 2 consumidores omitidos | **CRIT** | Task 3 Files + Step 2 los mapean a `props.Env.ReceiptRuleArn` |
| `CDK_DEFAULT_ACCOUNT` NO lo pone `configure-aws-credentials`; fallback silencioso a `000000000000` → diff gigante | **CRIT** | Task 5 Step 3 nuevo: export explícito `CDK_DEFAULT_ACCOUNT=$(aws sts get-caller-identity ...)` en el CI |
| Threading NO uniforme (5 firmas heterogéneas: `bucket` de receiving, 2 returns de mail_storage) | **MAJOR** | `EnvStackProps` wrapper (cambio de TIPO del props, no 4º param) — uniforme en los 5 |
| Baseline byte-identidad all-stacks nunca capturado (Task 4 diffea contra HEAD ya mutado) | **MAJOR** | Task 1 Step 1 captura `/tmp/all-head.json` (5 stacks) ANTES de editar; Task 4 Step 3 diffea contra él |
| Trampa de consistencia: tests deben derivar stack-Env Y asserts del MISMO `NewEnv()` | **MAJOR** | Task 4 Step 1: UN `e := NewEnv()` compartido por test, prescrito con código |
| `mail_storage_stack_test.go` (5º test) omitido — churn por el wrapper | **MINOR** | Task 4 Files lista los 5 tests |

**Verificado TRUE por la auditoría (no defectos):** los 9 `fmt.Sprintf` son byte-idénticos char-por-char a los
literales (incluidos los 4 `Cdk*RoleArn` con account-doble, args `a,a,r`); nada se vuelve token CDK (el template
queda literal); ningún test hardcodea `367707589526` (todos derivan de constantes → se mueven con la fuente);
redactar docs/iam NO rompe ningún test (0 `os.ReadFile` de docs/iam); sin broken-commit (el estado no-compilable
Task 2→3 nunca se commitea; `ci.yml` no corre en `feature/*`); los 8 iam files con account nombrados completos;
`mail_storage_stack.go` account-free; el cliente Go totalmente desacoplado. **Cero defectos de byte-identidad; los
7 hallazgos eran huecos de cobertura/robustez del plan, todos cerrados.** Es `layered-audit-catches-distinct-
defect-classes` aplicado a un plan: la auditoría del plan caza los consumidores omitidos y la fragilidad del CI que
la escritura del plan no vio — antes de tocar una línea de código.
