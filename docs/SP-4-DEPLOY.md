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
go run ./cmd/mail ls --mailbox test@erickaldama.com --count 5 --read-profile mail-client-read
go run ./cmd/mail read <s3Key-de-un-item-real> --read-profile mail-client-read

# enviar al Mailbox Simulator (mail-sender) — SES sigue en sandbox (200/día, solo destinatarios verificados)
go run ./cmd/mail send --to success@simulator.amazonses.com --subject "SP-4 smoke" \
  --body-file <(echo "hola desde el cliente") --send-profile mail-sender

# lazo completo: enviar a test@erickaldama.com → debe aparecer en ls
go run ./cmd/mail send --to test@erickaldama.com --subject "SP-4 loop" \
  --body-file <(echo "loop e2e") --send-profile mail-sender
sleep 15 && go run ./cmd/mail ls --mailbox test@erickaldama.com --count 1 --read-profile mail-client-read

# AI local (el correo no sale del Mac)
ollama serve >/dev/null 2>&1 &   # si no corre
ollama pull qwen3:32b            # para la capacidad agent
go run ./cmd/mail ai summarize <s3Key> --backend ollama
go run ./cmd/mail ai agent "¿cuántos correos tengo de la última semana?" --backend ollama
```

**Recordatorio SES sandbox** (`ProductionAccessEnabled=false`): solo se puede enviar a destinatarios verificados o
al Mailbox Simulator. El cliente traduce el rechazo a `ErrSandboxRecipient` con mensaje accionable (no string-match,
vía `errors.As(*MessageRejected)` + `DetectSandbox`).

### Resultado verificado del smoke (2026-06-24) — el lazo SÍ cierra
- `mail ls` (mail-client-read) listó el item real de SP-3 (correo de prueba del 18-jun) con claves camelCase correctas.
- `mail read` bajó el MIME de S3 y lo parseó (mostró el cuerpo en texto).
- `mail send` (mail-sender) al Mailbox Simulator → `sent: 0100019e...` (SES SendRawEmail OK).
- **Lazo completo:** un correo enviado por el cliente a `test@erickaldama.com` recorrió SES → S3 → Lambda → DynamoDB
  y reapareció en `mail ls` en ~12s (buzón 1 → 2); `mail read` mostró su cuerpo. **El sistema entero funciona end-to-end.**

**Hallazgo del smoke #1 — la policy de envío necesitaba el config-set:** `mail-config` es el config-set por defecto
de la identidad, así que SES lo aplica a todo envío y exige `ses:SendRawEmail` sobre **ambos** recursos (identity +
config-set). El primer `send` dio `AccessDenied` sobre `configuration-set/mail-config`. Fix: ampliar `mail-send` a
`Resources: [identity, configuration-set]` (`iam/mail-send-policy.json` + `internal/infra/sending_stack.go`) + redeploy.

**Hallazgo del smoke #2 — flag de límite es `--count`, no `-n`:** el CLI registra el límite como `--count int`
(default 20), no `-n`. Los comandos de §6 usan `--count`.

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

**Cuenta** 367707589526 · **región** us-east-1 · **boundary activo** v6 (CD: +sts:AssumeRole scoped) · **SES** sandbox (200/día).
Documentado 2026-06-24 con los 3 incidentes reales del deploy. NDA: este repo es público; sin secrets ni marcas internas.

---

## 9. Integración tmux / nvim (consumir el cliente — 2026-06-25)

Los binarios se instalan globales con `go install ./cmd/mail ./cmd/mail-tui` (quedan en `~/go/bin`, en PATH).
El subcomando `mail tmux` (cierra la deuda SP-4 §5.3) da la glue; los bindings están verificados sin colisión
contra la config real. Copy-paste:

**tmux** (`~/.tmux.conf`, prefix `C-a`, tecla `e` libre):
```tmux
# prefix+e → cliente de correo en popup flotante
bind e display-popup -E -w 90% -h 90% "mail-tui"
# (opcional) conteo en el status-right:
# set -g status-right "#(mail tmux status) #[fg=yellow]%Y-%m-%d #[fg=cyan]%H:%M:%S"
```

**nvim** (`~/.config/nvim/lua/config/keymaps.lua`, leader `\`, prefijo `<leader>m` libre):
```lua
keymap('n', '<leader>ml', ':split | terminal mail-tui<CR>i',        { desc = 'Mail: list (TUI)' })
keymap('n', '<leader>ms', ':split | terminal mail ls<CR>i',         { desc = 'Mail: search/list' })
keymap('n', '<leader>mc', ':split | terminal mail send<CR>i',       { desc = 'Mail: compose' })
keymap('n', '<leader>ma', ':split | terminal mail ai agent<CR>i',   { desc = 'Mail: AI agent' })
```

Notas:
- `mail tmux popup` falla con error claro si se corre fuera de tmux (guarda contra `$TMUX`).
- El AI (`mail ai …`) requiere Ollama corriendo (`ollama serve` + `qwen3:32b`) para el backend on-device por
  defecto; `--backend claude` es opt-in (el cuerpo cruza la red, aviso explícito una vez).
- El TUI v0.1 tiene stubs de AI (`s`/`a`) que NO llaman al AI real — esa función vive en el CLI (`mail ai`).

---

## 10. v0.2 — config.toml + CC/BCC + reply-all (2026-06-25)

### 10.1 config.toml multi-mailbox

El cliente lee `~/.config/erickaldama-mail/config.toml` en el arranque. La ruta es **XDG-explícita**: si
`$XDG_CONFIG_HOME` está definida, la usa; de lo contrario, `~/.config/` (NOT `~/Library/Application Support/`,
que es lo que retorna `os.UserConfigDir()` en macOS — el cliente lo ignora deliberadamente, auditoría B-1b).

```toml
# ~/.config/erickaldama-mail/config.toml
mailboxes    = ["erick@erickaldama.com", "test@erickaldama.com"]
default_from = "erick@erickaldama.com"
read_profile = "mail-client-read"
send_profile = "mail-sender"
```

| Campo | Uso |
|---|---|
| `mailboxes` | Lista de mailboxes que `mail ls` muestra cuando no se pasa `--mailbox`; ordenados por fecha desc según SK |
| `default_from` | Dirección `From:` por defecto en `mail send` / `mail reply` (puede sobreescribirse con `--from`) |
| `read_profile` | Perfil AWS (`~/.aws/credentials`) para queries DynamoDB + descargas S3 |
| `send_profile` | Perfil AWS para `ses:SendRawEmail` |

**`mail ls` sin `--mailbox`:** itera todos los mailboxes del config y los combina, ordenados por SK descendente
(ISO-8601 — `ts#<RFC3339>#<MsgID>`). El orden por SK es correcto porque el SK es lexicográfico sobre el
timestamp ISO; el campo `Date:` del header (RFC 1123Z) ordenaría incorrectamente.

**Sin config y sin `--mailbox`:** el cliente imprime un error claro y termina:
```
no hay config; crea ~/.config/erickaldama-mail/config.toml con tus mailboxes, o usa --mailbox <dirección>
```

### 10.2 CC y BCC — CLI

Los flags `--cc` y `--bcc` aceptan listas separadas por comas de **direcciones peladas** (`addr-spec`):

```bash
# enviar con Cc y Bcc
mail send \
  --from erick@erickaldama.com \
  --to erick@erickaldama.com \
  --cc test@erickaldama.com \
  --bcc success@simulator.amazonses.com \
  --subject "Prueba CC/BCC" \
  --body "cuerpo"

# reply-all AUTOMÁTICO: SIN --cc, el Cc se pre-llena con los To+Cc originales (minus self)
mail reply <s3Key>

# --cc EXPLÍCITO REEMPLAZA el reply-all (NO lo añade): este reply va SOLO a extra@, no a los originales
mail reply <s3Key> --cc extra@erickaldama.com

# listas multi-valor
mail send --to a@x.com,b@x.com --cc c@x.com,d@x.com --bcc e@x.com
```

**Formato de dirección soportado en v0.2:** solo `addr-spec` pura (`user@host`). La forma `"Nombre <user@host>"`
**no está soportada** — el campo se pasa directamente a enmime como addr-spec; enviar una name-addr resultará
en un MIME malformado o en un error de envío SES.

### 10.3 Invariante de privacidad del BCC

**El BCC viaja SOLO en el envelope SES (`Destinations`), nunca en el header MIME.**

Detalle técnico: enmime expone un método `.BCC()` que, si se llama, **escribe** un header `Bcc:` en el raw
MIME. El cliente NO llama ese método — construye los destinatarios del envelope (`To + Cc + Bcc`) por separado
y pasa el BCC únicamente a `SES.SendRawEmail(Destinations: [...])`. El encabezado `Bcc:` nunca aparece en el
mensaje que reciben los destinatarios To/Cc.

Invariante verificado en dos capas:
- **Núcleo** (`internal/message`): `TestBuildCcInHeaderBccNot` — construye un mensaje con Cc y Bcc, confirma
  que el raw MIME contiene `Cc:` pero no `Bcc:` ni la dirección BCC, y que el slice de destinations incluye
  los tres grupos (To + Cc + Bcc).
- **TUI composer** (`cmd/mail-tui`): `TestComposerBccNotInRaw` — verifica el mismo invariante en el path de
  envío del composer: el raw MIME no filtra el BCC aunque el campo Bcc esté relleno en la UI.

### 10.4 Nota BCC-2: campo Bcc VISIBLE en el composer TUI

El composer TUI muestra cuatro campos editables: `To`, `Cc`, `Bcc`, `Subject`. El campo `Bcc` **es visible
en pantalla** mientras se escribe — es el comportamiento estándar de cualquier cliente de correo (el remitente
ve sus propios campos antes de enviar). La privacidad del BCC aplica para los **destinatarios receptores**:
el `Bcc` no aparece en el header del mensaje que reciben.

Implicación operativa: si grabas la pantalla o compartes la sesión tmux mientras usas el composer (popup
`prefix+e`), los destinatarios BCC son visibles en pantalla. Tener esto en cuenta al presentar o grabar.

### 10.5 Smoke de CC/BCC — GATE HUMANO (no ejecutado por el agente)

El agente NO ejecuta `mail send` (mutación SES — gate humano out-of-band). El comando que ejecuta el humano:

```bash
# GATE HUMANO — ejecutar manualmente:
mail send \
  --from erick@erickaldama.com \
  --to erick@erickaldama.com \
  --cc test@erickaldama.com \
  --bcc success@simulator.amazonses.com \
  --subject "v0.2 cc/bcc smoke" \
  --body "test"

# verificar que llegó; abrir y confirmar: el header tiene To+Cc, NO Bcc
mail ls --mailbox erick@erickaldama.com
```

Verificación del invariante en vivo: abrir el correo recibido y confirmar que `success@simulator.amazonses.com`
(el BCC) **no aparece en ningún header** del mensaje recibido.

---

## 11. v0.3 — Render rico HTML + imágenes inline

### 11.1 Dependencias opcionales

El render rico en terminal requiere **chafa** para renderizar imágenes `cid:` embebidas como arte de bloques Unicode:

```bash
brew install chafa   # macOS — única dependencia opcional de v0.3
chafa --version      # verifica instalación (cualquier versión >= 1.12 funciona)
```

Sin chafa: el CLI `--rich` y el TUI funcionan normalmente (HTML sanitizado + glamour); solo la tecla `i`
(render de imagen inline) degrada graciosamente sin crash.

Dependencias Go (ya en `go.mod`, sin instalación manual):
- `github.com/microcosm-cc/bluemonday` — sanitización HTML (frontera de seguridad XSS + tracking pixels)
- `github.com/charmbracelet/glamour` — render markdown→ANSI
- `github.com/JohannesKaufmann/html-to-markdown` — conversión HTML→markdown (CRÍTICO: antes de glamour)
- `golang.org/x/net/html` — parse HTML para pass 1 de sanitización

### 11.2 Flujo del render rico

```
raw MIME (S3)
    │
    ▼
Parse(r io.Reader)       → Parsed{TextHTML, InlineImages[]{ContentID, Data}}
    │
    ▼
SanitizeHTML(html, false)
    pass 1 (x/net/html)  → reemplaza <img src="http://..."> por [imagen remota bloqueada]
                           → recolecta CIDRefs y BlockedRemotes
    pass 2 (bluemonday)  → UGCPolicy + restricción img.src=^cid: (HARD SAN-2)
    │
    ▼
htmltomd.Convert(sanitizedHTML)   → markdown (P-1 CRÍTICO: antes de glamour)
    │
    ▼
glamour.Render(md, width=termWidth)  → ANSI (ancho dinámico del panel TUI)
    │
    ▼
TUI reader / CLI stdout

── tecla `i` (TUI) ──────────────────────────────────────────────────────────
    InlineImages[i].Data  →  chafa -f symbols --size WxH  →  Unicode block art
    (tea.Cmd async — no bloquea el event loop de Bubble Tea)

── tecla `R` (TUI) / --load-remote (CLI) ────────────────────────────────────
    SanitizeHTML(rawHTML, allowRemote=true)  →  re-render con remotas permitidas
    (rawHTML se preserva en el modelo — no se descarta tras el primer render)
```

### 11.3 Política de privacidad — imágenes remotas bloqueadas por default

La política del cliente es: **las imágenes remotas NO cargan a menos que el usuario lo pida explícitamente**.

| Tipo de imagen | Comportamiento por default | Con `--load-remote` / tecla `R` |
|---|---|---|
| `cid:` (inline MIME) | Disponible — tecla `i` en TUI | Igual |
| `http://` / `https://` | Reemplazada por `[imagen remota bloqueada]` | Carga |
| `//host/...` (protocol-relative) | Bloqueada (tratada como remota) | Carga |
| `HTTP://` / `HTTPS://` (mayúsculas) | Bloqueada (case-insensitive) | Carga |

La frontera de seguridad es **bluemonday** (pass 2): aunque el DOM de pass 1 no reemplazara el nodo,
bluemonday eliminaría cualquier `src` que no coincida con `^cid:` (regexp). Las dos capas son
independientes — UX en pass 1, seguridad en pass 2.

### 11.4 Teclas del TUI reader (v0.3)

| Tecla | Acción |
|-------|--------|
| `i` | Renderiza imágenes `cid:` inline con chafa (on-demand, async) |
| `R` | Recarga el cuerpo permitiendo imágenes remotas (`allowRemote=true`) |

### 11.5 CLI — uso del flag `--rich`

```bash
# Render rico (HTML sanitizado + glamour + ANSI):
mail read <s3Key> --rich

# Render rico + imágenes remotas (solo si confías en el remitente):
mail read <s3Key> --rich --load-remote

# Render plano (default — sin glamour, texto puro):
mail read <s3Key>
```

### 11.6 Gate humano — smoke con correo real (NO ejecutado por el agente)

El smoke de render real requiere un correo con imagen en AWS (interactivo + NDA). El humano ejecuta:

```bash
# GATE HUMANO — ejecutar manualmente:
mail read <key-de-correo-con-imagen> --rich
# Abrir el TUI · confirmar glamour render
# Tecla 'i' · confirmar chafa renderiza la imagen cid: como arte unicode
# Confirmar que http://... remotas muestran [imagen remota bloqueada]
# Tecla 'R' · confirmar que carga remotas al aceptar explícitamente
```

El agente NO ejecuta este smoke (gate humano out-of-band — correo real NDA + interactivo). La suite
automatizada cubre el invariante de sanitización (SAN-5: `TestSAN5InlineImageFixture` con fixture sintético).
