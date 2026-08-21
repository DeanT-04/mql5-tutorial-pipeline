// Command assemble replays a run's extracted events into .mq5 files under the
// run's out/ directory, skipping work when inputs are unchanged.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/assemble"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/events"
	"github.com/DeanT-04/mql5-tutorial-pipeline/internal/runstore"
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
	data, err := r.ReadFileCapped(runstore.EventsJSONL, 256<<20)
	if err != nil {
		return fail(stderr, err)
	}
	inputHash := runstore.HashBytes(data)
	if r.UpToDate(runstore.StageAssemble, inputHash) {
		_, _ = fmt.Fprintf(stdout, "assemble: up to date (%s)\n", filepath.Join(r.Dir(), "out"))
		return 0
	}

	var evts []events.Event
	if err := events.Reader(bytes.NewReader(data), func(rec any) error {
		if e, ok := rec.(events.Event); ok {
			evts = append(evts, e)
		}
		return nil
	}); err != nil {
		return fail(stderr, err)
	}

	res := assemble.Run(evts)

	outDir, err := r.OutDir()
	if err != nil {
		return fail(stderr, err)
	}
	names := make([]string, 0, len(res.Files))
	for name := range res.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(outDir, name)
		if err := runstore.WriteFileAtomic(path, []byte(res.Files[name])); err != nil {
			return fail(stderr, err)
		}
	}

	report := struct {
		Applied int               `json:"applied"`
		Skipped int               `json:"skipped"`
		Files   []string          `json:"files"`
		Ops     []assemble.Record `json:"ops"`
	}{Applied: res.Applied, Skipped: res.Skipped, Files: names, Ops: res.Records}
	reportData, err := json.MarshalIndent(&report, "", "  ")
	if err != nil {
		return fail(stderr, err)
	}
	if err := runstore.WriteFileAtomic(r.Path(runstore.AssemblyReportJSON), append(reportData, '\n')); err != nil {
		return fail(stderr, err)
	}
	if err := r.MarkDone(runstore.StageAssemble, inputHash); err != nil {
		return fail(stderr, err)
	}

	_, _ = fmt.Fprintf(stdout, "assemble: %d ops applied, %d skipped -> %d file(s) in %s\n",
		res.Applied, res.Skipped, len(names), outDir)
	for _, rec := range res.Records {
		if rec.Status == "skipped" {
			_, _ = fmt.Fprintf(stderr, "assemble: conflict %s %s seq %d on %s: %s\n",
				rec.ChunkID, rec.Op, rec.Seq, rec.File, rec.Detail)
		}
	}
	return 0
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "assemble: %v\n", err)
	return 1
}
