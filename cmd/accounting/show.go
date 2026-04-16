package accounting

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

type BudgetDetail struct {
	Namespace    string  `json:"Namespace"`
	MonthlyCap   float64 `json:"MonthlyCap"`
	CurrentSpend float64 `json:"CurrentSpend"`
	Currency     string  `json:"Currency"`
	Status       string  `json:"Status"`
	AlertAt      float64 `json:"AlertAt"`
	BlockAt      float64 `json:"BlockAt"`
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show spend cap detail for a namespace (requires --cloud)",
		RunE:  runBudgetShow,
	}
	cmd.Flags().String("namespace", utils.EnvOrDefault("ABC_NAMESPACE", "NOMAD_NAMESPACE"),
		"Namespace to show budget for")
	return cmd
}

func runBudgetShow(cmd *cobra.Command, _ []string) error {
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

	var detail BudgetDetail
	if err := nc.CloudGetBudget(cmd.Context(), ns, &detail); err != nil {
		return fmt.Errorf("fetching budget for %q: %w", ns, err)
	}

	ccy := detail.Currency
	if ccy == "" {
		ccy = "USD"
	}
	cap := "unlimited"
	if detail.MonthlyCap > 0 {
		cap = fmt.Sprintf("%.2f %s/month", detail.MonthlyCap, ccy)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Namespace      %s\n", detail.Namespace)
	fmt.Fprintf(out, "  Cap            %s\n", cap)
	fmt.Fprintf(out, "  Current spend  %.2f %s\n", detail.CurrentSpend, ccy)
	fmt.Fprintf(out, "  Status         %s\n", detail.Status)
	if detail.AlertAt > 0 {
		fmt.Fprintf(out, "  Alert at       %.0f%%\n", detail.AlertAt*100)
	}
	if detail.BlockAt > 0 {
		fmt.Fprintf(out, "  Block at       %.0f%%\n", detail.BlockAt*100)
	}
	return nil
}
