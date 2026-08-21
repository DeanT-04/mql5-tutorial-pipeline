// Command fidelity compares an assembled .mq5 output against a ground-truth
// file and prints the deterministic token-F1 score.
//
// Usage: go run ./cmd/fidelity <golden.mq5> <run-dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/fidelity"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fidelity <golden.mq5> <run-dir>")
		os.Exit(2)
	}
	golden, err := os.ReadFile(os.Args[1]) // #nosec G304,G703 -- user-specified paths by design (offline scoring tool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fidelity: %v\n", err)
		os.Exit(1)
	}
	matches, err := filepath.Glob(filepath.Join(os.Args[2], "out", "*.mq5"))
	if err != nil || len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "fidelity: no .mq5 files in run out/")
		os.Exit(1)
	}
	worst := fidelity.Result{}
	for _, m := range matches {
		out, err := os.ReadFile(m) // #nosec G304,G703 -- paths come from the run's out/ glob
		if err != nil {
			fmt.Fprintf(os.Stderr, "fidelity: %v\n", err)
			os.Exit(1)
		}
		r := fidelity.Compare(string(golden), string(out))
		fmt.Printf("%s: %s\n", filepath.Base(m), r)
		if r.F1 < worst.F1 || worst.GoldenTokens == 0 && r.GoldenTokens > 0 {
			worst = r
		}
	}
	fmt.Printf("OVERALL %s\n", worst)
	if worst.F1 >= 0.95 {
		os.Exit(0)
	}
	os.Exit(1)
}
