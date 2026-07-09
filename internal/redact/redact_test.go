package redact

import "testing"

func TestRedactSecretShaped(t *testing.T) {
	cases := map[string]string{
		"key AKIAIOSFODNN7EXAMPLE here":                                 "key <REDACTED-SECRET> here",
		"token ghp_1234567890abcdefABCDEF1234567890abcd":                "token <REDACTED-SECRET>",
		"pat github_pat_11ABCDEFG0aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890": "pat <REDACTED-SECRET>",             // realistic PAT shape (>20 chars after prefix)
		"prose github_pat_x is not a token":                             "prose github_pat_x is not a token", // short → NOT masked (regex matches real shape only)
		"slack xoxb-123-456-abc":                                        "slack <REDACTED-SECRET>",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactThirdPartyEmail(t *testing.T) {
	got := Redact("contacta a juan.perez@otraempresa.com mañana")
	if got != "contacta a <REDACTED-EMAIL> mañana" {
		t.Errorf("email not redacted: %q", got)
	}
}

func TestRedactLeavesCleanTextUntouched(t *testing.T) {
	clean := "Hola, ¿nos vemos el martes para revisar el reporte?"
	if Redact(clean) != clean {
		t.Errorf("clean text altered: %q", Redact(clean))
	}
}

func TestCanarySeededSecretIsCaught(t *testing.T) {
	// canary: a known fake secret MUST be caught every build (gate working, not gate broken-open).
	if Redact("AKIAFAKEFAKEFAKE1234") == "AKIAFAKEFAKEFAKE1234" {
		t.Fatal("canary leaked: AKIA-shaped token not redacted")
	}
}
