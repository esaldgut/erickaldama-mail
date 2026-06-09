# SP-0 — Gobernanza CDK-Go: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the governance layer that makes "all AWS provisioning via AWS CDK Go" imperative — a project-scoped PreToolUse hook (friction/UX), a distributable Claude Code plugin with two recipe-skills + a verifier agent + an eval harness, and a pure/scoped IAM allowlist principal (the real boundary) with a bootstrap acceptance gate.

**Architecture:** Three independent artifacts built in testability order: (1) the bash hook `cdk-go-guard.sh` — pure bash, tested 100% offline with echo-pipe fixtures; (2) the plugin `cdk-go-aws-plugin/` — validated with `claude plugin validate --strict` + a Go eval harness, no AWS; (3) the IAM allowlist policy + bootstrap gate — the only AWS-touching piece, verified read-only via `iam simulate-principal-policy`. The IAM allowlist is THE security boundary; the hook is friction that self-denies on error; mutations run out-of-band (human executes `cdk deploy`).

**Tech Stack:** bash + `jq` (hook), Claude Code plugin spec (plugin.json/SKILL.md/agents/.mcp.json), Go 1.26 + `go test` (eval harness + hook test runner), AWS IAM JSON policy + `aws iam simulate-principal-policy` (boundary), `uv`/`uvx` (MCP runtime prereq).

**Source spec:** `docs/superpowers/specs/2026-06-07-sp0-governance-cdk-go-design.md` (commit 6717e72). Audit findings: `~/.claude/plans/email-project-research/08-sp0-audit-findings.md`.

**Account/region:** ErickSA `367707589526`, `us-east-1`. Apply `aws-cli-pre-flight-canonical` before any `aws` command (verify `get-caller-identity` Account == 367707589526 via `--profile AdministratorAccess-367707589526`).

---

## File Structure

```
erickaldama-mail/
├── .claude/
│   ├── hooks/
│   │   └── cdk-go-guard.sh            # NEW — the PreToolUse hook (friction/UX, scoped, self-deny)
│   └── settings.json                  # NEW/MODIFY — wires the hook (Bash + mcp__aws-api__.* matchers)
├── cdk-go-aws-plugin/                 # NEW — the distributable Claude Code plugin
│   ├── .claude-plugin/
│   │   └── plugin.json                # manifest (name, version, author obj, mcpServers, defaultEnabled:false)
│   ├── skills/
│   │   ├── cdk-go-recipe/SKILL.md     # recipe: CDK-Go + verify-before-act + cache mechanism
│   │   └── ses-domain-recipe/SKILL.md # recipe: SES 8 steps + runbook + 6 traps
│   ├── agents/
│   │   └── cdk-verifier.md            # the doc-verifier subagent (Knowledge MCP tools only)
│   ├── .mcp.json                      # aws-knowledge (http) + aws-api (uvx, server-key "aws-api")
│   └── eval/
│       ├── golden/                    # golden prompts (one .txt per case)
│       │   ├── ses-identity.txt
│       │   └── s3-bucket.txt
│       ├── assertions.go              # property assertions (positive/negative/6-trap, whitespace-resilient)
│       ├── run_eval.go                # runner: dry-run skill via claude -p, capture, assert, Pass@k
│       └── baseline.json              # Pass@1/@3 baseline (versioned)
├── test/hook/
│   ├── guard_test.go                  # Go test harness driving cdk-go-guard.sh via echo-pipe fixtures
│   └── fixtures/                      # JSON stdin fixtures (allow/deny cases)
├── iam/
│   ├── readonly-policy.json           # CANONICAL allowlist-pure scoped policy (the boundary)
│   ├── bootstrap-gate.sh              # acceptance gate: diff policy + run deny/allow probes
│   └── simulate-matrix.sh             # iam simulate-principal-policy falsifiability check
├── docs/
│   └── BOOTSTRAP.md                   # the t=0 manual procedure + ownership boundaries (SP-1/SP-3)
└── .gitignore                         # already has docs/cdk-verified.json (from spec commit)
```

**Responsibility split:** hook = one bash file + its wiring + its Go test harness (offline). plugin = self-contained dir, no AWS, validated + eval'd. iam = the canonical policy JSON + two verification scripts + the bootstrap doc. Each phase produces something testable on its own.

---

## PHASE 1 — The Hook (pure bash, 100% offline-testable)

The hook is the most deterministic, self-contained artifact. We TDD it with a Go test harness that pipes JSON fixtures to the script and asserts the decision. Build the test harness first, then the script, fixture by fixture.

### Task 1: Scaffold the hook test harness (Go driver + first failing fixture)

**Files:**
- Create: `test/hook/guard_test.go`
- Create: `test/hook/fixtures/deny_aws_write.json`
- Create: `.claude/hooks/cdk-go-guard.sh` (empty stub, non-executable logic yet)

- [x] **Step 1: Write the failing test (Go harness that runs the hook script with a fixture and asserts deny)**

`test/hook/guard_test.go`:
```go
package hook_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scriptPath resolves the hook relative to the repo root (two dirs up from test/hook).
func scriptPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", ".claude", "hooks", "cdk-go-guard.sh")
}

// runHook pipes fixtureJSON to the hook on stdin and returns combined stdout.
// MAIL_ROOT is injected so the scope check is deterministic in CI.
func runHook(t *testing.T, fixtureJSON, mailRoot string) string {
	t.Helper()
	cmd := exec.Command("bash", scriptPath(t))
	cmd.Stdin = strings.NewReader(fixtureJSON)
	cmd.Env = append(os.Environ(), "MAIL_ROOT="+mailRoot)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	_ = cmd.Run() // the hook always exits 0; decision is in stdout JSON
	return out.String()
}

func TestDeniesAwsWrite(t *testing.T) {
	fixture, err := os.ReadFile("fixtures/deny_aws_write.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got := runHook(t, string(fixture), "/Users/esaldgut/dev/src/go/src/erickaldama-mail")
	if !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("expected deny, got: %s", got)
	}
}
```

`test/hook/fixtures/deny_aws_write.json`:
```json
{"tool_name":"Bash","tool_input":{"command":"aws s3 mb s3://x"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}
```

`.claude/hooks/cdk-go-guard.sh` (stub — emits allow so the test FAILS for the right reason):
```bash
#!/usr/bin/env bash
echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}'
```

- [x] **Step 2: Run the test, verify it FAILS**

Run: `cd test/hook && go test -run TestDeniesAwsWrite -v`
Expected: FAIL — got `"permissionDecision":"allow"`, expected deny. (Confirms the harness wires stdin→script→stdout and the assertion is real.)

- [x] **Step 3: Make the script executable + add the shebang/trap skeleton (self-deny on error, SEC-C1)**

Replace `.claude/hooks/cdk-go-guard.sh` with the fail-safe skeleton:
```bash
#!/usr/bin/env bash
# cdk-go-guard.sh — PreToolUse hook (FRICTION/UX, scoped-to-mail-project, self-deny on error).
# NOT a security boundary (docs: hooks fail open). The boundary is IAM (see iam/readonly-policy.json).
# Default action is DENY; any internal error self-denies. Reads JSON on stdin.

emit_deny() { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%s}}\n' "$1"; exit 0; }
emit_allow() { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}\n'; exit 0; }

# SEC-C1: any error path → deny, never trust the harness to fail closed.
trap 'emit_deny "\"hook error, fail-safe deny\""' ERR
set -uo pipefail

INPUT="$(cat)" || emit_deny '"no stdin, fail-safe deny"'
command -v jq >/dev/null 2>&1 || emit_deny '"jq missing, fail-safe deny"'

CMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')"
CWD="$(printf '%s' "$INPUT" | jq -r '.cwd // empty')"
PMODE="$(printf '%s' "$INPUT" | jq -r '.permission_mode // "default"')"

# (logic added in following tasks)
emit_deny '"unimplemented, fail-safe deny"'
```

- [x] **Step 4: Run the test, verify it PASSES**

Run: `cd test/hook && go test -run TestDeniesAwsWrite -v`
Expected: PASS (the stub now denies by default).

- [x] **Step 5: Commit**

```bash
git add test/hook/guard_test.go test/hook/fixtures/deny_aws_write.json .claude/hooks/cdk-go-guard.sh
git commit -m "feat(sp-0): hook test harness + fail-safe-deny skeleton"
```

### Task 2: permission_mode gate (SEC-C2) + scope check (SEC-I4)

**Files:**
- Modify: `.claude/hooks/cdk-go-guard.sh`
- Test: `test/hook/guard_test.go`
- Create: `test/hook/fixtures/allow_out_of_scope.json`, `test/hook/fixtures/deny_bypass_mode.json`

- [x] **Step 1: Write the failing tests**

Append to `test/hook/guard_test.go`:
```go
func TestAllowsOutOfScopeProject(t *testing.T) {
	// A mutating command but cwd is NOT under the mail project → hook is a no-op (allow).
	fixture, _ := os.ReadFile("fixtures/allow_out_of_scope.json")
	got := runHook(t, string(fixture), "/Users/esaldgut/dev/src/go/src/erickaldama-mail")
	if !strings.Contains(got, `"permissionDecision":"allow"`) {
		t.Fatalf("expected allow (out of scope), got: %s", got)
	}
}

func TestDeniesBypassPermissionMode(t *testing.T) {
	// In-scope read that would normally allow, but permission_mode=bypassPermissions → deny.
	fixture, _ := os.ReadFile("fixtures/deny_bypass_mode.json")
	got := runHook(t, string(fixture), "/Users/esaldgut/dev/src/go/src/erickaldama-mail")
	if !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("expected deny (bypass mode), got: %s", got)
	}
}
```

`test/hook/fixtures/allow_out_of_scope.json`:
```json
{"tool_name":"Bash","tool_input":{"command":"aws s3 mb s3://x"},"cwd":"/Users/example/dev/src/swift/sample-ios-app","permission_mode":"default"}
```

`test/hook/fixtures/deny_bypass_mode.json`:
```json
{"tool_name":"Bash","tool_input":{"command":"aws s3 ls"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"bypassPermissions"}
```

- [x] **Step 2: Run, verify FAIL**

Run: `cd test/hook && go test -run 'TestAllowsOutOfScopeProject|TestDeniesBypassPermissionMode' -v`
Expected: both FAIL — the stub still denies everything (out-of-scope wrongly denied; bypass coincidentally "passes" by denying but for the wrong reason — keep both to lock behavior).

- [x] **Step 3: Implement the permission_mode gate + scope check**

In `cdk-go-guard.sh`, replace the final `emit_deny '"unimplemented..."'` line with:
```bash
# SEC-C2: bypass mode must not evaporate the deny.
if [[ "$PMODE" != "default" && "$PMODE" != "plan" ]]; then
  emit_deny '"permission_mode not default/plan; governed command denied"'
fi

# SEC-I4: scope to the mail project. MAIL_ROOT overridable for tests; default to the canonical path.
MAIL_ROOT="${MAIL_ROOT:-$HOME/dev/src/go/src/erickaldama-mail}"
real_cwd="$(cd "$CWD" 2>/dev/null && pwd -P || printf '%s' "$CWD")"
real_root="$(cd "$MAIL_ROOT" 2>/dev/null && pwd -P || printf '%s' "$MAIL_ROOT")"
# segment-boundary match (not raw prefix): cwd == root OR cwd starts with root + "/"
if [[ "$real_cwd" != "$real_root" && "$real_cwd" != "$real_root"/* ]]; then
  emit_allow  # out of scope → no-op
fi

# (command-inspection logic added in Task 3)
emit_deny '"unimplemented, fail-safe deny"'
```

- [x] **Step 4: Run, verify PASS**

Run: `cd test/hook && go test -run 'TestAllowsOutOfScopeProject|TestDeniesBypassPermissionMode|TestDeniesAwsWrite' -v`
Expected: all PASS. (Out-of-scope allows; bypass denies; the original aws-write still denies via the unimplemented fall-through — correct for now.)

- [x] **Step 5: Commit**

```bash
git add .claude/hooks/cdk-go-guard.sh test/hook/guard_test.go test/hook/fixtures/allow_out_of_scope.json test/hook/fixtures/deny_bypass_mode.json
git commit -m "feat(sp-0): hook permission_mode gate + project scope check"
```

### Task 3: Metacharacter deny (SEC-C3/I2) + command allowlist + aws/cdk refinement

**Files:**
- Modify: `.claude/hooks/cdk-go-guard.sh`
- Test: `test/hook/guard_test.go`
- Create fixtures: `deny_metachar.json`, `deny_go_test.json`, `allow_aws_read.json`, `allow_cdk_go_synth.json`, `deny_cdk_non_go.json`, `deny_aws_assume_role.json`
- Create: `test/hook/fixtures/cdkjson_go/cdk.json`, `test/hook/fixtures/cdkjson_ts/cdk.json`

- [x] **Step 1: Write the failing tests (table-driven, covers the adversarial cases the audit found)**

Append to `test/hook/guard_test.go`:
```go
func TestDecisionTable(t *testing.T) {
	root := "/Users/esaldgut/dev/src/go/src/erickaldama-mail"
	cases := []struct {
		name    string
		fixture string
		want    string // "allow" or "deny"
	}{
		{"metachar chaining", "fixtures/deny_metachar.json", "deny"},      // ls && aws s3 mb
		{"go test is an engine", "fixtures/deny_go_test.json", "deny"},    // go test ./... can call SDK
		{"aws read allowed", "fixtures/allow_aws_read.json", "allow"},     // aws s3 ls
		{"aws assume-role denied", "fixtures/deny_aws_assume_role.json", "deny"}, // sts assume-role
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx, err := os.ReadFile(c.fixture)
			if err != nil {
				t.Fatalf("read %s: %v", c.fixture, err)
			}
			got := runHook(t, string(fx), root)
			want := `"permissionDecision":"` + c.want + `"`
			if !strings.Contains(got, want) {
				t.Fatalf("%s: want %s, got: %s", c.name, c.want, got)
			}
		})
	}
}

// cdk decisions need a real cdk.json in cwd; use the fixture dirs as cwd.
func TestCdkGoVsNonGo(t *testing.T) {
	wd, _ := os.Getwd()
	goDir := filepath.Join(wd, "fixtures", "cdkjson_go")
	tsDir := filepath.Join(wd, "fixtures", "cdkjson_ts")

	mk := func(cwd, cmd string) string {
		return `{"tool_name":"Bash","tool_input":{"command":"` + cmd + `"},"cwd":"` + cwd + `","permission_mode":"default"}`
	}
	// cdk synth on a Go project → allow. Use the fixture dir AS its own MAIL_ROOT so scope passes.
	if got := runHook(t, mk(goDir, "cdk synth"), goDir); !strings.Contains(got, `"permissionDecision":"allow"`) {
		t.Fatalf("cdk synth (Go) should allow, got: %s", got)
	}
	// cdk synth on a TS project → deny.
	if got := runHook(t, mk(tsDir, "cdk synth"), tsDir); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("cdk synth (TS) should deny, got: %s", got)
	}
	// cdk deploy even on Go → deny (mutation, out-of-band).
	if got := runHook(t, mk(goDir, "cdk deploy"), goDir); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("cdk deploy should deny, got: %s", got)
	}
}
```

Fixtures (each one line):
- `deny_metachar.json`: `{"tool_name":"Bash","tool_input":{"command":"ls && aws s3 mb s3://x"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}`
- `deny_go_test.json`: `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}`
- `allow_aws_read.json`: `{"tool_name":"Bash","tool_input":{"command":"aws s3 ls"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}`
- `deny_aws_assume_role.json`: `{"tool_name":"Bash","tool_input":{"command":"aws sts assume-role --role-arn arn:aws:iam::1:role/x --role-session-name s"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}`

`test/hook/fixtures/cdkjson_go/cdk.json`: `{"app":"go mod download && go run ."}`
`test/hook/fixtures/cdkjson_ts/cdk.json`: `{"app":"npx ts-node bin/app.ts"}`

- [x] **Step 2: Run, verify FAIL**

Run: `cd test/hook && go test -run 'TestDecisionTable|TestCdkGoVsNonGo' -v`
Expected: FAIL (the unimplemented fall-through denies everything → the allow cases fail).

- [x] **Step 3: Implement the command-inspection logic**

In `cdk-go-guard.sh`, replace the final `emit_deny '"unimplemented..."'` with:
```bash
# SEC-C3/I2: deny compound/substitution commands rather than parsing shell grammar.
# Word-bounded check, on the raw command. (eval is handled by the allowlist below, not here,
# to avoid false-positives on substrings like --query 'retrieval'.)
if printf '%s' "$CMD" | grep -Eq '(\&\&|\|\||;|\||\$\(|`|>|<|&[^&])' || printf '%s' "$CMD" | grep -q $'\n'; then
  emit_deny '"compound command (metacharacters); hand it to the human"'
fi

# Strip leading VAR=val assignments, then take the first token.
STRIPPED="$(printf '%s' "$CMD" | sed -E 's/^([[:space:]]*[A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)+//')"
FIRST="$(printf '%s' "$STRIPPED" | awk '{print $1}')"

case "$FIRST" in
  aws)
    SUB="$(printf '%s' "$STRIPPED" | awk '{print $3}')"   # aws <service> <subcommand>
    SVCSUB="$(printf '%s' "$STRIPPED" | awk '{print $2" "$3}')"
    case "$SVCSUB" in
      "sts get-caller-identity") emit_allow ;;
    esac
    case "$SUB" in
      describe*|list*|get*|ls|help) emit_allow ;;
      *) emit_deny '"AWS mutation runs out-of-band (human); not via the agent"' ;;
    esac
    ;;
  cdk)
    CJSON="$CWD/cdk.json"
    [[ -f "$CJSON" ]] || emit_deny '"cdk.json missing; cannot confirm CDK-Go"'
    APP="$(jq -r '.app // empty' "$CJSON" 2>/dev/null)" || emit_deny '"cdk.json malformed; deny"'
    if ! printf '%s' "$APP" | grep -Eq 'go (run|mod)'; then
      emit_deny '"CDK must be Go (cdk.json app is not go run/go mod)"'
    fi
    SUB="$(printf '%s' "$STRIPPED" | awk '{print $2}')"
    case "$SUB" in
      diff|synth|ls) emit_allow ;;
      *) emit_deny '"cdk mutation (deploy/destroy) runs out-of-band (human)"' ;;
    esac
    ;;
  ls|cat|echo|grep|head|tail|jq|pwd|which|mkdir) emit_allow ;;
  *) emit_deny '"command not in narrow allowlist (go/git/find/env/scripts are execution engines); hand it to the human"' ;;
esac
```

- [x] **Step 4: Run the full hook suite, verify PASS**

Run: `cd test/hook && go test ./... -v`
Expected: all PASS — metachar deny, go-test deny, aws-read allow, assume-role deny, cdk-Go synth allow, cdk-TS deny, cdk deploy deny, plus Task 1/2 cases.

- [x] **Step 5: Commit**

```bash
git add .claude/hooks/cdk-go-guard.sh test/hook/
git commit -m "feat(sp-0): hook metachar-deny + allowlist + aws-read/cdk-Go refinement"
```

### Task 4: Wire the hook in settings.json (Bash + MCP matchers) + audit log (E1/N1)

**Files:**
- Create: `.claude/settings.json`
- Modify: `.claude/hooks/cdk-go-guard.sh` (append the audit-log line)
- Test: `test/hook/guard_test.go` (audit-log assertion)
- Create: `test/hook/fixtures/deny_mcp_write.json`

- [x] **Step 1: Write the failing test (audit log written on every decision, sanitized)**

Append to `test/hook/guard_test.go`:
```go
func TestWritesAuditLog(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "decision-log.json")
	cmd := exec.Command("bash", scriptPath(t))
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"aws s3 mb s3://x"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}`)
	cmd.Env = append(os.Environ(),
		"MAIL_ROOT=/Users/esaldgut/dev/src/go/src/erickaldama-mail",
		"DECISION_LOG="+logPath,
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	if !strings.Contains(string(data), `"decision":"deny"`) {
		t.Fatalf("audit log missing decision: %s", data)
	}
}
```

`test/hook/fixtures/deny_mcp_write.json` (for the MCP matcher path; the same script handles it):
```json
{"tool_name":"mcp__aws-api__call_aws","tool_input":{"cli_command":"aws s3 rb s3://x --force"},"cwd":"/Users/esaldgut/dev/src/go/src/erickaldama-mail","permission_mode":"default"}
```

- [x] **Step 2: Run, verify FAIL**

Run: `cd test/hook && go test -run TestWritesAuditLog -v`
Expected: FAIL — audit log not written (no logging yet).

- [x] **Step 3: Add the audit-log helper + call it in emit_deny/emit_allow; handle the MCP tool path**

In `cdk-go-guard.sh`, replace the two `emit_*` functions with logging versions, and add MCP command extraction. Put this near the top, after the shebang/comment block:
```bash
DECISION_LOG="${DECISION_LOG:-$HOME/.claude/hooks/decision-log.json}"

# Sanitize: strip secret-shaped tokens before logging (SEC E1/N1).
sanitize() {
  printf '%s' "$1" | sed -E \
    -e 's/(AKIA|ASIA)[A-Z0-9]{16}/<AWS_KEY>/g' \
    -e 's/AWS_SECRET_ACCESS_KEY=[^[:space:]]+/AWS_SECRET_ACCESS_KEY=<redacted>/g' \
    -e 's/--token-code[[:space:]]+[0-9]+/--token-code <redacted>/g'
}

audit() { # $1=decision $2=tool
  local line
  line="$(jq -nc --arg d "$1" --arg t "$2" --arg c "$(sanitize "${CMD:-}")" \
    '{ts:(now|todate),decision:$d,tool:$t,command:$c}')" || return 0
  # N1: atomic single-line append.
  printf '%s\n' "$line" >> "$DECISION_LOG" 2>/dev/null || true
}

emit_deny() { audit deny "${TOOL:-Bash}"; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%s}}\n' "$1"; exit 0; }
emit_allow() { audit allow "${TOOL:-Bash}"; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}\n'; exit 0; }
```
And after parsing `CMD`/`CWD`/`PMODE`, add MCP-tool handling (the MCP matcher routes `mcp__aws-api__*` here; its command lives under a different key):
```bash
TOOL="$(printf '%s' "$INPUT" | jq -r '.tool_name // "Bash"')"
if [[ "$TOOL" == mcp__aws-api__* ]]; then
  # Inspect the MCP call's AWS command; allowlist of reads, else deny (best-effort; IAM is the boundary).
  MCMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.cli_command // .tool_input.command // empty')"
  MSUB="$(printf '%s' "$MCMD" | awk '{print $3}')"
  case "$MSUB" in
    describe*|list*|get*|ls) emit_allow ;;
    *) emit_deny '"AWS mutation via MCP denied; IAM enforces, runs out-of-band"' ;;
  esac
fi
```
> Note: the exact `tool_input` key for `call_aws` is verified against the aws-api MCP README during Phase 3 Task 9 (the spec flags this); the `// .tool_input.command` fallback covers both shapes, and the `*) deny` default is the safe fallback the spec requires.

- [x] **Step 4: Run, verify PASS (audit + MCP deny)**

Run: `cd test/hook && go test ./... -v`
Expected: all PASS including `TestWritesAuditLog`. Add a quick MCP check:
Run: `echo '{"tool_name":"mcp__aws-api__call_aws","tool_input":{"cli_command":"aws s3 rb s3://x"},"cwd":"'"$HOME"'/dev/src/go/src/erickaldama-mail","permission_mode":"default"}' | MAIL_ROOT="$HOME/dev/src/go/src/erickaldama-mail" bash .claude/hooks/cdk-go-guard.sh`
Expected: `"permissionDecision":"deny"`.

- [x] **Step 5: Create settings.json wiring the hook for both matchers**

`.claude/settings.json`:
```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/cdk-go-guard.sh" }
        ]
      },
      {
        "matcher": "mcp__aws-api__.*",
        "hooks": [
          { "type": "command", "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/cdk-go-guard.sh" }
        ]
      }
    ]
  }
}
```

- [x] **Step 6: Commit**

```bash
chmod +x .claude/hooks/cdk-go-guard.sh
git add .claude/settings.json .claude/hooks/cdk-go-guard.sh test/hook/
git commit -m "feat(sp-0): wire hook (Bash + mcp__aws-api__.* matchers) + sanitized audit log"
```

---

## PHASE 2 — The Plugin (skills + verifier agent + eval, no AWS)

The plugin is self-contained and validated offline with `claude plugin validate --strict` plus the Go eval harness. No AWS calls.

### Task 5: Plugin manifest + structure (validates with --strict)

**Files:**
- Create: `cdk-go-aws-plugin/.claude-plugin/plugin.json`
- Create: `cdk-go-aws-plugin/.mcp.json`

- [x] **Step 1: Write the manifest (author as OBJECT, defaultEnabled:false, mcpServers pointer)**

`cdk-go-aws-plugin/.claude-plugin/plugin.json`:
```json
{
  "name": "cdk-go-aws-plugin",
  "version": "0.1.0",
  "description": "Recipes that provision AWS via CDK Go, verifying each construct against live docs before acting. SES domain + send/receive recipe with operational runbook.",
  "author": { "name": "Erick Aldama", "email": "esaldgut@gmail.com", "url": "https://erickaldama.com" },
  "mcpServers": "./.mcp.json",
  "defaultEnabled": false
}
```

`cdk-go-aws-plugin/.mcp.json` (server-key EXACTLY `aws-api` so the hook matcher + tool name align):
```json
{
  "mcpServers": {
    "aws-knowledge": {
      "type": "http",
      "url": "https://knowledge-mcp.global.api.aws"
    },
    "aws-api": {
      "type": "stdio",
      "command": "uvx",
      "args": ["awslabs.aws-api-mcp-server@latest"],
      "env": {
        "READ_OPERATIONS_ONLY": "true",
        "REQUIRE_MUTATION_CONSENT": "true",
        "AWS_REGION": "us-east-1",
        "AWS_API_MCP_PROFILE_NAME": "mail-readonly"
      }
    }
  }
}
```

- [x] **Step 2: Validate (this is the test for this task)**

Run: `claude plugin validate ./cdk-go-aws-plugin --strict`
Expected: validation errors about missing skills (skills dir not created yet) OR a clean structural pass for the manifest. If it complains the plugin has no components, that's expected until Task 6 — note it and proceed; re-validate at end of Task 6.

- [x] **Step 3: Commit**

```bash
git add cdk-go-aws-plugin/.claude-plugin/plugin.json cdk-go-aws-plugin/.mcp.json
git commit -m "feat(sp-0): plugin manifest + .mcp.json (aws-knowledge + aws-api read-only)"
```

### Task 6: The cdk-verifier agent (Knowledge-MCP-only) + cdk-go-recipe skill

**Files:**
- Create: `cdk-go-aws-plugin/agents/cdk-verifier.md`
- Create: `cdk-go-aws-plugin/skills/cdk-go-recipe/SKILL.md`

- [x] **Step 1: Write the verifier agent (tools allowlist = ONLY Knowledge MCP; no Bash/Write/aws-api)**

`cdk-go-aws-plugin/agents/cdk-verifier.md`:
```markdown
---
name: cdk-verifier
description: Verify a list of AWS CDK Go constructs / AWS APIs against live AWS docs. Dispatched by the cdk-go-recipe and ses-domain-recipe skills when the local cache (docs/cdk-verified.json) is missing entries, stale (cdk_version != go.mod), or past its 7-day TTL. Returns a consolidated verdict only — never raw docs.
tools:
  - mcp__aws-knowledge__search_documentation
  - mcp__aws-knowledge__read_documentation
---

You are a documentation verifier. You receive a list of CDK-Go constructs / AWS API symbols and a target
`aws-cdk-go` version. For EACH symbol, use ONLY the AWS Knowledge MCP tools to confirm against live docs:
1. that the symbol exists at the target version,
2. its current documented signature (props/arguments),
3. the canonical doc URL you consulted.

You have NO Bash, NO Write, NO aws-api tools — you read docs, you do not touch AWS or the filesystem.

Return ONLY this JSON (no prose, no raw doc text):
```json
{ "cdk_version": "<target>",
  "constructs": {
    "<symbol>": { "exists": true|false, "doc_url": "<url>", "signature_hash": "sha256:<hex of the documented signature>" }
  } }
```
This is the exact input schema of the recipe's cache (docs/cdk-verified.json). Compute signature_hash as the
SHA-256 of the normalized documented signature string (sorted prop names + types).
```

- [x] **Step 2: Write the cdk-go-recipe skill (4 phases + cache mechanism + best-effort dispatch)**

`cdk-go-aws-plugin/skills/cdk-go-recipe/SKILL.md`:
```markdown
---
name: cdk-go-recipe
description: Use when authoring or deploying ANY AWS infrastructure as code in this account. Provisions exclusively via AWS CDK in Go (aws-cdk-go latest), verifying each construct against live AWS docs before use, then preparing (not executing) the deploy for out-of-band human execution. Covers app structure, the diff→synth→deploy flow, version freshness, jsii idioms, and the verify-before-act cache.
---

# CDK-Go recipe — verify before you act

This is for **Claude Code**. The IAM allowlist is the security boundary; you NEVER run `cdk deploy` — you
prepare it and hand it to the human (out-of-band). You RAZONA and GENERATE; the human EXECUTES mutations.

## The 4 phases (every infra change)

**F1 — VERIFY RULES.**
- Live version: run `go list -m -versions github.com/aws/aws-cdk-go/awscdk/v2` (read-only; the hook allows
  `go list`? NO — `go` is not in the hook allowlist). Therefore run version discovery in a way the human can
  see, or read it from go.mod and flag if you cannot confirm freshness. NEVER hardcode a version (e.g. v2.258.0).
- Constructs: read `docs/cdk-verified.json`. For each construct you will use, an entry is VALID iff
  `cdk_version == go.mod's aws-cdk-go version` AND `verified_at` is within 7 days. Anti-poison: on read, if
  `cdk_version` != the live go.mod version, IGNORE the cache (a forged stale entry must not be trusted).
  If any construct is missing/stale, dispatch the `cdk-verifier` agent (via the Task tool) with the construct
  list + target version. This dispatch is BEST-EFFORT (you must choose to do it; it is not enforced). Write the
  returned verdict back into `docs/cdk-verified.json`. `--force-verify` invalidates the whole cache.

**F2 — READ STATE.** `aws sts get-caller-identity` (confirm account 367707589526). `cdk diff` (read-only delta).

**F3 — ACT (prepare, do not execute).** Generate the stack code + the exact `cdk deploy` command + the diff.
HAND IT TO THE HUMAN. The human runs it out-of-band with a named profile the agent never selects. Do NOT run
`cdk deploy` yourself (the hook denies it; the IAM read-only cred cannot mutate anyway).

**F4 — VERIFY OUTPUT.** After the human's deploy, read CloudFormation events/outputs. A known error → back to F1.

## Go / CDK-Go idioms (compose with modern-go-guidelines, do not duplicate)
- `cdk.json`: `"app": "go mod download && go run ."`. `main.go` instantiates the `App`; one stack per file.
- jsii: pointers via `jsii.String(...)`, `jsii.Number(...)`, `jsii.Bool(...)` — never raw literals into props.
- Cross-stack: SP-1 exposes the hosted zone; SP-2 consumes it via a cross-stack reference. `cdk diff` ALWAYS
  before handing off a deploy.

## --dry-run
When invoked with `--dry-run`, run F1–F3 (verify, read, GENERATE the code/commands) but do NOT ask the human to
execute — just show what you WOULD do. Used by the eval harness.
```

- [x] **Step 3: Re-validate the plugin**

Run: `claude plugin validate ./cdk-go-aws-plugin --strict`
Expected: PASS (manifest + at least one skill + agent present). If it flags the second skill missing, that's added in Task 7 — re-validate after Task 7.

- [x] **Step 4: Commit**

```bash
git add cdk-go-aws-plugin/agents/cdk-verifier.md cdk-go-aws-plugin/skills/cdk-go-recipe/SKILL.md
git commit -m "feat(sp-0): cdk-verifier agent (Knowledge-MCP-only) + cdk-go-recipe skill"
```

### Task 7: The ses-domain-recipe skill (8 steps + runbook + 6 traps)

**Files:**
- Create: `cdk-go-aws-plugin/skills/ses-domain-recipe/SKILL.md`

- [x] **Step 1: Write the SES recipe skill**

`cdk-go-aws-plugin/skills/ses-domain-recipe/SKILL.md`:
```markdown
---
name: ses-domain-recipe
description: Use when provisioning or operating Amazon SES for a domain — verifying a domain identity, DKIM, custom MAIL FROM, DMARC, configuration sets, requesting production access (sandbox exit), or setting up inbound receiving. Orchestrates the 8-step SES setup in dependency order, verifying each API against live AWS docs, and includes the post-provisioning reputation runbook.
---

# SES domain recipe — 8 steps, in dependency order

For Claude Code. Provisioning steps generate CDK-Go constructs (delegating "how to write Go" to cdk-go-recipe);
account-operations (steps 6, 8) are NOT CDK — they are commands/docs handed to the human. Each provisioning step
runs the 4 verify-before-act phases (same cache/agent mechanism as cdk-go-recipe).

## The 8 steps
1. **Domain identity (DKIM-based)** — infra (CDK-Go). Creates the 3 DKIM CNAMEs.
2. **DKIM verification** — depends on 1; ≤72h to Verified.
3. **Custom MAIL FROM** (`mail.erickaldama.com`) — infra; MX + SPF; for SPF→DMARC alignment.
4. **DMARC** (`_dmarc`, `p=none` → `quarantine` → `reject`) — infra; depends on 3.
5. **Configuration set + event destination** — infra; BEFORE sending (bounce/complaint capture).
6. **Sandbox exit (production access)** — ACCOUNT OPERATION → hand `aws sesv2 put-account-details` to the human;
   there is no CloudFormation construct for this.
7. **Inbound receiving** (receipt rule → S3 → Lambda) — infra; uses the **v1 `ses` API, NOT sesv2** (trap #6).
8. **Post-provisioning runbook** — see below; presented to the human, NOT executed.

## 6 traps (guardrails — verify against live docs, never assume)
1. Domain verification IS DKIM-based: steps 1+2 are ONE DNS transaction, not two.
2. DKIM suffix is region-dependent → derive from `SigningHostedZone`, NEVER hardcode `dkim.amazonses.com`.
3. Sandbox exit ≠ quota jump → after production access, read the live quota and request an increase separately.
4. `put-account-details` returns 409 after a denial → fall back to the Service Quotas API.
5. SPF has a 10 DNS-lookup limit (RFC 7208) → count lookups before writing the record.
6. Receiving uses the v1 `ses` API, NOT `sesv2`.

## Step 8 — reputation runbook (the skill GENERATES the alarm constructs; SP-3 deploys them)
Critical thresholds (AWS pauses sending account-wide): bounce > 5%, complaint > 0.5%. If crossed: STOP sending
immediately. CloudWatch alarms (conservative warning, well below the review line) — the skill EMITS this CDK-Go
as a recipe artifact (SP-3 owns/deploys the stack; SP-0 does not deploy):
```go
cloudwatch.NewAlarm(stack, jsii.String("SESBounceRateAlarm"), &cloudwatch.AlarmProps{
    Metric:             ses.MetricBounceRate(),
    Threshold:          jsii.Number(2),
    ComparisonOperator: cloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
    EvaluationPeriods:  jsii.Number(2),
})
// + SESBounceRateCritical (5%), SESComplaintRateAlarm (0.05%), SESComplaintRateCritical (0.5%) → SNS
```
"Reputation in the red" runbook: 1) pause sending; 2) identify cause (suppression list? content? dirty list?);
3) clear suppression list (`aws sesv2 put-suppressed-destination` — handed to the human); 4) request quota
increase (only if bounce is throttling, not quality); 5) resume gradually (1% → 10% → 100% while monitoring).

## --dry-run
Run verify/read/GENERATE the constructs + commands, but do NOT ask the human to execute. Used by the eval harness.
```

- [x] **Step 2: Validate the complete plugin**

Run: `claude plugin validate ./cdk-go-aws-plugin --strict`
Expected: PASS — manifest, two skills, one agent, .mcp.json all structurally valid.

- [x] **Step 3: Commit**

```bash
git add cdk-go-aws-plugin/skills/ses-domain-recipe/SKILL.md
git commit -m "feat(sp-0): ses-domain-recipe skill (8 steps + runbook + 6 traps)"
```

### Task 8: The eval harness (golden prompts → property assertions → Pass@k)

**Files:**
- Create: `cdk-go-aws-plugin/eval/golden/ses-identity.txt`, `cdk-go-aws-plugin/eval/golden/s3-bucket.txt`
- Create: `cdk-go-aws-plugin/eval/assertions.go`
- Create: `cdk-go-aws-plugin/eval/run_eval.go`
- Create: `cdk-go-aws-plugin/eval/baseline.json`
- Create: `cdk-go-aws-plugin/eval/assertions_test.go`

- [x] **Step 1: Write the failing test for the assertion engine (deterministic, on a captured-output string)**

`cdk-go-aws-plugin/eval/assertions_test.go`:
```go
package eval

import "testing"

func TestAssertSESIdentityOutput(t *testing.T) {
	// A GOOD generated output: uses the construct, jsii, derives DKIM from SigningHostedZone, no cdk deploy.
	good := `
		identity := awsses.NewEmailIdentity(stack, jsii.String("Identity"), &awsses.EmailIdentityProps{ ... })
		dkimHost := *identity.DkimDnsTokenName1() // derived from SigningHostedZone, not hardcoded
		// MAIL FROM
		identity.AddMailFromAttributes(...)
		// DMARC after MAIL FROM
	`
	res := AssertSESIdentity(good)
	if !res.Pass {
		t.Fatalf("good output should pass, failures: %v", res.Failures)
	}

	// A BAD output: hardcoded dkim suffix + contains "cdk deploy".
	bad := `domain := "erickaldama.com"; dkim := "token.dkim.amazonses.com"; run("cdk deploy")`
	res = AssertSESIdentity(bad)
	if res.Pass {
		t.Fatalf("bad output should fail (hardcoded dkim + cdk deploy)")
	}
}
```

- [x] **Step 2: Run, verify FAIL**

Run: `cd cdk-go-aws-plugin/eval && go test -run TestAssertSESIdentityOutput -v`
Expected: FAIL — `AssertSESIdentity` undefined.

- [x] **Step 3: Implement the assertion engine (whitespace-resilient, positive/negative/6-trap)**

`cdk-go-aws-plugin/eval/assertions.go`:
```go
package eval

import (
	"regexp"
	"strings"
)

type Result struct {
	Pass     bool
	Failures []string
}

// norm collapses all whitespace runs to a single space (whitespace-resilient assertions).
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func contains(hay, needle string) bool { return strings.Contains(norm(hay), norm(needle)) }
func matches(hay, pattern string) bool { return regexp.MustCompile(pattern).MatchString(hay) }

// AssertSESIdentity checks the generated CDK-Go for the SES-identity golden prompt.
func AssertSESIdentity(out string) Result {
	var f []string
	// positive
	if !contains(out, "awsses.NewEmailIdentity") {
		f = append(f, "missing awsses.NewEmailIdentity")
	}
	if !matches(out, `jsii\.String\(`) {
		f = append(f, "missing jsii.String usage")
	}
	// trap #2: DKIM derived, not hardcoded
	if contains(out, "dkim.amazonses.com") {
		f = append(f, "trap#2: hardcoded dkim suffix")
	}
	// negative: never executes a deploy
	if matches(out, `cdk\s+deploy`) {
		f = append(f, "negative: contains cdk deploy")
	}
	// negative: no hardcoded 12-digit account id
	if matches(out, `\b\d{12}\b`) {
		f = append(f, "negative: hardcoded 12-digit account id")
	}
	return Result{Pass: len(f) == 0, Failures: f}
}

// AssertS3Bucket checks the generated CDK-Go for the s3-bucket golden prompt.
func AssertS3Bucket(out string) Result {
	var f []string
	if !contains(out, "awss3.NewBucket") {
		f = append(f, "missing awss3.NewBucket")
	}
	if !matches(out, `jsii\.String\(`) {
		f = append(f, "missing jsii usage")
	}
	if !matches(out, `(?i)BUCKET_OWNER_ENFORCED|S3_MANAGED|SSE`) {
		f = append(f, "missing SSE/encryption config")
	}
	if matches(out, `(?i)PublicReadAccess:\s*jsii\.Bool\(true\)`) {
		f = append(f, "negative: bucket is public")
	}
	if matches(out, `cdk\s+deploy`) {
		f = append(f, "negative: contains cdk deploy")
	}
	return Result{Pass: len(f) == 0, Failures: f}
}
```

- [x] **Step 4: Run, verify PASS**

Run: `cd cdk-go-aws-plugin/eval && go test -run TestAssertSESIdentityOutput -v`
Expected: PASS.

- [x] **Step 5: Write the golden prompts, the runner, and the baseline**

`cdk-go-aws-plugin/eval/golden/ses-identity.txt`:
```
Using the ses-domain-recipe skill in --dry-run mode, generate the CDK-Go for a SES domain identity for erickaldama.com with DKIM and a custom MAIL FROM subdomain. Output only the generated Go code.
```
`cdk-go-aws-plugin/eval/golden/s3-bucket.txt`:
```
Using the cdk-go-recipe skill in --dry-run mode, generate the CDK-Go for a private S3 bucket with SSE for storing raw inbound mail. Output only the generated Go code.
```

`cdk-go-aws-plugin/eval/run_eval.go` (build-tagged so it doesn't run in normal `go test`):
```go
//go:build eval

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type caseDef struct {
	prompt   string
	file     string
	assertFn func(string) Result
}

var cases = []caseDef{
	{file: "golden/ses-identity.txt", assertFn: AssertSESIdentity},
	{file: "golden/s3-bucket.txt", assertFn: AssertS3Bucket},
}

type baselineEntry struct {
	Prompt  string  `json:"prompt"`
	PassAt1 float64 `json:"pass_at_1"`
	PassAt3 float64 `json:"pass_at_3"`
	Date    string  `json:"date"`
}

// RunEval invokes each golden prompt k times via `claude -p`, asserts, computes Pass@k, writes baseline.json.
// Invoked manually or in CI nightly: `go run -tags eval ./...` from the eval dir. NOT a unit test (LLM, non-deterministic).
func RunEval(k int, date string) error {
	var out []baselineEntry
	for _, c := range cases {
		promptBytes, err := os.ReadFile(c.file)
		if err != nil {
			return err
		}
		var passes int
		for i := 0; i < k; i++ {
			cmd := exec.Command("claude", "-p", string(promptBytes))
			b, err := cmd.Output()
			if err != nil {
				continue // a failed run counts as a non-pass
			}
			if c.assertFn(string(b)).Pass {
				passes++
			}
		}
		out = append(out, baselineEntry{
			Prompt:  c.file,
			PassAt1: passAt(passes, k, 1),
			PassAt3: passAt(passes, k, 3),
			Date:    date,
		})
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile("baseline.json", data, 0o644)
}

// passAt is the fraction of independent runs that fully passed, reported at the k granularity.
func passAt(passes, total, _ int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passes) / float64(total)
}

func main() {
	if err := RunEval(3, time.Now().UTC().Format("2006-01-02")); err != nil {
		fmt.Fprintln(os.Stderr, "eval error:", err)
		os.Exit(1)
	}
}
```
> The `date` is passed in (not `time.Now()` inside assertions) to keep the assertion engine deterministic; `main` stamps it at run time, which is fine for the manually-invoked runner.

`cdk-go-aws-plugin/eval/baseline.json` (seed — overwritten on first real run):
```json
[]
```

- [x] **Step 6: Run the assertion suite (the deterministic part)**

Run: `cd cdk-go-aws-plugin/eval && go test ./... -v`
Expected: PASS (assertion engine tested; the LLM-driven `RunEval` is build-tagged out and run separately).

- [x] **Step 7: Commit**

```bash
git add cdk-go-aws-plugin/eval/
git commit -m "feat(sp-0): eval harness (golden prompts + property assertions + Pass@k runner)"
```

---

## PHASE 3 — The IAM boundary + bootstrap (the only AWS-touching phase, read-only)

This phase authors the canonical policy JSON and two verification scripts, then runs them read-only against AWS. The principal is created MANUALLY by the human (bootstrap); the scripts verify it. Apply `aws-cli-pre-flight-canonical` first.

### Task 9: Verify the aws-api MCP `call_aws` tool_input shape (pre-plan spike → close the hook fallback)

**Files:**
- Modify: `.claude/hooks/cdk-go-guard.sh` (only if the key differs from the assumed `cli_command`)
- Create: `docs/MCP_NOTES.md`

- [x] **Step 1: Fetch the aws-api MCP README and confirm the call_aws argument key**

Run: WebFetch `https://github.com/awslabs/mcp/blob/main/src/aws-api-mcp-server/README.md` — find the `call_aws` tool's input schema (the exact parameter name carrying the AWS CLI command string).

- [x] **Step 2: Record the finding**

`docs/MCP_NOTES.md`: document the exact `call_aws` input key (e.g. `cli_command`), and whether the MCP nests it. State: "the hook's MCP branch reads `.tool_input.<key>`; if un-inspectable, it denies all non-read `mcp__aws-api__*` — Capa 1 (IAM) enforces regardless."

- [x] **Step 3: Reconcile the hook if needed**

If the key differs from `cli_command`/`command`, update the `jq` extraction in `cdk-go-guard.sh`'s MCP branch and re-run `cd test/hook && go test ./...` (expected: PASS).

- [x] **Step 4: Commit**

```bash
git add docs/MCP_NOTES.md .claude/hooks/cdk-go-guard.sh
git commit -m "docs(sp-0): verify aws-api MCP call_aws input shape; reconcile hook MCP branch"
```

### Task 10: Author the canonical IAM allowlist policy (the boundary)

**Files:**
- Create: `iam/readonly-policy.json`

- [x] **Step 1: Write the pure, scoped allowlist policy**

`iam/readonly-policy.json` (allowlist-pure; **4 statements verified action-by-action vs the AWS Service
Authorization Reference, SAR 2026-06-08**; structure = global-unconditioned + 2 regional-pinned + hard-deny).
The global/regional split is the key correction: STS `GetCallerIdentity` and Route53 are GLOBAL, so pinning
them to a region would WRONGLY DENY them (latent bug in the prior 2-statement shape). Copy verbatim:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowGlobalReadsUnconditioned",
      "Effect": "Allow",
      "Action": [
        "sts:GetCallerIdentity",
        "route53:ListHostedZones",
        "route53:GetHostedZone",
        "route53:ListResourceRecordSets"
      ],
      "Resource": "*"
    },
    {
      "Sid": "AllowRegionalReadsUsEast1",
      "Effect": "Allow",
      "Action": [
        "ses:Get*",
        "ses:List*",
        "ses:Describe*",
        "cloudformation:Describe*",
        "cloudformation:List*",
        "cloudwatch:DescribeAlarms",
        "cloudwatch:ListMetrics",
        "cloudwatch:GetMetricData",
        "cloudwatch:GetMetricStatistics"
      ],
      "Resource": "*",
      "Condition": { "StringEquals": { "aws:RequestedRegion": "us-east-1" } }
    },
    {
      "Sid": "AllowS3BucketLevelScopedUsEast1",
      "Effect": "Allow",
      "Action": [
        "s3:ListBucket",
        "s3:GetBucketLocation",
        "s3:GetBucketPolicy",
        "s3:GetBucketPublicAccessBlock",
        "s3:GetEncryptionConfiguration"
      ],
      "Resource": "arn:aws:s3:::*erickaldama*",
      "Condition": { "StringEquals": { "aws:RequestedRegion": "us-east-1" } }
    },
    {
      "Sid": "HardDenyMutationReconAndCredentialMinting",
      "Effect": "Deny",
      "Action": [
        "ses:Send*",
        "sts:AssumeRole",
        "sts:AssumeRoleWithWebIdentity",
        "sts:AssumeRoleWithSAML",
        "sts:GetSessionToken",
        "sts:GetFederationToken",
        "s3:GetObject",
        "cloudformation:GetTemplate",
        "cloudformation:GetTemplateSummary",
        "ses:GetIdentityPolicies",
        "ses:GetEmailIdentityPolicies",
        "iam:*"
      ],
      "Resource": "*"
    }
  ]
}
```
> Notes baked into the policy per SAR audit (full findings: `09-iam-policy-verified-vs-sar.md`):
> **(1) Global vs regional split** — `sts:GetCallerIdentity` + Route53 reads are GLOBAL and go in the
> UNCONDITIONED Statement 1 (a region-pin breaks GetCallerIdentity: the STS global endpoint reports
> `aws:RequestedRegion=us-east-1` regardless of the real region under CLI v2 → false AccessDenied). Statements 2 & 3
> carry the `us-east-1` region condition. **(2) No `sesv2:` IAM prefix** — SES v1+v2 are both `ses:`, so `ses:Get*`
> covers v2 reads and the Deny `ses:Send*` covers SendEmail/SendRaw/SendBulk* of both. **(3) No `cloudformation:Deploy*`**
> — it is NOT a real action (`aws cloudformation deploy` = CreateChangeSet+ExecuteChangeSet) → a Deny on it would be a
> dead statement; omitted. **(4) `sts:GetSessionToken`/`GetFederationToken` are Read-classified by the SAR but mint
> credentials**, so they are denied BY NAME (which is why the Allow uses `sts:GetCallerIdentity`, not `sts:Get*`).
> NO `s3:GetObject` (no mail-body reads, SEC2-C1); NO `iam:*` (no account recon, SEC2-C2); `GetIdentityPolicies`/`GetTemplate`
> denied (expose authz JSON / template bodies). `Resource:"*"` is acceptable because the Allow set is read-only-and-region-pinned
> and the Deny set closes the dangerous reads — Resource-level ARN scoping is SP-1's CDK-Go formalization (ownership boundary).

- [x] **Step 2: Validate the JSON is well-formed**

Run: `jq empty iam/readonly-policy.json && echo OK`
Expected: `OK`.

- [x] **Step 3: Commit**

```bash
git add iam/readonly-policy.json
git commit -m "feat(sp-0): canonical IAM allowlist-pure scoped policy (the boundary)"
```

### Task 11: Bootstrap doc + acceptance-gate script (SEC2-I1)

**Files:**
- Create: `docs/BOOTSTRAP.md`
- Create: `iam/bootstrap-gate.sh`

- [x] **Step 1: Write the bootstrap procedure + ownership boundaries**

`docs/BOOTSTRAP.md`:
```markdown
# SP-0 Bootstrap (t=0) — sanctioned manual exception to the CDK-Go premise

The MCP `aws-api` needs a read-only IAM principal. Creating it IS an AWS write, but the CDK-Go governance
does not exist yet at t=0 (chicken-and-egg). This is the one-time manual exception (like Terraform's state bucket).

## Procedure (human, with ADMIN credentials the agent NEVER sees)
1. Create an IAM user/role `mail-readonly` and attach `iam/readonly-policy.json` (inline), using profile
   `AdministratorAccess-367707589526`. Do this in the Console or `aws iam ... --profile AdministratorAccess-367707589526`.
2. Configure a NAMED profile `mail-readonly` in ~/.aws/config for that principal — NEVER `[default]`.
3. Run the ACCEPTANCE GATE (`iam/bootstrap-gate.sh`) BEFORE pointing the agent at it. It must pass fully.
4. Only then set the MCP's `AWS_API_MCP_PROFILE_NAME=mail-readonly` (already in .mcp.json) live.

## Ownership boundaries (resolved)
- **IAM:** SP-0 DEFINES and VERIFIES this policy (it is SP-0's boundary). SP-1 FORMALIZES it as CDK-Go
  (base IAM is SP-1's domain) and re-runs this same gate. The debt cannot silently widen the boundary.
- **CloudWatch alarms:** SP-0's ses-domain-recipe EMITS the alarm constructs as recipe artifacts; SP-3 OWNS
  and DEPLOYS them. SP-0 does not deploy.

## The deploy credential (out-of-band, SEC2-C3)
Mutations (`cdk deploy`) run with a SEPARATE named profile `mail-deploy` that the agent NEVER selects, ideally
on another machine / CloudShell. The agent's session is pinned to `mail-readonly`. Negative test below.
```

- [x] **Step 2: Write the acceptance-gate script (the test for this task — read-only probes)**

`iam/bootstrap-gate.sh` (probes cover the 4-statement policy: 5 HardDeny + 2 allow [1 regional, 1 global] +
1 region-pin deny; `sesv2`/`s3api` here are CLI command namespaces — they map to the `ses:`/`s3:` IAM actions,
NOT a `sesv2:` IAM prefix):
```bash
#!/usr/bin/env bash
# Bootstrap acceptance gate (SEC2-I1). Run BEFORE pointing the agent at the read-only principal.
# All probes are read-only or expected-to-be-denied; nothing here mutates.
# Applies aws-cli-pre-flight-canonical: verify identity + account before any service call.
set -uo pipefail
PROFILE="${MAIL_RO_PROFILE:-mail-readonly}"
fail=0

echo "== aws-cli-pre-flight-canonical =="
acct="$(aws sts get-caller-identity --profile "$PROFILE" --query Account --output text 2>/dev/null)"
[[ "$acct" == "367707589526" ]] || { echo "FAIL: wrong/empty account: '$acct' (expected 367707589526)"; exit 1; }
echo "ok: account 367707589526"

# expect_denied <description> <aws args...>
expect_denied() {
  local desc="$1"; shift
  if aws "$@" --profile "$PROFILE" >/dev/null 2>&1; then
    echo "FAIL (should be denied): $desc"; fail=1
  else
    echo "ok (denied): $desc"
  fi
}
# expect_allowed <description> <aws args...>
expect_allowed() {
  local desc="$1"; shift
  if aws "$@" --profile "$PROFILE" >/dev/null 2>&1; then
    echo "ok (allowed): $desc"
  else
    echo "FAIL (should be allowed): $desc"; fail=1
  fi
}

# --- DENY: mutation / credential-minting / recon (HardDeny + implicit-deny) ---
expect_denied  "ses send-email"        sesv2 send-email --region us-east-1 --from-email-address a@b.com --destination ToAddresses=c@d.com --content '{"Simple":{"Subject":{"Data":"x"},"Body":{"Text":{"Data":"y"}}}}'
expect_denied  "sts assume-role"       sts assume-role --role-arn arn:aws:iam::367707589526:role/none --role-session-name s
expect_denied  "sts get-session-token" sts get-session-token   # Read-classified but credential-minting → explicit Deny by name
expect_denied  "s3 get-object (mail)"  s3api get-object --bucket erickaldama-mail-raw --key any /dev/null
expect_denied  "iam list-access-keys"  iam list-access-keys

# --- ALLOW: regional read (region-pinned) + global read (unconditioned) ---
expect_allowed "ses get-account read"  sesv2 get-account --region us-east-1
expect_allowed "route53 list-zones (global, unconditioned)"  route53 list-hosted-zones

# --- region-pin enforcement: the SAME regional read in a different region must be DENIED ---
expect_denied  "ses get-account in eu-west-1 (region-pin)"  sesv2 get-account --region eu-west-1

[[ "$fail" -eq 0 ]] && echo "GATE PASS" || { echo "GATE FAIL"; exit 1; }
```

- [x] **Step 3: Lint the script (offline — it won't run without the principal yet)**

Run: `bash -n iam/bootstrap-gate.sh && echo "syntax ok"`
Expected: `syntax ok`. (Full execution happens in Task 13 after the human creates the principal.)

- [x] **Step 4: Commit**

```bash
chmod +x iam/bootstrap-gate.sh
git add docs/BOOTSTRAP.md iam/bootstrap-gate.sh
git commit -m "feat(sp-0): bootstrap doc + acceptance-gate script (deny/allow probes)"
```

### Task 12: Falsifiability via simulate-principal-policy (SEC2-I2)

**Files:**
- Create: `iam/simulate-matrix.sh`

- [x] **Step 1: Write the simulate-matrix script (run with a SEPARATE principal, not the read-only one)**

`iam/simulate-matrix.sh` (region-context-aware: the REGIONAL intended-allows of Statements 2 & 3 pass
`aws:RequestedRegion=us-east-1` via `--context-entries` so the StringEquals condition is satisfied — without
it `simulate-principal-policy` evaluates WITHOUT a region context and the region-pinned Allow returns a
false implicitDeny; the GLOBAL allows of Statement 1 and the explicit denies use no context):
```bash
#!/usr/bin/env bash
# Falsifiability of the allowlist (SEC2-I2): simulate the read-only principal's policy and assert the matrix.
# Run with an ADMIN/separate profile (simulate-principal-policy needs iam:Simulate*, NOT in the read-only set).
set -uo pipefail
ADMIN="${ADMIN_PROFILE:-AdministratorAccess-367707589526}"
PRINCIPAL_ARN="${RO_PRINCIPAL_ARN:?set RO_PRINCIPAL_ARN to the mail-readonly user/role arn}"
fail=0

# _sim <expected> <action> [extra args...] — core evaluator; pass-through extra args to the CLI.
_sim() {
  local expect="$1"; local action="$2"; shift 2
  local dec
  dec="$(aws iam simulate-principal-policy --profile "$ADMIN" \
    --policy-source-arn "$PRINCIPAL_ARN" --action-names "$action" "$@" \
    --query 'EvaluationResults[0].EvalDecision' --output text 2>/dev/null)"
  if [[ "$dec" == "$expect" || ( "$expect" == "*Deny" && "$dec" == *Deny ) ]]; then
    echo "ok $action -> $dec"
  else
    echo "FAIL $action -> $dec (expected $expect)"; fail=1
  fi
}

# sim <expected> <action> — global / unconditioned actions and all denies (no region context).
sim() { _sim "$1" "$2"; }

# sim_regional <expected> <action> — region-pinned Allows (Statements 2 & 3). Pass aws:RequestedRegion=us-east-1
# so the StringEquals condition is satisfied; without it simulate evaluates WITHOUT region context and the
# region-pinned Allow would WRONGLY return implicitDeny (latent false-FAIL).
sim_regional() {
  _sim "$1" "$2" \
    --context-entries ContextKeyName=aws:RequestedRegion,ContextKeyValues=us-east-1,ContextKeyType=string
}

# intended-allow — REGIONAL (Statements 2 & 3): pass region context so the us-east-1 condition is met.
sim_regional allowed  ses:GetAccount
sim_regional allowed  cloudformation:DescribeStacks
# intended-allow — GLOBAL (Statement 1, unconditioned): NO region context; allows regardless of region.
sim allowed  sts:GetCallerIdentity
sim allowed  route53:ListHostedZones
# intended-deny — explicit HardDeny set (verified vs SAR 2026-06-08). Explicit Deny wins regardless of region.
sim "*Deny"  ses:SendEmail
sim "*Deny"  sts:AssumeRole
sim "*Deny"  sts:GetSessionToken
sim "*Deny"  sts:GetFederationToken
sim "*Deny"  s3:GetObject
sim "*Deny"  cloudformation:GetTemplate
sim "*Deny"  ses:GetIdentityPolicies
sim "*Deny"  iam:ListAccessKeys
# intended-deny — implicit (not in any Allow)
sim "*Deny"  lambda:InvokeFunction

[[ "$fail" -eq 0 ]] && echo "SIMULATE MATRIX PASS" || { echo "SIMULATE MATRIX FAIL"; exit 1; }
```

- [x] **Step 2: Lint (offline)**

Run: `bash -n iam/simulate-matrix.sh && echo "syntax ok"`
Expected: `syntax ok`.

- [x] **Step 3: Commit**

```bash
chmod +x iam/simulate-matrix.sh
git add iam/simulate-matrix.sh
git commit -m "feat(sp-0): simulate-principal-policy falsifiability matrix"
```

### Task 13: Live bootstrap acceptance (human-gated; the boundary becomes real)

**Files:** none created — this is the execution gate. Apply `aws-cli-pre-flight-canonical`.

- [x] **Step 1: Human creates the principal (out-of-band, admin cred)**

Prompt the human (via the chat): "Create the `mail-readonly` IAM principal with `iam/readonly-policy.json` attached, using profile `AdministratorAccess-367707589526`, and a named `mail-readonly` profile in ~/.aws/config (never [default]). See docs/BOOTSTRAP.md." Wait for confirmation. **The agent does NOT run this** (it's an admin write).

- [x] **Step 2: Run the acceptance gate (read-only probes)**

Run (after pre-flight `aws sts get-caller-identity --profile mail-readonly` confirms account 367707589526):
`MAIL_RO_PROFILE=mail-readonly bash iam/bootstrap-gate.sh`
Expected: `GATE PASS` — 5 HardDeny probes (ses send-email, sts assume-role, sts get-session-token, s3 get-object, iam list-access-keys) + 2 allow probes (ses get-account in us-east-1 = regional, route53 list-hosted-zones = global/unconditioned) + 1 region-pin deny (ses get-account in eu-west-1 must be DENIED). If any FAIL → the hand-typed policy is wrong; fix in AWS, re-run. Do NOT proceed until PASS.

- [x] **Step 3: Run the simulate matrix (separate admin principal)**

Run: `RO_PRINCIPAL_ARN=arn:aws:iam::367707589526:user/mail-readonly ADMIN_PROFILE=AdministratorAccess-367707589526 bash iam/simulate-matrix.sh`
Expected: `SIMULATE MATRIX PASS` — regional intended-allows (ses:GetAccount, cloudformation:DescribeStacks) evaluated WITH `aws:RequestedRegion=us-east-1` context = allowed; global intended-allows (sts:GetCallerIdentity, route53:ListHostedZones) WITHOUT context = allowed; intended-deny set (incl. sts:GetSessionToken/GetFederationToken) = *Deny.

- [x] **Step 4: Negative out-of-band test (SEC2-C3)**

After the human runs any `cdk deploy` out-of-band with the `mail-deploy` profile, run:
`aws sts get-caller-identity --query Arn --output text`
Expected: the ARN is the **read-only** principal (`.../mail-readonly`), NOT the deploy principal — proving the elevated credential did not leak into the agent's credential chain. Record the result.

- [x] **Step 5: Commit the verification record**

```bash
# (record outputs into docs/BOOTSTRAP.md under a "## Acceptance record" section, sanitized)
git add docs/BOOTSTRAP.md
git commit -m "chore(sp-0): record bootstrap acceptance (gate PASS + simulate matrix PASS + out-of-band negative)"
```

---

## Self-Review

**Spec coverage:**
- Componente A (hook logic: self-deny/permission_mode/scope/metachar/allowlist/aws-read/cdk-Go) → Tasks 1–3. ✓
- Componente A2 Capa 1 (IAM allowlist-pure scoped) → Task 10; verification → Tasks 12–13. ✓
- Capa 3 (MCP hook matcher + call_aws shape) → Task 4 + Task 9. ✓
- Out-of-band mutation + negative test (SEC2-C3) → Task 13 Step 4. ✓
- Componente A3 (bootstrap + gate + ownership boundaries) → Tasks 11, 13. ✓
- Componente B (cdk-go-recipe + cache + cdk-verifier dispatch) → Task 6. ✓
- Componente C (ses-domain-recipe 8 steps + runbook + 6 traps + alarms) → Task 7. ✓
- Componente F (eval harness positive/negative/trap + Pass@k) → Task 8. ✓
- Componente D (plugin manifest, .mcp.json server-key=aws-api, defaultEnabled:false, validate --strict) → Tasks 5–7. ✓
- Componente E (audit log sanitized atomic; dry-run) → Task 4 + skill `--dry-run` sections (Tasks 6,7). ✓
- DoD items 1–10 map to: 1→T10/T12/T13, 2→T11/T13, 3→T13.4, 4→T1–T3, 5→T4, 6→T4, 7→T5–7, 8→T6, 9→T7, 10→T8. ✓

**Placeholder scan:** No "TBD"/"implement later"/"handle edge cases". Every code step shows the code; every run step shows the command + expected output. The one deferred lookup (call_aws key) is an explicit spike (Task 9) with a safe fallback already coded, not a placeholder.

**Type/name consistency:** hook env vars (`MAIL_ROOT`, `DECISION_LOG`, `MAIL_RO_PROFILE`, `ADMIN_PROFILE`, `RO_PRINCIPAL_ARN`) consistent across tasks; `emit_deny`/`emit_allow`/`audit`/`sanitize` defined once (Task 1/4) and reused; assertion funcs `AssertSESIdentity`/`AssertS3Bucket` defined in Task 8 and referenced by the runner in the same task; cache schema fields (`cdk_version`, `signature_hash`, `verified_at`) match the agent's output contract (Task 6) and the spec.

**Caveat carried forward:** `go list -m -versions` for live CDK version (F1) is NOT in the hook allowlist (`go` is an execution engine) — the skill notes this and reads go.mod / flags if it cannot confirm freshness. This is intentional and documented in the skill, consistent with the hook's deny of `go`.
