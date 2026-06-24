# SP-4 — Deploy Runbook (cliente TUI/CLI/AI Go + principales del cliente)

> Subproyecto SP-4: el primer componente que **consume** la infra (no la aprovisiona). El único cambio de
> infra es Task 0: dos IAM users que el cliente usa en runtime (`mail-client-read`, `mail-sender`).
> Cuenta ErickSA **367707589526** / **us-east-1**. Repo público — este runbook NO contiene secrets.
> Documenta comandos y **outputs reales** del deploy del 2026-06-24 (incluidos los 3 incidentes reales).

---

## 0. Pre-flight (disciplina `aws-cli-pre-flight-canonical`)

Antes de cualquier `aws`/`cdk` que toque recursos, verificar la identity:

```bash
aws sts get-caller-identity --profile AdministratorAccess-367707589526 --output json
```
```json
{
    "Account": "367707589526",
    "Arn": "arn:aws:sts::367707589526:assumed-role/AWSReservedSSO_AdministratorAccess_.../admin@esaldgut"
}
```
Si la sesión SSO expiró (`InvalidClientTokenId` / `no credentials have been configured`):
```bash
aws sso login --profile AdministratorAccess-367707589526
```

---

## 1. Modelo de principales (menor privilegio disjunto)

SP-4 NO reusa `mail-readonly` (verificador de gobernanza con hard-deny de `s3:GetObject` — lee metadata, no
contenido). Crea **dos principales nuevos del cliente**, con permisos **disjuntos**:

| Principal | Puede | NO puede | Origen |
|---|---|---|---|
| `mail-client-read` | `dynamodb:Query`/`GetItem` sobre `mail-index` + `s3:GetObject` sobre `erickaldama-mail-raw/inbound/*` | enviar, mutar | CDK (FoundationStack) |
| `mail-sender` | `ses:SendEmail`/`SendRawEmail` (identidad verificada, `From=erick@erickaldama.com`) | leer, mutar | CDK (SendingStack), reusa la `mail-send` managed policy |

El código CDK de estos users está en `internal/infra/foundation_stack.go` (MailClientReadPolicy + MailClientReadUser)
y `internal/infra/sending_stack.go` (MailSenderUser, reusando la `mailSendPolicy` existente).

---

## 2. Incidente #1 — `--require-approval` sin TTY

Primer intento con `--require-approval any-change` desde un comando no interactivo:
```
"--require-approval" is enabled and stack includes security-sensitive updates, but terminal (TTY)
is not attached so we are unable to get a confirmation from the user
```
**Causa:** el flag exige confirmación interactiva; el comando corrió sin TTY. **No se ejecutó nada** (el changeset
quedó en review). **Fix:** como el diff ya fue auditado (synth/diff + review 6-ejes), desplegar con
`--require-approval never`:
```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk deploy FoundationStack SendingStack --require-approval never
```

---

## 3. Incidente #2 — el permissions boundary deniega `iam:CreateUser` (HALLAZGO #6 del proyecto)

Segundo intento → `CREATE_FAILED` en `MailClientReadUser`:
```
User: arn:aws:sts::...:assumed-role/cdk-hnb659fds-cfn-exec-role-...  is not authorized to perform:
iam:CreateUser on resource: arn:aws:iam::367707589526:user/mail-client-read with an explicit deny
in a permissions boundary: arn:aws:iam::367707589526:policy/erickaldama-boundary (Status Code: 403)
```
El stack hizo **rollback limpio** (los recursos parciales se borraron; AWS quedó consistente).

**Causa raíz** — el patrón canónico del proyecto (`feedback_cdk_permissions_boundary_intersects`): el effective
permission del CDK exec-role = `exec-policy ∩ boundary`. El `erickaldama-boundary` (artefacto de bootstrap,
out-of-band) tenía un statement `DenyEscalationAndOutOfScope` que **deniega `iam:CreateUser`/`iam:CreateAccessKey`**
(anti-escalación, diseño SP-1). SP-4 es el **primer stack que crea un `AWS::IAM::User`** → primera vez que el
boundary topa con `iam:CreateUser`. (SP-1/2/3 solo crearon policies/roles/recursos; `mail-readonly` ya existía de
bootstrap t=0.)

**Fix (decisión de gobernanza, ampliar el boundary con menor privilegio):** sacar `iam:CreateUser`/`CreateAccessKey`
del deny general y añadir un deny scoped que las permite **solo** sobre los 2 ARNs del cliente (vía `NotResource`),
manteniendo el deny para cualquier otro user — el límite anti-escalación se preserva. El boundary editado vive en
`iam/erickaldama-boundary.json`:

```json
{
  "Sid": "DenyCreateUserExceptMailClientPrincipals",
  "Effect": "Deny",
  "Action": ["iam:CreateUser", "iam:CreateAccessKey"],
  "NotResource": [
    "arn:aws:iam::367707589526:user/mail-client-read",
    "arn:aws:iam::367707589526:user/mail-sender"
  ]
}
```

Aplicar el boundary como nueva versión (es bootstrap, out-of-band — el stack NO lo posee, cf. hallazgo SP-1 #4):
```bash
# 1. ¿cuántas versiones hay? (límite 5)
aws iam list-policy-versions --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --profile AdministratorAccess-367707589526 \
  --query 'Versions[].{V:VersionId,Default:IsDefaultVersion}' --output table

# 2. crear la nueva versión y hacerla default
aws iam create-policy-version --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --policy-document file://iam/erickaldama-boundary.json --set-as-default \
  --profile AdministratorAccess-367707589526
```
```json
{ "PolicyVersion": { "VersionId": "v4", "IsDefaultVersion": true, "CreateDate": "2026-06-24T22:44:16+00:00" } }
```

**Sub-incidente #2b — `MalformedPolicyDocument`:** el primer intento de `create-policy-version` falló con
`Syntax errors in policy`. **Causa:** se añadió un campo `"Comment"` dentro de un statement — IAM solo acepta
`Sid/Effect/Action/Resource/NotResource/Condition/Principal`. **Fix:** quitar `Comment` (el contexto va en el `Sid`
descriptivo, que sí es válido).

**Verificación read-only antes del re-deploy** (validar, no asumir):
```bash
aws iam get-policy-version --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --version-id v4 --profile AdministratorAccess-367707589526 \
  --query 'PolicyVersion.Document.Statement[?Sid==`DenyCreateUserExceptMailClientPrincipals`]' --output json
# + confirmar que iam:CreateUser ya NO está en el deny general DenyEscalationAndOutOfScope
```

Re-deploy (3er intento) → éxito:
```
FoundationStack | CREATE_COMPLETE | AWS::IAM::User | MailClientReadUser
 ✅  FoundationStack
SendingStack | CREATE_COMPLETE | AWS::IAM::User | MailSenderUser
 ✅  SendingStack
```
```bash
aws iam get-user --user-name mail-client-read --profile AdministratorAccess-367707589526 --query 'User.Arn'
# → arn:aws:iam::367707589526:user/mail-client-read
aws iam get-user --user-name mail-sender --profile AdministratorAccess-367707589526 --query 'User.Arn'
# → arn:aws:iam::367707589526:user/mail-sender
```

---

## 4. Incidente #3 — access key impresa en chat → rotación segura (canónico)

`aws iam create-access-key` imprime el `SecretAccessKey` en stdout **una sola vez**. Si ese output cruza un canal
no efímero (chat, log, issue), la credencial se considera comprometida y **debe rotarse** — aunque el principal sea
de bajo privilegio. (Mismo principio que la revocación de la API key de Anthropic, tarea #10.)

**Revocar la key expuesta:**
```bash
aws iam delete-access-key --user-name mail-client-read --access-key-id <AKIA-expuesta> \
  --profile AdministratorAccess-367707589526
# verificar que quedó en cero:
aws iam list-access-keys --user-name mail-client-read --profile AdministratorAccess-367707589526 \
  --query 'AccessKeyMetadata[].{Id:AccessKeyId,Status:Status}'   # → []
```

**Patrón canónico — generar la key SIN que la secret toque la pantalla** (escribirla directo a
`~/.aws/credentials` con `aws configure set`):
```bash
NEWKEY=$(aws iam create-access-key --user-name mail-client-read --output json \
  --profile AdministratorAccess-367707589526) && \
aws configure set aws_access_key_id     "$(echo "$NEWKEY" | python3 -c 'import json,sys;print(json.load(sys.stdin)["AccessKey"]["AccessKeyId"])')"     --profile mail-client-read && \
aws configure set aws_secret_access_key "$(echo "$NEWKEY" | python3 -c 'import json,sys;print(json.load(sys.stdin)["AccessKey"]["SecretAccessKey"])')" --profile mail-client-read && \
aws configure set region us-east-1 --profile mail-client-read && \
unset NEWKEY && echo "mail-client-read key guardada en ~/.aws/credentials (secret nunca impresa)"
# idéntico para mail-sender
```
**Regla:** nunca correr `create-access-key` "a pelo" si su stdout puede ser observado. Siempre capturarlo a una
variable y escribirlo con `aws configure set`.

---

## 5. Verificación post-deploy (prueba empírica — cierra el gate del Hallazgo #8)

El agente verifica read-only que cada profile resuelve y puede SU operación (assume-as cada principal, NO Admin):
```bash
# mail-client-read resuelve a su user y puede Query
aws sts get-caller-identity --profile mail-client-read --query Arn
# → arn:aws:iam::367707589526:user/mail-client-read
aws dynamodb query --table-name mail-index --key-condition-expression "PK = :pk" \
  --expression-attribute-values '{":pk":{"S":"mailbox#test@erickaldama.com"}}' \
  --region us-east-1 --profile mail-client-read --max-items 1
# (sin AccessDenied; s3:GetObject de un s3Key real también permitido)

# mail-sender resuelve a su user
aws sts get-caller-identity --profile mail-sender --query Arn
# → arn:aws:iam::367707589526:user/mail-sender
```

---

## 6. Smoke end-to-end (cierra el lazo SP-2 ↔ SP-3 ↔ SP-4)

Con los profiles configurados, el cliente Go consume la infra real:
```bash
# leer (mail-client-read)
go run ./cmd/mail ls --mailbox test@erickaldama.com -n 5 --read-profile mail-client-read
go run ./cmd/mail read <s3Key-de-un-item-real> --read-profile mail-client-read

# enviar al Mailbox Simulator (mail-sender) — SES sigue en sandbox (200/día, solo destinatarios verificados)
go run ./cmd/mail send --to success@simulator.amazonses.com --subject "SP-4 smoke" \
  --body-file <(echo "hola desde el cliente") --send-profile mail-sender

# lazo completo: enviar a test@erickaldama.com → debe aparecer en ls
go run ./cmd/mail send --to test@erickaldama.com --subject "SP-4 loop" \
  --body-file <(echo "loop e2e") --send-profile mail-sender
sleep 15 && go run ./cmd/mail ls --mailbox test@erickaldama.com -n 1 --read-profile mail-client-read

# AI local (el correo no sale del Mac)
ollama serve >/dev/null 2>&1 &   # si no corre
ollama pull qwen3:32b            # para la capacidad agent
go run ./cmd/mail ai summarize <s3Key> --backend ollama
go run ./cmd/mail ai agent "¿cuántos correos tengo de la última semana?" --backend ollama
```

**Recordatorio SES sandbox** (`ProductionAccessEnabled=false`): solo se puede enviar a destinatarios verificados o
al Mailbox Simulator. El cliente traduce el rechazo a `ErrSandboxRecipient` con mensaje accionable (no string-match,
vía `errors.As(*MessageRejected)` + `DetectSandbox`).

---

## 7. Rotación / kill-switch (postura de seguridad)

Las access keys de `mail-client-read`/`mail-sender` son **de larga vida** (trade-off aceptado: simplicidad de runtime
vs STS temporal). Mitigación: ambas policies fuertemente scopeadas → blast radius mínimo si se filtran.
- **Rotar:** crear nueva key (patrón §4), actualizar `~/.aws/credentials`, borrar la vieja.
- **Kill-switch:** `aws iam delete-access-key` revoca de inmediato; el principal queda sin credenciales hasta re-emitir.
- Las keys **nunca** en el binario, en git, ni en logs (van solo en `~/.aws/credentials`).

---

## 8. Resumen de comandos (quick reference)

| Acción | Comando |
|---|---|
| Pre-flight identity | `aws sts get-caller-identity --profile AdministratorAccess-367707589526` |
| Deploy users | `cdk deploy FoundationStack SendingStack --require-approval never` |
| Aplicar boundary nueva versión | `aws iam create-policy-version --policy-arn <boundary> --policy-document file://iam/erickaldama-boundary.json --set-as-default` |
| Revocar key | `aws iam delete-access-key --user-name <u> --access-key-id <AKIA>` |
| Crear key segura | ver §4 (capturar a var + `aws configure set`) |
| Verificar profile | `aws sts get-caller-identity --profile mail-client-read` |

**Cuenta** 367707589526 · **región** us-east-1 · **boundary activo** v4 · **SES** sandbox (200/día).
Documentado 2026-06-24 con los 3 incidentes reales del deploy. NDA: este repo es público; sin secrets ni marcas internas.
