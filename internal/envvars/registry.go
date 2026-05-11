// Package envvars is the single source of truth for environment-variable
// resolution in abc-cluster-cli.
//
// Spec: $ABC_UNIVERSE/specs/active/abc-cli-env-resolution.md
// Design: $ABC_UNIVERSE/brainstorms/cli-env-resolution/2026-05-11-revision-abc-first-vendor-fallback.md
//
// Model: ABC-first, vendor-fallback. ABC_* is the canonical user-facing
// surface; vendor env vars (NOMAD_*, VAULT_*, AWS_*, CONSUL_*) are read
// silently as last-resort fallback only. The CLI is responsible for
// constructing vendor env vars when shelling out to subprocesses (see
// subprocess.go) rather than inheriting them blindly from the parent shell.
//
// Naming scopes (commandment 4):
//
//   - ABC_API_*        transport / auth / identity
//   - ABC_CLI_*        local CLI config, behaviour, output, telemetry,
//                      debug, test, trace, log-level
//   - ABC_<COMPONENT>_* component-scoped (controller, khan, node-probe,
//                      nomad bin, vault bin, ...)
//   - ABC_<RESOURCE>   plain form reserved for cluster-resource selectors
//                      only: ABC_WORKSPACE, ABC_REGION, ABC_NAMESPACE,
//                      ABC_ORG
package envvars

// Bucket classifies an Entry for documentation, validation, and the
// `abc env list` output grouping.
type Bucket int

const (
	// BucketABCAPI: canonical user-facing API/auth surface (ABC_API_*).
	BucketABCAPI Bucket = iota
	// BucketABCCLI: canonical CLI-local config/behaviour (ABC_CLI_*).
	BucketABCCLI
	// BucketABCResource: cluster-resource selectors (ABC_WORKSPACE, etc.).
	BucketABCResource
	// BucketABCComponent: component-scoped (ABC_<COMPONENT>_*).
	BucketABCComponent
	// BucketVendorFallback: NOMAD_*, VAULT_*, AWS_*, CONSUL_*. Read silently
	// as last-resort fallback; never the documented path.
	BucketVendorFallback
	// BucketToolBinary: paths to subprocess binaries (ABC_*_BIN). Operator-only.
	BucketToolBinary
	// BucketSubprocessOut: vendor env vars the CLI INJECTS into subprocesses.
	// Outputs, not inputs. Listed for documentation only.
	BucketSubprocessOut
	// BucketDebugTest: ABC_CLI_DEBUG_* / ABC_CLI_TEST_*. Reserved namespace.
	BucketDebugTest
)

// String returns a stable identifier for the bucket (used in `abc env list`
// section headers and in generated docs).
func (b Bucket) String() string {
	switch b {
	case BucketABCAPI:
		return "abc-api"
	case BucketABCCLI:
		return "abc-cli"
	case BucketABCResource:
		return "abc-resource"
	case BucketABCComponent:
		return "abc-component"
	case BucketVendorFallback:
		return "vendor-fallback"
	case BucketToolBinary:
		return "tool-binary"
	case BucketSubprocessOut:
		return "subprocess-out"
	case BucketDebugTest:
		return "debug-test"
	}
	return "unknown"
}

// Entry is a single environment variable known to the registry.
//
// The registry is the single source of truth: docs/reference/env-vars.md is
// generated from it; `abc env list/show/validate` reads from it; the
// resolver's precedence walk consults exactly its fields.
type Entry struct {
	// Name is the canonical env var name (e.g. "ABC_API_ADDR"). Always
	// uppercase, always scope-prefixed unless the entry is a
	// BucketABCResource or a known vendor name.
	Name string

	// Bucket classifies the entry.
	Bucket Bucket

	// Aliases are deprecated names still accepted on read. The resolver
	// emits a one-time deprecation warning when an alias is the resolved
	// source. Each alias's deprecation window is tracked by DeprecatedIn
	// and SunsetIn on the *alias* entry (alias entries also exist in the
	// registry, marked by IsAlias=true).
	Aliases []string

	// VendorFallback (BucketABCAPI / BucketABCResource only): the vendor
	// env var to read when the canonical name and all aliases are unset.
	// Empty for entries with no fallback.
	VendorFallback string

	// ContextKey is a dot-path within the active context that the resolver
	// consults when no flag/env value is found. Empty if the entry has no
	// context-config mapping (e.g. ABC_CLI_DEBUG_*).
	//
	// Format: "url", "access_token", "workspace_id", "region", "namespace",
	// "org_id", "output_format". Resolved by the caller-supplied
	// ContextLookup function (resolver.go).
	ContextKey string

	// FlagName (optional): the matching cobra flag name. The resolver uses
	// this to consult cmd.Flags().Changed(name) before reading env. Empty
	// if no equivalent flag exists.
	FlagName string

	// Default is the value returned when no flag, env, or context value is
	// found. Empty string is a valid default.
	Default string

	// Purpose is a one-line human-readable description shown in
	// `abc env list` and the generated reference doc.
	Purpose string

	// SinceVersion is the CLI version in which this canonical name was
	// introduced (informational; populated when migration begins).
	SinceVersion string

	// DeprecatedIn / SunsetIn (alias entries only): the CLI versions
	// marking the deprecation window. Aliases work until SunsetIn.
	DeprecatedIn string
	SunsetIn     string

	// IsAlias is true for entries that exist only to back-stop a rename.
	// The alias's Name is the OLD env var; the resolver reads the alias
	// and warns; aliases never appear in generated docs except in the
	// deprecation section.
	IsAlias bool

	// CanonicalName (alias entries only): the new canonical name the user
	// should migrate to.
	CanonicalName string

	// Secret marks token-like values so `abc env list` redacts them.
	Secret bool
}

// Registry is the single source of truth. Order is presentation order in
// `abc env list` output (within each bucket; bucket order is the iota order
// above).
//
// To add a new env var: add an Entry here. To rename: keep the old name as
// an IsAlias=true Entry pointing at the new CanonicalName, and add the new
// name as a fresh canonical Entry.
var Registry = []Entry{
	// ── ABC API (canonical, transport) ──────────────────────────────────

	{
		Name:           "ABC_API_ADDR",
		Bucket:         BucketABCAPI,
		Aliases:        []string{"ABC_API_ENDPOINT", "ABC_ADDR"},
		VendorFallback: "", // intentionally not NOMAD_ADDR: the CLI talks to
		// the controller, not Nomad directly. NOMAD_ADDR is constructed for
		// subprocesses via InjectVendor.
		ContextKey:   "url",
		FlagName:     "address",
		Purpose:      "abc-cluster API endpoint (controller / API gateway)",
		SinceVersion: "0.next",
	},
	{
		Name:          "ABC_API_TOKEN",
		Bucket:        BucketABCAPI,
		Aliases:       []string{"ABC_ACCESS_TOKEN", "ABC_TOKEN"},
		ContextKey:    "access_token",
		FlagName:      "access-token",
		Purpose:       "bearer token for abc-cluster API",
		SinceVersion:  "0.next",
		Secret:        true,
	},
	{
		Name:         "ABC_API_AS_USER",
		Bucket:       BucketABCAPI,
		Aliases:      []string{"ABC_AS_USER"},
		Purpose:      "operator-only: impersonate another user (sent as identity-override header)",
		SinceVersion: "0.next",
	},

	// ── ABC CLI (canonical, local config + behaviour) ───────────────────

	{
		Name:         "ABC_CLI_CONTEXT",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_ACTIVE_CONTEXT"},
		Purpose:      "one-shot override of active_context for this invocation",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_CONFIG_FILE",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_CONFIG_FILE", "ABC_CONFIG"},
		Default:      "", // resolved to ~/.abc/config.yaml by config.DefaultConfigPath
		Purpose:      "override ~/.abc/config.yaml location",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_CACHE_DIR",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_CACHE_DIR"},
		Purpose:      "override ~/.abc/cache/ location",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_OUTPUT_FORMAT",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_OUTPUT"},
		ContextKey:   "output_format",
		FlagName:     "output",
		Default:      "table",
		Purpose:      "default output format: table | json | yaml",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_SUDO",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_CLI_SUDO_MODE"},
		Purpose:      "operator passthrough mode: =1 unlocks abc sudo <vendor> commands",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_NO_TELEMETRY",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_NO_TELEMETRY"},
		Purpose:      "=1 disables CLI-side telemetry (controller telemetry is separate)",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_QUIET",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_QUIET"},
		Purpose:      "=1 suppresses non-essential stderr output",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_AUTOMATION",
		Bucket:       BucketABCCLI,
		Aliases:      []string{"ABC_AUTOMATION"},
		Purpose:      "=1 signals non-interactive automation context (suppresses prompts)",
		SinceVersion: "0.next",
	},

	// ── ABC RESOURCE selectors (plain ABC_<RESOURCE>) ───────────────────

	{
		Name:           "ABC_WORKSPACE",
		Bucket:         BucketABCResource,
		Aliases:        []string{"ABC_WORKSPACE_ID"},
		ContextKey:     "workspace_id",
		FlagName:       "workspace",
		Purpose:        "workspace ID for this invocation",
		SinceVersion:   "0.next",
	},
	{
		Name:           "ABC_REGION",
		Bucket:         BucketABCResource,
		VendorFallback: "NOMAD_REGION",
		ContextKey:     "region",
		FlagName:       "region",
		Purpose:        "sovereignty region (ZA / KE / MZ / ...)",
		SinceVersion:   "0.next",
	},
	{
		Name:           "ABC_NAMESPACE",
		Bucket:         BucketABCResource,
		VendorFallback: "NOMAD_NAMESPACE",
		ContextKey:     "namespace",
		FlagName:       "namespace",
		Purpose:        "logical namespace within workspace",
		SinceVersion:   "0.next",
	},
	{
		Name:         "ABC_ORG",
		Bucket:       BucketABCResource,
		ContextKey:   "org_id",
		FlagName:     "org",
		Purpose:      "organization ID when multi-org",
		SinceVersion: "0.next",
	},

	// ── ABC COMPONENT (tool binaries) ───────────────────────────────────

	{
		Name:         "ABC_NOMAD_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the nomad binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_VAULT_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the vault binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_RCLONE_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the rclone binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_S5CMD_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the s5cmd binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_NEXTFLOW_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the nextflow binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_NODE_PROBE_BIN",
		Bucket:       BucketToolBinary,
		Aliases:      []string{"ABC_NODE_PROBE_CLI_BINARY"},
		Purpose:      "override path to the abc-node-probe binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_SHELLCHECK_BIN",
		Bucket:       BucketToolBinary,
		Purpose:      "override path to the shellcheck binary",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_EGET_BIN",
		Bucket:       BucketToolBinary,
		Aliases:      []string{"ABC_EGET_BINARY"},
		Purpose:      "override path to the eget binary",
		SinceVersion: "0.next",
	},

	// ── ABC CLI DEBUG / TEST (reserved namespace) ───────────────────────

	{
		Name:         "ABC_CLI_DEBUG",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_DEBUG"},
		Purpose:      "=1 enables CLI debug logging (internal-only)",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_DEBUG_KEEP_SCRIPT",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_DEBUG_KEEP_SCRIPT"},
		Purpose:      "internal: keep generated scripts on disk after run",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_LOG_LEVEL",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_LOG_LEVEL"},
		Default:      "info",
		Purpose:      "CLI log level: trace | debug | info | warn | error",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_TRACE",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_TRACE"},
		Purpose:      "=1 enables CLI execution tracing",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_TEST_NS",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_TEST_NS"},
		Purpose:      "internal: namespace override for integration tests",
		SinceVersion: "0.next",
	},
	{
		Name:         "ABC_CLI_TEST_TIMEOUT",
		Bucket:       BucketDebugTest,
		Aliases:      []string{"ABC_TEST_TIMEOUT"},
		Purpose:      "internal: timeout override for integration tests",
		SinceVersion: "0.next",
	},

	// ── VENDOR FALLBACK (compat-only; documented as last-resort) ────────

	{
		Name:    "NOMAD_ADDR",
		Bucket:  BucketVendorFallback,
		Purpose: "fallback: only consulted when no ABC context is configured",
	},
	{
		Name:    "NOMAD_TOKEN",
		Bucket:  BucketVendorFallback,
		Secret:  true,
		Purpose: "fallback: only consulted when no ABC context is configured",
	},
	{
		Name:    "NOMAD_REGION",
		Bucket:  BucketVendorFallback,
		Purpose: "fallback for ABC_REGION when ABC context lacks region",
	},
	{
		Name:    "NOMAD_NAMESPACE",
		Bucket:  BucketVendorFallback,
		Purpose: "fallback for ABC_NAMESPACE when ABC context lacks namespace",
	},
	{
		Name:    "VAULT_ADDR",
		Bucket:  BucketVendorFallback,
		Purpose: "fallback: consulted only in abc sudo vault passthrough",
	},
	{
		Name:    "VAULT_TOKEN",
		Bucket:  BucketVendorFallback,
		Secret:  true,
		Purpose: "fallback: consulted only in abc sudo vault passthrough",
	},
	{
		Name:    "AWS_ACCESS_KEY_ID",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd subprocesses from active context",
	},
	{
		Name:    "AWS_SECRET_ACCESS_KEY",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "constructed for rclone/s5cmd subprocesses from active context",
	},
	{
		Name:    "AWS_ENDPOINT_URL",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd subprocesses from active context",
	},
	{
		Name:    "AWS_REGION",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd subprocesses from active context",
	},
}

// Lookup returns the canonical Entry for name (matching either the Name or
// any Alias). Returns (Entry{}, false) if name is unknown.
func Lookup(name string) (Entry, bool) {
	for _, e := range Registry {
		if e.Name == name {
			return e, true
		}
		for _, a := range e.Aliases {
			if a == name {
				return e, true
			}
		}
	}
	return Entry{}, false
}

// ByBucket returns all canonical entries in a given bucket, in registry order.
// Alias-only entries are excluded (they aren't separate Entry values in this
// design — aliases are fields on their canonical Entry).
func ByBucket(b Bucket) []Entry {
	var out []Entry
	for _, e := range Registry {
		if e.Bucket == b {
			out = append(out, e)
		}
	}
	return out
}
