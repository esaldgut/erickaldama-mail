## Task 5 Report: CLI integration — ls multi-mailbox + --cc/--bcc + 4 call-sites + fallbacks

**SHA:** f6a4e75  
**Branch:** worktree-mail-v0.2  
**File modified:** `cmd/mail/main.go`

---

### 4 call-sites connected

| Location | Before | After |
|---|---|---|
| send Build (~line 190) | `raw, _, err := message.Build(...)` without Cc/Bcc | `raw, dests, err := message.Build({..., Cc: sendCc, Bcc: sendBcc})` |
| send Send (~line 213) | `s.Send(ctx, raw)` — missing dests arg | `s.Send(ctx, raw, dests)` |
| reply Build (~line 283) | `raw, _, err := message.Build(...)` without Cc/Bcc | `raw, dests, err := message.Build({..., Cc: replyCc, Bcc: replyBcc})` |
| reply Send (~line 303) | `s.Send(ctx, raw)` — missing dests arg | `s.Send(ctx, raw, dests)` |

---

### Sort by SK (not Date)

```go
slices.SortFunc(all, func(a, b mailbox.Header) int { return strings.Compare(b.SK, a.SK) })
```

SK is ISO8601 so lexicographic descending sort is correct. Date is RFC1123Z which sorts incorrectly.

---

### replyAllCc guard (REPLY-1)

```go
func replyAllCc(parsedTo, parsedCc, self string) string {
    seen := map[string]bool{}
    if self != "" {  // GUARD: if self="" do NOT seed seen[""] — would filter nothing, own addr enters Cc
        seen[strings.ToLower(self)] = true
    }
    ...
}
```

Guard verified: `self=""` skips seeding to avoid the empty-string filter-nothing bug.

---

### 3 profile fallbacks (GAP-1 / spec §3.5)

Applied in both `sendCmd.RunE` and `replyCmd.RunE`:

```go
if !cmd.Flags().Changed("from")         && hasCfg && cfg.DefaultFrom != "" { sendFrom    = cfg.DefaultFrom }
if !cmd.Flags().Changed("read-profile") && hasCfg && cfg.ReadProfile != "" { readProfile = cfg.ReadProfile }
if !cmd.Flags().Changed("send-profile") && hasCfg && cfg.SendProfile != "" { sendProfile = cfg.SendProfile }
```

`--from` flag removed from `MarkFlagRequired` on sendCmd since it can now come from config.

---

### Smoke output

```
# go run ./cmd/mail ls --mailbox erick@erickaldama.com
Thu, 25 Jun 2026 01:23:35 -0600  Erick Aldama <esaldgut@gmail.com>  Fwd: ¡Recibiste una transferencia!

# go run ./cmd/mail ls   (no config, no --mailbox)
no hay config; crea ~/.config/erickaldama-mail/config.toml con tus mailboxes, o usa --mailbox <dirección>
Error: no mailbox specified and no config
exit status 1
```

---

### Validation results

| Check | Result |
|---|---|
| `go build ./cmd/mail/` | GREEN |
| `go vet ./cmd/mail/` | PASS (no issues) |
| `gofmt -l cmd/mail/` | PASS (empty output) |
| `go test -count=1 ./cmd/mail/` | PASS (0.569s) |
| Smoke: ls --mailbox | 1 real message listed |
| Smoke: ls no config | error + exit 1 |
| `cmd/mail-tui` | STILL BROKEN (expected — Task 6) |

---

### cmd/mail-tui status

`cmd/mail-tui/model.go:254` still calls `s.Send(ctx, raw)` with 2 args; Task 6 will fix it.  
`go build ./...` remains red only for `cmd/mail-tui`. All other packages including `cmd/mail` build clean.

---

## Fix H-1/H-2/H-3

**SHA:** 0e3a17a  
**Branch:** worktree-mail-v0.2  
**Files modified:** `cmd/mail/main.go`, `internal/message/build.go`, `internal/message/build_test.go`

### H-1 — Onboarding error before AWS load (cmd/mail/main.go lsCmd.RunE)

Moved `wire.Reader(ctx, readProfile)` call to AFTER config/mailbox resolution. Order is now:
1. `config.Load()` → resolve mailboxes
2. If no mailbox is available → print onboarding message + return error (user never hits AWS)
3. Apply `readProfile` fallback from cfg (previously only done in send/reply RunE)
4. `wire.Reader(ctx, readProfile)` — only reached when config is valid

**Code proof (lines 124–142):** `config.Load()` is now the first call; `wire.Reader` is called only after the mailboxes block succeeds. The onboarding `return fmt.Errorf("no mailbox specified and no config")` at line 132 short-circuits before any AWS call.

### H-3 — Dead `readProfile` fallback removed from sendCmd.RunE (cmd/mail/main.go)

Deleted the line `readProfile = cfg.ReadProfile` from `sendCmd.RunE`. `send` only calls `wire.Sender`, never `wire.Reader`, so the fallback was unreachable dead code. The same fallback in `replyCmd.RunE` and the new one in `lsCmd.RunE` (added as part of H-1) were left intact — they both use `wire.Reader`.

### H-2 — Typed ErrMissingFrom sentinel (internal/message/build.go + build_test.go)

Added `var ErrMissingFrom = errors.New("from address not set")` at package level in `build.go`. At the top of `Build()`, added guard:

```go
if opt.From == "" {
    return nil, nil, ErrMissingFrom
}
```

This replaces the previous behavior where enmime would produce an untyped string error for an empty From, violating avoid-string-match-error-silencing. Callers can now use `errors.Is(err, message.ErrMissingFrom)` for structured handling.

### Test output

```
=== RUN   TestBuildCcInHeaderBccNot
--- PASS: TestBuildCcInHeaderBccNot (0.00s)
=== RUN   TestBuildRequiresFrom
--- PASS: TestBuildRequiresFrom (0.00s)
ok  	erickaldama-mail/internal/message	1.129s
```

Full run: `go test -count=1 -v ./cmd/mail/ ./internal/message/` — **16 tests, all PASS**.

- BCC invariant `TestBuildCcInHeaderBccNot`: PASS (not broken by H-2)
- New sentinel test `TestBuildRequiresFrom`: PASS (`errors.Is(err, ErrMissingFrom)` works)

### Onboarding error ordering (smoke by code inspection)

`lsCmd.RunE` now returns `fmt.Errorf("no mailbox specified and no config")` at line 132, **before** `wire.Reader` is called at line 138. A user with no config.toml and no `--mailbox` flag will see the onboarding message and never trigger an AWS credential error.

### Validation

| Check | Result |
|---|---|
| `go build ./cmd/mail/ ./internal/...` | GREEN |
| `go vet ./cmd/mail/ ./internal/message/` | PASS (no output) |
| `gofmt -l cmd/mail/ internal/message/` | PASS (no output) |
| `go test -count=1 ./cmd/mail/ ./internal/message/` | PASS — 16/16 |
| `TestBuildCcInHeaderBccNot` (BCC invariant) | PASS |
| `TestBuildRequiresFrom` (new ErrMissingFrom) | PASS |
| Onboarding error before AWS | CONFIRMED by code order |
| `cmd/mail-tui` | STILL BROKEN (expected — Task 6) |
