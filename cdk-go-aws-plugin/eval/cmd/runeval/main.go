//go:build eval

package main

import (
	"fmt"
	"os"
	"time"

	"erickaldama-mail/cdk-go-aws-plugin/eval"
)

func main() {
	// k must be a multiple of 3 so Pass@3 is computed over whole triples.
	dir := os.Getenv("EVAL_DIR")
	if dir == "" {
		dir = "."
	}
	if err := eval.RunEval(dir, 9, time.Now().UTC().Format("2006-01-02")); err != nil {
		fmt.Fprintln(os.Stderr, "eval error:", err)
		os.Exit(1)
	}
}
