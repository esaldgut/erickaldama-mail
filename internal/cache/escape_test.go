package cache

import "testing"

func TestEscapeFTS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"transfer", `"transfer"*`},     // prefix match on last token
		{"amazon web", `"amazon web"*`}, // prefix match on last token of phrase
		{"hello", `"hello"*`},
		{"amazon web services", `"amazon web services"*`},
		{`say "hi"`, `"say ""hi"""*`},     // embedded quotes doubled, prefix after closing quote
		{"foo AND bar", `"foo AND bar"*`}, // AND neutralized (inside quotes = literal), still prefixed
		{"", `""`},                        // empty → empty phrase, NO prefix (TUI clear-filter case)
		{"NEAR(a b)", `"NEAR(a b)"*`},     // NEAR neutralized, still prefixed
	}
	for _, tc := range cases {
		if got := EscapeFTS(tc.in); got != tc.want {
			t.Errorf("EscapeFTS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
