# Task 6 Report: CLI `mail read --rich` flag

**SHA:** `a21d03e`
**Rama:** `worktree-mail-v0.3-richrender`
**Estado:** COMPLETO — `go build ./...` verde, `go test -count=1 ./cmd/mail/` OK, `go vet ./cmd/mail/` limpio, `gofmt -l cmd/mail/` vacío.

---

## Cambios en `cmd/mail/main.go`

### Import añadido
`golang.org/x/term` — ya estaba en `go.mod` como indirect (`v0.43.0`); no fue necesario `go get`.

### Flags nuevos en `readCmd`

```go
var rich, loadRemote bool
readCmd.Flags().BoolVar(&rich, "rich", false, "Render HTML body as rich ANSI text (sanitized; terminal-width aware)")
readCmd.Flags().BoolVar(&loadRemote, "load-remote", false, "Allow remote images when --rich (default: blocked, placeholder shown)")
```

Los flags viven en `readCmd.Flags()` (locales al subcomando, no en PersistentFlags).

### Flujo `--rich` en `RunE`

- **Sin `--rich`:** llama `message.RenderPlain(parsed)` — comportamiento pipe-friendly intacto, no se toca.
- **Con `--rich`:**
  1. `width, _, err := term.GetSize(int(os.Stdout.Fd()))` — si falla o `width <= 0` → `width = 80` (degradación no-TTY).
  2. `san, err := message.SanitizeHTML(parsed.TextHTML, loadRemote)` — errores propagados a stderr y retornados.
  3. `clean := *parsed; clean.TextHTML = san.HTML` — copia shallow del Parsed con HTML limpio.
  4. `out, err := message.RenderRich(&clean, width)` — errores propagados (no silenciados).
  5. `fmt.Print(out)`.
- Placeholders `[imagen remota bloqueada]` son insertados por `SanitizeHTML` cuando `loadRemote=false`.

---

## Output de `go run ./cmd/mail read --help`

```
Read one message by its S3 key

Usage:
  mail read <s3Key> [flags]

Flags:
  -h, --help          help for read
      --load-remote   Allow remote images when --rich (default: blocked, placeholder shown)
      --rich          Render HTML body as rich ANSI text (sanitized; terminal-width aware)

Global Flags:
      --agent-model string    Ollama model for agent/summarize/draft (default "qwen3:32b")
      --backend string        AI backend: ollama|claude (default "ollama")
      --count int             Number of messages to list (default 20)
      --json                  Output as JSON (machine-readable)
      --mailbox string        Mailbox name (DynamoDB PK prefix) (default "inbox")
      --read-profile string   AWS SSO profile for reading mail (default "mail-client-read")
      --send-profile string   AWS SSO profile for sending mail (default "mail-sender")
```

---

## Validaciones

| Check | Resultado |
|---|---|
| `go build ./...` | OK |
| `go vet ./cmd/mail/` | OK |
| `gofmt -l cmd/mail/` | OK (sin archivos mal formateados) |
| `go mod verify` | all modules verified |
| `go run ./cmd/mail read --help` | flags `--rich`/`--load-remote` presentes |
| `go test -count=1 ./cmd/mail/` | ok (0.399s) |

---

## golang.org/x/term

Ya estaba en `go.mod` como indirect (`golang.org/x/term v0.43.0`). Solo se promovió a import directo en `cmd/mail/main.go`. No se requirió `go get` ni `go mod tidy`.

---

## Concerns

- `SanitizeHTML` recibe `parsed.TextHTML` directamente. Si el mensaje no tiene parte HTML, `san.HTML` queda vacío y `RenderRich` retorna `p.TextPlain` (fallback interno en `render.go:25-27`). Correcto.
- El flag `--load-remote` sin `--rich` no hace nada (se ignora silenciosamente). Comportamiento correcto.
- No se cambió `go.mod` ni `go.sum` (x/term ya era dependencia transitiva).
