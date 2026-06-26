## Task 5 Report: TUI Integration — termWidth/rawHTML/WindowSizeMsg + keys i/R

**SHA:** 1666d64
**Branch:** worktree-mail-v0.3-richrender
**Files modified:** `cmd/mail-tui/model.go`, `cmd/mail-tui/model_test.go`

---

### P-5 (D4 seguridad) — SanitizeHTML en body load: CONFIRMED

`bodyLoadedMsg` fue cambiado de `{body string}` a `{parsed *message.Parsed}`. El handler en `Update()` ahora:

```go
case bodyLoadedMsg:
    m.rawHTML = msg.parsed.TextHTML          // HTML original sin sanitizar
    m.loadRemote = false
    san, _ := message.SanitizeHTML(m.rawHTML, false)
    clean := *msg.parsed
    clean.TextHTML = san.HTML
    m.currentParsed = &clean
    body, _ := message.RenderRich(&clean, m.termWidth)
    m.body = body
    m.imageBlobs = nil
    m.showImages = false
    m.view = viewReader
    m.scrollOffset = 0
    return m, nil
```

El HTML nunca llega a RenderRich sin pasar por SanitizeHTML primero.

---

### P-6 (no congelar el TUI) — imágenes async via tea.Cmd: CONFIRMED

La tecla 'i' en `handleReaderKey` retorna un `func() tea.Msg` goroutine:

```go
case 'i':
    m.showImages = true
    // ...
    return m, func() tea.Msg {
        var blobs []string
        for _, im := range imgs {
            blobs = append(blobs, render.RenderImage(context.Background(), im.Data, w, h))
        }
        return imagesRenderedMsg{blobs: blobs}
    }
```

`render.RenderImage` (que llama chafa, hasta 5s) corre en goroutine separado. El event loop nunca se bloquea.

---

### P-7 — guard por currentParsed != nil: CONFIRMED

```go
case tea.WindowSizeMsg:
    m.termWidth = msg.Width
    if m.view == viewReader && m.currentParsed != nil {
        body, _ := message.RenderRich(m.currentParsed, m.termWidth)
        m.body = body
    }
    return m, nil
```

---

### Campos añadidos al struct model

| Campo | Tipo | Propósito |
|---|---|---|
| `showImages` | `bool` | true cuando el usuario presiona 'i' |
| `loadRemote` | `bool` | toggle de imágenes remotas ('R'), default false |
| `currentParsed` | `*message.Parsed` | parsed actual, para resize y render de imágenes |
| `rawHTML` | `string` | HTML original sin sanitizar (para re-sanitizar en 'R') |
| `imageBlobs` | `[]string` | blobs ANSI de render.RenderImage, uno por InlineImage |

### Nuevos tipos de mensaje

- `imagesRenderedMsg struct{ blobs []string }` — enviado por el goroutine de 'i'
- `bodyLoadedMsg` cambiado de `{body string}` a `{parsed *message.Parsed}`

### Call-sites afectados por el cambio de bodyLoadedMsg

Solo hay un call-site que construye `bodyLoadedMsg`: el async loader en `handleListKey` (línea ~314).
Fue actualizado de `return bodyLoadedMsg{body: body}` a `return bodyLoadedMsg{parsed: parsed}`.
La conversión RenderRich ahora ocurre en el Update handler (después de sanitizar), no en el goroutine.

---

### Output de los 2 nuevos tests

```
=== RUN   TestModelCapturesWindowSize
--- PASS: TestModelCapturesWindowSize (0.00s)
=== RUN   TestReaderKeyIRendersImages
--- PASS: TestReaderKeyIRendersImages (0.00s)
```

Full run (10/10):

```
=== RUN   TestJMovesDown
--- PASS: TestJMovesDown (0.00s)
=== RUN   TestGGoesToTop
--- PASS: TestGGoesToTop (0.00s)
=== RUN   TestEnterOpensReader
--- PASS: TestEnterOpensReader (0.00s)
=== RUN   TestComposerSendRequiresConfirmation
--- PASS: TestComposerSendRequiresConfirmation (0.00s)
=== RUN   TestSentMsgClearsConfirmState
--- PASS: TestSentMsgClearsConfirmState (0.00s)
=== RUN   TestReplyDraftPrePopulates
--- PASS: TestReplyDraftPrePopulates (0.00s)
=== RUN   TestComposerBccNotInRaw
--- PASS: TestComposerBccNotInRaw (0.00s)
=== RUN   TestComposerTabNavigation
--- PASS: TestComposerTabNavigation (0.00s)
=== RUN   TestModelCapturesWindowSize
--- PASS: TestModelCapturesWindowSize (0.00s)
=== RUN   TestReaderKeyIRendersImages
--- PASS: TestReaderKeyIRendersImages (0.00s)
PASS
ok  	erickaldama-mail/cmd/mail-tui	0.677s
```

---

### Validation

| Check | Result |
|---|---|
| `go build ./...` | GREEN |
| `go vet ./cmd/mail-tui/` | PASS (no output) |
| `gofmt -l cmd/mail-tui/` | PASS (no output) |
| `go test -count=1 ./cmd/mail-tui/` | PASS — 10/10 |
| P-5: SanitizeHTML en bodyLoadedMsg | CONFIRMED |
| P-6: key 'i' retorna tea.Cmd async | CONFIRMED |
| P-7: guard currentParsed != nil | CONFIRMED |

---

### Concerns

Ninguno. El TUI interactivo (gate humano) no fue abierto — está fuera del scope de esta tarea.
