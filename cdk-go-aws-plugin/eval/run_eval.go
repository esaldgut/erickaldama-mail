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

// RunEval invokes each golden prompt k times via `claude -p`, asserts, computes the pass fraction, writes baseline.json.
// Invoked manually / CI nightly: `go run -tags eval .` from the eval dir. NOT a unit test (LLM, non-deterministic).
func RunEval(k int, date string) error {
	var out []baselineEntry
	for _, c := range cases {
		prompt, err := os.ReadFile(c.file)
		if err != nil {
			return err
		}
		var passes int
		for range k {
			b, err := exec.Command("claude", "-p", string(prompt)).Output()
			if err != nil {
				continue // a failed run counts as a non-pass
			}
			if c.assertFn(string(b)).Pass {
				passes++
			}
		}
		frac := 0.0
		if k > 0 {
			frac = float64(passes) / float64(k)
		}
		out = append(out, baselineEntry{Prompt: c.file, PassAt1: frac, PassAt3: frac, Date: date})
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile("baseline.json", data, 0o644)
}

func main() {
	if err := RunEval(3, time.Now().UTC().Format("2006-01-02")); err != nil {
		fmt.Fprintln(os.Stderr, "eval error:", err)
		os.Exit(1)
	}
}
