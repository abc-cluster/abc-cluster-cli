package config

import (
	"fmt"
	"os"
	"strings"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newAccountingCmd returns the `abc config accounting` sub-verb group
// per spec abc-emissions-accounting §E.
func newAccountingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accounting",
		Short: "Manage per-context accounting (cost) rates in ~/.abc/config.yaml",
		Long: `Manage the per-context accounting block (Layer 1 of the rate-card resolver).

Acceptable keys:
  currency                          ISO-4217 alpha (e.g. ZAR, USD)
  cost.cpu_hour                     ZAR per CPU·hour (≥ 0)
  cost.gpu_hour                     ZAR per GPU·hour (≥ 0)
  cost.memory_gb_hour               ZAR per GB·hour memory (≥ 0)
  cost.storage_gb_month             reserved (Phase 2; accepted by set, unused by reports)
  cost.egress_gb                    reserved (Phase 2)

Examples:
  abc config accounting show
  abc config accounting set cost.cpu_hour=0.45 currency=ZAR
  abc config accounting unset cost.gpu_hour`,
	}
	cmd.AddCommand(newAccountingShowCmd())
	cmd.AddCommand(newAccountingSetCmd())
	cmd.AddCommand(newAccountingUnsetCmd())
	return cmd
}

func newAccountingShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the effective accounting rate card (Layer 0 + Layer 1)",
		RunE:  runAccountingShow,
	}
}

func newAccountingSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key>=<value> [<key>=<value> ...]",
		Short: "Set one or more accounting keys for the active context",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runAccountingSet,
	}
}

func newAccountingUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove an accounting key for the active context",
		Args:  cobra.ExactArgs(1),
		RunE:  runAccountingUnset,
	}
}

func runAccountingShow(cmd *cobra.Command, _ []string) error {
	contextName := state.ActiveContextName()
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return err
	}
	card, err := acct.Resolve(acct.ZADefaults(), layer1, acct.FlagOverrides{})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Accounting rate card for context %q:\n\n", contextName)
	printRate(out, "currency", card.Currency.Value, string(card.Currency.Source), card.Currency.Citation, card.Currency.UpdatedAt.IsZero(), card.Currency.UpdatedAt.Format("2006-01-02 15:04"))
	printRateF(out, "cost.cpu_hour", card.Cost.CpuHour)
	printRateF(out, "cost.gpu_hour", card.Cost.GpuHour)
	printRateF(out, "cost.memory_gb_hour", card.Cost.MemoryGbHour)
	printRateF(out, "cost.storage_scratch_gb_hour", card.Cost.StorageScratchGbHour)
	printRateF(out, "cost.storage_persistent_gb_month", card.Cost.StoragePersistentGbMonth)
	printRateF(out, "cost.storage_egress_gb", card.Cost.StorageEgressGb)
	return nil
}

func runAccountingSet(cmd *cobra.Command, args []string) error {
	contextName := state.ActiveContextName()
	if contextName == "" {
		return fmt.Errorf("no active context set; run 'abc context use <name>' first")
	}
	setKV := map[string]string{}
	for _, raw := range args {
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("argument %q is not key=value", raw)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Validate before writing.
		if _, _, err := acct.ValidateAccountingValue(k, v); err != nil {
			return err
		}
		setKV[k] = v
	}
	// Ensure the config file at least exists (so contextName resolves).
	// Only invoke Create() when the file is missing — calling it
	// unconditionally would round-trip through the typed Config struct
	// which strips unknown YAML blocks (accounting:, emissions:) that
	// previous invocations may have written.
	if _, err := os.Stat(cfg.DefaultConfigPath()); os.IsNotExist(err) {
		_, _ = cfg.Create()
	}
	if err := acct.SetContextBlock("accounting", contextName, setKV, nil); err != nil {
		return err
	}
	if quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet"); !quiet {
		for k, v := range setKV {
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ Set accounting.%s = %s for context %q\n", k, v, contextName)
		}
	}
	return nil
}

func runAccountingUnset(cmd *cobra.Command, args []string) error {
	contextName := state.ActiveContextName()
	if contextName == "" {
		return fmt.Errorf("no active context set")
	}
	key := args[0]
	if !isAccountingKey(key) {
		return fmt.Errorf("unknown accounting key %q (allowed: %v)", key, acct.AccountingKeys)
	}
	// Was the key set?
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return err
	}
	if _, was := layer1.Accounting[key]; !was {
		fmt.Fprintf(cmd.ErrOrStderr(), "(no override; uses built-in)\n")
		return nil
	}
	if err := acct.SetContextBlock("accounting", contextName, nil, []string{key}); err != nil {
		return err
	}
	if quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet"); !quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ Unset accounting.%s for context %q\n", key, contextName)
	}
	return nil
}

func isAccountingKey(k string) bool {
	for _, x := range acct.AccountingKeys {
		if x == k {
			return true
		}
	}
	return false
}
