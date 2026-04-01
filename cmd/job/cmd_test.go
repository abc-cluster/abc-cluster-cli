package job

import (
	"testing"

	"github.com/spf13/cobra"
)

// resolveRunCmd gets the embedded run command from the job command tree.
func resolveRunCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := NewCmd()
	runCmd, _, err := cmd.Find([]string{"run"})
	if err != nil {
		t.Fatalf("expected run command to exist: %v", err)
	}
	return runCmd
}

func TestNomadAddrEnvPriority(t *testing.T) {
	t.Setenv("ABC_ADDR", "http://abc.example")
	t.Setenv("NOMAD_ADDR", "http://nomad.example")

	runCmd := resolveRunCmd(t)
	addr := nomadAddrFromCmd(runCmd)
	if addr != "http://abc.example" {
		t.Fatalf("expected ABC_ADDR to be prioritized, got %q", addr)
	}
}

func TestNomadTokenEnvPriority(t *testing.T) {
	t.Setenv("ABC_TOKEN", "abc-token")
	t.Setenv("NOMAD_TOKEN", "nomad-token")

	runCmd := resolveRunCmd(t)
	nc := nomadClientFromCmd(runCmd)
	if nc.token != "abc-token" {
		t.Fatalf("expected ABC_TOKEN to be prioritized, got %q", nc.token)
	}
}
