package cache

import "testing"

func TestEscapeFTS(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", `"hello"`},
		{"amazon web services", `"amazon web services"`},
		{`say "hi"`, `"say ""hi"""`},     // embedded quotes doubled
		{"foo AND bar", `"foo AND bar"`}, // AND neutralized (inside quotes = literal)
		{"", `""`},                       // empty → empty phrase
		{"NEAR(a b)", `"NEAR(a b)"`},     // NEAR neutralized
	}
	for _, tc := range cases {
		if got := EscapeFTS(tc.in); got != tc.want {
			t.Errorf("EscapeFTS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
