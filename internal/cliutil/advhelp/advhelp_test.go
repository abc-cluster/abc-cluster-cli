package advhelp

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// captureStd runs fn with the process's real stdout and stderr redirected.
// SetOut/SetErr cannot be used here: cobra's OutOrStderr() returns the OUT
// writer whenever SetOut has been called, which would hide exactly the
// stream mix-up this test is for.
func captureStd(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wo, we
	fn()
	wo.Close()
	we.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	o, _ := io.ReadAll(ro)
	e, _ := io.ReadAll(re)
	return string(o), string(e)
}

// The footer is the only pointer to the hidden flags. On stderr it vanished
// whenever help was piped (`abc pipeline run --help | grep memory`), which made
// --memory look undocumented rather than tucked away.
func TestFooterIsOnTheSameStreamAsHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Int("memory", 0, "Head job memory in MB")
	Register(cmd, []string{"memory"})
	cmd.SetArgs([]string{"--help"})

	stdout, stderr := captureStd(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stdout, "--help --advanced") {
		t.Errorf("footer must be on stdout with the help it belongs to\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	if strings.Contains(stdout, "--memory") {
		t.Error("an advanced flag must stay hidden from plain --help")
	}
}
