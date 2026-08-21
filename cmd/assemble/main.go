// Command assemble replays a run's extracted events into .mq5 files under the
// run's out/ directory, skipping work when inputs are unchanged.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/stages"
)

const usage = `assemble — deterministically replay extracted events into .mq5 files

Usage:
  assemble --run DIR
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(stderr, usage)
			return 2
		}
		return 2
	}
	if *runDir == "" {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	parent, id := filepath.Dir(*runDir), filepath.Base(*runDir)
	r, err := runstore.New(parent, id)
	if err != nil {
		return fail(stderr, err)
	}
	res, outDir, err := stages.Assemble(r)
	if err != nil {
		return fail(stderr, err)
	}
	if res == nil {
		_, _ = fmt.Fprintf(stdout, "assemble: up to date (%s)\n", outDir)
		return 0
	}
	for _, rec := range res.Records {
		if rec.Status == "skipped" {
			_, _ = fmt.Fprintf(stderr, "assemble: conflict %s %s seq %d on %s: %s\n",
				rec.ChunkID, rec.Op, rec.Seq, rec.File, rec.Detail)
		}
	}
	_, _ = fmt.Fprintf(stdout, "assemble: %d ops applied, %d skipped -> %d file(s) in %s\n",
		res.Applied, res.Skipped, len(res.Files), outDir)
	return 0
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "assemble: %v\n", err)
	return 1
}
