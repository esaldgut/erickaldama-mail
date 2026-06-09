//go:build eval

package eval

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// RunEval invokes each golden prompt k times via `claude -p`, asserts, computes
// the Pass@1 / Pass@3 metrics, and writes baseline.json under evalDir.
//
// Pass@1 is the fraction of individual runs that passed. Pass@3 is computed over
// consecutive NON-overlapping triples of runs (so k should be a multiple of 3):
// the fraction of triples in which AT LEAST ONE of the 3 runs passed.
//
// Golden files are read via filepath.Join(evalDir, c.file) and baseline.json is
// written to filepath.Join(evalDir, "baseline.json"). Run from the eval dir, or
// set EVAL_DIR; invoked via `go run -tags eval ./cmd/runeval` from the eval dir.
// NOT a unit test (LLM, non-deterministic).
func RunEval(evalDir string, k int, date string) error {
	var out []baselineEntry
	for _, c := range cases {
		prompt, err := os.ReadFile(filepath.Join(evalDir, c.file))
		if err != nil {
			return err
		}
		results := make([]bool, 0, k)
		for range k {
			b, err := exec.Command("claude", "-p", string(prompt)).Output()
			results = append(results, err == nil && c.assertFn(string(b)).Pass)
		}
		// Pass@1 = mean of per-run results.
		p1 := 0.0
		for _, r := range results {
			if r {
				p1++
			}
		}
		if k > 0 {
			p1 /= float64(k)
		}
		// Pass@3 over non-overlapping triples: a triple passes if any of its 3 runs passed.
		triples, passTriples := 0, 0
		for i := 0; i+3 <= len(results); i += 3 {
			triples++
			if results[i] || results[i+1] || results[i+2] {
				passTriples++
			}
		}
		p3 := p1
		if triples > 0 {
			p3 = float64(passTriples) / float64(triples)
		}
		out = append(out, baselineEntry{Prompt: c.file, PassAt1: p1, PassAt3: p3, Date: date})
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile(filepath.Join(evalDir, "baseline.json"), data, 0o644)
}
