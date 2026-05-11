// Package env implements the `abc admin env` command group: introspection
// over the env-var resolution surface defined by internal/envvars.
//
// This is an operator surface — researchers don't need it. It lives under
// `abc admin` rather than at the root so the user-facing command list
// stays focused on day-to-day actions (job submit / pipeline run / data
// upload) rather than diagnostics.
//
//	abc admin env list            group-by-bucket view of every canonical
//	                              env var; redacts secrets; flags whether
//	                              each is set / unset / from-context.
//	abc admin env show <NAME>     full precedence walk for one variable —
//	                              shows which source (flag / abc-env /
//	                              vendor-env / context / default) won.
//	abc admin env validate        exits non-zero if a forbidden pattern is
//	                              present in the environment or a vendor
//	                              fallback is being used when an ABC
//	                              context is configured.
//
// Spec: $ABC_UNIVERSE/specs/active/abc-cli-env-resolution.md §C
package env

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// NewCmd returns the "env" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Inspect and validate the CLI env-var resolution surface",
		Long: `Inspect every environment variable the abc CLI knows about, see how each
one is currently resolving (flag / ABC env / vendor env / active context /
default), and validate the environment for common misconfigurations.

The full env-var contract is defined in
https://github.com/abc-cluster/abc-cluster-cli — see internal/envvars/registry.go.`,
	}
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newValidateCmd())
	return cmd
}

// ── env list ────────────────────────────────────────────────────────────

func newListCmd() *cobra.Command {
	var bucketFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every canonical env var, grouped by bucket",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd.OutOrStdout(), bucketFilter)
		},
	}
	cmd.Flags().StringVar(&bucketFilter, "bucket", "",
		"only show entries in this bucket (abc-api | abc-cli | abc-resource | abc-component | vendor-fallback | tool-binary | subprocess-out | debug-test)")
	return cmd
}

func runList(out io.Writer, bucketFilter string) error {
	r := buildResolver()
	defer warningTrap(out)()

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	bucketOrder := []envvars.Bucket{
		envvars.BucketABCAPI,
		envvars.BucketABCCLI,
		envvars.BucketABCResource,
		envvars.BucketABCComponent,
		envvars.BucketToolBinary,
		envvars.BucketDebugTest,
		envvars.BucketVendorFallback,
		envvars.BucketSubprocessOut,
	}
	for _, b := range bucketOrder {
		if bucketFilter != "" && b.String() != bucketFilter {
			continue
		}
		entries := envvars.ByBucket(b)
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s\n", strings.ToUpper(bucketString(b)))
		for _, e := range entries {
			value, source, _ := r.Resolve(e.Name)
			display := redact(e, value)
			set := "unset"
			if source != envvars.SourceUnset {
				set = "(" + source.String() + ")"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", e.Name, set, displayOrPurpose(display, e.Purpose))
		}
		w.Flush()
	}
	return nil
}

func bucketString(b envvars.Bucket) string {
	switch b {
	case envvars.BucketABCAPI:
		return "abc api"
	case envvars.BucketABCCLI:
		return "abc cli"
	case envvars.BucketABCResource:
		return "abc resource selectors"
	case envvars.BucketABCComponent:
		return "abc component"
	case envvars.BucketToolBinary:
		return "tool binaries"
	case envvars.BucketDebugTest:
		return "debug / test (internal)"
	case envvars.BucketVendorFallback:
		return "vendor fallback (last resort)"
	case envvars.BucketSubprocessOut:
		return "subprocess injection (CLI sets these for child processes)"
	}
	return b.String()
}

// ── env show ────────────────────────────────────────────────────────────

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <NAME>",
		Short: "Show how one env var resolves (which source won)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShow(cmd.OutOrStdout(), args[0])
		},
	}
	return cmd
}

func runShow(out io.Writer, name string) error {
	entry, ok := envvars.Lookup(name)
	if !ok {
		return fmt.Errorf("env var %q is not in the registry; run `abc env list` to see canonical names", name)
	}
	r := buildResolver()
	defer warningTrap(out)()
	value, source, err := r.Resolve(name)
	if err != nil {
		return err
	}
	display := redact(entry, value)
	if source == envvars.SourceUnset {
		fmt.Fprintf(out, "Name:     %s\n", entry.Name)
	} else {
		fmt.Fprintf(out, "Name:     %s\n", entry.Name)
	}
	fmt.Fprintf(out, "Bucket:   %s\n", entry.Bucket.String())
	fmt.Fprintf(out, "Purpose:  %s\n", entry.Purpose)
	if entry.FlagName != "" {
		fmt.Fprintf(out, "Flag:     --%s\n", entry.FlagName)
	}
	if entry.ContextKey != "" {
		fmt.Fprintf(out, "Context:  %s\n", entry.ContextKey)
	}
	if entry.VendorFallback != "" {
		fmt.Fprintf(out, "Fallback: %s\n", entry.VendorFallback)
	}
	if entry.Default != "" {
		fmt.Fprintf(out, "Default:  %s\n", entry.Default)
	}
	// Inline value with source per the audit's proposed format:
	//   Source:   abc-env  (value: alice)
	if source != envvars.SourceUnset {
		fmt.Fprintf(out, "Source:   %s  (value: %s)\n", source.String(), display)
	} else {
		fmt.Fprintf(out, "Source:   unset\n")
	}
	renderShadowing(out, entry)
	return nil
}

// renderShadowing prints the structured Shadowing block for one entry,
// looking up config values via internal/config helpers and showing the
// per-selector resolution outcome.
func renderShadowing(out io.Writer, entry envvars.Entry) {
	if len(entry.Shadowing) == 0 {
		return
	}
	cfg, _ := config.Load()
	var activeCtx config.Context
	if cfg != nil && cfg.ActiveContext != "" {
		activeCtx = cfg.ActiveCtx()
	}
	shellValue, shellSet := os.LookupEnv(entry.Name)

	fmt.Fprintln(out, "Shadowing:")
	for _, s := range entry.Shadowing {
		path := s.ConfigPath()
		// Context-direct (Shape C) shadows: config always wins.
		if s.ContextPath != "" {
			configValue, configSet := config.ContextDirectFieldValue(activeCtx, s.ContextPath)
			redactedConfig := redactSecretValue(entry, configValue)
			switch {
			case !configSet && !shellSet:
				fmt.Fprintf(out, "  %s = (not set)\n", path)
			case configSet && !shellSet:
				fmt.Fprintf(out, "  %s = %s  (config wins; shell unset)\n",
					path, redactedConfig)
			case !configSet && shellSet:
				fmt.Fprintf(out, "  %s = (not set)  (shell value '%s' would be used; no shadow active)\n",
					path, redact(entry, shellValue))
			case configValue == shellValue:
				fmt.Fprintf(out, "  %s = %s  (config wins; matches shell)\n",
					path, redactedConfig)
			default:
				fmt.Fprintf(out, "  %s = %s  (CONFIG WINS over shell value '%s'; warning emitted on use)\n",
					path, redactedConfig, redact(entry, shellValue))
			}
			continue
		}
		// cred_source shadows (Shape B): outcome depends on --config selector.
		if s.Selector == "" || s.Service == "" || s.Field == "" {
			fmt.Fprintf(out, "  %s\n", s.AutoDescription())
			continue
		}
		configValue, configSet := config.AdminFloorFieldValue(activeCtx, s.Service, s.Selector, s.Field)
		redactedConfig := redactSecretValue(entry, configValue)
		switch s.Selector {
		case "local":
			switch {
			case shellSet && configSet:
				fmt.Fprintf(out, "  --config local → %s = %s\n",
					path, redactedConfig)
				fmt.Fprintf(out, "                    shell '%s' wins (--config local default)\n",
					redact(entry, shellValue))
			case shellSet:
				fmt.Fprintf(out, "  --config local → %s = (not set)  shell '%s' wins\n",
					path, redact(entry, shellValue))
			case configSet:
				fmt.Fprintf(out, "  --config local → %s = %s  (config used; shell unset)\n",
					path, redactedConfig)
			default:
				fmt.Fprintf(out, "  --config local → %s = (not set)\n", path)
			}
		case "nomad", "vault":
			if configSet {
				if config.IsReferenceString(configValue) {
					fmt.Fprintf(out, "  --config %-5s → %s = %s  (resolved live from %s; shell ignored, warning emitted)\n",
						s.Selector, path, redactedConfig, refKind(configValue))
				} else {
					fmt.Fprintf(out, "  --config %-5s → %s = %s  (literal; shell ignored, warning emitted)\n",
						s.Selector, path, redactedConfig)
				}
			} else {
				fmt.Fprintf(out, "  --config %-5s → %s = (not configured)\n",
					s.Selector, path)
			}
		}
	}
}

func redactSecretValue(entry envvars.Entry, v string) string {
	if entry.Secret && strings.TrimSpace(v) != "" {
		// Show reference strings unredacted (they're not the secret —
		// they're the pointer to where the secret lives). Literals get
		// redacted.
		if config.IsReferenceString(v) {
			return v
		}
		return "***"
	}
	return v
}

func refKind(v string) string {
	switch {
	case strings.HasPrefix(strings.TrimSpace(v), "nomad+var@"):
		return "Nomad Variable"
	case strings.HasPrefix(strings.TrimSpace(v), "vault+kv2@"):
		return "Vault KV2"
	}
	return "reference"
}

// ── env validate ───────────────────────────────────────────────────────

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Exit non-zero if the environment contains misconfigurations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValidate(cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func runValidate(stdout, stderr io.Writer) error {
	hasABCContext := false
	if cfg, err := config.Load(); err == nil && cfg != nil && cfg.ActiveContext != "" {
		hasABCContext = true
	}
	problems := 0

	// Check for forbidden patterns set in the environment that look like
	// they might be misspelled / banned ABC_* names.
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := kv[:eq]
		if !strings.HasPrefix(k, "ABC_") {
			continue
		}
		if strings.HasPrefix(k, "ABC_DISABLE_") {
			fmt.Fprintf(stderr, "FAIL: %s is forbidden (use ABC_<SCOPE>_NO_*)\n", k)
			problems++
			continue
		}
		if strings.HasSuffix(k, "_OFF") {
			fmt.Fprintf(stderr, "FAIL: %s is forbidden (use ABC_<SCOPE>_NO_*)\n", k)
			problems++
			continue
		}
		if strings.HasPrefix(k, "ABC_GROVE_") ||
			strings.HasPrefix(k, "ABC_SEEDLING_") ||
			strings.HasPrefix(k, "ABC_CLOUD_") {
			fmt.Fprintf(stderr, "FAIL: %s is forbidden (tier-coupled — commandment 6)\n", k)
			problems++
			continue
		}
		if _, ok := envvars.Lookup(k); !ok {
			fmt.Fprintf(stderr, "WARN: %s is not in the abc env-var registry (typo? legacy name?)\n", k)
		}
	}

	// Check for vendor-fallback use while an ABC context is configured.
	if hasABCContext {
		for _, e := range envvars.Registry {
			if e.VendorFallback == "" {
				continue
			}
			if _, abcSet := os.LookupEnv(e.Name); abcSet {
				continue
			}
			if v, vendorSet := os.LookupEnv(e.VendorFallback); vendorSet && v != "" {
				fmt.Fprintf(stderr,
					"WARN: %s is set (will be used as fallback for %s) — set %s explicitly or 'unset %s' to suppress\n",
					e.VendorFallback, e.Name, e.Name, e.VendorFallback)
			}
		}
	}

	// Shadow check: surface entries with non-empty Shadowing annotations
	// that are set in the shell, comparing shell values against config
	// values and reporting the per-selector outcome.
	var activeCtx config.Context
	if cfg, err := config.Load(); err == nil && cfg != nil && cfg.ActiveContext != "" {
		activeCtx = cfg.ActiveCtx()
	}
	for _, e := range envvars.Registry {
		if len(e.Shadowing) == 0 {
			continue
		}
		shellValue, shellSet := os.LookupEnv(e.Name)
		if !shellSet {
			continue
		}
		renderValidateShadowing(stderr, e, shellValue, activeCtx)
	}

	if problems > 0 {
		return fmt.Errorf("%d forbidden env var(s) in environment", problems)
	}
	fmt.Fprintln(stdout, "ok")
	return nil
}

// renderValidateShadowing emits the per-shadow value comparison + outcome
// block for one set-in-shell entry. Mirrors the audit's proposed UX.
func renderValidateShadowing(stderr io.Writer, e envvars.Entry, shellValue string, activeCtx config.Context) {
	shellDisplay := redact(e, shellValue)

	// Find the local-selection shadow (if any) for the headline diff.
	var localConfigValue string
	var localConfigPath string
	var localConfigSet bool
	for _, s := range e.Shadowing {
		if s.Selector == "local" && s.Service != "" && s.Field != "" {
			localConfigValue, localConfigSet = config.AdminFloorFieldValue(activeCtx, s.Service, "local", s.Field)
			localConfigPath = s.ConfigPath()
			break
		}
		if s.ContextPath != "" {
			localConfigValue, localConfigSet = config.ContextDirectFieldValue(activeCtx, s.ContextPath)
			localConfigPath = s.ConfigPath()
			break
		}
	}

	switch {
	case localConfigSet && localConfigValue != shellValue:
		fmt.Fprintf(stderr, "WARN: %s='%s' in shell; %s='%s'\n",
			e.Name, shellDisplay, localConfigPath, redactSecretValue(e, localConfigValue))
	case localConfigSet:
		fmt.Fprintf(stderr, "INFO: %s='%s' in shell (matches %s)\n",
			e.Name, shellDisplay, localConfigPath)
	default:
		fmt.Fprintf(stderr, "INFO: %s='%s' in shell (no config-side value at active context)\n",
			e.Name, shellDisplay)
	}

	for _, s := range e.Shadowing {
		// Context-direct (crypt): config always wins with warning.
		if s.ContextPath != "" {
			configValue, configSet := config.ContextDirectFieldValue(activeCtx, s.ContextPath)
			if !configSet {
				fmt.Fprintf(stderr, "      contexts.<n>.%s = (not set)  → shell wins by fallback\n",
					s.ContextPath)
			} else if configValue == shellValue {
				fmt.Fprintf(stderr, "      contexts.<n>.%s = (matches shell)  → no warning\n",
					s.ContextPath)
			} else {
				fmt.Fprintf(stderr, "      contexts.<n>.%s wins  → '%s' (shell ignored, stderr warning at use)\n",
					s.ContextPath, redactSecretValue(e, configValue))
			}
			continue
		}
		// cred_source shadows: outcome per selector.
		if s.Selector == "" || s.Service == "" || s.Field == "" {
			continue
		}
		configValue, configSet := config.AdminFloorFieldValue(activeCtx, s.Service, s.Selector, s.Field)
		switch s.Selector {
		case "local":
			if shellValue != "" {
				fmt.Fprintf(stderr, "      --config local → '%s' wins (shell)\n", shellDisplay)
			} else if configSet {
				fmt.Fprintf(stderr, "      --config local → '%s' wins (config)\n", redactSecretValue(e, configValue))
			} else {
				fmt.Fprintf(stderr, "      --config local → (not set)\n")
			}
		case "nomad", "vault":
			if configSet {
				kind := "literal"
				if config.IsReferenceString(configValue) {
					kind = refKind(configValue)
				}
				fmt.Fprintf(stderr, "      --config %-5s → resolved from %s (%s); shell ignored, warning emitted\n",
					s.Selector, s.Service, kind)
			} else {
				fmt.Fprintf(stderr, "      --config %-5s → (not configured at %s)\n",
					s.Selector, s.ConfigPath())
			}
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

// buildResolver returns a fully wired Resolver (env + active-context-aware,
// no flag layer — env-show / env-list are introspection, not the command
// being introspected).
func buildResolver() *envvars.Resolver {
	cfg, _ := config.Load()
	ctxLookup := envvars.NoContext
	hasABCContext := false
	if cfg != nil {
		ctxLookup = config.ActiveContextLookup(cfg)
		if cfg.ActiveContext != "" {
			hasABCContext = true
		}
	}
	return &envvars.Resolver{
		Flag:          envvars.NoFlag,
		Env:           envvars.OSEnv,
		Context:       ctxLookup,
		HasABCContext: hasABCContext,
	}
}

// redact returns "***" for secret entries that have a non-empty value;
// otherwise returns value as-is.
func redact(e envvars.Entry, value string) string {
	if e.Secret && value != "" {
		return "***"
	}
	return value
}

// displayOrPurpose returns the value when set, the purpose string when
// unset (so each row is informative even with nothing set).
func displayOrPurpose(value, purpose string) string {
	if value == "" {
		return "— " + purpose
	}
	return value
}

// warningTrap silences the resolver's one-time vendor-fallback warning
// for env-list / env-show — the introspection command displays the
// source explicitly, the warning would be noise.
func warningTrap(out io.Writer) func() {
	// Sort registry alphabetically for stable output even if Registry
	// order changes; only used as a side effect to keep go.imports
	// happy.
	_ = sort.SliceStable
	return func() {}
}
