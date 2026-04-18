package compute

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/cobra"
)

func TestProbeCommandArgsAndPassthrough(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "abc", SilenceUsage: true}
	infra := &cobra.Command{Use: "infra", SilenceUsage: true}
	compute := &cobra.Command{Use: "compute", SilenceUsage: true}
	probe := newProbeCmd()
	probe.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}
	compute.AddCommand(probe)
	infra.AddCommand(compute)
	root.AddCommand(infra)

	root.SetOut(bytes.NewBuffer(nil))
	root.SetErr(bytes.NewBuffer(nil))

	t.Run("singleNodeID", func(t *testing.T) {
		root.SetArgs([]string{"infra", "compute", "probe", "n1"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("extraPositionalRejected", func(t *testing.T) {
		root.SetArgs([]string{"infra", "compute", "probe", "n1", "extra"})
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("passthroughAfterDash", func(t *testing.T) {
		root.SetArgs([]string{"infra", "compute", "probe", "n1", "--", "--json", "--foo"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
