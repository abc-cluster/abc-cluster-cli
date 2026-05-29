package config

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	acct "github.com/abc-cluster/abc-cluster-cli/internal/accounting"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newEmissionsCmd returns the `abc config emissions` sub-verb group
// per spec abc-emissions-accounting §E.
func newEmissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emissions",
		Short: "Manage per-context emissions coefficients in ~/.abc/config.yaml",
		Long: `Manage the per-context emissions block (Layer 1 of the rate-card resolver).

These values are shared by both ` + "`abc emissions`" + ` (CO₂e) and ` + "`abc water`" + ` (freshwater)
since both use the same energy calculation as their base.

Acceptable keys:
  grid_factor_gco2_per_kwh   0–2000 g CO2e/kWh          (abc emissions)
  cpu_w                      watts per CPU (≥ 0)          (both)
  gpu_w                      watts per GPU (≥ 0)          (both)
  memory_gb_w                watts per GB DRAM (≥ 0)      (both)
  pue                        PUE multiplier (1.0–3.0)     (both)
  wue_site                   facility cooling evaporation, 0–10 L/kWh  (abc water)
  grid_water_intensity       grid I_water, 0–50 L/kWh                  (abc water)

Water formula: Water (L) = energy_kWh × (wue_site + grid_water_intensity)

Built-in defaults for Cape Town / Eskom coal:
  wue_site = 1.5 L/kWh   (evaporative cooling midpoint)
  grid_water_intensity = 2.5 L/kWh   (Eskom thermal cooling towers)

Per-node estimates (see brainstorms/water-carbon-scheduling/):
  Belgium (KU Leuven nuclear+wind): wue_site=0.2 grid_water_intensity=0.9
  Kenya (KPLC hydro, uncertain):    wue_site=0.5 grid_water_intensity=15
  Eskom (SA coal, on-prem):         wue_site=1.5 grid_water_intensity=2.5

Examples:
  abc config emissions show
  abc config emissions set pue=1.27 grid_factor_gco2_per_kwh=950
  abc config emissions set wue_site=1.5 grid_water_intensity=2.5
  abc config emissions unset cpu_w`,
	}
	cmd.AddCommand(newEmissionsShowCmd())
	cmd.AddCommand(newEmissionsSetCmd())
	cmd.AddCommand(newEmissionsUnsetCmd())
	return cmd
}

func newEmissionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the effective emissions rate card (Layer 0 + Layer 1)",
		RunE:  runEmissionsShow,
	}
}

func newEmissionsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key>=<value> [<key>=<value> ...]",
		Short: "Set one or more emissions keys for the active context",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runEmissionsSet,
	}
}

func newEmissionsUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove an emissions key for the active context",
		Args:  cobra.ExactArgs(1),
		RunE:  runEmissionsUnset,
	}
}

func runEmissionsShow(cmd *cobra.Command, _ []string) error {
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
	fmt.Fprintf(out, "Emissions rate card for context %q:\n\n", contextName)
	printRateF(out, "grid_factor_gco2_per_kwh", card.Emissions.GridFactorGco2PerKwh)
	printRateF(out, "cpu_w", card.Emissions.CpuW)
	printRateF(out, "gpu_w", card.Emissions.GpuW)
	printRateF(out, "memory_gb_w", card.Emissions.MemoryGbW)
	printRateF(out, "pue", card.Emissions.Pue)
	printRateF(out, "storage_scratch_w_per_tb", card.Emissions.StorageScratchWPerTb)
	printRateF(out, "storage_persistent_w_per_tb", card.Emissions.StoragePersistentWPerTb)
	printRateF(out, "storage_ec_amplification", card.Emissions.StorageEcAmplification)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Water (shared with `abc water`):")
	printRateF(out, "wue_site", card.Emissions.WueSite)
	printRateF(out, "grid_water_intensity", card.Emissions.GridWaterIntensity)
	return nil
}

func runEmissionsSet(cmd *cobra.Command, args []string) error {
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
		if _, err := acct.ValidateEmissionsValue(k, v); err != nil {
			return err
		}
		setKV[k] = v
	}
	// Only invoke Create() when the config file is missing — calling it
	// unconditionally would round-trip through the typed Config struct
	// which strips unknown YAML blocks (accounting:, emissions:) that
	// previous invocations may have written.
	if _, err := os.Stat(cfg.DefaultConfigPath()); os.IsNotExist(err) {
		_, _ = cfg.Create()
	}
	if err := acct.SetContextBlock("emissions", contextName, setKV, nil); err != nil {
		return err
	}
	if quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet"); !quiet {
		for k, v := range setKV {
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ Set emissions.%s = %s for context %q\n", k, v, contextName)
		}
	}
	return nil
}

func runEmissionsUnset(cmd *cobra.Command, args []string) error {
	contextName := state.ActiveContextName()
	if contextName == "" {
		return fmt.Errorf("no active context set")
	}
	key := args[0]
	if !isEmissionsKey(key) {
		return fmt.Errorf("unknown emissions key %q (allowed: %v)", key, acct.EmissionsKeys)
	}
	layer1, err := acct.LoadLayer1(contextName)
	if err != nil {
		return err
	}
	if _, was := layer1.Emissions[key]; !was {
		fmt.Fprintf(cmd.ErrOrStderr(), "(no override; uses built-in)\n")
		return nil
	}
	if err := acct.SetContextBlock("emissions", contextName, nil, []string{key}); err != nil {
		return err
	}
	if quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet"); !quiet {
		fmt.Fprintf(cmd.ErrOrStderr(), "✓ Unset emissions.%s for context %q\n", key, contextName)
	}
	return nil
}

func isEmissionsKey(k string) bool {
	for _, x := range acct.EmissionsKeys {
		if x == k {
			return true
		}
	}
	return false
}

// ----- shared rate-card pretty-printer helpers -----

func printRate(w io.Writer, key, value, source, citation string, ts0 bool, mtimeStr string) {
	prov := citation
	if prov == "" {
		if source == "local" && !ts0 {
			prov = "~/.abc/config.yaml mtime " + mtimeStr + "  [advisory]"
		} else if source == "flag" {
			prov = "this invocation  [advisory]"
		}
	}
	fmt.Fprintf(w, "  %-28s  %-10s  %-10s  (%s)\n", key, value, source, prov)
}

func printRateF(w io.Writer, key string, rv acct.RateValue) {
	val := fmt.Sprintf("%g", rv.Value)
	mtime := ""
	if !rv.UpdatedAt.IsZero() {
		mtime = rv.UpdatedAt.Format("2006-01-02 15:04")
	}
	printRate(w, key, val, string(rv.Source), rv.Citation, rv.UpdatedAt.IsZero(), mtime)
}

// keep time imported.
var _ = time.Time{}
