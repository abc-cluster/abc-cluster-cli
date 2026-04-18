package compute

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestNomadTokenFlagFromCmdChain_InheritedPersistent(t *testing.T) {
	parent := &cobra.Command{Use: "compute"}
	parent.PersistentFlags().String("nomad-token", "", "")
	if err := parent.PersistentFlags().Set("nomad-token", "from-parent"); err != nil {
		t.Fatal(err)
	}
	child := &cobra.Command{Use: "add"}
	parent.AddCommand(child)

	got := nomadTokenFlagFromCmdChain(child)
	if got != "from-parent" {
		t.Fatalf("got %q want from-parent", got)
	}
}

func TestNomadTokenForNodeAdd_PrefersFlagOverEnv(t *testing.T) {
	t.Setenv("NOMAD_TOKEN", "from-env")

	parent := &cobra.Command{Use: "compute"}
	parent.PersistentFlags().String("nomad-token", "", "")
	if err := parent.PersistentFlags().Set("nomad-token", "from-flag"); err != nil {
		t.Fatal(err)
	}
	child := &cobra.Command{Use: "add"}
	parent.AddCommand(child)

	got := nomadTokenForNodeAdd(child)
	if got != "from-flag" {
		t.Fatalf("got %q want from-flag (flag should win over env when set)", got)
	}
}
