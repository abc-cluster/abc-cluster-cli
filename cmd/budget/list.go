package budget

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type BudgetStub struct {
	Namespace   string  `json:"Namespace"`
	MonthlyCap  float64 `json:"MonthlyCap"`
	CurrentSpend float64 `json:"CurrentSpend"`
	Currency    string  `json:"Currency"`
	Status      string  `json:"Status"`
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List budgets for all namespaces (requires --cloud)",
		RunE:  runBudgetList,
	}
}

func runBudgetList(cmd *cobra.Command, _ []string) error {
	if err := requireCloud(cmd); err != nil {
		return err
	}
	nc := nomadClientFromCmd(cmd)

	var budgets []BudgetStub
	if err := nc.CloudListBudgets(cmd.Context(), &budgets); err != nil {
		return fmt.Errorf("listing budgets: %w", err)
	}

	if len(budgets) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "  No budgets configured.")
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  %-24s %-12s %-14s %-8s %-10s\n",
		"NAMESPACE", "CAP/MONTH", "CURRENT SPEND", "CCY", "STATUS")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 74))
	for _, b := range budgets {
		ccy := b.Currency
		if ccy == "" {
			ccy = "USD"
		}
		cap := "unlimited"
		if b.MonthlyCap > 0 {
			cap = fmt.Sprintf("%.2f", b.MonthlyCap)
		}
		fmt.Fprintf(out, "  %-24s %-12s %-14.2f %-8s %-10s\n",
			b.Namespace, cap, b.CurrentSpend, ccy, b.Status)
	}
	return nil
}
