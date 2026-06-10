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
`route53domains:UpdateDomainNameservers` — un write FUERA del límite read-only y FUERA del scope
de `mail-deploy`. Lo ejecuta el humano out-of-band (ver Flujo, paso 7).

## Decisiones tomadas (6)

1. **Región: `us-east-1`.** Resuelve el conflicto del dossier (01-region-decision sugería us-east-2
   por latencia a MX; 02-architecture y RETOMAR-AQUI dicen us-east-1) a favor de us-east-1 — coherente
   con el límite IAM de SP-0 ya verificado-en-vivo (cero re-trabajo del boundary/gate/simulate).
   Para correo personal la latencia us-east-1 vs us-east-2 es marginal (los MTAs reintentan).
2. **Alcance: zona + base de cuenta, sin records de correo.** Hosted zone (vacía salvo NS/SOA) + IAM
   formalizado + tags/naming. MX/DKIM/SPF/DMARC → SP-2 (dependen de la identidad SES).
3. **Re-delegación NS: humano out-of-band.** CDK crea la zona (emite 4 NS nuevos); el humano corre
   `UpdateDomainNameservers` con AdministratorAccess; el agente solo VERIFICA propagación con reads
   (`dig`, `route53 get-hosted-zone`). El agente nunca toca el registrar.
4. **IAM en CDK: la policy se gestiona, el user NO.** CDK define una `ManagedPolicy` (los 4 statements
   verificados vs SAR) y la adjunta al user `mail-readonly` **importado por nombre** (referencia, no
   recurso CFN). Evita `EntityAlreadyExists`, preserva el principal verificado-en-vivo, mantiene el
   boundary como código versionado.
5. **`mail-deploy` scoped a servicios del proyecto.** Principal de deploy separado que el agente NUNCA
   selecciona. Permisos acotados a los servicios que el correo usa (ver tabla IAM). Sin route53domains,
   EC2, RDS, organizations.
6. **Bootstrap: humano one-time + exec-policy scoped + `mail-deploy` solo-assume.** El humano corre
   `cdk bootstrap` UNA VEZ con AdministratorAccess pasando `--cloudformation-execution-policies` = la
   managed policy scoped-a-servicios. La least-privilege real vive en el exec-role del bootstrap (lo que
   CFN puede mutar), no en el humano. `mail-deploy` queda mínimo: solo `sts:AssumeRole` sobre `cdk-*`.

   > Verificado contra docs CDK oficiales (`cdk/v2/guide/ref-cli-cmd-bootstrap`,
   > `bootstrapping-customizing`, `best-practices-security`): el bootstrap moderno separa dos capas —
   > el setup one-time (crea CDKToolkit: S3+ECR+4 roles IAM, necesita casi-admin) y el deploy (asume el
   > exec-role, permisos = `--cloudformation-execution-policies`, default `AdministratorAccess`).

## Arquitectura del stack `FoundationStack` (CDK-Go)

```
FoundationStack (account=367707589526, region=us-east-1)
├── HostedZone "erickaldama.com"        → Route53 (global); emite 4 NS nuevos (output)
├── ManagedPolicy "mail-readonly-boundary"  → los 4 statements (vs SAR), Users:[mail-readonly importado]
└── User "mail-deploy" + ManagedPolicy "mail-deploy-assume" → sts:AssumeRole sobre arn:…:role/cdk-*

Out-of-band (NO en el stack):
- ManagedPolicy scoped-a-servicios (deploy-exec-policy.json) → se pasa al cdk bootstrap como
  --cloudformation-execution-policies. **Default**: el `FoundationStack` la EMITE como recurso (queda
  versionada en CDK, fuente de verdad) y exporta su ARN como output; el humano referencia ese ARN al
  re-bootstrappear. Quiebre del huevo-y-gallina del bootstrap inicial: el PRIMER bootstrap usa el JSON
  `iam/deploy-exec-policy.json` aplicado a mano (excepción t=0), luego el stack lo adopta. El audit
  confirma que CFN puede poseer una policy que el bootstrap consume sin ciclo de dependencia.
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
│   ├── deploy-exec-policy.json     # NUEVO: scoped exec-policy del bootstrap
│   ├── bootstrap-gate.sh           # de SP-0 (re-corre post-deploy)
│   ├── simulate-matrix.sh          # de SP-0 (re-corre post-deploy)
│   └── post-deploy-identity-check.sh  # NUEVO: el test diferido de T13
└── docs/superpowers/{specs,plans,EXECUTION-LOG.md}
```

### Unidades (propósito único cada una)

| Unidad | Qué hace | Depende de |
|---|---|---|
| `cmd/cdk/main.go` | Construye `App`, instancia `FoundationStack` con env (account/region), `app.Synth()` | aws-cdk-go/awscdk/v2 |
| `internal/infra/foundation_stack.go` | Constructs: HostedZone + ManagedPolicy(readonly→user importado) + User/ManagedPolicy(mail-deploy) | naming.go |
| `internal/infra/naming.go` | Constantes: nombres, tags (`Project=erickaldama-mail`, `Subproject=SP-1`, `ManagedBy=CDK-Go`), region | — |
| `iam/post-deploy-identity-check.sh` | Post-deploy: agente sigue en mail-readonly + re-corre gate+simulate | gate/simulate SP-0 |

### IAM scope de `mail-deploy` (exec-policy, scoped-a-servicios)

Acotado a lo que el proyecto realmente usa en SP-1..SP-3:
`cloudformation:*` (lo necesita CDK), `route53:*`, `ses:*`/`sesv2:*` reads+writes de identidad,
`s3:*` sobre buckets `erickaldama-*`, `dynamodb:*` sobre tablas `erickaldama-*`, `lambda:*` sobre
funciones `erickaldama-*`, `iam:*` acotado a roles/policies con path/prefijo `erickaldama-*`,
`cloudwatch:*`, `sns:*`, `sqs:*`, `kms:*` (para CMKs del proyecto).
**Excluido:** `route53domains:*` (registrar), `ec2:*`, `rds:*`, `organizations:*`, `iam:*` sobre
principals fuera del prefijo del proyecto. (Los wildcards se refinan en el plan/audit.)

## Flujo de despliegue (frontera agente/humano)

```
AGENTE (mail-readonly, reads):
  1. Escribe código CDK-Go + iam/*.json
  2. cdk synth   → template (no toca AWS)         [hook: ALLOW]
  3. cdk diff    → contra estado real (read-only) [hook: ALLOW]
  4. go test ./internal/infra → asserts template
  → ENTREGA los comandos exactos al humano

HUMANO (out-of-band, excepciones t=0):
  5. cdk bootstrap aws://367707589526/us-east-1 \
       --cloudformation-execution-policies <scoped-exec-policy-arn> \
       --profile AdministratorAccess-367707589526     [UNA VEZ]
  6. cdk deploy FoundationStack --profile mail-deploy
  7. UpdateDomainNameservers (los 4 NS nuevos)        [registrar, una vez]
  → pega outputs (stack outputs, NS) al agente

AGENTE (verificación read-only):
  8. iam/post-deploy-identity-check.sh:
       get-caller-identity (sin --profile) == mail-readonly ✓
       bootstrap-gate.sh 8/8 ✓ ; simulate-matrix.sh 13/13 ✓
  9. route53 get-hosted-zone + dig NS erickaldama.com → propagación
  10. registra todo en EXECUTION-LOG.md
```

## Manejo de errores / casos límite

| Caso | Manejo |
|---|---|
| `cdk deploy` falla a media | CFN auto-rollback; el agente lee stack-status (read), no re-deploya |
| NS no propaga (TTL registrar) | paso 9 read-only y reintentable; `dig` puede tardar — se documenta esperar, no bloquea SP-1 |
| inline policy vs managed (doble grant) | decisión explícita en plan: eliminar inline o dejar defensa-en-profundidad — auditada (`infra-plan-three-source-cross-check`) |
| bootstrap previo con exec-policy admin | `cdk diff` del CDKToolkit lo revela; humano re-bootstrappea con exec-policy scoped (idempotente) |
| `mail-deploy` no puede asumir cdk-* | el trust same-account debe verificarse en el audit (¿implícito o requiere `--trust`?) |

## Testing

| Nivel | Qué | Cómo |
|---|---|---|
| Template (offline, TDD) | el stack contiene exactamente lo diseñado | CDK `assertions` en `go test ./internal/infra`: 1 HostedZone `erickaldama.com.`; ManagedPolicy readonly con los 4 statements; mail-deploy SOLO `sts:AssumeRole`; tags presentes. Sin tocar AWS. |
| Boundary (live, smoke) | el deploy no ensanchó el límite del agente | `post-deploy-identity-check.sh`: identidad==mail-readonly + gate 8/8 + simulate 13/13 + dig NS propagado. A EXECUTION-LOG. |

## Auditoría adversarial antes de implementar (disciplina `adversarial-audit-before-new-pattern`)

SP-1 es el "primer CDK deploy real" del proyecto. Antes de escribir el plan, agentes de exploración/
análisis profundo verifican contra docs AWS/CDK oficiales VIVAS:

1. Idiom CDK-Go exacto: importar `mail-readonly` por nombre + adjuntar ManagedPolicy (`User_FromUserName` + prop `Users`, vs alternativas).
2. Trust del bootstrap: ¿`mail-deploy` (same-account) necesita `--trust` explícito o es implícito en los `cdk-*` roles?
3. Drift inline-vs-managed: ¿doble-grant o conflicto de evaluación IAM si coexisten?
4. Versión viva de `aws-cdk-go/awscdk/v2` (no hardcodear) + Go 1.26 idioms.
5. Region-pin: que el deploy de HostedZone (Route53 global) no choque con condiciones; SES intacto en SP-1.
6. `cdk diff` del CDKToolkit: detectar bootstrap previo con exec-policy admin (drift silencioso).

## Criterios de aceptación (Definition of Done)

1. `go build ./... && go test ./...` verde (template asserts + hook + eval de SP-0 siguen verdes).
2. `cdk synth` + `cdk diff` corren bajo el agente (mail-readonly) sin que el hook deniegue.
3. Humano: bootstrap one-time con exec-policy scoped + `cdk deploy` con mail-deploy → HostedZone existe en vivo.
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
