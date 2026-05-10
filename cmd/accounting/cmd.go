// Package accounting implements the "abc accounting" command group —
// namespace budget management (admission-gate spend caps via the cloud
// gateway). The read-side showback verb (`abc accounting report` and
// `abc emissions [report]`) is consolidated under the top-level
// `abc report` per the cli-verb-tree-restructure spec.
//
// Subverbs: `abc accounting budget {list,set,show}`. Available at
// grove+ and cloud tiers; rejected at seedling via the capability
// layer.
package accounting

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/accounting/budget"
	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// NewCmd returns the "accounting" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounting",
		Aliases: []string{"cost"},
		Short:   "Accounting: namespace budget management",
		Long: `Manage per-namespace budget caps and admission-gate thresholds via the
cloud gateway.

  abc --cloud accounting budget list
  abc --cloud accounting budget show --namespace=nf-genomics-lab
  abc --cloud accounting budget set --namespace=nf-genomics-lab --monthly=500

For showback (spend, emissions, hours saved), use the top-level
` + "`abc report`" + ` verb.

Legacy: abc cost … is an alias for abc accounting ….`,
		// Force "unknown command" for removed subverbs (report, list,
		// set, show — moved under budget/ per spec §A). Without this
		// RunE, cobra falls back to printing help with exit 0 when
		// given an unknown subcommand. Spec §C requires the standard
		// cobra error; this is the smallest hook that delivers it
		// while preserving the help-on-no-args behaviour.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().String("budget", "",
		"optional budget / allocation id (for future filters; reserved when gateway supports it)")
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Cloud gateway address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Auth token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(budget.NewCmd())
	return cmd
}
