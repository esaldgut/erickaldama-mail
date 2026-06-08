# SP-0 — Gobernanza CDK-Go: design spec

> Subproyecto 0 del sistema de correo erickaldama.com. Transversal: hace la premisa CDK-Go imperativa
> e idempotente. Debe estar activo antes de que cualquier infra (SP-1/2/3) toque AWS.
> Fecha: 2026-06-07. Cuenta ErickSA 367707589526. Región us-east-1.

## Context

El proyecto de correo se aprovisiona EXCLUSIVAMENTE vía AWS CDK en Go (`github.com/aws/aws-cdk-go/awscdk/v2`,
versión más reciente verificada en vivo). SP-0 garantiza esa premisa de forma imperativa (no depende de la
memoria/juicio del agente) e idempotente (se aplica idéntico en cada sesión). El valor diferenciador no es
"usar CDK" — es la GOBERNANZA que fuerza cómo se aprovisiona + las RECETAS que codifican cómo hacerlo bien,
verificadas contra docs oficiales vivos. Mismo principio que lessongate: control determinista (hook) gobierna,
conocimiento (skills) ejecuta dentro de los rieles.

## Dos artefactos separados (por scope)

```
ARTEFACTO 1 — El plugin (distribuible, versionable, portafolio)
  <plugin>/cdk-go-aws-plugin/
  ├── .claude-plugin/plugin.json
  ├── skills/
  │   ├── cdk-go-recipe/SKILL.md       (receta CDK-Go + verify-before-act)
  │   └── ses-domain-recipe/SKILL.md   (receta SES 8 pasos + runbook)
  └── .mcp.json   (aws-knowledge MCP [docs vivos] + aws-api MCP [manos, read-only])

ARTEFACTO 2 — El hook (fricción/UX, scoped-al-proyecto-mail)
  ~/.claude/settings.json → hooks.PreToolUse (matcher "Bash" + matcher "mcp__aws-api__.*")
  + ~/.claude/hooks/cdk-go-guard.sh
```
**Por qué separados (decisión de scope, NO necesidad técnica):** el plugin son recetas reutilizables
instalables en cualquier proyecto AWS; el hook es **scoped al proyecto de correo** (se activa solo cuando cwd
canonicaliza bajo su root; promoción a global = tarea #20, cuando madure). Un plugin SÍ PUEDE llevar el hook
(`hooks/hooks.json`) — lo ponemos en settings.json aparte porque su ciclo de vida (scope, promoción) es distinto
al del plugin (recetas portables), no porque la plataforma lo exija. **Relación:** el hook reduce fricción (atrapa
olvidos comunes con buen mensaje) — NO es el límite; el plugin ENSEÑA cómo hacer CDK-Go y SES bien. El LÍMITE es
IAM (ver Framing de seguridad + Componente A2).

## ⚠️ FRAMING DE SEGURIDAD (reescrito tras auditoría adversarial 2026-06-08, doc 08)

**El LÍMITE de seguridad es la IAM allowlist-pura (Componente A2 Capa 1), NO el hook.** Documentación
oficial de Anthropic, verbatim: *"hooks are not security boundaries... use the permission system, not a
hook, to enforce a hard allow or deny."* Además el harness **falla ABIERTO**: si el hook script crashea,
hace timeout o emite JSON malformado, el comando se PERMITE. Y `bypassPermissions` puede saltarse el deny.

Por tanto el hook se reframe a **fricción / UX**: atrapa los olvidos comunes con un mensaje claro y
self-deniega ante cualquier error, pero NO se vende como fail-closed ni como el control. El control real
vive en 3 lugares que NO dependen del agente: (1) **IAM allowlist-pura** (AWS rechaza la mutación),
(2) **el patrón "preparo, el humano ejecuta out-of-band"** (toda mutación corre en una terminal que el
agente no ve), (3) **branch protection + secret-scan** del repo público (rechaza el PR si algo se cuela).

## Componente A — Hook PreToolUse (cdk-go-guard.sh) — capa de FRICCIÓN/UX, scoped-al-proyecto

Lee de stdin (formato verificado contra docs Claude Code 2026-06-08): `tool_input.command`, `cwd`,
**`permission_mode`**.
Deny vía JSON `hookSpecificOutput.permissionDecision="deny"` + `permissionDecisionReason` (da razón rica al agente).

Lógica (orden de evaluación) — SCOPED-AL-PROYECTO + SELF-DENY-ON-ERROR (reescrito tras auditoría):
```
0. trap de error: CUALQUIER fallo del script (set -euo pipefail, jq error, unbound var, timeout interno)
   → emitir {permissionDecision:"deny", reason:"hook error, fail-safe deny"} + exit 0. NUNCA confiar en
   que el harness falle cerrado (falla ABIERTO). El default del script es DENY, no allow. (SEC-C1)
1. permission_mode != "default" y != "plan" (ej. bypassPermissions)?
   → DENY todo comando gobernado + audit CRITICAL. El bypass no debe evaporar el deny. (SEC-C2)
2. SCOPE: canonicalizar cwd con realpath. ¿cwd NO está bajo el root del proyecto de correo
   (realpath ~/dev/src/go/src/erickaldama-mail, match por segmento de path, no prefijo crudo)?
   → ALLOW (no-op). El hook SOLO gobierna el proyecto de correo. NO rompe another-project/sample-ios-app/sample-android-app. (SEC-I4)
   [Futuro: promover a global cuando esté maduro — tarea #20.]
3. ¿el comando contiene METACARACTERES de encadenamiento/substitución? (&&, ||, ;, |, $(, backtick,
   newline, &, >, <, eval)
   → DENY "comando compuesto: entrégalo al humano". NO intentar parsear shell-grammar (la auditoría mostró
   que ls && aws s3 mb, $(aws...), FOO=x aws, find -exec aws burlan el first-token). Fail-closed por
   no-parseabilidad. (SEC-C3/I2/I3 — también mata el cd <x> && mutate spoof de cwd.)
4. PRIMER TOKEN (tras strip de asignaciones VAR=x) NO en allowlist estrecha?
   allowlist: aws, cdk, ls, cat, echo, grep, head, tail, jq, pwd, which, mkdir
   NO en allowlist: go, git, find, env, xargs, command, make, python3, node, bash, ./*, ruby, deno
   (go/git/find/env/xargs son MOTORES DE EJECUCIÓN — go test corre TestXxx con el AWS SDK,
    git -c core.pager='aws...', find -exec aws. Se entregan al humano.) (SEC-C3)
   → DENY/entregar al humano.
5. comando es `aws`? sub-comando en ALLOWLIST DE READS (describe*|list*|get*|ls|help + sts get-caller-identity)?
   → SÍ: allow.  → NO (incl. sts assume-role): DENY "mutación AWS la ejecuta el humano out-of-band".
6. comando es `cdk`? inspeccionar cdk.json (ausente/malformado → DENY): "app" contiene go run/go mod?
   → NO Go: DENY. → Go + diff|synth|ls (read-only): allow. → Go + deploy|destroy (muta): DENY/entregar al humano.
7. resto de la allowlist (ls, cat...) → allow.
```

**El hook es FRICCIÓN, no límite (framing honesto):**
- **Default DENY + self-deny on error.** El script nunca confía en el harness para fallar cerrado; emite deny
  explícito ante cualquier error. Pero esto NO lo hace un límite de seguridad — un bug sutil o bypass sigue
  siendo posible. Por eso el LÍMITE es IAM (A2 Capa 1).
- **Deny de metacaracteres, no parsing.** Más robusto que intentar replicar la gramática de shell (que pierde).
- **go/git/find/env fuera del allowlist plano** (son motores de ejecución que pueden llamar el SDK).
- **Scoped-al-proyecto-mail.** No global (rompería another-project/sample-android-app). Promoción a global = tarea #20, cuando madure.
- **Carrera que el hook no gana:** la auditoría es explícita — Write/Edit tool stagean payloads sin tocar Bash.
  El hook reduce fricción y atrapa olvidos; la garantía la da IAM + ejecución humana out-of-band + branch protection.

**El patrón resultante:** el agente RAZONA y GENERA (escribe el stack CDK-Go, propone reads); el HUMANO EJECUTA toda
mutación OUT-OF-BAND. La cuenta AWS nunca recibe un write que el humano no haya leído y ejecutado él mismo.

## Componente A2 — Control de mutaciones: IAM allowlist-puro (LÍMITE) + ejecución out-of-band (reescrito 2026-06-08)

**Capa 1 — IAM allowlist-PURO Y SCOPEADO = EL LÍMITE (única garantía dura).** (SEC-C4 + SEC2-C1/C2)
El MCP aws-api (y la credencial base de la sesión) usan una IAM policy **Allow-only**, scopeada a recursos
del proyecto en us-east-1, e implicit-deny todo lo demás. El allowlist NO es "lee toda la cuenta".
**La policy (`iam/readonly-policy.json`) tiene 4 statements, verificada acción-por-acción contra el AWS
Service Authorization Reference (SAR, 2026-06-08).** Estructura = **global-unconditioned + 2 regional-pinned +
hard-deny** — el split global-vs-regional es la corrección clave (un region-pin sobre acciones GLOBALES las
DENIEGA por error: era un bug latente de la versión de 2 statements):
```
1. AllowGlobalReadsUnconditioned (SIN condición — STS GetCallerIdentity y Route53 son GLOBALES):
     sts:GetCallerIdentity   route53:ListHostedZones/GetHostedZone/ListResourceRecordSets
     [aws:RequestedRegion sobre el endpoint STS global = us-east-1 sin importar la región real → un
      region-pin rompería el pre-flight bajo CLI v2; Route53 usa ARN sin región = señal de servicio global.]
2. AllowRegionalReadsUsEast1  (Condition aws:RequestedRegion = us-east-1):
     ses:Get*/List*/Describe*  (v1 y v2 — MISMO prefijo IAM `ses:` per SAR; cubre ambos)
     cloudformation:Describe*/List*   cloudwatch DescribeAlarms/ListMetrics/GetMetricData/GetMetricStatistics
3. AllowS3BucketLevelScopedUsEast1  (Resource arn:aws:s3:::*erickaldama*, Condition us-east-1):
     s3 SOLO bucket-level → ListBucket, GetBucketLocation, GetBucketPolicy, GetBucketPublicAccessBlock,
     GetEncryptionConfiguration   (NO s3:GetObject — el agente NO lee cuerpos MIME del correo, SEC2-C1)
4. HardDenyMutationReconAndCredentialMinting (Deny explícito — gana sobre cualquier Allow):
     ses:Send*  sts:AssumeRole/AssumeRoleWithWebIdentity/AssumeRoleWithSAML
     sts:GetSessionToken + sts:GetFederationToken (Read-classified per SAR pero MINTEAN credenciales →
       se deniegan POR NOMBRE; por eso el Allow usa sts:GetCallerIdentity y NO sts:Get*)
     s3:GetObject  cloudformation:GetTemplate/GetTemplateSummary  ses:GetIdentityPolicies/GetEmailIdentityPolicies
     iam:*  (recon de privilegios de la cuenta — SP-0 no lo necesita, SEC2-C2)
```
Por qué allowlist y NO denylist de verbos (SEC-C4): "Deny Create/Put/Delete/Update" deja pasar **ses:SendEmail
(corazón del proyecto), sts:AssumeRole, lambda:Invoke, ec2:Terminate, cfn:ExecuteChangeSet** — ninguno lleva
esos verbos. Allowlist scopeado cierra mutación Y recon/exfil: la 1ª reescritura cambió hueco-de-mutación por
hueco-de-lectura-total; este scoping cierra ambos.
**`sesv2` NO es un prefijo IAM** (verificado contra SAR): SES v1 y v2 son ambos `ses:`. Por eso el Allow
`ses:Get*` cubre v2 y el Deny `ses:Send*` cubre SendEmail/SendRawEmail/SendBulk*/etc. de AMBAS versiones.
NUNCA escribir `sesv2:` en el JSON (IAM lo trata como servicio desconocido → no concede nada). **`cloudformation:Deploy*`
NO es una acción real** (`aws cloudformation deploy` = CreateChangeSet+ExecuteChangeSet) → un Deny sobre ella
sería statement muerto; por eso NO aparece. Defensa en profundidad nativa del MCP abajo.
Env nativa del MCP: `READ_OPERATIONS_ONLY=true` + `REQUIRE_MUTATION_CONSENT=true` (complementan, no reemplazan IAM).
**Verificación REAL del límite (SEC2-I2):** `aws iam simulate-principal-policy` (corrido con un principal SEPARADO,
NO el read-only) para assert: intended-deny → implicitDeny/explicitDeny; intended-allow → allowed. Convierte
"verificable" de hand-wave a script falsificable. Live probes (ses send-email→Deny, s3 get-object→Deny,
iam list-access-keys→Deny, sts assume-role→Deny) como belt-and-suspenders.

**Capa 2 — hook Bash (FRICCIÓN/UX, Componente A).** Self-deniega, scoped-al-proyecto. NO es límite.

**Capa 3 — hook MCP matcher `mcp__aws-api__.*` (FRICCIÓN/UX).** (corrección DOCS)
Verificado: el hook SÍ recibe los argumentos del `call_aws` en `tool_input` → puede inspeccionar el comando.
Best-effort, self-deniega on error. server-key del MCP en .mcp.json DEBE ser `aws-api` exacto. Redundante con
Capa 1 (AWS rechaza el write igual).

**MUTACIONES (cdk deploy) — OUT-OF-BAND con hardening real, sin AssumeRole in-session.** (SEC-C5 + SEC2-C3)
"Terminal separada" NO es un límite por sí solo en la misma máquina (comparten ~/.aws/env/fs; si la cred
elevada cae en [default] o un AWS_PROFILE exportado, el siguiente Bash del agente la hereda — el mismo fallo
del JIT in-session, reubicado). El límite real es **qué credencial puede resolver la cadena del agente**:
```
1. El skill GENERA el stack CDK-Go + el comando exacto + el cdk diff a revisar. Lo ENTREGA (no ejecuta).
2. EL HUMANO ejecuta el deploy con su credencial elevada, bajo estos INVARIANTES (no opcionales):
   - cred elevada = named profile que el agente NUNCA selecciona (p.ej. [profile mail-deploy]);
     JAMÁS en [default], JAMÁS exportada a un shell que el agente herede.
   - mejor aún: deploy en OTRA máquina / CloudShell / CI, o un user OS cuyo ~/.aws el proceso del agente no lee.
   - la sesión del agente pinned al profile read-only.
3. El humano pega el output. El agente verifica (F4).
```
La IAM base **Deny sts:AssumeRole** → cierra auto-elevación. **DoD test negativo (SEC2-C3):** tras un deploy
out-of-band, `aws sts get-caller-identity` del SIGUIENTE Bash del agente AÚN devuelve el principal READ-ONLY,
no el de deploy. Sin ese test, "out-of-band" es afirmación, no propiedad verificada (mismo critique que mató el JIT).
Claude Platform on AWS (tarea #19) podría dar un mecanismo IAM más limpio a futuro; NO bloquea SP-0.

## Componente A3 — Bootstrap (t=0): excepción sancionada a la premisa CDK-Go (COMP-C3)

Chicken-and-egg: el MCP aws-api necesita un principal IAM read-only; crearlo ES un write AWS, pero la
gobernanza CDK-Go aún no existe en t=0 (o si existiera, bloquearía su propio setup). Resolución:
```
1. Acto manual, único, consciente: el HUMANO crea (Console AWS o aws-cli manual, con su credencial ADMIN
   —que el agente NUNCA ve—, NO vía agente) el principal IAM read-only (allowlist scopeado de Capa 1).
2. **GATE DE VERIFICACIÓN antes de apuntar el agente al cred (SEC2-I1)** — el límite entero depende de una
   policy tecleada a mano; si el humano se equivoca (pega ReadOnlyAccess, pone ses:* en vez de ses:Get*,
   olvida Deny sts:AssumeRole), el límite es nulo y nada lo cacha. Antes del primer uso:
   - diff del policy attached vs el JSON canónico de la spec; assert NO managed ReadOnlyAccess/PowerUser/Admin.
   - correr las probes de aceptación: ses send-email→Deny, sts assume-role→Deny, s3 get-object(bucket mail)→Deny,
     iam list-access-keys→Deny, + un read del set→OK. Solo si TODAS pasan, el principal se "acepta".
3. Se documenta como DEUDA DE BOOTSTRAP: SP-1 reescribe este IAM como stack CDK-Go, reproduciendo EXACTO el
   mismo policy y re-corriendo las mismas probes (la deuda no puede ensanchar el límite silenciosamente).
4. La premisa "todo vía CDK-Go" gobierna la INFRA del correo (SP-1/2/3). El principal read-only es andamiaje
   de la gobernanza — pre-requisito, no parte del proyecto. NO viola la premisa.
```
Analogía: es el bootstrap del state-bucket de Terraform — el primer recurso que habilita la herramienta se
crea fuera de ella, una vez, y luego se formaliza. Así arrancan los IaC.

**Fronteras de propiedad con SP-1 y SP-3 (resolución explícita de COH-I4 / doc 08 COMP-C4):**
- **IAM:** SP-0 DEFINE y VERIFICA el policy read-only (es su límite); **SP-1 lo FORMALIZA en CDK-Go** (es base IAM,
  dominio de SP-1). SP-0 dueño de la definición+verificación; SP-1 dueño de la formalización. No es ambiguo: son
  fases distintas del mismo artefacto.
- **Alarmas CloudWatch:** el skill SES de SP-0 EMITE el construct de alarma como ARTEFACTO DE RECETA (conocimiento
  de umbrales); **SP-3 lo POSEE y lo DESPLIEGA** (es su stack). SP-0 NO despliega. Receta genera, SP-3 deploya.

## Componente B — skill cdk-go-recipe

Frontmatter description: dispara al escribir/desplegar CUALQUIER infra AWS. Receta CDK-Go completa + verificación
contra docs vivos. 4 fases verify-before-act:
- **F1 VERIFY RULES:** (a) versión viva de aws-cdk-go (`go list -m -versions` / pkg.go.dev) vs go.mod — nunca
  hardcodear v2.258.0. (b) cada construct/API verificado contra docs vivos (ver Mecanismo de verificación abajo).
- **F2 READ STATE:** `aws sts get-caller-identity` (read, pasa hook) + `cdk diff` (read-only, muestra delta).
- **F3 ACT:** NO ejecuta `cdk deploy` — lo PREPARA y lo ENTREGA al humano (regla del hook). Genera comando + contexto.
- **F4 VERIFY OUTPUT:** tras deploy del humano, leer CloudFormation events/outputs. Error conocido → volver a F1.

Buenas prácticas Go codificadas (componen con modern-go-guidelines de JetBrains, no duplican):
- cdk.json `"app": "go mod download && go run ."`; main.go instancia App; stacks en archivos separados.
- jsii idioms: `jsii.String()`, `jsii.Number()` (CDK-Go usa jsii; punteros, no literales).
- cross-stack references (SP-1 expone zona → SP-2 consume) con `ReferenceStrength` (v2.258.0).
- `cdk diff` SIEMPRE antes de entregar deploy.

### Mecanismo de verificación (total + agente + caché) — aplica a CDK Y a SES
Verificación TOTAL (cada construct/API), optimizada con agente aislado + caché. **COMP-C1: un SKILL.md NO
puede lanzar un agente** (es texto pasivo); el plugin empaqueta un **agent definition real**
`agents/cdk-verifier.md`, y el skill INSTRUYE al agente principal a hacer dispatch vía Task. Honesto: el
dispatch es **best-effort** (depende de que el agente siga la instrucción), NO determinista — a diferencia
del LÍMITE IAM que sí lo es. La tesis se ajusta: *el límite (IAM) es imperativo; la verificación de calidad
es guiada-por-skill, best-effort*.
```
1. LEE caché local (docs/cdk-verified.json del proyecto): ¿todos los constructs verificados Y cdk_version
   == go.mod vivo Y verified_at dentro de TTL 7d?
   → SÍ: usa caché. CERO dispatch, CERO tokens de docs. (caso común)
   → NO ↓
2. El skill instruye dispatch de agents/cdk-verifier (su propio contexto, best-effort):
   - recibe: lista de constructs/APIs + versión objetivo
   - hace: search/read_documentation contra AWS Knowledge MCP para CADA uno (total)
   - devuelve: SOLO veredicto consolidado (~200 tokens), no los docs crudos (~decenas de miles)
3. ESCRIBE caché con el schema de abajo.
```

**Schema del caché (COMP-C2 — "firma" definida; per-proyecto; gitignoreado):**
```json
{
  "cdk_version": "v2.258.0",                    // de go.mod; invalida TODO si cambia
  "constructs": {
    "awsroute53.NewHostedZone": {
      "exists": true,
      "doc_url": "https://...",                 // fuente viva consultada
      "signature_hash": "sha256:...",           // hash SHA-256 de la firma documentada (props/args)
      "verified_at": "2026-06-08T12:00:00Z"     // TTL por-entrada (7 días)
    }
  }
}
```
- **"firma" = `signature_hash`**: hash de la firma documentada del construct → detecta si la firma CAMBIÓ
  entre verificaciones, no solo si existe.
- **Validez de una entrada:** `cdk_version == go.mod vivo` **Y** `verified_at` dentro de TTL 7d. Falla
  cualquiera → re-verificar. (Resuelve "matched how": no es set-equality difusa, es esta condición exacta.)
- **Ubicación: per-proyecto** (`docs/cdk-verified.json`), NO per-plugin — la invalidación depende de
  go.mod DEL proyecto. El plugin lo computa relativo al cwd en runtime.
- **GITIGNOREADO** (artefacto derivado, machine-local, time-sensitive; commitearlo = trampa de verdad-stale
  de otra máquina). Añadir `docs/cdk-verified.json` a .gitignore.
- **Anti-poisoning (N2):** el caché es escribible vía Write tool → al LEER, validar `cdk_version` contra
  go.mod vivo. Una entrada forjada con versión vieja no aplica. No HMAC (sobre-ingeniería para cache local);
  la validación-contra-versión-viva cierra el vector práctico.

Invalidación: (a) cdk_version de go.mod cambia; (b) TTL 7d por entrada (un patch release o cambio de AWS en
la misma versión quedaría stale sin esto); (c) flag `--force-verify` para invalidar manual.
Costo: AWS Knowledge MCP es GRATIS en USD (remoto, sin auth, rate-limited); el costo es TOKENS de contexto,
que el agente aislado contiene y el caché amortiza.

## Componente C — skill ses-domain-recipe (8 pasos cohesivos)

Un solo skill que conoce el ORDEN y las dependencias (lo que un skill granular perdería). Cada paso de
aprovisionamiento ejecuta las 4 fases verify-before-act (mismo mecanismo agente+caché que CDK-Go).

```
PASO 1 — Domain identity (DKIM-based)          [infra CDK-Go; crea 3 CNAMEs]
PASO 2 — DKIM verification                      [infra; depende de 1; ≤72h a Verified]
PASO 3 — Custom MAIL FROM (mail.erickaldama)    [infra; MX+SPF; SPF alignment]
PASO 4 — DMARC (_dmarc, p=none → quarantine → reject) [infra; depende de 3]
PASO 5 — Configuration set + event destination  [infra; ANTES de enviar]
PASO 6 — Sandbox exit (production access)        [OPERACIÓN DE CUENTA → comando entregado al humano,
                                                   no hay construct CFN para esto; aws sesv2 put-account-details]
PASO 7 — Inbound receiving (receipt rule→S3→Lambda) [infra; API v1 `ses`, NO sesv2]
PASO 8 — Post-provisioning runbook (monitoreo reputación)  [DOCUMENTACIÓN presentada al humano, NO ejecuta]
```

### PASO 8 — Runbook operacional (detalle)
**Cuándo:** inmediatamente tras paso 7, ANTES del primer envío. **NO es CDK ni AWS CLI** — el skill lo PRESENTA.

Umbrales críticos (AWS SES limits):
- Bounce > 5% → AWS pausa envíos de TODA la cuenta (no solo el dominio).
- Complaint > 0.5% → AWS pausa envíos.
- Si se cruza cualquiera: detener envíos INMEDIATAMENTE (no esperar a que AWS actúe).

Alarmas CloudWatch — umbrales CONSERVADORES (warning muy por debajo de la línea de review):
- `ses-bounce-rate > 2%` (warning) · `> 5%` (critical → SNS)
- `ses-complaint-rate > 0.05%` (warning) · `> 0.5%` (critical → SNS)

**El Paso 8 GENERA el código CDK-Go de las alarmas, no solo lo menciona (auditoría #4 — handoff débil).**
En lessongate el runbook era ejecutable; aquí el conocimiento (umbrales) debe producir el ARTEFACTO, no remitir
vagamente a SP-3. El skill emite el construct y lo entrega al humano como cualquier otra mutación CDK:
```go
// Generado por el Paso 8 del ses-domain-recipe
cloudwatch.NewAlarm(stack, jsii.String("SESBounceRateAlarm"), &cloudwatch.AlarmProps{
    Metric:             ses.MetricBounceRate(),
    Threshold:          jsii.Number(2),  // warning conservador
    ComparisonOperator: cloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
    EvaluationPeriods:  jsii.Number(2),
})
// + SESBounceRateCritical (5%), SESComplaintRateAlarm (0.05%), SESComplaintRateCritical (0.5%) → SNS
```
SP-3 los DESPLIEGA (es su stack), pero el conocimiento de QUÉ alarmas y QUÉ umbrales vive en el skill SES y
produce el código. El humano no tiene que recordar implementarlas: el skill las genera, el hook las entrega.

Runbook "reputación en rojo":
1. Pausar envíos (detener todas las campañas).
2. Identificar causa (suppression list? contenido? lista sucia?).
3. Limpiar suppression list (`aws sesv2 put-suppressed-destination` — comando entregado al humano).
4. Solicitar aumento de cuota (si es bounce por throttling, no por calidad).
5. Reanudar gradualmente (1% → 10% → 100% mientras monitorea).

### Composición SES ↔ CDK-Go (sin duplicar conocimiento)
El skill SES aporta el CONOCIMIENTO DE DOMINIO (qué hacer, en qué orden, las trampas); el skill CDK-Go aporta el
CONOCIMIENTO DE HERRAMIENTA (cómo escribir el construct en Go, verificado vs docs). El skill SES dice "necesito una
EmailIdentity con DKIM y custom MAIL FROM en este orden"; el CDK-Go dice "así se escribe ese construct". Una sola
fuente de verdad por tipo de conocimiento. Pasos de operación (6, 8) NO son CDK → comando/doc al humano.

### 6 trampas codificadas como guardrails (de la investigación)
1. Verificación de dominio ES DKIM-based (paso 1+2 = una transacción DNS, no dos).
2. DKIM suffix region-dependent → derivar de SigningHostedZone, no hardcodear dkim.amazonses.com.
3. Sandbox-exit ≠ salto de quota → leer quota viva, pedir aumento aparte.
4. put-account-details da 409 tras denegación → fallback Service Quotas API.
5. SPF límite 10 DNS lookups (RFC 7208) → contar antes de escribir el record.
6. Receiving = API v1 `ses`, NO sesv2.

## Componente F — Eval harness del skill (COMP-I1 — "el skill funciona" objetivo)

Un skill es no determinista (LLM-driven); el DoD no puede ser "el skill funciona". Eval harness =
**golden prompts → aserciones de propiedad sobre output no determinista**, vía `--dry-run` (genera el
CDK-Go sin ejecutar). Eval periódico (no unit test en cada build). Estructuralmente idéntico al eval
corpus de lessongate; la mejora es que aquí se AUTOMATIZA lo que en lessongate el humano leía.
```
Prompt golden: "crea SES domain identity para erickaldama.com con DKIM y custom MAIL FROM"
Correr: ses-domain-recipe --dry-run → capturar el CDK-Go generado
Aserciones POSITIVAS (lo que debe tener):
  ✓ contiene awsses.NewEmailIdentity         ✓ usa jsii.String(...)
  ✓ MAIL FROM antes de DMARC (orden de pasos)
Aserciones NEGATIVAS (lo que NO debe aparecer) — atrapan las regresiones peligrosas:
  ✗ NO contiene `cdk deploy`                  ✗ NO account IDs / ARNs hardcodeados
  ✗ NO literales crudos donde toca jsii       ✗ NO dkim.amazonses.com hardcodeado
Aserciones de las 6 TRAMPAS (una por trampa, no solo la #2):
  ✓ #1 DKIM-based (identity+DKIM juntos)      ✓ #2 DKIM derivado de SigningHostedZone
  ✓ #3 quota leída aparte del sandbox-exit    ✓ #4 fallback Service Quotas en 409
  ✓ #5 SPF cuenta ≤10 lookups                 ✓ #6 receiving usa ses v1, NO sesv2
```
- **Baseline de recall (Pass@1, Pass@3) registrado** — métrica estándar de generación LLM. Sin baseline
  no se detecta una degradación futura del skill. (Tu hallazgo Alta.)
- **Aserciones whitespace-resilientes** — normalizar whitespace / regex tolerante, no match exacto de
  string (el formato LLM varía → falsos negativos). (Tu hallazgo Baja.)
- Análogo para cdk-go-recipe: "crea un bucket S3" → usa awss3.NewBucket, jsii, SSE-S3, NO público.

## Componente D — Packaging del plugin
```
cdk-go-aws-plugin/
├── .claude-plugin/plugin.json   (name; version; author OBJETO {name,email,url}; mcpServers; defaultEnabled:false)
├── skills/{cdk-go-recipe,ses-domain-recipe}/SKILL.md   (description ≤1536 chars, caso clave primero)
├── agents/cdk-verifier.md       (el subagente verificador — COMP-C1; ver contrato abajo)
├── hooks/hooks.json             (OPCIONAL — el plugin SÍ PUEDE llevar el hook; ver nota de scope abajo)
└── .mcp.json   (aws-knowledge {type:http, url:https://knowledge-mcp.global.api.aws}
                 + server-key EXACTO "aws-api" → uvx awslabs.aws-api-mcp-server, READ_OPERATIONS_ONLY + REQUIRE_MUTATION_CONSENT)
```
- **server-key DEBE ser `aws-api`** (no `awslabs.aws-api-mcp-server`) para que el tool sea `mcp__aws-api__call_aws`
  y el matcher `mcp__aws-api__.*` del hook MCP coincida. (corrección DOCS)
- **`defaultEnabled:false` requiere Claude Code v2.1.154+** (versiones viejas habilitan on-install). Anotar floor.
- **El plugin SÍ puede llevar el hook** (hooks/hooks.json). PERO lo ponemos en `~/.claude/settings.json` aparte
  por DECISIÓN de scope (el hook es scoped-al-proyecto-mail ahora, global a futuro — tarea #20), no por necesidad
  técnica. (corrección DOCS — antes la spec implicaba que era obligatorio separarlo.)
- Distribuible/versionable (portafolio: demuestra que construyes plugins). Validar: `claude plugin validate --strict`.

### Setup paso 0 del subproyecto (decisión diferida resuelta aquí)
Plugins a instalar: aws-core (T1, trae aws-mcp + skill aws-cdk = manos) SÍ. aws-dev-toolkit (T3 sample, solapa con
la estrategia propia) NO. MCP: aws-knowledge (docs vivos, sin auth) + aws-api **con credencial IAM allowlist-puro
scopeado** (no solo `READ_OPERATIONS_ONLY` del MCP — una IAM policy Allow-only scopeada a recursos del proyecto en
us-east-1, sin s3:GetObject ni iam:*; ver Componente A2 Capa 1).
Prerequisito: `uv` (Astral) + `uv python install 3.10`.

**Este plugin es para Claude Code** (claude.com/code). El agente principal es Claude Code, que ejecuta los skills
según la especificación de Anthropic. (Vacío C de la auditoría.)

### Contratos de los mecanismos nuevos (cerrar COH-I1/I2/I3 antes del plan)

**agents/cdk-verifier.md (COH-I1):** frontmatter `name: cdk-verifier`, `description` (cuándo despacharlo),
**`tools:` allowlist EXACTA = solo las tools del AWS Knowledge MCP** (search_documentation, read_documentation) —
NADA de Bash, Write, ni el aws-api MCP (es un lector de docs, no toca AWS ni el FS). System prompt: "recibe lista
de constructs/APIs + versión objetivo; verifica cada uno contra docs vivos; devuelve SOLO el veredicto consolidado
(construct → {exists, doc_url, signature_hash}), nunca los docs crudos". Contrato de salida = el schema de entrada
del caché (Componente B).

**Detección de metacaracteres del hook (COH-I2):** regex sobre el string crudo `tool_input.command`. Set:
`&&  ||  ;  |  $(  ` + backtick + ` newline  &  >  <`. `eval` NO va en este set (sería substring que da
falso-positivo en `--query 'retrieval'`) → `eval` se maneja en el paso 4 (allowlist de primer-token: `eval` NO
está en la allowlist → deny por no-reconocido). Cualquier match del regex → deny "comando compuesto, entrégalo
al humano". (Resuelve la ambigüedad de qué paso posee `eval`.)

**Eval harness (COH-I3) — dónde vive, qué lo corre, dónde se guarda:**
- **Vive en** el repo del plugin: `cdk-go-aws-plugin/eval/` (golden prompts + runner + baseline).
- **Lo corre** un script Go `eval/run_eval.go` (o `go test ./eval -run Eval`) que: por cada golden prompt, invoca
  la skill en modo `--dry-run` vía `claude -p` (subprocess), captura el CDK-Go generado, y aplica las aserciones
  (positivas/negativas/6-trampas, whitespace-normalized). NO corre en cada build (es LLM, no determinista) — es
  eval periódico, invocado a mano o en CI nightly.
- **Pass@k:** cada golden prompt se corre k veces (Pass@1 = 1 run; Pass@3 = 3 runs independientes); pasa si TODAS
  las aserciones pasan. El baseline se guarda en `eval/baseline.json` (`{prompt, pass_at_1, pass_at_3, date}`),
  versionado en el repo del plugin. Una regresión = pass-rate por debajo del baseline → falla el eval.

## Componente E — Audit log + dry-run (vacíos A y B de la auditoría)

**E1 — Audit log de decisiones (`~/.claude/hooks/decision-log.json`)** [Vacío A]
Cada vez que el hook deniega/permite una acción gobernada, o el humano rechaza un cambio entregado, se registra una
línea append-only: `{ts, command (sanitizado), tool (Bash|mcp), decision (allow|deny|human-rejected), reason}`.
Mismo patrón que el rejection-audit de lessongate (N4): convierte un "se denegó algo" silencioso en señal queryable.
Si el humano rechaza un cambio, queda el porqué — el agente NO reintenta a ciegas; lee el rechazo.
**Integridad (N1):** append atómico (un solo `printf` línea o flock — dos hooks corren en paralelo); el log es
escribible por el agente vía Bash → para auditoría seria, append-only OS-level (uchg) o ship off-box. Secretos
nunca al log (sanitizar con los patrones estructurales, como obs de lessongate; ojo `--token-code`, presigned URLs,
`AWS_SECRET_ACCESS_KEY=` inline).

**E2 — Modo dry-run del skill SES (`ses-domain-recipe --dry-run`)** [Vacío B]
El skill SES corre las 4 fases (verifica contra docs, lee estado, GENERA los constructs/comandos) pero NO solicita
ejecución al humano — solo muestra qué HARÍA. Para probar la receta sin tocar producción ni gastar sandbox-exit.
El hook lo permite porque dry-run no muta AWS (solo reads + generación de código). Equivalente al dry-run de lessongate.

## Non-goals (SP-0)
- NO aprovisiona infra real (eso es SP-1/2/3). SP-0 es gobernanza + recetas.
- NO ejecuta mutaciones AWS — las prepara; el humano las ejecuta OUT-OF-BAND.
- NO trata el hook como límite de seguridad (la doc oficial dice que no lo es). El límite es IAM allowlist-puro.
- NO hace AssumeRole in-session (es teatro; el agente heredaría la credencial).
- NO duplica modern-go-guidelines ni aws-sdk-version-policy (compone con ellos).
- NO incluye los skills de aws-dev-toolkit (estrategia propia los reemplaza).

## Definition of Done (SP-0)

**EL LÍMITE (debe existir y ser verificable):**
1. **IAM allowlist-PURO Y SCOPEADO** para el principal del MCP aws-api: Allow SOLO el read-set necesario (ses
   Get*/List*/Describe* [v1+v2 mismo prefijo], cfn Describe*/List*, route53/cloudwatch read, sts:GetCallerIdentity,
   s3 SOLO bucket-level) — **SIN s3:GetObject, SIN iam:*, SIN cfn:GetTemplate**; Resource scopeado a recursos del
   proyecto en us-east-1; implicit-deny el resto; **Deny explícito de sts:AssumeRole + ses:Send***. Verificable
   vía `iam:simulate-principal-policy` (principal separado): intended-deny→deny, intended-allow→allow.
2. **Bootstrap (t=0) con GATE**: el humano crea el principal manualmente (cred admin que el agente no ve); ANTES de
   apuntar el agente, gate de aceptación: diff vs JSON canónico + NO managed Read/Power/Admin + probes pasan
   (ses send→Deny, sts assume-role→Deny, s3 get-object→Deny, iam list-access-keys→Deny, un read→OK). SP-1 formaliza
   en CDK-Go reproduciendo exacto + re-corre probes.
3. **Mutaciones out-of-band con hardening**: skill prepara cdk deploy → humano ejecuta con named profile que el
   agente NUNCA selecciona (jamás [default]/exportado), idealmente en otra máquina/CloudShell. IAM base Deny
   sts:AssumeRole. Test negativo: tras el deploy, el siguiente Bash del agente AÚN ve el principal read-only.

**FRICCIÓN/UX (no es el límite, pero debe self-fallar seguro):**
4. **Hook Bash** scoped-al-proyecto-mail: self-deny on error (trap); hard-deny si permission_mode≠default/plan;
   deny de metacaracteres (&&|;|$()|backtick); allowlist estrecha (aws/cdk/ls/cat… — NO go/git/find/env). Probado (ver Verification).
5. **Hook MCP** matcher exacto `mcp__aws-api__.*`: inspecciona call_aws, deniega writes, self-deny on error. server-key=`aws-api`.
6. **Audit log** decision-log.json (allow/deny/human-rejected, sanitizado, append atómico).

**RECETAS (calidad guiada-por-skill, best-effort, con eval objetivo):**
7. Plugin cdk-go-aws-plugin valida con `claude plugin validate --strict` (defaultEnabled:false; v2.1.154+; author objeto).
8. skill cdk-go-recipe + `agents/cdk-verifier.md`: verify-before-act; caché per-proyecto gitignoreado (schema con
   signature_hash; validez = cdk_version==go.mod ∧ TTL 7d; anti-poison valida vs versión viva; `--force-verify`).
9. skill ses-domain-recipe: 8 pasos + Paso 8 GENERA código de alarmas + 6 trampas + composición CDK-Go + `--dry-run`.
10. **Eval harness** (Componente F): golden prompts → aserciones positivas + NEGATIVAS + 1-por-trampa, whitespace-resilientes,
    baseline Pass@1/Pass@3 registrado. Esta es la definición objetiva de "el skill funciona".

## Verification
**El límite (IAM) — lo que de verdad protege (con la credencial del MCP aws-api):**
- `aws ses send-email ...` → **AccessDenied** (Deny ses:Send*, cubre v1 y v2; el corazón del proyecto no envía sin elevación).
- `aws sts assume-role ...` → **AccessDenied** (Deny explícito; cierra auto-elevación).
- `aws s3api get-object ...` sobre el bucket de correo → **AccessDenied** (sin s3:GetObject; no lee cuerpos MIME, SEC2-C1).
- `aws iam list-access-keys ...` → **AccessDenied** (sin iam:*; no enumera la cuenta, SEC2-C2).
- `aws cloudformation get-template ...` de otro stack → **AccessDenied** (sin cfn:GetTemplate).
- Un read del set scopeado (`aws ses get-account` en us-east-1) → **OK**. El mismo en otra región → AccessDenied (condición de región).
- **Falsabilidad real:** `aws iam simulate-principal-policy` (principal separado) → matriz intended-deny=deny / intended-allow=allow.
- **Bootstrap gate:** las 4 probes Deny + 1 Allow pasan ANTES de apuntar el agente; diff vs JSON canónico = 0.
- **Out-of-band negativo:** tras un `cdk deploy` del humano, `aws sts get-caller-identity` del siguiente Bash del agente devuelve el principal READ-ONLY (no el de deploy).

**El hook (fricción) — self-falla seguro:**
- `echo '{"tool_name":"Bash","tool_input":{"command":"aws s3 mb s3://x"},"cwd":".../erickaldama-mail","permission_mode":"default"}' | cdk-go-guard.sh` → deny.
- `aws s3 ls` → allow. `ls && aws s3 mb` (metacaracteres) → deny. `python3 deploy.py` / `go test` → deny+entregar. cwd en another-project/sample-ios-app → allow (fuera de scope).
- permission_mode `bypassPermissions` → deny igual. Hook con error inyectado (jq malformado) → deny (self-deny, no allow).
- hook MCP: `mcp__aws-api__call_aws` con un write → deny.

**Las recetas:**
- `claude plugin validate ./cdk-go-aws-plugin --strict` → ok.
- skill cdk-go-recipe: verifica construct, escribe cache; 2ª vez lee cache; `--force-verify` re-verifica; cache forjado con versión vieja → ignorado.
- `ses-domain-recipe --dry-run` → genera constructs/comandos SIN ejecutar.
- eval harness: golden prompts pasan aserciones positivas+negativas+6-trampas; baseline Pass@1/Pass@3 registrado.
- forzar un deny → aparece en decision-log.json sanitizado.
