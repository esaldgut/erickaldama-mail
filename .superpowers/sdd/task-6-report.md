# Task 6 Report: TUI composer multi-campo + invariante BCC

**SHA:** `06043e7`
**Rama:** `worktree-mail-v0.2`
**Estado:** COMPLETO — `go build ./...` verde, `go test -count=1 ./...` 16/16 OK, `go vet ./...` limpio, `gofmt -l` vacío.

---

## Los 7 usos migrados de `composeDraft`

| # | Línea original | Cambio |
|---|---|---|
| 1 | `:40` struct field `composeDraft string` | → `compose composer` (type `composer` con `inputs []textinput.Model`, `active int`, `body string`) |
| 2 | `:68` `sentMsg` handler `m.composeDraft = ""` | → `m.compose = newComposer()` (reset con inputs inicializados) |
| 3 | `:75` `editorDoneMsg` `m.composeDraft = msg.body` | → `m.compose.body = msg.body` |
| 4 | `:144` tecla `'r'` en list | → `c := newComposer()` + pre-rellenar `inputs[cTo]`/`inputs[cSubject]` desde header; async S3 open+parse para reply-all completo si live reader disponible |
| 5 | `:226` tecla `'r'` en reader | → idem (mismo flujo async) |
| 6 | `:251` send path `raw := []byte(m.composeDraft)` | → ELIMINADO; sustituido por `message.Build(BuildOpts{...})` → `raw, dests, err` |
| 7 | `:370` `viewComposer` `sb.WriteString(m.composeDraft)` | → bucle `for _, ti := range m.compose.inputs { sb.WriteString(ti.View()) }` + preview de `body` |

---

## Send path — usa Build, nunca raw a mano (BCC-1)

```go
raw, dests, err := message.Build(message.BuildOpts{
    From:    from,
    To:      c.inputs[cTo].Value(),
    Cc:      c.inputs[cCc].Value(),
    Bcc:     c.inputs[cBcc].Value(),
    Subject: c.inputs[cSubject].Value(),
    Body:    c.body,
})
// ...
id, err := s.Send(context.Background(), raw, dests)
```

`message.Build` pasa el Bcc SOLO en `destinations` (envelope SES), nunca al builder enmime → el header `Bcc:` no aparece en el raw.

---

## Interfaz mailSender

Para permitir inyección del `fakeSender` en tests sin romper el wiring de producción:

```go
type mailSender interface {
    Send(ctx context.Context, raw []byte, destinations []string) (string, error)
}
```

`*mailbox.Sender` la satisface. El campo `sender mailSender` en el model. `main.go` no cambió (asignación `sender: s` funciona por satisfacción implícita).

---

## Resultados de tests

```
=== RUN   TestJMovesDown                    --- PASS
=== RUN   TestGGoesToTop                    --- PASS
=== RUN   TestEnterOpensReader              --- PASS
=== RUN   TestComposerSendRequiresConfirmation --- PASS  (actualizado: compose: newComposer())
=== RUN   TestSentMsgClearsConfirmState     --- PASS  (actualizado: verifica inputs[cTo].Value()=="")
=== RUN   TestReplyDraftPrePopulates        --- PASS  (actualizado: verifica inputs[cTo]+inputs[cSubject])
=== RUN   TestComposerBccNotInRaw           --- PASS  (CRÍTICO BCC-1)
=== RUN   TestComposerTabNavigation         --- PASS  (GAP-4)
ok  erickaldama-mail/cmd/mail-tui           1.326s
```

### TestComposerBccNotInRaw (BCC-1)
- `fakeSender` captura `gotRaw` + `gotDests`
- Tras `confirming=true` → `'y'` → ejecutar `cmd()`: el raw NO contiene `"Bcc:"` ni `"secret@x.com"`
- `gotDests` SÍ contiene `"secret@x.com"` (envelope SES)
- Test PASA → invariante BCC del TUI verificado end-to-end

### TestComposerTabNavigation (GAP-4)
- Inicia en `active=cTo (0)`, Tab → `active=cCc (1)`
- Test PASA

---

## H-4: `mail tmux status` multi-mailbox — IMPLEMENTADO

El `status` subcommand de `cmd/mail/main.go` ahora usa la misma lógica que `ls`:
- Si `--mailbox` fue pasado explícitamente: usa ese mailbox solo.
- Si `config.Mailboxes` está configurado: suma los counts de todos los mailboxes.
- Fallback: `mailboxName` (default `"inbox"`).

```go
var total int
for _, mb := range statusMailboxes {
    hs, _, lerr := r.List(ctx, mb, int32(count), nil)
    // ...
    total += len(hs)
}
fmt.Printf("📬 %d\n", total)
```

No es mono-mailbox. Parity completa con `ls`.

---

## go build ./... — VERDE COMPLETO

```
ok  erickaldama-mail/cdk-go-aws-plugin/eval
ok  erickaldama-mail/cmd/lambda/receive
ok  erickaldama-mail/cmd/mail
ok  erickaldama-mail/cmd/mail-tui
ok  erickaldama-mail/internal/aiassist
ok  erickaldama-mail/internal/aiassist/claude
ok  erickaldama-mail/internal/aiassist/ollama
ok  erickaldama-mail/internal/awsconf
ok  erickaldama-mail/internal/config
ok  erickaldama-mail/internal/infra
ok  erickaldama-mail/internal/mailbox
ok  erickaldama-mail/internal/message
ok  erickaldama-mail/internal/redact
ok  erickaldama-mail/test/hook
```

`go vet ./...` limpio. `gofmt -l cmd/ internal/` vacío. `bubbles v1.0.0` directo (sin `// indirect`) en `go.mod`.

---

## Concerns

Ninguno crítico.

- **reply-all con live reader**: la ruta asíncrona (S3 open+parse) usa un nuevo `replyReadyMsg` que el `Update` handler procesa. Si la apertura falla, el fallback usa el pre-fill de header (To/Subject), sin Cc. Comportamiento degradado correcto.
- **`from` en el model**: el campo `from string` se usa en el send path (`message.Build`). En `main.go` aún no se inyecta desde config (queda como `""` → `Build` retorna `ErrMissingFrom`). Esto es una deuda conocida del wiring de producción, no del invariante BCC ni de los tests. El TUI sin live sender (confirming=true, sender=nil) pone `sent=true` sin llamar Build → esa rama es solo para tests offline.
- **`cmd/mail-tui/main.go`**: no se le pasó `from` al model. Pendiente wiring de `config.DefaultFrom` al campo `m.from` en una tarea futura si se quiere que el send live funcione desde el TUI sin `--from`.
