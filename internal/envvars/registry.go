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
//
// No legacy aliases are accepted. Pre-1.0 the CLI is canonical-only.
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
	// BucketToolBinary: paths to subprocess binaries (ABC_BIN_<TOOL>). Operator-only.
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
	// Name is the canonical env var name (e.g. "ABC_API_ADDRESS"). Always
	// uppercase, always scope-prefixed unless the entry is a
	// BucketABCResource or a known vendor name.
	Name string

	// Bucket classifies the entry.
	Bucket Bucket

	// VendorFallback (BucketABCAPI / BucketABCResource only): the vendor
	// env var to read when the canonical name is unset. Empty for entries
	// with no fallback.
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

	// Secret marks token-like values so `abc env list` redacts them.
	Secret bool

	// Shadowing lists alternative resolution paths beyond the standard
	// precedence ladder that may take precedence over this env var in
	// specific contexts.
	//
	// Two patterns are encoded:
	//
	//   1. cred_source indirection — Selector + Service + Field. The
	//      `--config <selector>` flag on admin floor passthrough
	//      commands selects which backend to consult. `--config local`
	//      preserves shell-wins precedence; `--config nomad/vault`
	//      force-replaces the shell value with the resolved one and
	//      emits a one-time warning.
	//
	//   2. context-direct shadowing — ContextPath. The config value
	//      under contexts.<n>.<path> wins over the env var regardless
	//      of selector, with a stderr warning emitted on disagreement.
	//      Used by ABC_CRYPT_PASSWORD / ABC_CRYPT_SALT.
	//
	// See brainstorms/cli-env-resolution/2026-05-11-cred-source-shadowing-audit.md.
	Shadowing []Shadow
}

// Shadow describes one alternate resolution path for an env var.
type Shadow struct {
	// Selector is the --config selector that activates this path:
	// "local", "nomad", or "vault". Empty (zero value) means the
	// shadow applies regardless of selector — paired with ContextPath
	// for direct context-config shadowing (Shape C / crypt).
	Selector string

	// Service is the admin floor service name when Selector is set.
	// One of: minio | rustfs | vault | loki | prometheus | grafana | ntfy.
	Service string

	// Field is the field within the service struct when Selector is set.
	// One of: http | endpoint | access_key | secret_key | user | password.
	Field string

	// ContextPath is a dot-path under contexts.<n>.* used for direct
	// context shadowing (no admin-floor indirection). Mutually exclusive
	// with Selector/Service/Field.
	//
	// Supported paths: "crypt.password", "crypt.salt".
	ContextPath string

	// Description is a one-line human-readable override. Optional; the
	// rendering layer auto-derives a description from Service+Field or
	// ContextPath when empty.
	Description string
}

// ConfigPath returns the dot-path within ~/.abc/config.yaml that this
// shadow reads from. Used by `abc admin env show` / `validate` for
// presentation.
func (s Shadow) ConfigPath() string {
	if s.ContextPath != "" {
		return "contexts.<n>." + s.ContextPath
	}
	if s.Service != "" && s.Field != "" {
		if s.Selector != "" && s.Selector != "local" {
			return "contexts.<n>.admin.services." + s.Service +
				".cred_source." + s.Selector + "." + s.Field
		}
		return "contexts.<n>.admin.services." + s.Service +
			".cred_source.local." + s.Field +
			" (or top-level ." + s.Field + ")"
	}
	return ""
}

// AutoDescription returns Description when set, otherwise a synthesized
// one-line summary from the structured fields.
func (s Shadow) AutoDescription() string {
	if s.Description != "" {
		return s.Description
	}
	if s.ContextPath != "" {
		return s.ConfigPath() + " wins over env var; stderr warning on disagreement"
	}
	switch s.Selector {
	case "local":
		return "--config local: " + s.ConfigPath() + " (shell env wins by default)"
	case "nomad":
		return "--config nomad: " + s.ConfigPath() + " (shell env ignored, warning emitted)"
	case "vault":
		return "--config vault: " + s.ConfigPath() + " (shell env ignored, warning emitted)"
	}
	return ""
}

// Registry is the single source of truth. Order is presentation order in
// `abc env list` output (within each bucket; bucket order is the iota order
// above).
var Registry = []Entry{
	// ── ABC API (canonical, transport) ──────────────────────────────────

	{
		Name:       "ABC_API_ADDRESS",
		Bucket:     BucketABCAPI,
		ContextKey: "url",
		FlagName:   "address",
		Purpose:    "abc-cluster API endpoint (controller / API gateway)",
	},
	{
		Name:       "ABC_API_TOKEN",
		Bucket:     BucketABCAPI,
		ContextKey: "access_token",
		FlagName:   "access-token",
		Purpose:    "bearer token for abc-cluster API",
		Secret:     true,
	},
	{
		Name:    "ABC_API_AS_USER",
		Bucket:  BucketABCAPI,
		Purpose: "operator-only: impersonate another user (sent as identity-override header)",
	},

	// ── ABC CLI (canonical, local config + behaviour) ───────────────────

	{
		Name:    "ABC_CLI_CONTEXT",
		Bucket:  BucketABCCLI,
		Purpose: "one-shot override of active_context for this invocation",
	},
	{
		Name:    "ABC_CLI_CONFIG_FILE",
		Bucket:  BucketABCCLI,
		Purpose: "override ~/.abc/config.yaml location",
	},
	{
		Name:    "ABC_CLI_CACHE_DIR",
		Bucket:  BucketABCCLI,
		Purpose: "override ~/.abc/cache/ location",
	},
	{
		Name:       "ABC_CLI_OUTPUT_FORMAT",
		Bucket:     BucketABCCLI,
		ContextKey: "output_format",
		FlagName:   "output",
		Default:    "table",
		Purpose:    "default output format: table | json | yaml",
	},
	{
		Name:    "ABC_CLI_SUDO",
		Bucket:  BucketABCCLI,
		Purpose: "operator passthrough mode: =1 unlocks abc sudo <vendor> commands",
	},
	{
		Name:    "ABC_CLI_CLOUD_MODE",
		Bucket:  BucketABCCLI,
		Purpose: "=1 unlocks abc-cloud control-plane commands (operator only)",
	},
	{
		Name:    "ABC_CLI_EXP_MODE",
		Bucket:  BucketABCCLI,
		Purpose: "=1 enables experimental CLI commands",
	},
	{
		Name:    "ABC_CLI_NO_TELEMETRY",
		Bucket:  BucketABCCLI,
		Purpose: "=1 disables CLI-side telemetry (controller telemetry is separate)",
	},
	{
		Name:    "ABC_CLI_QUIET",
		Bucket:  BucketABCCLI,
		Purpose: "=1 suppresses non-essential stderr output",
	},
	{
		Name:    "ABC_CLI_AUTOMATION",
		Bucket:  BucketABCCLI,
		Purpose: "=1 signals non-interactive automation context (suppresses prompts)",
	},
	{
		Name:    "ABC_CLI_USE_EGET",
		Bucket:  BucketABCCLI,
		Default: "auto",
		Purpose: "eget preference: auto | 0/false/no/off (never use eget)",
	},
	{
		Name:    "ABC_CLI_TMPDIR",
		Bucket:  BucketABCCLI,
		Purpose: "override temp dir for CLI staging (uploads, generated scripts, ...)",
	},
	{
		Name:    "ABC_CLI_ASSETS_DIR",
		Bucket:  BucketABCCLI,
		Purpose: "override location of CLI-bundled assets (templates, schemas, ...)",
	},
	{
		Name:    "ABC_CLI_BINARIES_DIR",
		Bucket:  BucketABCCLI,
		Purpose: "override managed-binaries dir (default: ~/.abc/bin)",
	},

	// ── ABC RESOURCE selectors (plain ABC_<RESOURCE>) ───────────────────

	{
		Name:       "ABC_WORKSPACE",
		Bucket:     BucketABCResource,
		ContextKey: "workspace_id",
		FlagName:   "workspace",
		Purpose:    "workspace ID for this invocation",
	},
	{
		Name:           "ABC_REGION",
		Bucket:         BucketABCResource,
		VendorFallback: "NOMAD_REGION",
		ContextKey:     "region",
		FlagName:       "region",
		Purpose:        "sovereignty region (ZA / KE / MZ / ...)",
	},
	{
		Name:           "ABC_NAMESPACE",
		Bucket:         BucketABCResource,
		VendorFallback: "NOMAD_NAMESPACE",
		ContextKey:     "namespace",
		FlagName:       "namespace",
		Purpose:        "logical namespace within workspace",
	},
	{
		Name:       "ABC_ORG",
		Bucket:     BucketABCResource,
		ContextKey: "org_id",
		FlagName:   "org",
		Purpose:    "organization ID when multi-org",
	},
	{
		Name:     "ABC_CLUSTER",
		Bucket:   BucketABCResource,
		FlagName: "cluster",
		Purpose:  "cluster identifier within active context",
	},
	{
		Name:     "ABC_PROJECT",
		Bucket:   BucketABCResource,
		FlagName: "project",
		Purpose:  "project identifier within workspace",
	},
	{
		Name:     "ABC_INVESTIGATION",
		Bucket:   BucketABCResource,
		FlagName: "investigation",
		Purpose:  "investigation identifier within project",
	},

	// ── ABC COMPONENT (tool binaries) ───────────────────────────────────

	{
		Name:    "ABC_BIN_NOMAD",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the nomad binary",
	},
	{
		Name:    "ABC_BIN_VAULT",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the vault binary",
	},
	{
		Name:    "ABC_BIN_RCLONE",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the rclone binary",
	},
	{
		Name:    "ABC_BIN_MC",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the mc (MinIO Client) binary",
	},
	{
		Name:    "ABC_BIN_S5CMD",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the s5cmd binary",
	},
	{
		Name:    "ABC_BIN_NEXTFLOW",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the nextflow binary",
	},
	{
		Name:    "ABC_BIN_NODE_PROBE",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the abc-node-probe binary",
	},
	{
		Name:    "ABC_BIN_SHELLCHECK",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the shellcheck binary",
	},
	{
		Name:    "ABC_BIN_EGET",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the eget binary",
	},
	{
		Name:    "ABC_BIN_ARIA2C",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the aria2c binary (used by abc data download for presigned-URL downloads)",
	},
	{
		Name:    "ABC_BIN_MCLI",
		Bucket:  BucketToolBinary,
		Purpose: "override path to the mcli (MinIO Client) binary; ABC_BIN_MC is also checked as fallback",
	},

	// ── JupyterHub env (injected by SystemdSpawner into the slot's
	// singleuser environment; read by `abc workbench token …`) ─────────
	{
		Name:    "JUPYTERHUB_API_TOKEN",
		Bucket:  BucketABCComponent,
		Secret:  true,
		Purpose: "JupyterHub user token for the spawned singleuser server; injected by SystemdSpawner. `abc workbench token` uses this to call the hub user-tokens API.",
	},
	{
		Name:    "JUPYTERHUB_API_URL",
		Bucket:  BucketABCComponent,
		Purpose: "JupyterHub REST API base URL (e.g. http://127.0.0.1:15001/hub/api); injected by SystemdSpawner inside the slot.",
	},
	{
		Name:    "JUPYTERHUB_USER",
		Bucket:  BucketABCComponent,
		Purpose: "JupyterHub username for the active singleuser server (e.g. slot-calm_dassie); injected by SystemdSpawner. Used to compose connect URLs.",
	},

	// ── ABC COMPONENT (capability / upload / crypt / node) ──────────────

	{
		Name:    "ABC_CAPABILITY_TTL",
		Bucket:  BucketABCComponent,
		Purpose: "capability cache TTL (e.g. 24h)",
	},
	{
		Name:    "ABC_CAPABILITY_HARD_EXPIRY",
		Bucket:  BucketABCComponent,
		Purpose: "capability hard-expiry window beyond TTL (e.g. 72h)",
	},
	{
		Name:       "ABC_UPLOAD_ENDPOINT",
		Bucket:     BucketABCComponent,
		ContextKey: "upload_endpoint",
		Purpose:    "tusd upload endpoint override",
	},
	{
		Name:       "ABC_UPLOAD_TOKEN",
		Bucket:     BucketABCComponent,
		ContextKey: "upload_token",
		Purpose:    "tusd upload token override",
		Secret:     true,
	},
	{
		Name:    "ABC_TRANSFER_ENDPOINT",
		Bucket:  BucketABCComponent,
		Purpose: "transfer.sh endpoint override for `abc data courier`",
	},
	{
		Name:    "ABC_CRYPT_PASSWORD",
		Bucket:  BucketABCComponent,
		Purpose: "rclone-crypt password (also keys abc secrets)",
		Secret:  true,
		Shadowing: []Shadow{
			{ContextPath: "crypt.password"},
		},
	},
	{
		Name:    "ABC_CRYPT_SALT",
		Bucket:  BucketABCComponent,
		Purpose: "rclone-crypt salt",
		Secret:  true,
		Shadowing: []Shadow{
			{ContextPath: "crypt.salt"},
		},
	},
	{
		Name:    "ABC_NODE_PASSWORD",
		Bucket:  BucketABCComponent,
		Purpose: "abc-node bootstrap password",
		Secret:  true,
	},
	{
		Name:    "ABC_NODE_NO_PROBE",
		Bucket:  BucketABCComponent,
		Purpose: "=1 skips the periodic node-probe sysbatch job",
	},

	// ── ABC CLI DEBUG / TEST (reserved namespace) ───────────────────────

	{
		Name:    "ABC_CLI_DEBUG",
		Bucket:  BucketDebugTest,
		Purpose: "=1 (or =N) enables CLI debug logging at level N",
	},
	{
		Name:    "ABC_CLI_DEBUG_KEEP_SCRIPT",
		Bucket:  BucketDebugTest,
		Purpose: "internal: keep generated scripts on disk after run",
	},
	{
		Name:    "ABC_CLI_LOG_LEVEL",
		Bucket:  BucketDebugTest,
		Default: "info",
		Purpose: "CLI log level: trace | debug | info | warn | error",
	},
	{
		Name:    "ABC_CLI_TRACE",
		Bucket:  BucketDebugTest,
		Purpose: "=1 enables CLI execution tracing",
	},
	{
		Name:    "ABC_CLI_TEST_NS",
		Bucket:  BucketDebugTest,
		Purpose: "internal: namespace override for integration tests",
	},
	{
		Name:    "ABC_CLI_TEST_TIMEOUT",
		Bucket:  BucketDebugTest,
		Purpose: "internal: timeout override for integration tests",
	},
	{
		Name:    "ABC_CLI_INTEGRATION_LOKI_REQUIRE",
		Bucket:  BucketDebugTest,
		Purpose: "internal: integration test requires Loki present",
	},
	{
		Name:    "ABC_CLI_INTEGRATION_LOKI_WAIT_SEC",
		Bucket:  BucketDebugTest,
		Purpose: "internal: integration test Loki wait timeout (seconds)",
	},
	{
		Name:    "ABC_CLI_INTEGRATION_OBS_STACK",
		Bucket:  BucketDebugTest,
		Purpose: "internal: integration test observability stack flag",
	},
	{
		Name:    "ABC_CLI_INTEGRATION_STRESS_NG",
		Bucket:  BucketDebugTest,
		Purpose: "internal: stress-ng integration test toggle",
	},
	{
		Name:    "ABC_CLI_INTEGRATION_STRESS_TIMEOUT",
		Bucket:  BucketDebugTest,
		Purpose: "internal: stress-ng integration test timeout",
	},

	// ── VENDOR FALLBACK (compat-only; documented as last-resort) ────────

	{
		Name:    "NOMAD_ADDR",
		Bucket:  BucketVendorFallback,
		Purpose: "fallback: consulted only when no ABC context is configured",
	},
	{
		Name:    "NOMAD_TOKEN",
		Bucket:  BucketVendorFallback,
		Secret:  true,
		Purpose: "fallback: consulted only when no ABC context is configured",
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
		Shadowing: []Shadow{
			{Selector: "local", Service: "vault", Field: "http"},
			{Selector: "nomad", Service: "vault", Field: "http"},
			// vault does not support --config vault (chicken/egg).
		},
	},
	{
		Name:    "VAULT_TOKEN",
		Bucket:  BucketVendorFallback,
		Secret:  true,
		Purpose: "fallback: consulted only in abc sudo vault passthrough",
		Shadowing: []Shadow{
			{Selector: "local", Service: "vault", Field: "access_key"},
			{Selector: "nomad", Service: "vault", Field: "access_key"},
		},
	},
	// ── SUBPROCESS OUT — AWS SDK family ─────────────────────────────────
	{
		Name:    "AWS_ACCESS_KEY_ID",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd/nextflow/mc subprocesses from active context",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "access_key"},
			{Selector: "nomad", Service: "minio", Field: "access_key"},
			{Selector: "vault", Service: "minio", Field: "access_key"},
			{Selector: "local", Service: "rustfs", Field: "access_key"},
			{Selector: "nomad", Service: "rustfs", Field: "access_key"},
			{Selector: "vault", Service: "rustfs", Field: "access_key"},
		},
	},
	{
		Name:    "AWS_SECRET_ACCESS_KEY",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "constructed for rclone/s5cmd/nextflow/mc subprocesses from active context",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "secret_key"},
			{Selector: "nomad", Service: "minio", Field: "secret_key"},
			{Selector: "vault", Service: "minio", Field: "secret_key"},
			{Selector: "local", Service: "rustfs", Field: "secret_key"},
			{Selector: "nomad", Service: "rustfs", Field: "secret_key"},
			{Selector: "vault", Service: "rustfs", Field: "secret_key"},
		},
	},
	{
		Name:    "AWS_ENDPOINT_URL",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd/nextflow/mc subprocesses from active context",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "endpoint"},
			{Selector: "nomad", Service: "minio", Field: "endpoint"},
			{Selector: "vault", Service: "minio", Field: "endpoint"},
		},
	},
	{
		Name:    "AWS_REGION",
		Bucket:  BucketSubprocessOut,
		Purpose: "constructed for rclone/s5cmd/nextflow/mc subprocesses from active context",
	},
	{
		Name:    "AWS_DEFAULT_REGION",
		Bucket:  BucketSubprocessOut,
		Purpose: "older vendor variant of AWS_REGION; some tools (older aws-cli, boto) only read this",
	},
	{
		Name:    "AWS_SESSION_TOKEN",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "session token for grove+ short-lived credentials (STS-style)",
	},
	{
		Name:    "AWS_CA_BUNDLE",
		Bucket:  BucketSubprocessOut,
		Purpose: "custom CA cert path for self-signed S3 endpoints (lab deployments)",
	},
	{
		Name:    "AWS_S3_FORCE_PATH_STYLE",
		Bucket:  BucketSubprocessOut,
		Purpose: "force path-style addressing; required for MinIO and RustFS",
	},
	{
		Name:    "S3_FORCE_PATH_STYLE",
		Bucket:  BucketSubprocessOut,
		Purpose: "older vendor variant of AWS_S3_FORCE_PATH_STYLE",
	},
	{
		Name:    "AWS_REQUEST_CHECKSUM_CALCULATION",
		Bucket:  BucketSubprocessOut,
		Purpose: "set to when_required for MinIO compatibility with newer AWS SDKs",
	},

	// ── SUBPROCESS OUT — MinIO Client (mc) ──────────────────────────────
	{
		Name:    "MC_HOST_local",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "MinIO Client connection alias for the active context; URL with embedded credentials",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "endpoint",
				Description: "constructed from admin.services.minio.cred_source.local.{endpoint,user,password}"},
			{Selector: "nomad", Service: "minio", Field: "endpoint",
				Description: "constructed from admin.services.minio.cred_source.nomad.{endpoint,user,password}; shell env ignored"},
			{Selector: "vault", Service: "minio", Field: "endpoint",
				Description: "constructed from admin.services.minio.cred_source.vault.{endpoint,user,password}; shell env ignored"},
		},
	},
	{
		Name:    "MC_INSECURE",
		Bucket:  BucketSubprocessOut,
		Purpose: "MinIO Client: skip TLS verification (self-signed endpoints)",
	},

	// ── SUBPROCESS OUT — Pulumi MinIO provider ──────────────────────────
	{
		Name:    "MINIO_SERVER",
		Bucket:  BucketSubprocessOut,
		Purpose: "host:port for Pulumi MinIO provider (scheme stripped)",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "endpoint"},
			{Selector: "nomad", Service: "minio", Field: "endpoint"},
			{Selector: "vault", Service: "minio", Field: "endpoint"},
		},
	},
	{
		Name:    "MINIO_USER",
		Bucket:  BucketSubprocessOut,
		Purpose: "user for Pulumi MinIO provider",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "user"},
			{Selector: "nomad", Service: "minio", Field: "user"},
			{Selector: "vault", Service: "minio", Field: "user"},
		},
	},
	{
		Name:    "MINIO_PASSWORD",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "password for Pulumi MinIO provider",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "password"},
			{Selector: "nomad", Service: "minio", Field: "password"},
			{Selector: "vault", Service: "minio", Field: "password"},
		},
	},

	// ── SUBPROCESS OUT — MinIO server admin (operator passthrough) ──────
	{
		Name:    "MINIO_ROOT_USER",
		Bucket:  BucketSubprocessOut,
		Purpose: "MinIO server root user (constructed for `abc admin services minio cli` passthrough)",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "user",
				Description: "admin.services.minio.cred_source.local.user OR admin.abc_nodes.minio_root_user as fallback"},
			{Selector: "nomad", Service: "minio", Field: "user"},
			{Selector: "vault", Service: "minio", Field: "user"},
		},
	},
	{
		Name:    "MINIO_ROOT_PASSWORD",
		Bucket:  BucketSubprocessOut,
		Secret:  true,
		Purpose: "MinIO server root password (constructed for `abc admin services minio cli` passthrough)",
		Shadowing: []Shadow{
			{Selector: "local", Service: "minio", Field: "password",
				Description: "admin.services.minio.cred_source.local.password OR admin.abc_nodes.minio_root_password as fallback"},
			{Selector: "nomad", Service: "minio", Field: "password"},
			{Selector: "vault", Service: "minio", Field: "password"},
		},
	},

	// ── SUBPROCESS OUT — rclone ─────────────────────────────────────────
	{
		Name:    "RCLONE_CONFIG",
		Bucket:  BucketSubprocessOut,
		Purpose: "path to rclone config file (the CLI generates one per invocation)",
	},
}

// Lookup returns the Entry for the given canonical name. Returns
// (Entry{}, false) if name is not registered.
func Lookup(name string) (Entry, bool) {
	for _, e := range Registry {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// ByBucket returns all entries in a given bucket, in registry order.
func ByBucket(b Bucket) []Entry {
	var out []Entry
	for _, e := range Registry {
		if e.Bucket == b {
			out = append(out, e)
		}
	}
	return out
}
