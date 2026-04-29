// Package samplesheet implements the `abc module samplesheet` subcommand
// group plus a shallow local pre-flight used by `abc module run
// --samplesheet`. The authoritative samplesheet validation
// (`pipeline-gen --validate-samplesheet`) runs cluster-side; this
// package only catches obviously-malformed CSVs without making the user
// wait for a Nomad allocation.
package samplesheet

import (
	"encoding/csv"
	"fmt"
	"os"
)

// PreflightResult is what the local CSV check returns to the caller. The
// CSV bytes are returned alongside so the caller can hand them straight
// to the cluster job without re-reading from disk.
type PreflightResult struct {
	Path        string
	CSVBytes    []byte
	HeaderCols  []string
	DataRows    int
	HumanReport string
}

// Preflight does a shallow shape check on a user-supplied samplesheet.
// It deliberately does NOT touch meta.yml — the cluster-side validator
// is the source of truth for input-port matching, blank-cell detection,
// and per-column semantics. Catches here:
//
//   - file missing / unreadable
//   - file is empty
//   - file is not parseable as RFC-4180 CSV
//   - header row has zero columns
//
// Anything stricter belongs cluster-side.
func Preflight(path string) (*PreflightResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("samplesheet not found: %s", path)
		}
		return nil, fmt.Errorf("stat samplesheet: %w", err)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("samplesheet is empty: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read samplesheet: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open samplesheet: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	// Allow ragged rows here — let the cluster-side validator complain
	// about unequal column counts so the user gets ONE consistent
	// error report from the authoritative source.
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("samplesheet is not valid CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("samplesheet has no rows: %s", path)
	}
	header := records[0]
	if len(header) == 0 {
		return nil, fmt.Errorf("samplesheet header has no columns: %s", path)
	}
	dataRows := len(records) - 1

	return &PreflightResult{
		Path:        path,
		CSVBytes:    data,
		HeaderCols:  header,
		DataRows:    dataRows,
		HumanReport: fmt.Sprintf("Pre-flight (local): %s exists, %d data row(s), %d column(s). OK", path, dataRows, len(header)),
	}, nil
}
