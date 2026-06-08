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
