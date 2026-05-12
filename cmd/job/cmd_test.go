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

// Nomad-passthrough flags read vendor env vars directly. The ABC-namespace
// names (ABC_API_ADDRESS / ABC_API_TOKEN) address the abc-cluster API, not the
// backing Nomad cluster — they are distinct concepts and do NOT substitute
// for NOMAD_ADDR / NOMAD_TOKEN.
func TestNomadAddrFromNOMAD_ADDR(t *testing.T) {
	t.Setenv("NOMAD_ADDR", "http://nomad.example")
	runCmd := resolveRunCmd(t)
	if got := nomadAddrFromCmd(runCmd); got != "http://nomad.example" {
		t.Fatalf("expected NOMAD_ADDR to win, got %q", got)
	}
}

func TestNomadTokenFromNOMAD_TOKEN(t *testing.T) {
	t.Setenv("NOMAD_TOKEN", "nomad-token")
	runCmd := resolveRunCmd(t)
	nc := nomadClientFromCmd(runCmd)
	if nc.Token() != "nomad-token" {
		t.Fatalf("expected NOMAD_TOKEN, got %q", nc.Token())
	}
}

func TestJobLogsCommandFlags(t *testing.T) {
	cmd := NewCmd()
	logsCmd, _, err := cmd.Find([]string{"logs"})
	if err != nil {
		t.Fatalf("expected logs command to exist: %v", err)
	}
	if logsCmd.Flags().Lookup("output") == nil {
		t.Fatal("expected --output flag on job logs command")
	}
	if logsCmd.Flags().Lookup("error") == nil {
		t.Fatal("expected --error flag on job logs command")
	}
}

func TestJobHelloClusterCommandDoesNotExist(t *testing.T) {
	cmd := NewCmd()
	helloCmd, _, err := cmd.Find([]string{"hello-cluster"})
	if err == nil && helloCmd != nil && helloCmd.Name() == "hello-cluster" {
		t.Fatal("did not expect top-level job hello-cluster command; use `abc job run hello-cluster`")
	}
}

func TestNewLogsCmd_TypeFlag(t *testing.T) {
	cmd := NewLogsCmd()
	if cmd == nil {
		t.Fatalf("expected NewLogsCmd() to return a command")
	}
	flag := cmd.Flags().Lookup("type")
	if flag == nil {
		t.Fatal("expected --type flag on logs command")
	}
	if flag.DefValue != "stdout" {
		t.Fatalf("expected default --type stdout, got %q", flag.DefValue)
	}
}
