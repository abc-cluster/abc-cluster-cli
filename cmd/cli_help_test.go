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

// TestAccountingHelpShowsOnlyBudget: `abc accounting --help` must list
// `budget` as a child command and must NOT list `report`, `list`,
// `set`, or `show` as direct children (those moved under `budget` per
// §A; report deleted entirely).
func TestAccountingHelpShowsOnlyBudget(t *testing.T) {
	out, err := runAbc(t, "accounting", "--help")
	if err != nil {
		t.Fatalf("accounting --help failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "budget") {
		t.Errorf("accounting --help missing 'budget' subcommand:\n%s", out)
	}
	// The removed verbs must not appear as direct children of
	// accounting. Cobra formats child commands as "  <name>  <short>"
	// in the Available Commands block.
	for _, removed := range []string{"report", "list", "set", "show"} {
		marker := "\n  " + removed + " "
		if strings.Contains(out, marker) {
			t.Errorf("accounting --help still lists removed direct child %q:\n%s", removed, out)
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
