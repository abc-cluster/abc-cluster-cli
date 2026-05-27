// Package accounting implements the "abc accounting" command group —
// namespace budget management (admission-gate spend caps via the cloud
// gateway).
//
// The read-side showback verbs (`abc report` and friends) are
// consolidated under the top-level `abc report` per the
// cli-verb-tree-restructure spec; this package owns only the write-side
// budget management surface.
//
// Subverbs: `abc accounting {list,set,show}`. Available at grove+ and
// cloud tiers; rejected at seedling via the capability layer. The
// `budget` subnoun used between 2026-05-10 and 2026-05-27 was dropped —
// see brainstorms/cli-ux-harmonization/2026-05-27-drop-budget-noun-
// from-accounting.md for the rationale.
package accounting

import (
	"fmt"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/capability"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// accountingCapabilities declares what `abc accounting {list,set,show}`
// requires. Budget caps are an admission gate; admission flows through
// Jurist (abc-policy-svc), and the cap state itself lives in the cloud
// gateway (abc-controller-svc). At seedling neither service is present,
// so capability.Require returns the standard rejection message.
var accountingCapabilities = capability.Required{
	AllOf: []capability.Need{
		{Service: "abc-controller-svc"},
		{Service: "abc-policy-svc"},
	},
}

// loadActiveContextCapabilities returns the active context's Capabilities
// map. Mirrors the helper in cmd/report; nil result is fine —
// capability.Require treats it as "Services map empty".
func loadActiveContextCapabilities() *config.Capabilities {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return nil
	}
	return cfg.Contexts[ctxName].Capabilities
}

// requireAccountingCapabilities runs the capability gate for every
// accounting subverb. Returns the capability.Require failure error
// verbatim when the active context lacks the required services.
func requireAccountingCapabilities(_ *cobra.Command) error {
	caps := loadActiveContextCapabilities()
	decision := capability.Require(accountingCapabilities, caps, nil)
	if decision.Failed() {
		return decision.AsError()
	}
	return nil
}

// NewCmd returns the "accounting" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounting",
		Aliases: []string{"cost"},
		Short:   "Accounting: namespace budget management",
		Long: `Manage per-namespace monthly spend caps and admission-gate thresholds
via the cloud gateway.

  abc --cloud accounting list
  abc --cloud accounting show --namespace=nf-genomics-lab
  abc --cloud accounting set --namespace=nf-genomics-lab --monthly=500

Available at grove+ and cloud tiers (requires abc-controller-svc and
abc-policy-svc). At seedling, these verbs reject with the standard
capability message.

For showback (spend, emissions, runs), use the top-level ` + "`abc report`" + `
verb. Rate-card overrides live under ` + "`abc config accounting set`" + `.

Legacy: ` + "`abc cost …`" + ` is an alias for ` + "`abc accounting …`" + `.`,
		// Force "unknown command" for any subverb name that isn't one
		// of {list,set,show}. Without this RunE, cobra falls back to
		// printing help with exit 0 on unknown subcommands; we want
		// the standard error so script authors see the typo. Also
		// catches stale invocations of `abc accounting budget` (the
		// noun was dropped 2026-05-27).
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if args[0] == "budget" {
					return fmt.Errorf(
						"the `budget` subnoun was dropped on 2026-05-27; "+
							"use `abc accounting %s` directly. "+
							"See brainstorms/cli-ux-harmonization/2026-05-27-drop-budget-noun-from-accounting.md",
						subverbHint(args))
				}
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().String("budget", "",
		"optional budget / allocation id (for future filters; reserved when gateway supports it)")
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("NOMAD_ADDR"),
		"Cloud gateway address (or set NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("NOMAD_TOKEN"),
		"Auth token (or set NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("NOMAD_REGION"),
		"Nomad region (or set NOMAD_REGION)")

	cmd.AddCommand(
		newListCmd(),
		newShowCmd(),
		newSetCmd(),
	)
	return cmd
}

// subverbHint returns "<verb>" when the user typed `abc accounting
// budget <verb>` so the error pointing them at the new shape mentions
// the right subcommand. Falls back to "list/set/show" when no
// recognisable subverb follows.
func subverbHint(args []string) string {
	if len(args) >= 2 {
		switch args[1] {
		case "list", "set", "show":
			return args[1]
		}
	}
	return "list/set/show"
}

// nomadClientFromCmd builds a Nomad client from the command's
// persistent flags (defined on the parent `accounting` command),
// falling back to config defaults.
func nomadClientFromCmd(cmd *cobra.Command) *utils.NomadClient {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ := cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	if addr == "" || token == "" || region == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

// requireCloud rejects when not invoked with --cloud (or
// ABC_CLI_CLOUD_MODE=1). Budget management flows through the cloud
// gateway today; the capability-layer rejection at seedling is a
// separate gate.
func requireCloud(cmd *cobra.Command) error {
	if !utils.CloudFromCmd(cmd) {
		return fmt.Errorf("accounting commands require --cloud (or ABC_CLI_CLOUD_MODE=1)")
	}
	return nil
}
