package namespace

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or update a namespace (requires --sudo)",
		RunE:  runNsCreate,
	}
	cmd.Flags().String("name", "", "Namespace name (required)")
	cmd.Flags().String("description", "", "Short description")
	cmd.Flags().String("group", "", "Research group or team name (stored in meta)")
	cmd.Flags().String("contact", "", "Contact email for the namespace owner (stored in meta)")
	cmd.Flags().String("priority", "", "Job priority for this namespace (stored in meta)")
	cmd.Flags().String("node-pool", "", "Default node pool for this namespace (stored in meta)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func runNsCreate(cmd *cobra.Command, _ []string) error {
	if err := requireSudo(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	name, _ := cmd.Flags().GetString("name")
	desc, _ := cmd.Flags().GetString("description")

	meta := map[string]string{}
	for _, k := range []string{"group", "contact", "priority", "node-pool"} {
		v, _ := cmd.Flags().GetString(k)
		if v != "" {
			meta[k] = v
		}
	}

	ns := &utils.NomadNamespace{
		Name:        name,
		Description: desc,
		Meta:        meta,
	}
	if err := nc.ApplyNamespace(cmd.Context(), ns); err != nil {
		return fmt.Errorf("creating namespace %q: %w", name, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "  Namespace %q applied.\n", name)
	return nil
}
