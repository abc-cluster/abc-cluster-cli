package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Help-output regression tests for the cli-verb-tree-restructure spec
// (§C). TestMain builds a fresh `abc` binary in a tempdir at the start
// of the package's test run; individual tests exec it as a subprocess
// to exercise the real cobra command tree exactly the way an end user
// does. Subprocess matters: cobra's "unknown command" error path only
// surfaces through Execute() at the binary boundary.

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "abc-cli-help-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make tempdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	name := "abc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binPath = filepath.Join(dir, name)

	// Build the module root (..). Tests in this package run from cmd/.
	build := exec.Command("go", "build", "-o", binPath, "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build abc binary: %v\n%s", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// runAbc executes the prebuilt binary with args and returns combined
// stdout+stderr plus the exit error (nil on success).
func runAbc(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if binPath == "" {
		t.Fatal("binPath unset; TestMain must run first")
	}
	cmd := exec.Command(binPath, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestAccountingHelpShowsListSetShow: after the 2026-05-27 budget-noun
// flatten (brainstorms/cli-ux-harmonization/2026-05-27-drop-budget-noun-from-accounting.md),
// `abc accounting --help` lists list/set/show directly. It must NOT
// reintroduce a `budget` subnoun or a `report` child.
func TestAccountingHelpShowsListSetShow(t *testing.T) {
	out, err := runAbc(t, "accounting", "--help")
	if err != nil {
		t.Fatalf("accounting --help failed: %v\n%s", err, out)
	}
	// Cobra formats child commands as "  <name>  <short>" in the
	// Available Commands block.
	for _, want := range []string{"list", "set", "show"} {
		if !strings.Contains(out, "\n  "+want+" ") {
			t.Errorf("accounting --help missing direct child %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"budget", "report"} {
		if strings.Contains(out, "\n  "+gone+" ") {
			t.Errorf("accounting --help still lists removed child %q:\n%s", gone, out)
		}
	}
}

// TestAccountingReportIsUnknownCommand: `abc accounting report` must
// fail with the standard "unknown command" error. Clean break per §C —
// no shim, no forward.
func TestAccountingReportIsUnknownCommand(t *testing.T) {
	out, err := runAbc(t, "accounting", "report")
	if err == nil {
		t.Fatalf("expected non-zero exit; got success:\n%s", out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("expected 'unknown command' error, got:\n%s", out)
	}
	if !strings.Contains(out, "report") {
		t.Errorf("error doesn't reference the rejected verb 'report':\n%s", out)
	}
}

// TestEmissionsIsUnknownCommand: `abc emissions` (no subverb) must be
// rejected as an unknown command. The entire cmd/emissions/ package
// was removed in this branch; the registration is gone from
// cmd/root.go.
func TestEmissionsIsUnknownCommand(t *testing.T) {
	out, err := runAbc(t, "emissions")
	if err == nil {
		t.Fatalf("expected non-zero exit; got success:\n%s", out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("expected 'unknown command' error, got:\n%s", out)
	}
	if !strings.Contains(out, "emissions") {
		t.Errorf("error doesn't reference the rejected verb 'emissions':\n%s", out)
	}
}

// TestEmissionsReportIsUnknownCommand: `abc emissions report` must be
// rejected at the first unknown verb (`emissions`).
func TestEmissionsReportIsUnknownCommand(t *testing.T) {
	out, err := runAbc(t, "emissions", "report")
	if err == nil {
		t.Fatalf("expected non-zero exit; got success:\n%s", out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("expected 'unknown command' error, got:\n%s", out)
	}
}

// TestWaterIsUnknownCommand: top-level `abc water` was folded into
// `abc report water` (2026-06-05); the top-level verb must be gone.
func TestWaterIsUnknownCommand(t *testing.T) {
	out, err := runAbc(t, "water")
	if err == nil {
		t.Fatalf("expected non-zero exit; got success:\n%s", out)
	}
	if !strings.Contains(out, "unknown command") {
		t.Errorf("expected 'unknown command' error, got:\n%s", out)
	}
}

// TestReportEmissionsAndWaterExist: emissions + water now live under
// `abc report` (folded 2026-06-05 from former top-level verbs). Their
// --help must succeed as report subcommands.
func TestReportEmissionsAndWaterExist(t *testing.T) {
	for _, sub := range []string{"emissions", "water"} {
		out, err := runAbc(t, "report", sub, "--help")
		if err != nil {
			t.Errorf("`abc report %s --help` failed: %v\n%s", sub, err, out)
		}
		if !strings.Contains(out, sub) {
			t.Errorf("`abc report %s --help` output doesn't mention %q:\n%s", sub, sub, out)
		}
	}
}
