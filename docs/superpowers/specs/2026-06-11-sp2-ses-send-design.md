# SP-2 — Identidad SES + envío — Diseño (spec)

> Fecha: 2026-06-11. Subproyecto: SP-2 (tarea #16). Worktree aislado: `sp-2-ses-send`.
> Cuenta ErickSA `367707589526`, región **us-east-1**. Aprovisionamiento exclusivo vía **AWS CDK Go**.
> Primer SES del proyecto: ejercita la receta SES (6 trampas) + verify-before-act. Auditado por 4 agentes
> adversariales (uno COMPILÓ el código contra awscdk v2.258.1). Hallazgos: `~/.claude/plans/email-project-research/11-sp2-audit-findings.md`.

## Propósito y alcance

SP-2 hace que `erickaldama.com` pueda **enviar correo autenticado** (DKIM/SPF/DMARC pasan) vía SES v2,
aprovisionado en CDK-Go sobre la fundación de SP-1. Entregable: se puede ENVIAR correo autenticado desde la
cuenta, dentro del sandbox, con captura de bounce/complaint y red de seguridad de reputación.

**Estado de partida (verificado en vivo 2026-06-10):** SES us-east-1 SANDBOX (ProductionAccessEnabled=false),
SendingEnabled, HEALTHY, 200/día @1/s, 0 enviados, suppression BOUNCE+COMPLAINT ya ON, cero identidades/config-sets.
Hosted zone `Z023932911KA6S98A6ZRW` (erickaldama.com) viva para colgar records.

## Las 8 decisiones (cerradas con el usuario)

1. **Alcance:** identidad + DKIM + custom MAIL FROM + DMARC + configuration set + alarmas de reputación + el
   camino de envío (IAM). **SIN production access** (paso humano out-of-band, SLA 24h). **SIN recepción** (SP-3).
2. **Wiring DNS↔SES:** `awsses.EmailIdentity` con `Identity_PublicHostedZone(zone)` → CDK auto-publica los
   record sets (3 DKIM CNAME + MAIL FROM MX + MAIL FROM SPF TXT). El region-suffix DKIM lo resuelve el construct
   vía Fn::GetAtt en deploy-time (NUNCA hardcodear — trampa #2).
3. **Import de la zona:** `HostedZone_FromHostedZoneAttributes(id=Z023932911KA6S98A6ZRW, name=erickaldama.com)`
   — referencia pura, cero llamadas AWS en synth, NO necesita lookup-role ni escribe cdk.context.json.
4. **SPF: NO crearlo a mano.** El construct EmailIdentity con MailFromDomain YA emite el SPF TXT
   (`v=spf1 include:amazonses.com ~all` en mail.erickaldama.com). Crearlo aparte = TXT duplicado → conflicto
   Route53 → deploy falla. Solo el **DMARC** se añade explícito (el construct no lo pone).
5. **Event destination: EventBridge** (coherente con SP-3, que disparará el Lambda de recepción). → la exec-policy
   de SP-1 NO tiene events:* → **SP-2 amplía iam/deploy-exec-policy.json con events:\*** (sino el deploy revienta
   en events:PutRule — el gap tipo-SP-1, anticipado esta vez). SES publica al default bus sin permiso; el
   Events::Rule+target que enruta requiere events:PutRule.
6. **DMARC rua: `esaldgut+dmarc@gmail.com`** (truco + de Gmail → etiqueta filtrable). Recoge reportes reales
   desde el día 1 (p=none existe PARA monitorear). En SP-3 se reapunta al buzón propio. CAVEAT NDA: el DMARC es
   DNS público → esaldgut@gmail.com queda visible — BENIGNO (correo personal, no expone marcas/account-IDs).
   Verificación previa: `dig` confirma el wildcard *._report._dmarc.gmail.com (cross-domain rua, RFC 7489 §7.1).
7. **MailFromBehaviorOnMxFailure = USE_DEFAULT_VALUE inicial.** Durante propagación del MX (Pending, hasta 72h),
   cae a amazonses.com y el correo SIGUE SALIENDO (cero outage); DMARC pasa por DKIM igual. Endurecer a
   REJECT_MESSAGE es decisión POSTERIOR opcional (estado Success estable).
8. **Send IAM: `mail-send` policy + `mail-sender-role` asumible.** ManagedPolicy mail-send (ses:SendEmail +
   ses:SendRawEmail sobre el ARN de identidad + Condition ses:FromAddress=erick@erickaldama.com) adjunta a un
   `mail-sender-role` asumible vía SSO (trust al permission-set SSO, NO a un user con keys). NO policy suelta, NO
   AmazonSESFullAccess. El SDK Go v2 sesv2.SendEmail (incl. Content.Raw) consume ses:SendEmail (no SendRawEmail).

## Arquitectura del stack `SendingStack` (CDK-Go)

```
SendingStack (account=367707589526, region=us-east-1)
├── EmailIdentity erickaldama.com  (Identity_PublicHostedZone(zona importada de SP-1))
│   ├── Easy DKIM RSA-2048 → 3 CNAME auto-publicados (region-suffix vía Fn::GetAtt)
│   └── Custom MAIL FROM mail.erickaldama.com → MX + SPF TXT auto-publicados; OnMxFailure=USE_DEFAULT_VALUE
├── TxtRecord _dmarc.erickaldama.com  → "v=DMARC1; p=none; rua=mailto:esaldgut+dmarc@gmail.com"  (manual)
├── ConfigurationSet mail-config  (FeedbackForwarding OFF — evita notificaciones duplicadas)
│   └── EventDestination → EventBridge (default bus); event types BOUNCE, COMPLAINT (+ DELIVERY/SEND opc.)
│   └── Events::Rule + target (SNS topic) para enrutar bounce/complaint al operador  [requiere events:*]
├── CloudWatch Alarms: Reputation.BounceRate>=0.02, Reputation.ComplaintRate>=0.0005
│   (namespace AWS/SES, treatMissingData: IGNORE — sin envíos = INSUFFICIENT_DATA si no)
├── ManagedPolicy mail-send  (ses:SendEmail+SendRawEmail @ identity ARN + ses:FromAddress condition)
└── Role mail-sender-role  (assumable vía SSO; mail-send adjunta; CFN le estampa el boundary de SP-1)

Out-of-band (NO en el stack):
- iam/deploy-exec-policy.json AMPLIADA con events:* (re-aplicar versión antes del deploy de SP-2).
- Production access (put-account-details) — paso humano, SLA 24h, one-shot (409 si se reintenta).
- Smoke: enviar de erick@erickaldama.com a success@/bounce@/complaint@simulator.amazonses.com (tras DKIM=SUCCESS).
```

### Layout del módulo (extiende el de SP-1)
```
erickaldama-mail/
├── cmd/cdk/main.go                 # MODIFY — instanciar SendingStack además de FoundationStack
├── internal/infra/
│   ├── foundation_stack.go         # EXISTS (SP-1)
│   ├── sending_stack.go            # NEW — EmailIdentity + DMARC + ConfigSet + EventBridge + alarmas + send-IAM
│   ├── sending_stack_test.go       # NEW — CDK assertions (6 RecordSets, EmailIdentity, alarmas, role/policy)
│   └── naming.go                   # MODIFY — constantes SP-2 (MailFromDomain, ConfigSetName, etc.)
├── iam/
│   ├── deploy-exec-policy.json     # MODIFY — añadir events:*
│   ├── mail-send-policy.json       # NEW — espejo del statement mail-send (referencia/doc)
│   └── ses-dkim-wait.sh            # NEW — gate de polling: get-email-identity hasta DkimAttributes.Status=SUCCESS
├── docs/BOOTSTRAP.md               # MODIFY — procedimiento SP-2 (re-aplicar exec-policy, smoke simulator, prod-access diferido)
└── docs/superpowers/{specs,plans,EXECUTION-LOG.md}
```

### Firmas CDK-Go verificadas (compiladas vs v2.258.1) — el resto el implementer las cruza con doc
```go
zone := awsroute53.HostedZone_FromHostedZoneAttributes(stack, jsii.String("ImportedZone"),
    &awsroute53.HostedZoneAttributes{HostedZoneId: jsii.String("Z023932911KA6S98A6ZRW"), ZoneName: jsii.String("erickaldama.com")})
identity := awsses.NewEmailIdentity(stack, jsii.String("SendingIdentity"), &awsses.EmailIdentityProps{
    Identity:                    awsses.Identity_PublicHostedZone(zone),
    DkimIdentity:                awsses.DkimIdentity_EasyDkim(awsses.EasyDkimSigningKeyLength_RSA_2048_BIT),
    MailFromDomain:              jsii.String("mail.erickaldama.com"),
    MailFromBehaviorOnMxFailure: awsses.MailFromBehaviorOnMxFailure_USE_DEFAULT_VALUE,
})
awsroute53.NewTxtRecord(stack, jsii.String("Dmarc"), &awsroute53.TxtRecordProps{
    Zone: zone, RecordName: jsii.String("_dmarc.erickaldama.com"),
    Values: jsii.Strings("v=DMARC1; p=none; rua=mailto:esaldgut+dmarc@gmail.com"),
})
```
Campo es `Identity` (NO `Domain`); `MailFromBehaviorOnMxFailure` (NO `BehaviorOnMxFailure`). Las firmas de
ConfigurationSet, ConfigurationSetEventDestination(EventBridge), Events::Rule, CloudWatch Alarm, ManagedPolicy y
Role el implementer las VERIFICA contra docs oficiales actuales + compila (las pseudo-firmas de ejemplo no compilan).

## Flujo de despliegue (frontera agente/humano)

```
AGENTE (mail-readonly, reads):
  1. Escribe sending_stack.go + tests + iam/*.json (events:* en exec-policy)
  2. go test ./internal/infra/ (template asserts: 6 RecordSets, EmailIdentity, alarmas, role/policy)
  3. cdk synth / diff (read-only)  → ENTREGA comandos al humano

HUMANO (out-of-band, SSO Admin):
  4. Re-aplicar exec-policy con events:*:
       aws iam create-policy-version --policy-arn …:policy/erickaldama-deploy-exec \
         --policy-document file://iam/deploy-exec-policy.json --set-as-default --profile AdministratorAccess-367707589526
  5. cdk deploy SendingStack --profile AdministratorAccess-367707589526   (termina con DKIM PENDING)
  → pega outputs al agente

AGENTE (verificación, read-only):
  6. ses-dkim-wait.sh: polling get-email-identity hasta DkimAttributes.Status=SUCCESS (minutos típico)
  7. Confirmar VerifiedForSendingStatus=true, MAIL FROM status, records en la zona (list-resource-record-sets)
  8. post-deploy-identity-check.sh (de SP-1) → identity mail-readonly intacta + gate/simulate

HUMANO (smoke, out-of-band, tras DKIM=SUCCESS):
  9. Enviar de erick@erickaldama.com a success@/bounce@/complaint@simulator.amazonses.com (SSO/role)
  → confirma envío + que bounce/complaint disparan el event destination + encienden AWS/SES metrics
```

## Manejo de errores / casos límite
| Caso | Manejo |
|---|---|
| cdk deploy termina con DKIM PENDING | ESPERADO (verificación async). El gate ses-dkim-wait.sh espera SUCCESS antes del smoke. NO declarar verde por exit-0. |
| events:PutRule AccessDenied | la exec-policy debe llevar events:* re-aplicado (paso 4) ANTES del deploy. Gap tipo-SP-1, anticipado. |
| SPF TXT duplicado en mail. | NO crear SPF manual; el construct ya lo emite. Test asserta 6 RecordSets (7 = bug). |
| put-account-details 409 | production access es one-shot/in-review; NO reintentar ni silenciar como idempotencia. Diferido a humano. |
| alarma INSUFFICIENT_DATA | treatMissingData: IGNORE; la métrica AWS/SES no existe hasta el 1er envío (el smoke al simulator la enciende). |
| notificaciones bounce duplicadas | FeedbackForwarding OFF en el config set cuando hay event destination. |
| Gmail rechaza rua externo | verificar con dig el wildcard *._report._dmarc.gmail.com ANTES de mergear. |

## Testing
| Nivel | Qué | Cómo |
|---|---|---|
| Template (offline, TDD) | el stack contiene exactamente lo diseñado | assertions: 1 EmailIdentity (EmailIdentity=erickaldama.com, DKIM RSA_2048, MailFrom mail.erickaldama.com USE_DEFAULT_VALUE); 6 RecordSets (3 CNAME DKIM por Match_ObjectLike + MX + SPF TXT auto + DMARC TXT); 1 ConfigurationSet; event destination EventBridge; 2 alarmas (treatMissingData IGNORE, thresholds 0.02/0.0005); mail-send policy (ses:SendEmail @ identity ARN + FromAddress condition); mail-sender-role con boundary estampado. Sin tocar AWS. |
| Live (smoke, out-of-band) | envío autenticado funciona | tras DKIM=SUCCESS, enviar al simulator; confirmar event destination dispara + métricas AWS/SES encienden. A EXECUTION-LOG. |

## Auditoría adversarial — HECHA 2026-06-10/11 (adversarial-audit-before-new-pattern)
4 agentes vs docs AWS/CDK oficiales vivas; el agente CDK compiló el SendingStack + corrió assertions contra
v2.258.1. 3 CRÍTICOS (SPF auto-publicado, DKIM async, events:*) + endurecimiento. Hallazgos completos en
`~/.claude/plans/email-project-research/11-sp2-audit-findings.md`.

## Criterios de aceptación (Definition of Done)
1. `go build ./... && go test ./...` verde (template asserts SP-2 + SP-1 infra + SP-0 hook/eval siguen verdes).
2. cdk synth/diff bajo el agente (mail-readonly) sin denegación del hook.
3. Humano: exec-policy con events:* re-aplicada + cdk deploy SendingStack → EmailIdentity + 6 records + config
   set + alarmas + role/policy en vivo.
4. ses-dkim-wait.sh → DkimAttributes.Status=SUCCESS (verificación async completa).
5. Smoke al Mailbox Simulator: envío OK + event destination dispara + métricas AWS/SES encienden.
6. post-deploy-identity-check.sh PASA (identity mail-readonly intacta + gate 8/8 + simulate 13/13).
7. EXECUTION-LOG + checkboxes del plan + task #16 actualizados; merge --no-ff a main.

## Fuera de alcance (decomposición limpia)
Production access / sandbox exit (paso humano post-SP-2) · recepción (rule sets, S3, Lambda, DynamoDB → SP-3) ·
apex MX (es recepción → SP-3) · BIMI/VMC (futuro, requiere p=quarantine/reject) · el cliente TUI (SP-4, solo
creamos el mail-sender-role que consumirá) · endurecer ~all→-all y USE_DEFAULT_VALUE→REJECT_MESSAGE (post, opc.).

## Disciplinas aplicables
aws-cli-pre-flight-canonical · adversarial-audit-before-new-pattern · verify-provider-api-supports-property ·
avoid-string-match-error-silencing (el 409 de put-account-details) · modern-go-guidelines · infra-plan-three-source-cross-check.
