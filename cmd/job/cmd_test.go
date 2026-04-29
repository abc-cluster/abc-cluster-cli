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
	if nc.Token() != "abc-token" {
		t.Fatalf("expected ABC_TOKEN to be prioritized, got %q", nc.Token())
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

func TestJobHelloAbcCommandDoesNotExist(t *testing.T) {
	cmd := NewCmd()
	helloCmd, _, err := cmd.Find([]string{"hello-abc"})
	if err == nil && helloCmd != nil && helloCmd.Name() == "hello-abc" {
		t.Fatal("did not expect top-level job hello-abc command; use `abc job run hello-abc`")
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
