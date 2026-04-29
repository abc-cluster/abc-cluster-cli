package samplesheet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflight_MissingFile(t *testing.T) {
	_, err := Preflight(filepath.Join(t.TempDir(), "does-not-exist.csv"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention 'not found': %v", err)
	}
}

func TestPreflight_EmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.csv")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(p); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-file error, got %v", err)
	}
}

func TestPreflight_HeaderOnlyOK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.csv")
	if err := os.WriteFile(p, []byte("sample,bed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Preflight(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataRows != 0 {
		t.Errorf("DataRows = %d, want 0", res.DataRows)
	}
	if len(res.HeaderCols) != 2 {
		t.Errorf("HeaderCols = %v, want 2", res.HeaderCols)
	}
}

func TestPreflight_WithRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.csv")
	body := "sample,bed,bim\n" +
		"a,a.bed,a.bim\n" +
		"b,b.bed,b.bim\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Preflight(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DataRows != 2 {
		t.Errorf("DataRows = %d, want 2", res.DataRows)
	}
	if string(res.CSVBytes) != body {
		t.Errorf("CSVBytes round-trip mismatch")
	}
	// Pre-flight must NOT enforce file-input column counts (cluster-side
	// owns that). Three columns and two rows is just fine here.
}

func TestPreflight_RaggedRowsAllowed(t *testing.T) {
	// Pre-flight is shallow on purpose — let the cluster-side validator
	// produce one consistent issue list. So a ragged sheet should still
	// pass the local check.
	p := filepath.Join(t.TempDir(), "ragged.csv")
	body := "sample,bed,bim\n" +
		"a,a.bed\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(p); err != nil {
		t.Fatalf("ragged rows should not fail local pre-flight: %v", err)
	}
}

func TestPreflight_GarbageFile(t *testing.T) {
	// CSV reader is lenient (it accepts most byte streams as a single
	// quoted-or-bare field). To exercise the parse-error branch we feed
	// it a definitively bad sequence: a stray closing quote in the
	// middle of an unquoted field.
	p := filepath.Join(t.TempDir(), "bad.csv")
	if err := os.WriteFile(p, []byte("sample,col\nfoo\"bar,baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(p); err == nil {
		t.Fatal("expected parse error on malformed CSV")
	}
}
