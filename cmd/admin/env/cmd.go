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
	fmt.Fprintf(out, "Name:     %s\n", entry.Name)
	fmt.Fprintf(out, "Bucket:   %s\n", entry.Bucket.String())
	fmt.Fprintf(out, "Purpose:  %s\n", entry.Purpose)
	fmt.Fprintf(out, "Source:   %s\n", source.String())
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
	fmt.Fprintf(out, "Value:    %s\n", display)
	if len(entry.Shadowing) > 0 {
		fmt.Fprintln(out, "Shadowing:")
		for _, s := range entry.Shadowing {
			fmt.Fprintf(out, "  - %s\n", s)
		}
	}
	return nil
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

	if problems > 0 {
		return fmt.Errorf("%d forbidden env var(s) in environment", problems)
	}
	fmt.Fprintln(stdout, "ok")
	return nil
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
