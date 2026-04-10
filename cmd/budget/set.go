package budget

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

func newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set or update the budget cap for a namespace (requires --cloud)",
		RunE:  runBudgetSet,
	}
	cmd.Flags().String("namespace", utils.EnvOrDefault("ABC_NAMESPACE", "NOMAD_NAMESPACE"),
		"Namespace to configure (required)")
	cmd.Flags().Float64("monthly", 0, "Monthly spend cap in the workspace currency (0 = unlimited)")
	cmd.Flags().String("currency", "USD", "Currency code (e.g. USD, ZAR, EUR)")
	cmd.Flags().Float64("alert-at", 0.8, "Send an alert when spend reaches this fraction of cap (0.0–1.0)")
	cmd.Flags().Float64("block-at", 1.0, "Block new submissions when spend reaches this fraction of cap (0.0–1.0)")
	return cmd
}

func runBudgetSet(cmd *cobra.Command, _ []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)
	ns, _ := cmd.Flags().GetString("namespace")
	if ns == "" {
		ns, _ = cmd.Root().PersistentFlags().GetString("namespace")
	}
	if ns == "" {
		return fmt.Errorf("specify --namespace or set ABC_NAMESPACE")
	}

	monthly, _ := cmd.Flags().GetFloat64("monthly")
	currency, _ := cmd.Flags().GetString("currency")
	alertAt, _ := cmd.Flags().GetFloat64("alert-at")
	blockAt, _ := cmd.Flags().GetFloat64("block-at")

	req := map[string]interface{}{
		"Namespace":  ns,
		"MonthlyCap": monthly,
		"Currency":   currency,
		"AlertAt":    alertAt,
		"BlockAt":    blockAt,
	}
	if err := nc.CloudSetBudget(cmd.Context(), ns, req); err != nil {
		return fmt.Errorf("setting budget for %q: %w", ns, err)
	}

	if monthly == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Budget cap for %q removed (unlimited).\n", ns)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "  Budget cap for %q set to %.2f %s/month.\n", ns, monthly, currency)
	}
	return nil
}
