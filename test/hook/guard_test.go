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

func TestAllowsOutOfScopeEvenInBypassMode(t *testing.T) {
	// I-1 regression lock: out-of-scope project must be a no-op even under bypassPermissions.
	fixture, _ := os.ReadFile("fixtures/allow_out_of_scope_bypass.json")
	got := runHook(t, string(fixture), "/Users/esaldgut/dev/src/go/src/erickaldama-mail")
	if !strings.Contains(got, `"permissionDecision":"allow"`) {
		t.Fatalf("out-of-scope + bypass must allow (no-op), got: %s", got)
	}
}

func TestDecisionTable(t *testing.T) {
	root := "/Users/esaldgut/dev/src/go/src/erickaldama-mail"
	cases := []struct {
		name    string
		fixture string
		want    string // "allow" or "deny"
	}{
		{"metachar chaining", "fixtures/deny_metachar.json", "deny"},
		{"go test is an engine", "fixtures/deny_go_test.json", "deny"},
		{"aws read allowed", "fixtures/allow_aws_read.json", "allow"},
		{"aws assume-role denied", "fixtures/deny_aws_assume_role.json", "deny"},
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

func TestCdkGoVsNonGo(t *testing.T) {
	wd, _ := os.Getwd()
	goDir := filepath.Join(wd, "fixtures", "cdkjson_go")
	tsDir := filepath.Join(wd, "fixtures", "cdkjson_ts")
	mk := func(cwd, cmd string) string {
		return `{"tool_name":"Bash","tool_input":{"command":"` + cmd + `"},"cwd":"` + cwd + `","permission_mode":"default"}`
	}
	if got := runHook(t, mk(goDir, "cdk synth"), goDir); !strings.Contains(got, `"permissionDecision":"allow"`) {
		t.Fatalf("cdk synth (Go) should allow, got: %s", got)
	}
	if got := runHook(t, mk(tsDir, "cdk synth"), tsDir); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("cdk synth (TS) should deny, got: %s", got)
	}
	if got := runHook(t, mk(goDir, "cdk deploy"), goDir); !strings.Contains(got, `"permissionDecision":"deny"`) {
		t.Fatalf("cdk deploy should deny, got: %s", got)
	}
}

func TestDeniesStsCredentialMinting(t *testing.T) {
	root := "/Users/esaldgut/dev/src/go/src/erickaldama-mail"
	for _, sub := range []string{"get-session-token", "get-federation-token"} {
		fixture := `{"tool_name":"Bash","tool_input":{"command":"aws sts ` + sub + `"},"cwd":"` + root + `","permission_mode":"default"}`
		got := runHook(t, fixture, root)
		if !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("sts %s must deny (credential-minting), got: %s", sub, got)
		}
	}
}
