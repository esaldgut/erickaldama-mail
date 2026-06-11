# SP-1 — Fundación DNS + cuenta — Diseño (spec)

> Fecha: 2026-06-10. Subproyecto: SP-1 (tarea #15). Worktree aislado: `sp-1-foundation`.
> Cuenta ErickSA `367707589526`, región **us-east-1**. Aprovisionamiento exclusivo vía **AWS CDK Go**.
> Es el **examen inaugural de la gobernanza SP-0**: primer recurso AWS real vía CDK-Go deploy
> out-of-band, demostrando en vivo que el límite IAM (`mail-readonly`) aguanta.

## Propósito y alcance

SP-1 hace existir el primer recurso AWS real del proyecto vía CDK-Go, con el mínimo entregable
honesto: una hosted zone + la base de identidad (IAM) formalizada como código. Nada de correo
(MX/DKIM/SPF/DMARC dependen de la identidad SES que nace en SP-2). El valor real de SP-1 no es la
zona —es trivial— sino **ejercitar el flujo de deploy out-of-band y verificar que el boundary de
SP-0 aguanta un deploy real**.

## Estado real verificado en vivo (2026-06-10, cuenta 367707589526, reads only)

| Hecho | Valor verificado |
|---|---|
| `erickaldama.com` | **Registrado** (Amazon Registrar, exp. 2027-05-28, AutoRenew=true, TransferLock=false) |
| Hosted zone `erickaldama.com` | **NO existe** (`list-hosted-zones` solo devuelve `esaldgut.com`) |
| Delegación del registrar | Apunta a un delegation set **muerto** (`ns-1390.awsdns-45.org`, `ns-1918.awsdns-47.co.uk`, `ns-46.awsdns-05.com`, `ns-527.awsdns-01.net`); `dig NS erickaldama.com` devuelve **vacío** → la zona detrás de esos NS fue borrada |
| `esaldgut.com` | Hosted zone viva (`Z01407861IBJBBDKJ7N6V`, 2 records) — **pre-existente, NO se toca** |
| SES v2 us-east-1 | Sandbox (`ProductionAccessEnabled=false`), `SendingEnabled=true`, `EnforcementStatus=HEALTHY`, 200/día @ 1/s, **cero identidades** |
| `mail-readonly` | Existe (`arn:aws:iam::367707589526:user/mail-readonly`, creado 2026-06-08 en bootstrap t=0 de SP-0), inline policy `mail-readonly-boundary`, sin managed policies |

**Implicación crítica del estado:** crear una hosted zone NUEVA vía CDK hace que Route53 asigne
4 NS aleatorios DISTINTOS de los que el registrar tiene. Hay que actualizar el registrar con
`route53domains:UpdateDomainNameservers` — un write FUERA del límite read-only. Lo ejecuta el humano
out-of-band (ver Flujo). El reusable delegation set viejo NO se puede recuperar (la zona fue borrada).

## Decisiones tomadas (8) — incl. cambios de la auditoría adversarial 2026-06-10

> Auditadas por 4 agentes adversariales contra docs AWS/CDK oficiales vivas (ver
> `~/.claude/plans/email-project-research/10-sp1-audit-findings.md`). 2 hallazgos IMPORTANTES
> simplificaron Y endurecieron el diseño: se eliminó `mail-deploy`, se adoptó un permissions boundary.

1. **Región: `us-east-1`.** Resuelve el conflicto del dossier (01-region-decision sugería us-east-2
   por latencia a MX; 02-architecture y RETOMAR-AQUI dicen us-east-1) a favor de us-east-1 — coherente
   con el límite IAM de SP-0 ya verificado-en-vivo (cero re-trabajo del boundary/gate/simulate).
   Para correo personal la latencia us-east-1 vs us-east-2 es marginal (los MTAs reintentan).
2. **Alcance: zona + base de cuenta, sin records de correo.** Hosted zone (NS/SOA + CAA) + IAM
   formalizado + tags/naming. MX/DKIM/SPF/DMARC → SP-2 (dependen de la identidad SES).
3. **Re-delegación NS: humano out-of-band.** CDK crea la zona (emite 4 NS nuevos); el humano corre
   `UpdateDomainNameservers` con SSO Admin; el agente solo VERIFICA propagación con reads
   (`dig`, `route53 get-hosted-zone`). El agente nunca toca el registrar. (Verificado: zona nueva →
   delegation set nuevo SIEMPRE → tocar registrar inevitable; solo `--region us-east-1`; async +
   `GetOperationDetail`; NO poblar GlueIps.)
4. **IAM en CDK: la policy se gestiona, el user NO.** CDK define una `ManagedPolicy` (los 4 statements
   verificados vs SAR) y la adjunta al user `mail-readonly` **importado por nombre** vía la prop
   `Users: &[]awsiam.IUser{importedUser}` (NO `importedUser.AddManagedPolicy()` — eso lanza
   `ValidationError` en synth, verificado en fuente aws-cdk-lib). El template emite SOLO
   `AWS::IAM::ManagedPolicy` con `Users:["mail-readonly"]`, NO `AWS::IAM::User` → cero `EntityAlreadyExists`.
   Tras adoptar la managed, **eliminar la inline `mail-readonly-boundary`** (creada a mano en SP-0) para
   evitar drift de allows; el boundary queda como código versionado en CDK (fuente única de verdad).
5. **`mail-deploy` ELIMINADO** (hallazgo auditoría, IMPORTANTE). Tras `cdk bootstrap`, CUALQUIER principal
   de la cuenta con `sts:AssumeRole` sobre los roles `cdk-*` puede deployar — y el SSO
   `AdministratorAccess-367707589526` ya lo cumple. Un IAM user con access keys de larga vida no habilita
   nada nuevo y es justo el anti-patrón de secreto-de-larga-vida que el proyecto evita. **El humano deploya
   out-of-band con su SSO Admin.** Si futuro CI sin-humano: un ROLE vía OIDC (nunca user con keys), añadido
   vía `--trust` en re-bootstrap, no como recurso del stack.
6. **Permissions boundary `erickaldama-boundary` como el least-privilege REAL** (hallazgo auditoría,
   IMPORTANTE). SP-1 crea una `ManagedPolicy erickaldama-boundary` (el techo: servicios del proyecto, sin
   iam-escalation / route53domains / ec2 / rds / organizations). El bootstrap usa
   `--custom-permissions-boundary erickaldama-boundary` → CDK inyecta el boundary en TODO role IAM que
   sintetice en SP-1..SP-3 automáticamente, cerrando la escalada de raíz (un template no puede crear un
   `erickaldama-role` con AdministratorAccess efectiva). Este es el control de seguridad de mayor ROI.
7. **Exec-policy = allowlist-de-SERVICIOS** (no ARN-prefix uniforme). El ARN-prefix `erickaldama-*` sobre
   `service:*` es teatro parcial (la mayoría de acciones SES/S3/KMS no soportan resource-level) y frágil
   (un nombre auto-generado sin prefijo revienta el deploy). El valor vive en QUÉ servicios EXCLUYE.
   Incluye: `route53:*`, `ses:*`, `s3:*`, `dynamodb:*`, `lambda:*`, `sns:*`, `sqs:*`, `kms:*`,
   `cloudwatch:*`, `iam:*` (acotado por el boundary, no por ARN). **Excluye:** `route53domains`, `ec2`,
   `rds`, `organizations`. **NO incluye `cloudformation:*`** (el exec-role no se autoinvoca — la orquestación
   la hace el deploy-role del bootstrap). **NO usar `sesv2:*`** (no existe; `ses:` cubre v1+v2). `logs:*`
   se añade al llegar a SP-3 (LogGroups de Lambda).
8. **Bootstrap: humano one-time con SSO Admin + boundary + exec-policy scoped.** El humano corre
   `cdk bootstrap aws://367707589526/us-east-1 --custom-permissions-boundary erickaldama-boundary
   --cloudformation-execution-policies <exec-policy-arn> --profile AdministratorAccess-367707589526`
   UNA VEZ. El bootstrap moderno separa dos capas (verificado): setup one-time (crea CDKToolkit: S3+ECR+4
   roles, necesita casi-admin) y deploy (asume el exec-role, permisos = `--cloudformation-execution-policies`,
   default `AdministratorAccess`). La least-privilege real vive en el boundary + la exec-policy, no en el humano.

## Arquitectura del stack `FoundationStack` (CDK-Go)

```
FoundationStack (account=367707589526, region=us-east-1)
├── PublicHostedZone "erickaldama.com"   → Route53 (global); NS+SOA + CAA Amazon-only
│   └── CfnOutput NameServers = Fn::Join(",", zone.HostedZoneNameServers())  [token de lista]
│   └── CfnOutput HostedZoneId
├── ManagedPolicy "mail-readonly-managed" → los 4 statements (vs SAR), Users:[mail-readonly importado]
└── ManagedPolicy "erickaldama-boundary"  → el techo de permisos (permissions boundary del deploy)

Out-of-band (NO en el stack — JSON declarativo en iam/, lo consume el bootstrap):
- iam/deploy-exec-policy.json → allowlist-de-servicios → --cloudformation-execution-policies del bootstrap.
- iam/erickaldama-boundary.json → espejo de la ManagedPolicy boundary → --custom-permissions-boundary.
  (El stack EMITE la boundary como ManagedPolicy versionada; el bootstrap one-time la referencia por nombre.
   El PRIMER bootstrap aplica el JSON a mano si la managed aún no existe — excepción t=0 — luego el stack la adopta.)

SIN mail-deploy. SIN access keys de larga vida. SIN cloudformation:*/sesv2: en la exec-policy.
```

### Layout del módulo

```
erickaldama-mail/
├── go.mod                          # ya existe (module erickaldama-mail, go 1.26.4)
├── cdk.json                        # app = "go mod download && go run ./cmd/cdk"
├── cmd/cdk/main.go                 # App + FoundationStack + app.Synth()
├── internal/infra/
│   ├── foundation_stack.go         # el stack: hosted zone + IAM
│   ├── naming.go                   # constantes canónicas (project, region, tags)
│   └── foundation_stack_test.go    # CDK assertions sobre el template sintetizado
├── iam/                            # (de SP-0, se EXTIENDE)
│   ├── readonly-policy.json        # los 4 statements (fuente declarativa, ya existe)
│   ├── deploy-exec-policy.json     # NUEVO: allowlist-de-servicios (--cloudformation-execution-policies)
│   ├── erickaldama-boundary.json   # NUEVO: el techo (--custom-permissions-boundary), espejo de la managed
│   ├── bootstrap-gate.sh           # de SP-0 (re-corre post-deploy)
│   ├── simulate-matrix.sh          # de SP-0 (re-corre post-deploy)
│   └── post-deploy-identity-check.sh  # NUEVO: el test diferido de T13
└── docs/superpowers/{specs,plans,EXECUTION-LOG.md}
```

### Versión CDK-Go (verificada viva 2026-06-10, NO hardcodear)
`github.com/aws/aws-cdk-go/awscdk/v2` — viva hoy **v2.258.1**; arrastra `jsii-runtime-go v1.133.0` +
`constructs/v10 v10.6.0` como deps transitivas (jsii NO se elige a mano). En el bootstrap:
`go get github.com/aws/aws-cdk-go/awscdk/v2@latest` y escribir el número resuelto a `go.mod`.
`cdk.json` app = `"go mod download && go run ./cmd/cdk"` (package-path; `go run .` NO funciona).
`main.go`: `defer jsii.Close()` como primera sentencia (oficial). Go 1.26: usar `jsii.*` para props
(NO `new(val)`); evitar over-engineering (stack ~50 líneas factory-calls).

### Unidades (propósito único cada una)

| Unidad | Qué hace | Depende de |
|---|---|---|
| `cmd/cdk/main.go` | Construye `App`, instancia `FoundationStack` con env (account/region), `app.Synth()` | aws-cdk-go/awscdk/v2 |
| `internal/infra/foundation_stack.go` | Constructs: PublicHostedZone(+CAA) + ManagedPolicy(readonly→user importado) + ManagedPolicy(boundary) | naming.go |
| `internal/infra/naming.go` | Constantes: nombres, tags (`Project=erickaldama-mail`, `Subproject=SP-1`, `ManagedBy=CDK-Go`), region. Funciones puras string→string | — |
| `iam/post-deploy-identity-check.sh` | Post-deploy: agente sigue en mail-readonly + re-corre gate+simulate | gate/simulate SP-0 |

### exec-policy del bootstrap (allowlist-de-servicios, NO ARN-prefix)

`route53:*`, `ses:*` (cubre v1+v2 — NO existe `sesv2:`), `s3:*`, `dynamodb:*`, `lambda:*`, `sns:*`,
`sqs:*`, `kms:*`, `cloudwatch:*`, `iam:*` (la granularidad la da el boundary, no el ARN).
**Excluye:** `route53domains:*` (registrar), `ec2:*`, `rds:*`, `organizations:*`.
**NO `cloudformation:*`** (el exec-role no se autoinvoca). `logs:*` se añade en SP-3 (LogGroups Lambda).
El control de escalada IAM lo da el **permissions boundary** (statement 6), no el scoping de la exec-policy.

## Flujo de despliegue (frontera agente/humano)

```
AGENTE (mail-readonly, reads):
  1. Escribe código CDK-Go + iam/*.json
  2. cdk synth   → template (no toca AWS)         [hook: ALLOW]
  3. cdk diff    → contra estado real (read-only) [hook: ALLOW]
  4. go test ./internal/infra → asserts template
  → ENTREGA los comandos exactos al humano

HUMANO (out-of-band, excepciones t=0, todo con SSO AdministratorAccess-367707589526):
  5. cdk bootstrap aws://367707589526/us-east-1 \
       --custom-permissions-boundary erickaldama-boundary \
       --cloudformation-execution-policies <exec-policy-arn> \
       --profile AdministratorAccess-367707589526     [UNA VEZ]
  6. cdk deploy FoundationStack --profile AdministratorAccess-367707589526
  7. aws route53domains update-domain-nameservers --region us-east-1 \
       --domain-name erickaldama.com --nameservers Name=<ns1> ... [×4]   [registrar, una vez]
  → pega outputs (stack outputs, NS) al agente

AGENTE (verificación read-only):
  8. iam/post-deploy-identity-check.sh:
       get-caller-identity (sin --profile) == mail-readonly ✓  (deploy con SSO Admin ≠ profile del agente)
       bootstrap-gate.sh 8/8 ✓ ; simulate-matrix.sh 13/13 ✓
  9. route53 get-hosted-zone + GetOperationDetail(SUCCESSFUL) + dig NS erickaldama.com → propagación
  10. registra todo en EXECUTION-LOG.md
```

## Manejo de errores / casos límite

| Caso | Manejo |
|---|---|
| `cdk deploy` falla a media | CFN auto-rollback; el agente lee stack-status (read), no re-deploya |
| NS no propaga (TTL registrar) | paso 9 read-only y reintentable; async — `GetOperationDetail` hasta SUCCESSFUL + `dig`; no bloquea SP-1 |
| `UpdateDomainNameservers` con op en curso | error `DuplicateRequest` — esperar a que termine la op previa (es replace declarativo, reintentable) |
| inline `mail-readonly-boundary` redundante | el plan la ELIMINA tras adoptar la managed (evita drift de allows); `infra-plan-three-source-cross-check` |
| bootstrap previo con exec-policy admin / sin boundary | `cdk diff` del CDKToolkit lo revela; humano re-bootstrappea con boundary + exec-policy (idempotente) |

## Testing

| Nivel | Qué | Cómo |
|---|---|---|
| Template (offline, TDD) | el stack contiene exactamente lo diseñado | CDK `assertions` en `go test ./internal/infra`: 1 HostedZone con `"Name":"erickaldama.com."` (CON punto — Route53 lo añade); CAA presente; ManagedPolicy readonly con los 4 statements (`Match_ArrayEquals` para exclusividad, no `ArrayWith`); ManagedPolicy boundary; tags presentes; NINGÚN `AWS::IAM::User`. Props como `map[string]interface{}`. Sin tocar AWS. |
| Boundary (live, smoke) | el deploy no ensanchó el límite del agente | `post-deploy-identity-check.sh`: identidad==mail-readonly + gate 8/8 + simulate 13/13 + dig NS propagado. A EXECUTION-LOG. |

## Auditoría adversarial — HECHA 2026-06-10 (disciplina `adversarial-audit-before-new-pattern`)

4 agentes adversariales verificaron contra docs AWS/CDK oficiales vivas. Hallazgos completos en
`~/.claude/plans/email-project-research/10-sp1-audit-findings.md`. Resultado: `mail-deploy` eliminado,
permissions boundary adoptado, código CDK-Go verificado (compilado + test pasando contra v2.258.1),
trampa del punto final de Route53 + exclusividad con `Match_ArrayEquals` capturadas, `sesv2:`/`cloudformation:*`
ruido eliminado, export de NS con `Fn_Join`. El diseño quedó más simple Y más seguro.

## Criterios de aceptación (Definition of Done)

1. `go build ./... && go test ./...` verde (template asserts + hook + eval de SP-0 siguen verdes).
2. `cdk synth` + `cdk diff` corren bajo el agente (mail-readonly) sin que el hook deniegue.
3. Humano: bootstrap one-time (boundary + exec-policy) + `cdk deploy` con SSO Admin → HostedZone existe en vivo.
4. Humano: `UpdateDomainNameservers` → `dig NS erickaldama.com` resuelve a los NS nuevos.
5. `post-deploy-identity-check.sh` PASA (identidad del agente intacta + gate 8/8 + simulate 13/13).
6. EXECUTION-LOG + checkboxes del plan + task #15 actualizados; merge `--no-ff` a main.

## Persistencia triple-capa (igual que SP-0)
checkboxes del plan + `docs/superpowers/EXECUTION-LOG.md` + task framework (#15). `RETOMAR-AQUI.md`
se actualiza al cierre (SP-1 done → SP-2 next).

## Disciplinas aplicables
aws-cli-pre-flight-canonical · adversarial-audit-before-new-pattern · infra-plan-three-source-cross-check
· modern-go-guidelines · aws-sdk-version-policy · project-init · verify-provider-api-supports-property

## Fuera de alcance (decomposición limpia)
MX/DKIM/SPF/DMARC/MAIL-FROM (SP-2) · identidad SES (SP-2) · buckets/DynamoDB/Lambdas de correo (SP-2/SP-3)
· production access / sandbox exit (SP-2) · cliente TUI Go (SP-4).
