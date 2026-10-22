# Parametrización NDA — sacar el Account ID + ARNs del repo público (spec de diseño)

**Estado:** aprobado, listo para plan.
**Frente:** NDA / reducción de superficie de reconocimiento. El repo `esaldgut/erickaldama-mail` es **público** y
expone el AWS Account ID `<ACCOUNT_ID>`, ARNs y nombres de recurso en el source, el CI, y los docs. Este frente los
saca del código versionado **sin romper el CI/CD ni el CDK que ya despliega**.
**Approach:** #3 (Env struct inyectado) para el CDK, GitHub secrets para el CI, redacción a placeholders para docs.
Regla universal codificada en el skill `iac-inject-env-never-hardcode-account`.

---

## 1. Encuadre — identificadores, NO credenciales (inventario verificado)

Un inventario exhaustivo (3 agentes, lectura directa por el hook que deniega grep) estableció:

- **CERO credenciales en git.** No hay `AKIA`/`ASIA`, secret access keys, session tokens, API keys, MongoDB URIs
  ni bearer tokens. Cada string tipo-secreto es una regex de redacción (`.claude/hooks/cdk-go-guard.sh`), un
  nombre de env var (`ANTHROPIC_API_KEY` leído con `os.Getenv`), o un placeholder de runbook. El historial fue
  reescrito con `git-filter-repo` (89 commits) antes del primer push público. **Nada que rotar.**
- **Lo expuesto son IDENTIFICADORES:** el Account ID, ARNs, HostedZoneID, nombres de recurso. No otorgan acceso
  sin un principal autenticado — el OIDC trust (`sub: repo:esaldgut/erickaldama-mail:environment:production`) es
  el boundary real y sigue intacto. La exposición es **superficie de reconocimiento**, no compromiso.
- **Tráfico GitHub (2026-08-07):** 0 forks, 0 views, 22 clones / 12 únicos en 14 días — el pico de 16 de hoy son
  los runners de CI de la sesión de trabajo; ~5 dispersos previos podrían ser terceros/bots (GitHub no revela
  quién). Sin forks = sin copias persistentes públicas.

**Consecuencia:** esto es higiene NDA hacia adelante, no contención de brecha. Sin urgencia de emergencia, pero es
lo correcto — un Account ID + estructura IAM en un repo público es reconocimiento gratis para un atacante.

## 2. Superficies de fuga (mapeadas con `file:line`)

Cuatro superficies distintas; ningún mecanismo único las cubre todas:

| Superficie | Qué | Consumidor | Rompe si se cambia mal |
|---|---|---|---|
| **A — CI role ARNs** | `role-to-assume` literal ×2 | GitHub Actions runner (`cd.yml:39,94`) | El CD (diff en PR, deploy en main) |
| **B — CDK account/region** | `env()` hardcoded + `Account`/`Region` const | `cdk synth`/`deploy` (`cmd/cdk/main.go:44`, `internal/infra/naming.go:5-6`) | El deploy y los template-assertion tests |
| **C — ARNs de recurso** | 6 constantes ARN con account embebido | Los stacks al sintetizar (`naming.go:29,33,58,71-74`) | El template sintetizado |
| **D — docs + iam/*.json** | ~100 ocurrencias en prosa + 7 JSON out-of-band | Nadie en runtime (verificado: 0 `go:embed`/`os.ReadFile`) | Nada |

**Hallazgos del mapa de call-sites que moldean el diseño:**
- **Cero const-context blockers** — ningún identificador se usa en posición de constante de compilación; las 4
  alternativas compilan. La elección es robustez, no compilación.
- **El cliente Go está TOTALMENTE desacoplado** — sus consts viven en `internal/awsconf/config.go` (region +
  nombres, SIN account); nada del cliente importa `internal/infra`. Blast radius del fix = **cero en el cliente**.
- **Dos deudas pre-existentes que el fix cierra de paso:** (1) `cmd/cdk/main.go:44` re-hardcodea el account en
  vez de usar la constante (segunda fuente de verdad); (2) los ARN de DynamoDB/SNS se recomponen inline por
  concat (`foundation_stack.go:71`, `receiving_stack.go:196`) duplicando el literal `us-east-1` en vez de reusar
  `Region`.
- **Los template-assertion tests** (`cd_stack_test.go`, `foundation_stack_test.go`, etc.) **ya derivan todo de las
  constantes** (`Account`, `CdkLookupRoleArn`, `":iam::" + Account + ":..."`) — ninguno hardcodea `<ACCOUNT_ID>`.
  Consecuencia clave: si las constantes pasan a derivarse del `Env`, template y assert se mueven juntos → **los
  tests pasan sin cambiar sus expectativas** (byte-identidad por construcción).

## 3. Decisión de approach — #3 (Env struct inyectado), no #4 (tokens CDK)

Se evaluaron 4 alternativas (detalle y árbol de decisión en el skill `iac-inject-env-never-hardcode-account`):

| # | Approach | Veredicto para este frente |
|---|---|---|
| 1 | `const → var` con `os.Getenv` en init | **Descartado** — atajo frágil (estado global capturado en init; un test que setea la env después ve el valor viejo) |
| 2 | `const → func` (`IdentityArn()`) | Aceptable pero ruidoso (~15 call-sites a `X()`); inferior a #3 |
| 3 | **`Env` struct construido 1× + inyectado** | **ELEGIDO** |
| 4 | tokens CDK nativos (`*stack.Account()`, `Fn_Sub`) | Descartado para este frente (ver abajo) |

**Por qué #3 y no #4** (el criterio de corte es la byte-identidad del template):
- **#4 cambia el template** (account literal → `{"Ref":"AWS::AccountId"}`) → el `cdk diff` ya NO saldría vacío, y
  rompe los template-assertion tests que asertan el string ARN exacto. Se exige byte-identidad para **no arriesgar
  el CD ya vivo** (`b22449f` y anteriores desplegados).
- Los 4 `cdk-hnb659fds-*` role ARNs llevan el account **DENTRO del nombre**
  (`cdk-hnb659fds-lookup-role-<ACCOUNT_ID>-us-east-1`), no solo en el campo ARN → `*stack.Account()` no basta,
  obligarían a `Fn::Sub` de todos modos. #4 no sería limpio aquí.
- **#3 preserva byte-identidad** — los ARN siguen siendo los mismos strings, solo construidos desde `Env.Account`
  en vez de literales. `cdk diff` vacío, tests intactos.

**#3 es el piso mínimo universal del skill; #4 el techo. Aquí #3 es la elección correcta, no una concesión.**

## 4. El cambio — por superficie

### 4.1 Superficie B+C — Env struct inyectado en `internal/infra`

**Nuevo: un resolver de env + un `Env` struct construido una vez** (archivo nuevo `internal/infra/env.go`):

```go
package infra

import (
	"fmt"
	"os"
)

// awsAccount resolves the target account: CDK_DEFAULT_ACCOUNT in CI/deploy (populated by the CDK CLI
// from the OIDC-assumed credentials), falling back to a NON-SECRET placeholder for offline synth/test
// determinism. The real account never lives in source.
func awsAccount() string {
	if a := os.Getenv("CDK_DEFAULT_ACCOUNT"); a != "" {
		return a
	}
	return "000000000000" // placeholder — deterministic synth/test; never the real account
}

func awsRegion() string {
	if r := os.Getenv("CDK_DEFAULT_REGION"); r != "" {
		return r
	}
	return "us-east-1" // region is not secret; this default keeps offline synth deterministic
}

// Env carries the account/region-derived identifiers, built once. Injected via StackProps helpers so
// no stack reads a package-global account constant. Closes the pre-existing duplication (main.go
// re-hardcode + inline ARN concat).
type Env struct {
	Account, Region string
	IdentityArn, ConfigSetArn, ReceiptRuleArn                                    string
	CdkLookupRoleArn, CdkDeployRoleArn, CdkFilePublishingRoleArn, CdkImagePublishingRoleArn string
	DynamoTableArn, BounceTopicArn                                               string
}

func NewEnv() Env {
	a, r := awsAccount(), awsRegion()
	return Env{
		Account: a, Region: r,
		IdentityArn:    fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", r, a, DomainName),
		ConfigSetArn:   fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", r, a, ConfigSetName),
		ReceiptRuleArn: fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s:receipt-rule/%s", r, a, ReceiptRuleSetName, ReceiptRuleName),
		CdkLookupRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-lookup-role-%s-%s", a, a, r),
		CdkDeployRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-deploy-role-%s-%s", a, a, r),
		CdkFilePublishingRoleArn:  fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-file-publishing-role-%s-%s", a, a, r),
		CdkImagePublishingRoleArn: fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-image-publishing-role-%s-%s", a, a, r),
		DynamoTableArn: fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", r, a, MailIndexTableName),
		BounceTopicArn: fmt.Sprintf("arn:aws:sns:%s:%s:%s", r, a, BounceTopicName),
	}
}
```

**Cambios en `naming.go`:** ELIMINAR las 6 constantes ARN con-account (`IdentityArn`, `ConfigSetArn`,
`ReceiptRuleArn`, los 4 `Cdk*RoleArn`) y las constantes `Account`/`Region` — pasan a ser campos de `Env`. Las
constantes de NOMBRE (`DomainName`, `ConfigSetName`, `MailIndexTableName`, `BounceTopicName`, role names, etc.) se
CONSERVAN (no son sensibles y `NewEnv` las usa para componer). `HostedZoneID` NO embebe account → se conserva como
const (es un id de recurso público, aparece en el README; opcional moverlo a env, pero no es fuga de account).

**Cambios en `cmd/cdk/main.go`:** `env()` deja de hardcodear; construye `NewEnv()` UNA vez y pasa
`env.Account`/`env.Region` a `StackProps.Env`, y threadea el `Env` (o los campos necesarios) a cada `NewXxxStack`.
Esto cierra la deuda de la segunda fuente de verdad.

**Cambios en los 5 stacks:** cada `NewXxxStack` recibe el `Env` (o los campos que use) por parámetro/prop, y usa
`env.IdentityArn` etc. en vez de la constante global. Las composiciones inline (`"arn:...:" + Account + ...`) se
reemplazan por `env.DynamoTableArn` / `env.BounceTopicArn` (deuda de duplicación cerrada).

**Cambios en los 4 tests:** `synthXxx` construye su `Env` con `NewEnv()` (misma env/fallback) y asierta contra
`env.CdkLookupRoleArn` etc. en vez de la constante. Como template y assert derivan del mismo `Env`, **las
expectativas no cambian** — el placeholder `000000000000` (sin `CDK_DEFAULT_ACCOUNT`) aparece en AMBOS lados.

### 4.2 Superficie A — CI role ARNs → GitHub secrets

`cd.yml:39` y `:94`: reemplazar el ARN literal por `${{ secrets.AWS_CD_DIFF_ROLE_ARN }}` y
`${{ secrets.AWS_CD_DEPLOY_ROLE_ARN }}`. Drop-in confirmado por la doc de `aws-actions/configure-aws-credentials`.
El deploy-role secret se scopea al environment `production` (que el workflow ya usa como gate). **Acción humana
out-of-band:** crear los 2 secrets en GitHub (repo o environment settings) ANTES de mergear — si no, el CD falla
al no resolver el ARN. Esto se documenta en el runbook y el PR.

`aws-region: us-east-1` (×2 en `cd.yml`) → opcional a `${{ vars.AWS_REGION }}` (región no es secreta; variable
plaintext basta). Decisión menor; se puede dejar el literal `us-east-1` (no es fuga de account).

### 4.3 Superficie D — docs + iam/*.json redactados a placeholders

- **`iam/*.json` (7 archivos):** son espejos de auditoría out-of-band (0 `go:embed`/`os.ReadFile` — verificado).
  Redactar el account a `<ACCOUNT_ID>` en los ARN. **Riesgo: cero en CI/CD** (nadie los lee en runtime). Nota: si
  un humano los aplica con `aws iam ... file://`, tendrá que sustituir el placeholder — documentarlo en el header
  de cada JSON o en BOOTSTRAP.md.
- **`iam/*.sh` (4 scripts):** el guard `[[ "$acct" == "<ACCOUNT_ID>" ]]` de `bootstrap-gate.sh` y los defaults de
  perfil `AdministratorAccess-<ACCOUNT_ID>` — parametrizar a una env var (`${EXPECTED_ACCOUNT}`) con el account
  inyectado al correr, o redactar a placeholder documentado. Estos scripts son out-of-band (el humano los corre).
- **docs `*.md` (~100 ocurrencias):** redactar `<ACCOUNT_ID>` → `<ACCOUNT_ID>` y los ARN completos → forma
  placeholder. Es sanitización pura de prosa; no rompe nada. Incluye README, CHANGELOG, architecture.md, todos los
  `*-DEPLOY.md`, BOOTSTRAP.md, CD-DEPLOY.md, EXECUTION-LOG.md, y los diagramas (labels).

## 5. Verificación (el gate — byte-identidad)

- **Gate 1 (byte-identidad del template) — el más importante:** con `CDK_DEFAULT_ACCOUNT=<ACCOUNT_ID>` y
  `CDK_DEFAULT_REGION=us-east-1` exportados, `cdk synth` debe producir un template **byte-idéntico** al de HEAD
  antes del cambio → `cdk diff` **VACÍO** contra el stack desplegado. Ese diff vacío prueba que #3 no cambió infra
  real, solo de dónde salen los valores. Procedimiento: `cdk synth` en HEAD → guardar `cdk.out/*.template.json`;
  aplicar el cambio; `cdk synth` de nuevo con las env vars; `diff` de los templates = 0.
- **Gate 2 (tests) — sin credenciales, determinista:** `go build ./... && go vet ./... && go test -count=1 ./...`
  verde. Los template-assertion tests corren SIN env (usan el placeholder `000000000000` en ambos lados) → pasan.
  Los tests del cliente (`awsconf`) intactos (desacoplado).
- **Gate 3 (grep de fuga):** `LC_ALL=C grep -rn '<ACCOUNT_ID>' --include=*.go --include=*.yml` sobre el source de
  código = **cero** (el account solo puede quedar en docs redactadas como `<ACCOUNT_ID>`, nunca en `.go`/`.yml`).
- **Gate 4 (CI real):** el `cdk-diff` job del PR (que corre con el rol OIDC real) debe salir con diff vacío — es la
  prueba end-to-end de que el CD sigue funcionando con los secrets nuevos y el account de la env.

## 6. Orden de ejecución (sin romper nada en ningún punto intermedio)

0. **SPIKE de validación de la hipótesis central, ANTES de tocar los 5 stacks.** El approach #3 asume que
   construir los ARN desde `Env.Account` (via `fmt.Sprintf`) produce strings **byte-idénticos** a los literales de
   hoy → template idéntico. Esto NO está verificado — es la premisa de todo el frente. Probarlo con UN solo stack
   primero (p.ej. `SendingStack`, que usa `IdentityArn`+`ConfigSetArn`): parametrizar solo ese, correr Gate 1
   (diff de `cdk synth` HEAD vs cambiado con `CDK_DEFAULT_ACCOUNT=<ACCOUNT_ID>`) = 0. Si el diff NO es vacío
   (p.ej. `fmt.Sprintf` mete un espacio, o el orden de un campo cambia), PARAR y ajustar el patrón antes de
   propagarlo. Cómo threadear el `Env` a los stacks (campo en un `StackProps` custom vs parámetro extra en
   `NewXxxStack`) lo decide este spike mirando las firmas reales — el plan lo fija con el código enfrente.
1. **Env struct (B+C) al resto de los stacks**, verificado con Gate 1+2 localmente. El código compila y los tests
   pasan ANTES de tocar el CI.
2. **cd.yml → secrets (A)** — pero los secrets de GitHub se crean out-of-band ANTES del push, o el `cdk-diff` job
   del PR falla. El PR se abre solo cuando los secrets existen.
3. **docs + iam/*.json (D)** — sanitización, sin riesgo; puede ir en el mismo PR o commit separado.
4. **PR a develop** con Gate 4 (el `cdk-diff` job verde con diff vacío) como evidencia final.

## 7. Invariantes preservados

- **El CD sigue funcionando** — el account viene del rol OIDC en runtime; los secrets resuelven el role ARN. Gate 4
  lo prueba end-to-end.
- **Byte-identidad del template** — `cdk diff` vacío (Gate 1). Cero cambio de infra real.
- **El cliente Go intacto** — desacoplado de `internal/infra`.
- **Los template-assertion tests pasan sin cambiar expectativas** — template y assert derivan del mismo `Env`.

## 8. Fuera de alcance (registrado, no en este frente)

- Migrar `HostedZoneID` o los nombres de recurso a env — no son fuga de account; over-reach.
- Mover el cliente `awsconf` a leer de env — el cliente no expone account; su config es local.
- SSM Parameter Store como fuente única CI+código — el approach en capas ($0) no lo necesita; queda como opción
  futura si se quiere una sola fuente de verdad.

## 9. Referencias

- Skill universal (la regla): `iac-inject-env-never-hardcode-account`.
- Inventario + análisis de consumo + costo-beneficio: los 4 agentes de F0 de este frente (2026-08-07/08).
- Contrato CDK env-agnostic: `https://docs.aws.amazon.com/cdk/v2/guide/environments.html` (CDK_DEFAULT_ACCOUNT).
- GitHub secrets en `role-to-assume`: `https://github.com/aws-actions/configure-aws-credentials`.
- Memoria del frente: `project-nda-parametrization`. Estado del proyecto: `project-erickaldama-mail-state`.
