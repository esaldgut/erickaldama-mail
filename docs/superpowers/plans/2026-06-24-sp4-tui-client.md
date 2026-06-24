# SP-4 — Cliente TUI/CLI/AI Go — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Un cliente de correo terminal-native en Go (CLI + TUI + agente AI doble backend) que lee `mail-index`
(DynamoDB) + cuerpos S3 y envía/responde vía SES, cerrando el lazo end-to-end de erickaldama.com.

**Architecture:** Núcleo de dominio sin UI (`internal/mailbox`, `internal/message`, `internal/aiassist`,
`internal/awsconf`) consumido por dos binarios delgados (`cmd/mail` CLI Cobra, `cmd/mail-tui` Bubble Tea) y por
el agente AI. Dos planos de lectura: listar = DynamoDB Query; abrir = S3 GetObject + parse MIME. El agente AI
usa una interfaz `LLMProvider` con dos implementaciones (Ollama local default / Claude API opt-in) y un agent-loop propio.

**Tech Stack:** Go 1.26.4 · AWS SDK Go v2 (dynamodb v1.59.0, s3 v1.104.0, ses v1.35.2, sesv2 v1.62.4, config,
credentials, feature/dynamodb/attributevalue) · anthropic-sdk-go v1.51.1 · enmime/v2 v2.4.1 ·
charmbracelet/bubbletea v1.3.10 + bubbles + lipgloss + glamour v1.0.0 · JohannesKaufmann/html-to-markdown v1.6.0 ·
spf13/cobra v1.10.2 · Ollama HTTP (net/http) a localhost:11434 · CDK-Go v2.258.1 (solo Task 0).

## Global Constraints

- **Go 1.26.4** (go.mod del repo). Aplicar modern-go-guidelines (errors.As, slog, etc.).
- **Cuenta AWS `367707589526`, región `us-east-1`** (constantes, nunca adivinadas).
- **Repo PÚBLICO con Git Flow:** cierre = PR a develop con CI verde, NO merge local. Gate NDA sobre todo el output.
- **El cliente Go NO aprovisiona infra** → el hook CDK-Go NO aplica a `internal/{mailbox,message,aiassist,awsconf}`
  ni a `cmd/`. SOLO Task 0 (users CDK) es infra → hook aplica + deploy humano out-of-band.
- **Disciplina avoid-string-match-error-silencing:** errores tipados del SDK (`errors.As`), NUNCA match contra `.Error()`.
- **NDA / privacidad:** default = Ollama LOCAL (el correo no sale del Mac). Claude = opt-in. Backends `:cloud` PROHIBIDOS en v0.1.
- **Cero secretos en binario/logs/git:** AWS desde profiles `~/.aws`, API key Claude desde `ANTHROPIC_API_KEY`/Keychain.
- **Versiones de deps verificadas vivas 2026-06-24** (`go list -m -versions`). Pinear las del Tech Stack en go.mod.
- **subagent-driven en worktree:** todo commit con `git -C "$WT"`, validar rama ≠ develop/main, validar cada SHA.

**Schema `mail-index` (fuente de verdad, de `cmd/lambda/receive/main.go`):**
`PK="mailbox#<addr-lower>"`, `SK="ts#<RFC3339-UTC>#<RFC5322-MessageID>"`, attrs `messageId, s3Key, from, subject, date`.
Cuerpo MIME en S3 `erickaldama-mail-raw`, key `inbound/<messageId>` (= `messageId`, NO el RFC5322-id).

---

## File Structure

| Archivo | Responsabilidad |
|---|---|
| `internal/infra/naming.go` (mod) | +`ClientReadUserName="mail-client-read"`, `SenderUserName="mail-sender"` |
| `internal/infra/foundation_stack.go` (mod) | +user `mail-client-read` con policy dynamodb:Query/GetItem + s3:GetObject scoped |
| `internal/infra/sending_stack.go` (mod) | +user `mail-sender` con la mailSendPolicy existente (en `addSendIam`) |
| `internal/message/parse.go` + `parse_test.go` | enmime/v2 ReadEnvelope → `Parsed`; fixtures MIME en `testdata/` |
| `internal/message/render.go` + `render_test.go` | HTML→markdown→glamour (TUI) ; texto plano (CLI) |
| `internal/message/build.go` + `build_test.go` | enmime.Builder() saliente + Message-ID + ReplyHeaders (threading) |
| `internal/awsconf/config.go` + `config_test.go` | carga 2 aws.Config por profile; constantes región/tabla/bucket |
| `internal/mailbox/reader.go` + `reader_test.go` | Reader: List (DynamoDB Query) + Open (S3 GetObject); interfaces para fakes |
| `internal/mailbox/sender.go` + `sender_test.go` | Sender: Send (SES v1 SendRawEmail) + DetectSandbox (sesv2 GetAccount) |
| `internal/aiassist/provider.go` + `*_test.go` | interfaz LLMProvider + tipos neutrales Message/ToolSpec/ToolCall/Response |
| `internal/aiassist/agent.go` + `agent_test.go` | agent-loop propio + tools read-only + Summarize/Draft; LLMProvider fake |
| `internal/aiassist/ollama/ollama.go` + `*_test.go` | provider Ollama HTTP (localhost:11434/api/chat) |
| `internal/aiassist/claude/claude.go` + `*_test.go` | provider Claude (anthropic-sdk-go) |
| `cmd/mail/main.go` + subcomandos | CLI Cobra (ls/read/send/reply/ai) |
| `cmd/mail-tui/main.go` + vistas | TUI Bubble Tea (list/reader/composer) |
| `docs/SP-4-DEPLOY.md` (new) | runbook Task 0 + bindings tmux/nvim + rotación de keys |

---

## Task 0: Mini-cambio CDK — users `mail-client-read` + `mail-sender` (GATE HUMANO)

**El agente escribe el código CDK-Go + corre synth/diff read-only + entrega comandos exactos. El HUMANO ejecuta
`cdk deploy` out-of-band (SSO Admin) y `create-access-key`. El agente verifica post-deploy.**

**Files:**
- Modify: `internal/infra/naming.go` (añadir 2 constantes tras `SenderRoleName`)
- Modify: `internal/infra/foundation_stack.go` (añadir user `mail-client-read` + su managed policy)
- Modify: `internal/infra/sending_stack.go:151-172` (añadir user `mail-sender` en `addSendIam`)
- Test: `internal/infra/sending_stack_test.go` / `foundation_stack_test.go` (template-asserts)

**Interfaces:**
- Produces: dos IAM users en AWS → profiles `mail-client-read` (Query+GetObject) y `mail-sender` (SendRawEmail). El cliente Go los consume vía `~/.aws/credentials`.

- [ ] **Step 1: Añadir constantes de naming**

En `internal/infra/naming.go`, tras la línea `SenderRoleName  = "mail-sender-role"`:
```go
	// SP-4 — client principals (long-lived access keys generated out-of-band; never in CDK/git).
	ClientReadUserName = "mail-client-read" // dynamodb:Query/GetItem on mail-index + s3:GetObject on inbound/*
	SenderUserName     = "mail-sender"      // attaches mail-send policy directly (SendRawEmail)
```

- [ ] **Step 2: Escribir template-assert que falla (user mail-client-read en FoundationStack)**

En `internal/infra/foundation_stack_test.go`, añadir:
```go
func TestFoundationStackHasClientReadUser(t *testing.T) {
	app := awscdk.NewApp(nil)
	stack := NewFoundationStack(app, "FoundationStack", &awscdk.StackProps{
		Env: &awscdk.Environment{Account: jsii.String(Account), Region: jsii.String(Region)},
	})
	template := assertions.Template_FromStack(stack, nil)
	template.HasResourceProperties(jsii.String("AWS::IAM::User"), map[string]any{
		"UserName": ClientReadUserName,
	})
}
```

- [ ] **Step 3: Run test → FAIL**

Run: `go test ./internal/infra/ -run TestFoundationStackHasClientReadUser`
Expected: FAIL (no AWS::IAM::User con UserName mail-client-read).

- [ ] **Step 4: Implementar el user mail-client-read en foundation_stack.go**

En `internal/infra/foundation_stack.go`, dentro de `NewFoundationStack` (antes de `return stack`), añadir
una managed policy scoped + un user que la lleva. Usar los ARNs reales de `mail-index` y `erickaldama-mail-raw`:
```go
	clientReadPolicy := awsiam.NewManagedPolicy(stack, jsii.String("MailClientReadPolicy"),
		&awsiam.ManagedPolicyProps{
			ManagedPolicyName: jsii.String("mail-client-read"),
			Statements: &[]awsiam.PolicyStatement{
				awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
					Sid:       jsii.String("ReadMailIndex"),
					Effect:    awsiam.Effect_ALLOW,
					Actions:   jsii.Strings("dynamodb:Query", "dynamodb:GetItem"),
					Resources: jsii.Strings("arn:aws:dynamodb:us-east-1:" + Account + ":table/" + MailIndexTableName),
					Conditions: &map[string]any{
						"StringEquals": map[string]any{"aws:RequestedRegion": "us-east-1"},
					},
				}),
				awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
					Sid:       jsii.String("ReadInboundBodies"),
					Effect:    awsiam.Effect_ALLOW,
					Actions:   jsii.Strings("s3:GetObject"),
					Resources: jsii.Strings("arn:aws:s3:::" + RawBucketName + "/" + InboundObjectPrefix + "*"),
					Conditions: &map[string]any{
						"StringEquals": map[string]any{"aws:RequestedRegion": "us-east-1"},
					},
				}),
			},
		})
	awsiam.NewUser(stack, jsii.String("MailClientReadUser"), &awsiam.UserProps{
		UserName:        jsii.String(ClientReadUserName),
		ManagedPolicies: &[]awsiam.IManagedPolicy{clientReadPolicy},
	})
```

- [ ] **Step 5: Run test → PASS**

Run: `go test ./internal/infra/ -run TestFoundationStackHasClientReadUser`
Expected: PASS.

- [ ] **Step 6: Escribir template-assert que falla (user mail-sender en SendingStack)**

En `internal/infra/sending_stack_test.go`, añadir:
```go
func TestSendingStackHasSenderUser(t *testing.T) {
	app := awscdk.NewApp(nil)
	stack := NewSendingStack(app, "SendingStack", &awscdk.StackProps{
		Env: &awscdk.Environment{Account: jsii.String(Account), Region: jsii.String(Region)},
	})
	template := assertions.Template_FromStack(stack, nil)
	template.HasResourceProperties(jsii.String("AWS::IAM::User"), map[string]any{
		"UserName": SenderUserName,
	})
}
```

- [ ] **Step 7: Run test → FAIL**

Run: `go test ./internal/infra/ -run TestSendingStackHasSenderUser`
Expected: FAIL.

- [ ] **Step 8: Implementar el user mail-sender en addSendIam**

En `internal/infra/sending_stack.go`, dentro de `addSendIam`, tras el `NewRole(...MailSenderRole...)`,
añadir un user que lleva la MISMA `mailSendPolicy` directa:
```go
	awsiam.NewUser(stack, jsii.String("MailSenderUser"), &awsiam.UserProps{
		UserName:        jsii.String(SenderUserName),
		ManagedPolicies: &[]awsiam.IManagedPolicy{mailSendPolicy},
	})
```

- [ ] **Step 9: Run test → PASS + suite completa**

Run: `go test ./internal/infra/`
Expected: PASS (incl. los asserts SP-1/2/3 existentes).

- [ ] **Step 10: synth + diff read-only (permitidos por el hook)**

Run:
```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk synth FoundationStack SendingStack >/dev/null && echo "synth OK"
AWS_PROFILE=AdministratorAccess-367707589526 cdk diff FoundationStack SendingStack 2>&1 | grep -A3 "AWS::IAM::User"
```
Expected: synth OK; diff muestra `+ AWS::IAM::User` para mail-client-read (FoundationStack) y mail-sender (SendingStack), nada más fuera de esos users+policies. Canario: NO debe tocar HostedZone, identity, rule, ni la mailSendPolicy existente.

- [ ] **Step 11: Commit del código CDK**

```bash
git add internal/infra/naming.go internal/infra/foundation_stack.go internal/infra/sending_stack.go internal/infra/foundation_stack_test.go internal/infra/sending_stack_test.go
git commit -m "feat(sp-4): CDK users mail-client-read + mail-sender (Task 0 infra)"
```

- [ ] **Step 12: GATE HUMANO — entregar comandos exactos al usuario**

El usuario ejecuta out-of-band (SSO Admin):
```bash
AWS_PROFILE=AdministratorAccess-367707589526 cdk deploy FoundationStack SendingStack --require-approval any-change
AWS_PROFILE=AdministratorAccess-367707589526 aws iam create-access-key --user-name mail-client-read
AWS_PROFILE=AdministratorAccess-367707589526 aws iam create-access-key --user-name mail-sender
# Guardar cada par en ~/.aws/credentials bajo [mail-client-read] y [mail-sender] (region us-east-1).
```
**El agente NO ejecuta esto** (hook bloquea writes). Espera confirmación del humano.

- [ ] **Step 13: Verificación post-deploy (agente, read-only, prueba empírica)**

Tras el deploy + keys del humano, el agente verifica que cada profile resuelve y puede su operación:
```bash
aws sts get-caller-identity --profile mail-client-read   # → user/mail-client-read
aws dynamodb query --table-name mail-index --key-condition-expression "PK = :pk" \
  --expression-attribute-values '{":pk":{"S":"mailbox#test@erickaldama.com"}}' \
  --region us-east-1 --profile mail-client-read --max-items 1   # → OK (no AccessDenied)
aws sts get-caller-identity --profile mail-sender        # → user/mail-sender
```
Expected: ambos profiles resuelven a su user; mail-client-read puede Query (y GetObject de un s3Key real). Si AccessDenied → revisar policy. Cierra el gate del Hallazgo #8 (assume-as + prueba empírica).

---

## Task 1: Bootstrap del módulo cliente — deps + skeleton + awsconf

**Files:**
- Modify: `go.mod` (añadir deps directas)
- Create: `internal/awsconf/config.go` + `internal/awsconf/config_test.go`

**Interfaces:**
- Produces: `awsconf.Load(ctx, profile string) (aws.Config, error)`; constantes `awsconf.Region="us-east-1"`, `awsconf.TableName="mail-index"`, `awsconf.BucketName="erickaldama-mail-raw"`, `awsconf.InboundPrefix="inbound/"`.

- [ ] **Step 1: Añadir deps directas y verificar build**

Run (versiones verificadas vivas 2026-06-24):
```bash
go get github.com/aws/aws-sdk-go-v2/config@latest \
  github.com/aws/aws-sdk-go-v2/credentials@latest \
  github.com/aws/aws-sdk-go-v2/service/dynamodb@v1.59.0 \
  github.com/aws/aws-sdk-go-v2/service/s3@v1.104.0 \
  github.com/aws/aws-sdk-go-v2/service/ses@v1.35.2 \
  github.com/aws/aws-sdk-go-v2/service/sesv2@v1.62.4 \
  github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue@latest
go build ./...
```
Expected: build OK (no rompe los stacks CDK existentes).

- [ ] **Step 2: Escribir test de awsconf (constantes + firma)**

`internal/awsconf/config_test.go`:
```go
package awsconf

import "testing"

func TestConstants(t *testing.T) {
	if Region != "us-east-1" || TableName != "mail-index" || BucketName != "erickaldama-mail-raw" || InboundPrefix != "inbound/" {
		t.Fatalf("constants drifted: %s %s %s %s", Region, TableName, BucketName, InboundPrefix)
	}
}
```

- [ ] **Step 3: Run → FAIL** (paquete no existe)

Run: `go test ./internal/awsconf/`
Expected: FAIL (undefined Region/TableName/...).

- [ ] **Step 4: Implementar awsconf/config.go**

```go
// Package awsconf loads segregated AWS configs (one per profile) and exposes the canonical
// resource identifiers the client reads/writes. Read path uses mail-client-read; send uses mail-sender.
package awsconf

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	Region        = "us-east-1"
	TableName     = "mail-index"
	BucketName    = "erickaldama-mail-raw"
	InboundPrefix = "inbound/"
)

// Load returns an aws.Config bound to a named shared-config profile, pinned to us-east-1.
func Load(ctx context.Context, profile string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(Region),
	)
}
```

- [ ] **Step 5: Run → PASS**

Run: `go test ./internal/awsconf/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/awsconf/
git commit -m "feat(sp-4): client module deps + awsconf (profile loader + resource ids)"
```

---

## Task 2: `internal/message` — parse MIME (enmime/v2)

**Files:**
- Create: `internal/message/parse.go` + `internal/message/parse_test.go`
- Create: `internal/message/testdata/plain.eml`, `html.eml`, `multipart-attach.eml`

**Interfaces:**
- Produces: `type Parsed struct { Subject, From, Date, TextPlain, TextHTML, MessageID, References string; Attachments []Attachment }`; `type Attachment struct { FileName, ContentType string; Size int }`; `func Parse(r io.Reader) (*Parsed, error)`.

- [ ] **Step 1: Crear fixtures MIME en testdata/**

`internal/message/testdata/plain.eml`:
```
From: alice@example.com
To: test@erickaldama.com
Subject: Hola plano
Message-ID: <plain-001@example.com>
Date: Mon, 23 Jun 2026 10:00:00 +0000
Content-Type: text/plain; charset=utf-8

Cuerpo en texto plano con acento: café.
```
`internal/message/testdata/html.eml`:
```
From: bob@example.com
To: test@erickaldama.com
Subject: Hola HTML
Message-ID: <html-001@example.com>
References: <thread-root@example.com>
Date: Mon, 23 Jun 2026 11:00:00 +0000
Content-Type: text/html; charset=utf-8

<html><body><p>Hola <b>mundo</b> con <a href="https://x.test">link</a>.</p></body></html>
```
`internal/message/testdata/multipart-attach.eml`:
```
From: carol@example.com
To: test@erickaldama.com
Subject: Con adjunto
Message-ID: <attach-001@example.com>
Date: Mon, 23 Jun 2026 12:00:00 +0000
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8

Mira el adjunto.
--b1
Content-Type: application/pdf; name="doc.pdf"
Content-Disposition: attachment; filename="doc.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQK
--b1--
```

- [ ] **Step 2: Escribir test de Parse**

`internal/message/parse_test.go`:
```go
package message

import (
	"os"
	"testing"
)

func parseFixture(t *testing.T, name string) *Parsed {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParsePlain(t *testing.T) {
	p := parseFixture(t, "plain.eml")
	if p.Subject != "Hola plano" || p.From != "alice@example.com" {
		t.Fatalf("headers: %+v", p)
	}
	if p.TextPlain == "" || !contains(p.TextPlain, "café") {
		t.Fatalf("plain text not decoded: %q", p.TextPlain)
	}
	if p.MessageID != "<plain-001@example.com>" {
		t.Fatalf("message-id: %q", p.MessageID)
	}
}

func TestParseHTMLWithReferences(t *testing.T) {
	p := parseFixture(t, "html.eml")
	if p.TextHTML == "" || !contains(p.TextHTML, "<b>mundo</b>") {
		t.Fatalf("html missing: %q", p.TextHTML)
	}
	if p.References != "<thread-root@example.com>" {
		t.Fatalf("references: %q", p.References)
	}
}

func TestParseAttachment(t *testing.T) {
	p := parseFixture(t, "multipart-attach.eml")
	if len(p.Attachments) != 1 || p.Attachments[0].FileName != "doc.pdf" {
		t.Fatalf("attachments: %+v", p.Attachments)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub { return true }
	}
	return false
})() }
```

- [ ] **Step 3: Run → FAIL** (Parse no existe)

Run: `go test ./internal/message/ -run TestParse`
Expected: FAIL.

- [ ] **Step 4: Implementar parse.go con enmime/v2**

```bash
go get github.com/jhillyerd/enmime/v2@v2.4.1
```
```go
// Package message parses inbound MIME and builds outbound MIME for the mail client. Pure (no AWS/network).
package message

import (
	"io"

	"github.com/jhillyerd/enmime/v2"
)

type Attachment struct {
	FileName    string
	ContentType string
	Size        int
}

type Parsed struct {
	Subject     string
	From        string
	Date        string
	TextPlain   string
	TextHTML    string
	MessageID   string
	References  string
	Attachments []Attachment
}

// Parse reads a raw MIME message. enmime decodes quoted-printable/base64 and converts charsets to utf-8.
func Parse(r io.Reader) (*Parsed, error) {
	env, err := enmime.ReadEnvelope(r)
	if err != nil {
		return nil, err
	}
	p := &Parsed{
		Subject:    env.GetHeader("Subject"),
		From:       env.GetHeader("From"),
		Date:       env.GetHeader("Date"),
		TextPlain:  env.Text,
		TextHTML:   env.HTML,
		MessageID:  env.GetHeader("Message-ID"),
		References: env.GetHeader("References"),
	}
	for _, a := range env.Attachments {
		p.Attachments = append(p.Attachments, Attachment{
			FileName:    a.FileName,
			ContentType: a.ContentType,
			Size:        len(a.Content),
		})
	}
	return p, nil
}
```

- [ ] **Step 5: Run → PASS**

Run: `go test ./internal/message/ -run TestParse`
Expected: PASS (3 tests). Si `env.GetHeader` no existe en v2.4.1, usar `env.Root.Header.Get(...)` — verificar con `go doc github.com/jhillyerd/enmime/v2.Envelope`.

- [ ] **Step 6: Commit**

```bash
git add internal/message/parse.go internal/message/parse_test.go internal/message/testdata/
git commit -m "feat(sp-4): MIME parsing via enmime/v2 (text/html/attachments/charsets)"
```

---

## Task 3: `internal/message` — render (HTML→markdown→glamour) + build (enmime.Builder) + threading

**Files:**
- Create: `internal/message/render.go` + `internal/message/build.go` + tests

**Interfaces:**
- Consumes: `Parsed` (Task 2).
- Produces: `func RenderPlain(p *Parsed) string`; `func RenderRich(p *Parsed) (string, error)`; `func NewMessageID() string`; `func ReplyHeaders(orig *Parsed) (inReplyTo, references, subject string)`; `func Build(opt BuildOpts) ([]byte, error)` con `type BuildOpts struct { From, To, Subject, Body, InReplyTo, References, MessageID string; Attachments []FileAttach }`, `type FileAttach struct { Path string }`.

- [ ] **Step 1: Test de threading (ReplyHeaders) y Message-ID**

`internal/message/build_test.go`:
```go
package message

import (
	"strings"
	"testing"
)

func TestReplyHeaders(t *testing.T) {
	orig := &Parsed{Subject: "Hola", MessageID: "<html-001@example.com>", References: "<thread-root@example.com>"}
	irt, refs, subj := ReplyHeaders(orig)
	if irt != "<html-001@example.com>" {
		t.Fatalf("in-reply-to: %q", irt)
	}
	if refs != "<thread-root@example.com> <html-001@example.com>" {
		t.Fatalf("references chain: %q", refs)
	}
	if subj != "Re: Hola" {
		t.Fatalf("subject: %q", subj)
	}
}

func TestReplyHeadersAlreadyRe(t *testing.T) {
	orig := &Parsed{Subject: "Re: Hola", MessageID: "<m@x>"}
	_, _, subj := ReplyHeaders(orig)
	if subj != "Re: Hola" {
		t.Fatalf("must not double Re:, got %q", subj)
	}
}

func TestNewMessageIDFormat(t *testing.T) {
	id := NewMessageID()
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@erickaldama.com>") {
		t.Fatalf("message-id format: %q", id)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/message/ -run 'TestReply|TestNewMessageID'`
Expected: FAIL.

- [ ] **Step 3: Implementar build.go (threading + Message-ID + Builder)**

```bash
go get github.com/jhillyerd/enmime/v2@v2.4.1  # ya añadido en Task 2
```
```go
package message

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jhillyerd/enmime/v2"
)

const Domain = "erickaldama.com"

// NewMessageID builds an RFC 5322 msg-id <unixnano.randhex@erickaldama.com>. No stdlib/Builder helper exists.
func NewMessageID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(b), Domain)
}

// ReplyHeaders derives threading headers per RFC 5322 §3.6.4 from the PARSED original (References lives in
// the S3 MIME headers, not in DynamoDB). References = parent's References + parent's Message-ID.
func ReplyHeaders(orig *Parsed) (inReplyTo, references, subject string) {
	inReplyTo = orig.MessageID
	if orig.References != "" {
		references = orig.References + " " + orig.MessageID
	} else {
		references = orig.MessageID
	}
	subject = orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return inReplyTo, references, subject
}

type FileAttach struct{ Path string }

type BuildOpts struct {
	From, To, Subject, Body string
	InReplyTo, References    string
	MessageID               string
	Attachments             []FileAttach
}

// Build assembles outbound MIME via enmime.Builder (NOT hand-rolled). Message-ID/threading via Header().
func Build(opt BuildOpts) ([]byte, error) {
	if opt.MessageID == "" {
		opt.MessageID = NewMessageID()
	}
	b := enmime.Builder().
		From("", opt.From).
		To("", opt.To).
		Subject(opt.Subject).
		Text([]byte(opt.Body)).
		Header("Message-ID", opt.MessageID)
	if opt.InReplyTo != "" {
		b = b.Header("In-Reply-To", opt.InReplyTo)
	}
	if opt.References != "" {
		b = b.Header("References", opt.References)
	}
	for _, a := range opt.Attachments {
		b = b.AddFileAttachment(a.Path)
	}
	part, err := b.Build()
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	if err := part.Encode(&sb); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}
```
Nota: verificar las firmas exactas de enmime/v2 Builder (`From(name, addr)`, `Text([]byte)`, `AddFileAttachment(path)`, `Build() (*enmime.Part, error)`, `part.Encode(io.Writer)`) con `go doc github.com/jhillyerd/enmime/v2.MailBuilder` antes de fijar; ajustar si difieren.

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/message/ -run 'TestReply|TestNewMessageID'`
Expected: PASS.

- [ ] **Step 5: Test de render (plain + rich)**

`internal/message/render_test.go`:
```go
package message

import (
	"strings"
	"testing"
)

func TestRenderPlainUsesText(t *testing.T) {
	p := &Parsed{TextPlain: "hola plano", TextHTML: "<b>x</b>"}
	if RenderPlain(p) != "hola plano" {
		t.Fatalf("plain render: %q", RenderPlain(p))
	}
}

func TestRenderRichConvertsHTML(t *testing.T) {
	p := &Parsed{TextHTML: "<p>Hola <b>mundo</b></p>"}
	out, err := RenderRich(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<p>") || strings.Contains(out, "<b>") {
		t.Fatalf("rich render still has raw HTML tags: %q", out)
	}
	if out == "" {
		t.Fatal("rich render empty")
	}
}
```

- [ ] **Step 6: Run → FAIL**

Run: `go test ./internal/message/ -run TestRender`
Expected: FAIL.

- [ ] **Step 7: Implementar render.go (glamour renderiza MARKDOWN, no HTML → html-to-markdown intermedio)**

```bash
go get github.com/JohannesKaufmann/html-to-markdown@v1.6.0 github.com/charmbracelet/glamour@v1.0.0
```
```go
package message

import (
	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/charmbracelet/glamour"
)

// RenderPlain returns the plain-text body for the CLI (enmime already down-converts HTML→text into TextPlain).
func RenderPlain(p *Parsed) string {
	if p.TextPlain != "" {
		return p.TextPlain
	}
	return p.TextHTML // worst case: raw; CLI is for piping, TUI uses RenderRich
}

// RenderRich converts the HTML body to markdown then renders it to ANSI for the TUI. glamour renders
// MARKDOWN, not HTML — the HTML→markdown step (html-to-markdown) is mandatory and lives here.
func RenderRich(p *Parsed) (string, error) {
	src := p.TextHTML
	if src == "" {
		return p.TextPlain, nil
	}
	md, err := htmltomd.NewConverter("", true, nil).ConvertString(src)
	if err != nil {
		return "", err
	}
	return glamour.Render(md, "dark")
}
```
Nota: verificar firmas vivas (`htmltomd.NewConverter(domain, useReadability, opts).ConvertString(html)`, `glamour.Render(in, style string) (string, error)`) con `go doc`; ajustar si difieren.

- [ ] **Step 8: Run → PASS**

Run: `go test ./internal/message/`
Expected: PASS (todo el paquete message).

- [ ] **Step 9: Commit**

```bash
git add internal/message/build.go internal/message/build_test.go internal/message/render.go internal/message/render_test.go go.mod go.sum
git commit -m "feat(sp-4): MIME build (enmime.Builder + threading + msg-id) and render (html-to-md + glamour)"
```

---

## Task 4: `internal/mailbox/reader.go` — List (DynamoDB Query) + Open (S3 GetObject)

**Files:**
- Create: `internal/mailbox/reader.go` + `internal/mailbox/reader_test.go`

**Interfaces:**
- Consumes: `awsconf.TableName/BucketName/InboundPrefix`.
- Produces: `type Header struct { PK, SK, MessageID, S3Key, From, Subject, Date string }`; `type Reader struct{...}`; `func NewReader(ddb DynamoAPI, s3c S3API) *Reader`; `func (r *Reader) List(ctx, mailbox string, limit int32, start map[string]types.AttributeValue) ([]Header, map[string]types.AttributeValue, error)`; `func (r *Reader) Open(ctx, s3Key string) (io.ReadCloser, error)`. Interfaces `DynamoAPI`/`S3API` para fakes.

- [ ] **Step 1: Test con fakes (List unmarshala, Open devuelve body)**

`internal/mailbox/reader_test.go`:
```go
package mailbox

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeDDB struct{ out *dynamodb.QueryOutput }
func (f fakeDDB) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return f.out, nil
}
type fakeS3 struct{ body string }
func (f fakeS3) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestListUnmarshals(t *testing.T) {
	out := &dynamodb.QueryOutput{Items: []map[string]ddbtypes.AttributeValue{{
		"PK":        &ddbtypes.AttributeValueMemberS{Value: "mailbox#test@erickaldama.com"},
		"SK":        &ddbtypes.AttributeValueMemberS{Value: "ts#2026-06-23T10:00:00Z#<m@x>"},
		"messageId": &ddbtypes.AttributeValueMemberS{Value: "abc123"},
		"s3Key":     &ddbtypes.AttributeValueMemberS{Value: "inbound/abc123"},
		"from":      &ddbtypes.AttributeValueMemberS{Value: "alice@example.com"},
		"subject":   &ddbtypes.AttributeValueMemberS{Value: "Hola"},
		"date":      &ddbtypes.AttributeValueMemberS{Value: "Mon, 23 Jun 2026 10:00:00 +0000"},
	}}}
	r := NewReader(fakeDDB{out: out}, fakeS3{})
	hs, _, err := r.List(context.Background(), "test@erickaldama.com", 10, nil)
	if err != nil || len(hs) != 1 || hs[0].MessageID != "abc123" || hs[0].S3Key != "inbound/abc123" {
		t.Fatalf("list: %+v err=%v", hs, err)
	}
}

func TestOpenReadsBody(t *testing.T) {
	r := NewReader(fakeDDB{}, fakeS3{body: "RAW MIME"})
	rc, err := r.Open(context.Background(), "inbound/abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "RAW MIME" {
		t.Fatalf("body: %q", b)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/mailbox/ -run 'TestList|TestOpen'`
Expected: FAIL.

- [ ] **Step 3: Implementar reader.go**

```go
// Package mailbox is the mail data plane: Reader (DynamoDB Query + S3 GetObject) and Sender (SES).
package mailbox

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"erickaldama-mail/internal/awsconf"
)

// Header mirrors one mail-index row (schema source: cmd/lambda/receive/main.go).
type Header struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	MessageID string `dynamodbav:"messageId"`
	S3Key     string `dynamodbav:"s3Key"`
	From      string `dynamodbav:"from"`
	Subject   string `dynamodbav:"subject"`
	Date      string `dynamodbav:"date"`
}

type DynamoAPI interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}
type S3API interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type Reader struct {
	ddb DynamoAPI
	s3c S3API
}

func NewReader(ddb DynamoAPI, s3c S3API) *Reader { return &Reader{ddb: ddb, s3c: s3c} }

// List queries one mailbox, newest first (ScanIndexForward=false). start is the pagination cursor (nil for first page).
func (r *Reader) List(ctx context.Context, mailbox string, limit int32, start map[string]ddbtypes.AttributeValue) ([]Header, map[string]ddbtypes.AttributeValue, error) {
	out, err := r.ddb.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(awsconf.TableName),
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: "mailbox#" + mailbox},
		},
		ScanIndexForward:  aws.Bool(false),
		Limit:             aws.Int32(limit),
		ExclusiveStartKey: start,
	})
	if err != nil {
		return nil, nil, err
	}
	var hs []Header
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &hs); err != nil {
		return nil, nil, err
	}
	return hs, out.LastEvaluatedKey, nil
}

// Open streams the raw MIME from S3. CALLER MUST Close the returned ReadCloser (no explicit doc, mandatory).
func (r *Reader) Open(ctx context.Context, s3Key string) (io.ReadCloser, error) {
	out, err := r.s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(awsconf.BucketName),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/mailbox/ -run 'TestList|TestOpen'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mailbox/reader.go internal/mailbox/reader_test.go
git commit -m "feat(sp-4): mailbox Reader (DynamoDB Query newest-first + S3 GetObject)"
```

---

## Task 5: `internal/mailbox/sender.go` — Send (SES v1 SendRawEmail) + DetectSandbox (sesv2) + error tipado

**Files:**
- Create: `internal/mailbox/sender.go` + `internal/mailbox/sender_test.go`

**Interfaces:**
- Produces: `type Sender struct{...}`; `func NewSender(ses SESRawAPI, sesv2 SESAccountAPI) *Sender`; `func (s *Sender) Send(ctx, raw []byte) (string, error)`; `func (s *Sender) DetectSandbox(ctx) (bool, error)`; `var ErrSandboxRecipient` (sentinel para UI). Interfaces `SESRawAPI`/`SESAccountAPI` para fakes.

- [ ] **Step 1: Test (Send OK; MessageRejected en sandbox → ErrSandboxRecipient; DetectSandbox)**

`internal/mailbox/sender_test.go`:
```go
package mailbox

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

type fakeRaw struct {
	id  string
	err error
}
func (f fakeRaw) SendRawEmail(ctx context.Context, in *ses.SendRawEmailInput, _ ...func(*ses.Options)) (*ses.SendRawEmailOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ses.SendRawEmailOutput{MessageId: aws.String(f.id)}, nil
}
type fakeAcct struct{ prod bool }
func (f fakeAcct) GetAccount(ctx context.Context, in *sesv2.GetAccountInput, _ ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error) {
	return &sesv2.GetAccountOutput{ProductionAccessEnabled: f.prod}, nil
}

func TestSendOK(t *testing.T) {
	s := NewSender(fakeRaw{id: "mid-1"}, fakeAcct{prod: false})
	id, err := s.Send(context.Background(), []byte("RAW"))
	if err != nil || id != "mid-1" {
		t.Fatalf("send: id=%q err=%v", id, err)
	}
}

func TestSendSandboxRejectMapped(t *testing.T) {
	// AWS does NOT type recipient-not-verified — it surfaces as MessageRejected generic.
	rejected := &sestypes.MessageRejected{}
	s := NewSender(fakeRaw{err: rejected}, fakeAcct{prod: false})
	_, err := s.Send(context.Background(), []byte("RAW"))
	if !errors.Is(err, ErrSandboxRecipient) {
		t.Fatalf("expected ErrSandboxRecipient (MessageRejected + sandbox), got %v", err)
	}
}

func TestDetectSandbox(t *testing.T) {
	s := NewSender(fakeRaw{}, fakeAcct{prod: false})
	sb, err := s.DetectSandbox(context.Background())
	if err != nil || !sb {
		t.Fatalf("sandbox: %v err=%v", sb, err)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/mailbox/ -run 'TestSend|TestDetect'`
Expected: FAIL.

- [ ] **Step 3: Implementar sender.go**

```go
package mailbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// ErrSandboxRecipient is returned when a send is rejected AND the account is in sandbox — the most likely
// cause is an unverified recipient. AWS does NOT type this case (it surfaces as MessageRejected generic), so
// we classify by errors.As(*MessageRejected) + a prior DetectSandbox, NEVER by string-matching the message.
var ErrSandboxRecipient = errors.New("send rejected; SES in sandbox — verify the recipient or use success@simulator.amazonses.com")

type SESRawAPI interface {
	SendRawEmail(context.Context, *ses.SendRawEmailInput, ...func(*ses.Options)) (*ses.SendRawEmailOutput, error)
}
type SESAccountAPI interface {
	GetAccount(context.Context, *sesv2.GetAccountInput, ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error)
}

type Sender struct {
	raw  SESRawAPI
	acct SESAccountAPI
}

func NewSender(raw SESRawAPI, acct SESAccountAPI) *Sender { return &Sender{raw: raw, acct: acct} }

// DetectSandbox reports whether the account is in the SES sandbox. ProductionAccessEnabled exists ONLY in
// sesv2 (GetAccount), never in SES v1.
func (s *Sender) DetectSandbox(ctx context.Context) (bool, error) {
	out, err := s.acct.GetAccount(ctx, &sesv2.GetAccountInput{})
	if err != nil {
		return false, err
	}
	return !out.ProductionAccessEnabled, nil
}

// Send delivers raw MIME via SES v1 SendRawEmail (sesv2 has no SendRawEmail). On a typed MessageRejected, if
// the account is in sandbox, wrap as ErrSandboxRecipient (the actionable cause) — no string-match.
func (s *Sender) Send(ctx context.Context, raw []byte) (string, error) {
	out, err := s.raw.SendRawEmail(ctx, &ses.SendRawEmailInput{
		RawMessage: &sestypes.RawMessage{Data: raw},
	})
	if err != nil {
		var rejected *sestypes.MessageRejected
		if errors.As(err, &rejected) {
			if sb, derr := s.DetectSandbox(ctx); derr == nil && sb {
				return "", fmt.Errorf("%w: %v", ErrSandboxRecipient, err)
			}
		}
		return "", fmt.Errorf("send raw email: %w", err)
	}
	return aws.ToString(out.MessageId), nil
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/mailbox/`
Expected: PASS (Reader + Sender).

- [ ] **Step 5: Commit**

```bash
git add internal/mailbox/sender.go internal/mailbox/sender_test.go
git commit -m "feat(sp-4): mailbox Sender (SES v1 SendRawEmail + sesv2 DetectSandbox, typed reject)"
```

---

## Task 6: `internal/aiassist` — interfaz LLMProvider + agent loop + tools (con provider fake)

**Files:**
- Create: `internal/aiassist/provider.go`, `internal/aiassist/agent.go`, `internal/aiassist/tools.go` + tests

**Interfaces:**
- Consumes: `mailbox.Reader`/`mailbox.Header` (Task 4).
- Produces: `type Message struct { Role, Content string; ToolCalls []ToolCall; ToolName string }`; `type ToolSpec struct { Name, Description string; Parameters map[string]any }`; `type ToolCall struct { ID, Name string; Args map[string]any }`; `type Response struct { Text string; ToolCalls []ToolCall; Stop string }`; `type LLMProvider interface { Chat(ctx, []Message, []ToolSpec) (Response, error); Name() string }`; `func Summarize(ctx, p LLMProvider, body string) (string, error)`; `func RunAgent(ctx, p LLMProvider, reader *mailbox.Reader, mailbox, goal string, maxIters int) (string, error)`.

- [ ] **Step 1: Test del agent loop con provider FAKE (tool-call → ejecuta → texto final; cap iteraciones)**

`internal/aiassist/agent_test.go`:
```go
package aiassist

import (
	"context"
	"testing"
)

// fakeProvider scripts a tool-call on turn 1, then a final answer on turn 2.
type fakeProvider struct{ turn int }
func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Chat(_ context.Context, _ []Message, _ []ToolSpec) (Response, error) {
	f.turn++
	if f.turn == 1 {
		return Response{Stop: "tool_use", ToolCalls: []ToolCall{{ID: "t1", Name: "list_messages", Args: map[string]any{"limit": 1.0}}}}, nil
	}
	return Response{Stop: "end", Text: "Tienes 1 mensaje."}, nil
}

func TestRunAgentExecutesToolThenAnswers(t *testing.T) {
	fp := &fakeProvider{}
	out, err := RunAgent(context.Background(), fp, nil, "test@erickaldama.com", "¿cuántos correos?", 5)
	if err != nil || out != "Tienes 1 mensaje." {
		t.Fatalf("agent: %q err=%v", out, err)
	}
	if fp.turn != 2 {
		t.Fatalf("expected 2 turns, got %d", fp.turn)
	}
}

func TestRunAgentCapsIterations(t *testing.T) {
	// provider that always asks for a tool → must hit the cap, not loop forever.
	loop := providerFunc(func() Response {
		return Response{Stop: "tool_use", ToolCalls: []ToolCall{{ID: "x", Name: "list_messages", Args: map[string]any{}}}}
	})
	_, err := RunAgent(context.Background(), loop, nil, "m", "g", 3)
	if err == nil {
		t.Fatal("expected error on iteration cap")
	}
}

type providerFunc func() Response
func (p providerFunc) Name() string { return "loop" }
func (p providerFunc) Chat(_ context.Context, _ []Message, _ []ToolSpec) (Response, error) { return p(), nil }
```
Nota: cuando `reader == nil`, las tools devuelven un resultado fijo de prueba (el loop no debe panic). El plan de Task 6 implementa la ejecución de tools tolerante a `reader == nil` para test (devuelve `"[]"`).

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/aiassist/ -run TestRunAgent`
Expected: FAIL.

- [ ] **Step 3: Implementar provider.go (tipos neutrales)**

```go
// Package aiassist is the AI layer: a neutral LLMProvider interface with a shared agent loop. Two providers
// (ollama local default / claude API opt-in) translate to/from these neutral types. ToolCall carries ID
// (Anthropic correlation) + Name (Ollama correlation by name+order).
package aiassist

import "context"

type Message struct {
	Role      string // "user" | "assistant" | "tool"
	Content   string
	ToolCalls []ToolCall // assistant turn
	ToolName  string     // tool result turn (Ollama-style correlation)
	ToolID    string     // tool result turn (Anthropic tool_use_id correlation)
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON-schema-ish
}

type ToolCall struct {
	ID   string         // Anthropic tool_use_id; empty for Ollama
	Name string         // both
	Args map[string]any // Ollama arguments is an OBJECT (not string); Anthropic input is object
}

type Response struct {
	Text      string
	ToolCalls []ToolCall
	Stop      string // "tool_use" continues the loop; anything else ends it
}

type LLMProvider interface {
	Chat(ctx context.Context, msgs []Message, tools []ToolSpec) (Response, error)
	Name() string
}
```

- [ ] **Step 4: Implementar tools.go (tools read-only + ejecución)**

```go
package aiassist

import (
	"context"
	"encoding/json"
	"fmt"

	"erickaldama-mail/internal/mailbox"
)

// ReadOnlyTools are the agent's tools — NO send tool (blast radius bounded by design).
func ReadOnlyTools() []ToolSpec {
	return []ToolSpec{
		{Name: "list_messages", Description: "List recent message headers for the mailbox.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer"}}}},
		{Name: "read_message", Description: "Read one message body by s3Key.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"s3Key": map[string]any{"type": "string"}}, "required": []string{"s3Key"}}},
		{Name: "search_subject", Description: "Find messages whose subject contains the query.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}},
	}
}

// execTool runs one read-only tool. Tolerant of reader==nil (returns "[]") for unit tests.
func execTool(ctx context.Context, reader *mailbox.Reader, mb string, call ToolCall) string {
	if reader == nil {
		return "[]"
	}
	switch call.Name {
	case "list_messages":
		limit := int32(20)
		if v, ok := call.Args["limit"].(float64); ok && v > 0 {
			limit = int32(v)
		}
		hs, _, err := reader.List(ctx, mb, limit, nil)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		b, _ := json.Marshal(hs)
		return string(b)
	case "read_message":
		key, _ := call.Args["s3Key"].(string)
		rc, err := reader.Open(ctx, key)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		defer rc.Close()
		buf := make([]byte, 64*1024)
		n, _ := rc.Read(buf)
		return string(buf[:n])
	case "search_subject":
		q, _ := call.Args["query"].(string)
		hs, _, err := reader.List(ctx, mb, 100, nil)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		var hits []mailbox.Header
		for _, h := range hs {
			if containsFold(h.Subject, q) {
				hits = append(hits, h)
			}
		}
		b, _ := json.Marshal(hits)
		return string(b)
	}
	return fmt.Sprintf("unknown tool %q", call.Name)
}

func containsFold(s, sub string) bool {
	// case-insensitive contains; small helper to avoid importing strings twice across files
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return len(lsub) == 0
}
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
```

- [ ] **Step 5: Implementar agent.go (loop propio + Summarize + Draft)**

```go
package aiassist

import (
	"context"
	"fmt"

	"erickaldama-mail/internal/mailbox"
)

// Summarize asks for a summary + required action + urgency. One turn, no tools.
func Summarize(ctx context.Context, p LLMProvider, body string) (string, error) {
	resp, err := p.Chat(ctx, []Message{
		{Role: "user", Content: "Resume este correo en 3 líneas: tema, acción requerida, urgencia.\n\n" + body},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// Draft produces a reply draft given the thread context and a short instruction. Never sends.
func Draft(ctx context.Context, p LLMProvider, thread, instruction string) (string, error) {
	resp, err := p.Chat(ctx, []Message{
		{Role: "user", Content: "Redacta SOLO el cuerpo de una respuesta. Instrucción: " + instruction + "\n\nHilo:\n" + thread},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// RunAgent runs the shared agent loop: Chat → if tool_use, exec read-only tools, append results, loop.
// Bounded by maxIters (anti-runaway). Accumulates ALL tool results of a turn into ONE user message
// (Anthropic rule). Tools are read-only — no send.
func RunAgent(ctx context.Context, p LLMProvider, reader *mailbox.Reader, mb, goal string, maxIters int) (string, error) {
	msgs := []Message{{Role: "user", Content: goal}}
	tools := ReadOnlyTools()
	for i := 0; i < maxIters; i++ {
		resp, err := p.Chat(ctx, msgs, tools)
		if err != nil {
			return "", err
		}
		if resp.Stop != "tool_use" || len(resp.ToolCalls) == 0 {
			return resp.Text, nil
		}
		// assistant turn that requested tools
		msgs = append(msgs, Message{Role: "assistant", ToolCalls: resp.ToolCalls})
		// execute every requested tool, append each result (correlated by ID for Anthropic / Name for Ollama)
		for _, call := range resp.ToolCalls {
			result := execTool(ctx, reader, mb, call)
			msgs = append(msgs, Message{Role: "tool", ToolName: call.Name, ToolID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("agent exceeded %d iterations without final answer", maxIters)
}
```

- [ ] **Step 6: Run → PASS**

Run: `go test ./internal/aiassist/`
Expected: PASS (agent loop + cap).

- [ ] **Step 7: Commit**

```bash
git add internal/aiassist/provider.go internal/aiassist/tools.go internal/aiassist/agent.go internal/aiassist/agent_test.go
git commit -m "feat(sp-4): aiassist LLMProvider interface + shared agent loop + read-only tools"
```

---

## Task 7: `internal/aiassist/ollama` — provider HTTP local

**Files:**
- Create: `internal/aiassist/ollama/ollama.go` + `ollama_test.go`

**Interfaces:**
- Consumes: `aiassist.Message/ToolSpec/ToolCall/Response/LLMProvider`.
- Produces: `func New(model, host string) *Provider` (implementa `aiassist.LLMProvider`); default host `http://localhost:11434`.

- [ ] **Step 1: Test contra httptest server (arguments es OBJETO; result {role:tool,tool_name,content}; stream:false)**

`internal/aiassist/ollama/ollama_test.go`:
```go
package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erickaldama-mail/internal/aiassist"
)

func TestChatParsesToolCallObjectArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":false`) {
			t.Errorf("request must set stream:false, got %s", body)
		}
		// Ollama: arguments is an OBJECT, tool_calls has no id
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"list_messages","arguments":{"limit":5}}}]},"done":true}`))
	}))
	defer srv.Close()

	p := New("llama3.2", srv.URL)
	resp, err := p.Chat(context.Background(), []aiassist.Message{{Role: "user", Content: "hi"}}, aiassist.ReadOnlyTools())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_messages" {
		t.Fatalf("tool calls: %+v", resp.ToolCalls)
	}
	if v, ok := resp.ToolCalls[0].Args["limit"].(float64); !ok || v != 5 {
		t.Fatalf("arguments must parse as object, got %+v", resp.ToolCalls[0].Args)
	}
	if resp.Stop != "tool_use" {
		t.Fatalf("stop: %q", resp.Stop)
	}
	_ = json.Marshal // keep import if unused elsewhere
}

func TestChatFinalText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Hola final"},"done":true}`))
	}))
	defer srv.Close()
	p := New("llama3.2", srv.URL)
	resp, err := p.Chat(context.Background(), []aiassist.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || resp.Text != "Hola final" || resp.Stop == "tool_use" {
		t.Fatalf("resp: %+v err=%v", resp, err)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/aiassist/ollama/`
Expected: FAIL.

- [ ] **Step 3: Implementar ollama.go (HTTP directo, verificado vs docs.ollama.com)**

```go
// Package ollama implements aiassist.LLMProvider against a local Ollama daemon (default localhost:11434).
// Verified shape (docs.ollama.com/capabilities/tool-calling): tools wrapped {type:function,function:{...}};
// arguments is an OBJECT; tool_calls have no id (correlate by tool_name+order); result {role:tool,tool_name,content};
// stream:false explicit. DEFAULT-safe: the mail body never leaves the Mac.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"erickaldama-mail/internal/aiassist"
)

type Provider struct {
	model  string
	host   string
	client *http.Client
}

func New(model, host string) *Provider {
	if host == "" {
		host = "http://localhost:11434"
	}
	return &Provider{model: model, host: host, client: http.DefaultClient}
}

func (p *Provider) Name() string { return "ollama:" + p.model }

type olToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type olTool struct {
	Type     string   `json:"type"`
	Function olToolFn `json:"function"`
}
type olMsg struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolCalls []struct {
		Function struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"` // OBJECT, not string
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}
type olReq struct {
	Model    string   `json:"model"`
	Messages []olMsg  `json:"messages"`
	Tools    []olTool `json:"tools,omitempty"`
	Stream   bool     `json:"stream"`
}
type olResp struct {
	Message olMsg `json:"message"`
}

func (p *Provider) Chat(ctx context.Context, msgs []aiassist.Message, tools []aiassist.ToolSpec) (aiassist.Response, error) {
	req := olReq{Model: p.model, Stream: false}
	for _, m := range msgs {
		om := olMsg{Role: m.Role, Content: m.Content, ToolName: m.ToolName}
		req.Messages = append(req.Messages, om)
	}
	for _, ts := range tools {
		req.Tools = append(req.Tools, olTool{Type: "function", Function: olToolFn{Name: ts.Name, Description: ts.Description, Parameters: ts.Parameters}})
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return aiassist.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return aiassist.Response{}, fmt.Errorf("ollama unreachable (is the daemon running on %s?): %w", p.host, err)
	}
	defer resp.Body.Close()
	var or olResp
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return aiassist.Response{}, err
	}
	out := aiassist.Response{Text: or.Message.Content, Stop: "end"}
	for _, tc := range or.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, aiassist.ToolCall{Name: tc.Function.Name, Args: tc.Function.Arguments})
	}
	if len(out.ToolCalls) > 0 {
		out.Stop = "tool_use"
	}
	return out, nil
}
```

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/aiassist/ollama/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/aiassist/ollama/
git commit -m "feat(sp-4): Ollama provider (HTTP /api/chat, object args, stream:false, no tool_call_id)"
```

---

## Task 8: `internal/aiassist/claude` — provider API (anthropic-sdk-go)

**Files:**
- Create: `internal/aiassist/claude/claude.go` + `claude_test.go`

**Interfaces:**
- Consumes: `aiassist.*`.
- Produces: `func New(apiKey string) *Provider` (implementa `aiassist.LLMProvider`); model `claude-opus-4-8`, adaptive thinking, no sampling params.

- [ ] **Step 1: Test de mapeo neutral↔Anthropic (sin red — testear el builder de params)**

`internal/aiassist/claude/claude_test.go`:
```go
package claude

import (
	"testing"

	"erickaldama-mail/internal/aiassist"
)

func TestToolSpecMapping(t *testing.T) {
	specs := []aiassist.ToolSpec{{Name: "list_messages", Description: "d", Parameters: map[string]any{"type": "object"}}}
	tools := toAnthropicTools(specs)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool union, got %d", len(tools))
	}
}

func TestModelConstant(t *testing.T) {
	if Model != "claude-opus-4-8" {
		t.Fatalf("model must be claude-opus-4-8, got %q", Model)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./internal/aiassist/claude/`
Expected: FAIL.

- [ ] **Step 3: Implementar claude.go (anthropic-sdk-go v1.51.1)**

```bash
go get github.com/anthropics/anthropic-sdk-go@v1.51.1
```
```go
// Package claude implements aiassist.LLMProvider against the Anthropic Messages API (claude-opus-4-8).
// OPT-IN: the mail body crosses the network to api.anthropic.com (not trained on by default; ZDR recommended).
// Tools is []ToolUnionParam (wrap with OfTool). Adaptive thinking via union (no helper). NO temperature/top_p/
// budget_tokens (Opus 4.8 → 400). Key from ANTHROPIC_API_KEY or option.WithAPIKey(keyFromKeychain).
package claude

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"erickaldama-mail/internal/aiassist"
)

const Model = "claude-opus-4-8"

type Provider struct{ client anthropic.Client }

func New(apiKey string) *Provider {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Provider{client: anthropic.NewClient(opts...)} // falls back to ANTHROPIC_API_KEY env
}

func (p *Provider) Name() string { return "claude:" + Model }

func toAnthropicTools(specs []aiassist.ToolSpec) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, s := range specs {
		tp := anthropic.ToolParam{
			Name:        s.Name,
			Description: anthropic.String(s.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: s.Parameters},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out
}

func (p *Provider) Chat(ctx context.Context, msgs []aiassist.Message, tools []aiassist.ToolSpec) (aiassist.Response, error) {
	var amsgs []anthropic.MessageParam
	for _, m := range msgs {
		switch m.Role {
		case "user":
			amsgs = append(amsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			var blocks []anthropic.ContentBlockParamUnion
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Args, tc.Name))
			}
			amsgs = append(amsgs, anthropic.NewAssistantMessage(blocks...))
		case "tool":
			amsgs = append(amsgs, anthropic.NewUserMessage(anthropic.NewToolResultBlock(m.ToolID, m.Content, false)))
		}
	}
	params := anthropic.MessageNewParams{
		Model:     Model,
		MaxTokens: 2048,
		Messages:  amsgs,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
	}
	if len(tools) > 0 {
		params.Tools = toAnthropicTools(tools)
	}
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return aiassist.Response{}, err
	}
	out := aiassist.Response{Stop: string(resp.StopReason)}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			out.Text += b.Text
		case anthropic.ToolUseBlock:
			args := map[string]any{}
			_ = b // b.Input is json.RawMessage; unmarshal into args
			out.ToolCalls = append(out.ToolCalls, aiassist.ToolCall{ID: b.ID, Name: b.Name, Args: args})
		}
	}
	if string(resp.StopReason) == "tool_use" {
		out.Stop = "tool_use"
	}
	return out, nil
}
```
Nota: verificar firmas exactas con `go doc github.com/anthropics/anthropic-sdk-go` — `NewToolUseBlock`, `NewToolResultBlock`, `ToolUseBlock.Input` (unmarshal a `args`), `ThinkingConfigAdaptiveParam`. Ajustar al SDK real v1.51.1.

- [ ] **Step 4: Run → PASS**

Run: `go test ./internal/aiassist/claude/`
Expected: PASS (mapping + model constant; sin red).

- [ ] **Step 5: Commit**

```bash
git add internal/aiassist/claude/ go.mod go.sum
git commit -m "feat(sp-4): Claude provider (anthropic-sdk-go, opus-4-8 adaptive, ToolUnionParam)"
```

---

## Task 9: `cmd/mail` — CLI Cobra (ls/read/send/reply/ai)

**Files:**
- Create: `cmd/mail/main.go` + `cmd/mail/main_test.go`

**Interfaces:**
- Consumes: `mailbox.Reader/Sender`, `message.*`, `aiassist.*`, `awsconf.Load`.
- Produces: binario `mail` con subcomandos. Default read-profile `mail-client-read`, send-profile `mail-sender`, backend ollama `llama3.2` (agent: `qwen3:32b`).

- [ ] **Step 1: Test de `ls --json` con Reader fake (stdout determinista)**

`cmd/mail/main_test.go`:
```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"erickaldama-mail/internal/mailbox"
)

func TestRenderListJSON(t *testing.T) {
	hs := []mailbox.Header{{MessageID: "abc", Subject: "Hola", From: "a@x"}}
	var buf bytes.Buffer
	if err := renderList(&buf, hs, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"messageId"`) || !strings.Contains(buf.String(), "abc") {
		t.Fatalf("json output: %s", buf.String())
	}
}

func TestRenderListTable(t *testing.T) {
	hs := []mailbox.Header{{Subject: "Hola", From: "a@x", Date: "Mon"}}
	var buf bytes.Buffer
	if err := renderList(&buf, hs, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Hola") {
		t.Fatalf("table output: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./cmd/mail/`
Expected: FAIL.

- [ ] **Step 3: Implementar main.go (Cobra + renderList testeable)**

```bash
go get github.com/spf13/cobra@v1.10.2
```
Implementar `cmd/mail/main.go` con:
- `renderList(w io.Writer, hs []mailbox.Header, asJSON bool) error` — función PURA testeable (json.NewEncoder o tabla con text/tabwriter).
- root command `mail` + subcomandos `ls`, `read`, `send`, `reply`, `ai summarize|draft|agent`.
- flags globales: `--read-profile` (default `mail-client-read`), `--send-profile` (default `mail-sender`), `--mailbox`, `--backend` (default `ollama`).
- `ls`: `awsconf.Load(ctx, readProfile)` → `mailbox.NewReader(dynamodb.NewFromConfig(cfg), s3.NewFromConfig(cfg))` → `List` → `renderList`.
- `read`: `Open(s3Key)` → `message.Parse` → `RenderPlain` (CLI) a stdout. **defer body.Close().**
- `send`: construye `message.Build` → `Sender.Send`. Si `errors.Is(err, mailbox.ErrSandboxRecipient)` → imprime el mensaje accionable + `mr.ErrorMessage()` como contexto.
- `reply <id>`: lee el original (Open+Parse), `message.ReplyHeaders(parsed)`, abre `$EDITOR` con el cuerpo (draft AI opcional con `--instruction`), confirma, envía.
- `ai`: subcomandos que instancian el provider (ollama default / claude si `--backend claude`) y llaman `aiassist.Summarize/Draft/RunAgent`.

(El test cubre `renderList`; el wiring de subcomandos se valida en el smoke de Task 11.)

- [ ] **Step 4: Run → PASS + build**

Run: `go test ./cmd/mail/ && go build -o /dev/null ./cmd/mail`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add cmd/mail/ go.mod go.sum
git commit -m "feat(sp-4): mail CLI (Cobra ls/read/send/reply/ai, testeable renderList)"
```

---

## Task 10: `cmd/mail-tui` — TUI Bubble Tea (list/reader/composer)

**Files:**
- Create: `cmd/mail-tui/main.go` + `cmd/mail-tui/model.go` + `cmd/mail-tui/model_test.go`

**Interfaces:**
- Consumes: `mailbox.Reader/Sender`, `message.*`, `aiassist.*`.
- Produces: binario `mail-tui`. Model Bubble Tea con vistas list/reader/composer y Vim-motions.

- [ ] **Step 1: Test de Update con keypresses simulados (j/k mueven selección; Enter cambia a reader)**

`cmd/mail-tui/model_test.go`:
```go
package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"erickaldama-mail/internal/mailbox"
)

func newTestModel() model {
	return model{
		view:     viewList,
		headers:  []mailbox.Header{{Subject: "A"}, {Subject: "B"}, {Subject: "C"}},
		selected: 0,
	}
}

func TestJMovesDown(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.(model).selected != 1 {
		t.Fatalf("j should move to 1, got %d", updated.(model).selected)
	}
}

func TestGGoesToTop(t *testing.T) {
	m := newTestModel()
	m.selected = 2
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g', 'g'}})
	if updated.(model).selected != 0 {
		t.Fatalf("gg should go to top, got %d", updated.(model).selected)
	}
}

func TestEnterOpensReader(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(model).view != viewReader {
		t.Fatalf("Enter should open reader, got view %d", updated.(model).view)
	}
}
```

- [ ] **Step 2: Run → FAIL**

Run: `go test ./cmd/mail-tui/`
Expected: FAIL.

- [ ] **Step 3: Implementar model.go (Bubble Tea v1.3.10, patrón Elm)**

```bash
go get github.com/charmbracelet/bubbletea@v1.3.10 github.com/charmbracelet/bubbles@latest github.com/charmbracelet/lipgloss@latest
```
Implementar `model` con: campos `view (viewList|viewReader|viewComposer)`, `headers []mailbox.Header`, `selected int`, `lastKey rune` (para `gg`), `body string`, viewport. `Update(tea.Msg)` maneja:
- `j`/`down`→selected++; `k`/`up`→selected--; `gg`→0; `G`→len-1 (con clamp).
- `Enter` en list → carga el cuerpo (Open+Parse+RenderRich) y `view=viewReader`.
- `r`→composer (reply con threading), `s`→summarize, `a`→agent, `q`/`Esc`.
- composer: `Ctrl-E` $EDITOR, `Ctrl-S` envía con confirmación `y/n`.
`View()` devuelve `string` (Bubble Tea v1.x; en v2 sería `tea.View`). Render list con lipgloss; reader con el markdown de glamour.

- [ ] **Step 4: Run → PASS + build**

Run: `go test ./cmd/mail-tui/ && go build -o /dev/null ./cmd/mail-tui`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add cmd/mail-tui/ go.mod go.sum
git commit -m "feat(sp-4): mail-tui (Bubble Tea v1 list/reader/composer, Vim-motions, tested Update)"
```

---

## Task 11: Smoke end-to-end real + runbook + README + diagrama

**Files:**
- Create: `docs/SP-4-DEPLOY.md`
- Modify: `README.md`, `docs/architecture.md`, `docs/diagrams/architecture_icons.py`

**Interfaces:** N/A (integración + docs).

- [ ] **Step 1: Suite completa + build de ambos binarios**

Run: `go build ./... && go test ./... && go vet ./... && gofmt -l .`
Expected: todo verde; `gofmt -l` vacío.

- [ ] **Step 2: Smoke real de lectura (mail-client-read)**

Run (tras Task 0 deployado):
```bash
go run ./cmd/mail ls --mailbox test@erickaldama.com -n 5 --read-profile mail-client-read
go run ./cmd/mail read <s3Key-de-un-item-real> --read-profile mail-client-read
```
Expected: lista los items reales de SP-3; `read` muestra el cuerpo parseado. Verifica el plano de lectura end-to-end.

- [ ] **Step 3: Smoke real de envío al Mailbox Simulator (mail-sender) → cierra el lazo SP-2↔SP-3↔SP-4**

Run:
```bash
go run ./cmd/mail send --to success@simulator.amazonses.com --subject "SP-4 smoke" --body-file <(echo "hola desde el cliente") --send-profile mail-sender
# Y un envío real a test@erickaldama.com → debe aparecer en mail ls (cierra el lazo):
go run ./cmd/mail send --to test@erickaldama.com --subject "SP-4 loop" --body-file <(echo "loop e2e") --send-profile mail-sender
sleep 15 && go run ./cmd/mail ls --mailbox test@erickaldama.com -n 1 --read-profile mail-client-read
```
Expected: simulator OK (MessageId devuelto); el correo a test@ aparece en `ls` → el lazo enviar→recibir→indexar→leer cierra.

- [ ] **Step 4: Smoke AI con Ollama local**

Run:
```bash
ollama serve >/dev/null 2>&1 &   # si no corre
ollama pull qwen3:32b            # para la capacidad agent (si no está)
go run ./cmd/mail ai summarize <s3Key> --backend ollama
go run ./cmd/mail ai agent "¿cuántos correos tengo de la última semana?" --backend ollama
```
Expected: summarize con `llama3.2`; agent con `qwen3:32b` ejecuta tools read-only y responde. El cuerpo no sale del Mac.

- [ ] **Step 5: Escribir docs/SP-4-DEPLOY.md (runbook)**

Contenido: Task 0 (comandos cdk deploy + create-access-key + setup de profiles), bindings tmux (`bind e display-popup -E "mail-tui"`, prefix=C-a) y nvim (`<leader>ml/mc/ms/ma`, anclados a config real, copy-paste), rotación de access keys, manejo de sandbox SES (success@simulator), arranque de Ollama + pull de qwen3:32b, postura de privacidad (Ollama default / Claude opt-in / :cloud prohibido). Estilo: datos reales, comandos copy-paste, ~500 líneas (feedback_documentation).

- [ ] **Step 6: Actualizar README + architecture.md + diagrama (SP-4 ✅, el cliente cierra el lazo)**

README: sección SP-4 (CLI+TUI+AI doble backend, los 4 stacks + el cliente). architecture.md: añadir el cliente al diagrama Mermaid (lee DynamoDB+S3, envía SES, AI Ollama/Claude). Regenerar el PNG con el nuevo nodo cliente.

- [ ] **Step 7: Gate NDA + commit final**

Run: `grep -rIE "esagiosapp|yaan|roatech|MercadoPago|476114125529|288761749126|468227865963" --include="*.go" --include="*.md" cmd/ internal/ docs/SP-4-DEPLOY.md README.md docs/architecture.md || echo "✅ NDA clean"`
Expected: NDA clean.
```bash
git add docs/SP-4-DEPLOY.md README.md docs/architecture.md docs/diagrams/
git commit -m "docs(sp-4): runbook + README + architecture (client closes end-to-end loop)"
```

---

## Cierre

Tras Task 11: PR a develop con CI verde (Git Flow — NO merge local). `gh pr create --base develop`.
Recordar el quirk de `gh pr merge` (verificar state=MERGED, limpiar con `git -C` si falla la fase 2).
Persistencia triple-capa: checkboxes (este plan) + `docs/superpowers/EXECUTION-LOG.md` + task #18.
