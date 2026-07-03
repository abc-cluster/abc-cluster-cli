package config

import "strings"

// NomadService holds Nomad API / CLI connection details for one context.
// YAML path: contexts.<name>.admin.services.nomad
//
// The short field names (addr, token, region, namespace) are canonical.
// The prefixed forms (nomad_addr, nomad_token, nomad_region) are accepted on
// read for backward compatibility and migrated to short names on save by
// normalizeContextNomad; new configs should use the short names.
type NomadService struct {
	Addr      string `yaml:"addr,omitempty"`
	Token     string `yaml:"token,omitempty"`
	Region    string `yaml:"region,omitempty"`    // Nomad multi-region ID (e.g. global), not contexts.region
	Namespace string `yaml:"namespace,omitempty"` // default Nomad namespace for all operations on this context

	// Datacenters is the ordered list of Nomad datacenters to target by
	// default for `abc pipeline run` (and, in the future, `abc job run`)
	// when neither `--datacenter` nor a saved-spec value is provided.
	// CLI flags still win when present. Typical seedling configuration:
	// `datacenters: [seedling-prod]`. Empty falls through to the pipeline
	// spec's own default (`["*"]`) so unconfigured contexts keep working.
	Datacenters []string `yaml:"datacenters,omitempty"`

	// HeadPool is the Nomad node-pool the pipeline HEAD job must land in.
	// On seedling, this is "platform" (a tiny pool — typically a single
	// node — sized for orchestrators, not heavy compute). When empty, the
	// CLI applies a build-time default of "platform".
	//
	// Set this explicitly when the operator wants a non-default pool name,
	// or to "" + a `default` Nomad pool when running against single-node
	// clusters that don't have the platform/compute split.
	HeadPool string `yaml:"head_pool,omitempty"`

	// WorkerPool is the Nomad node-pool worker (per-process) jobs spawned
	// by nf-nomad should land in. Default "compute". Bypassed when the
	// user passes `--pin-workers` (which forces workers onto the same node
	// as the head, regardless of pool).
	WorkerPool string `yaml:"worker_pool,omitempty"`

	// TokenType caches the Nomad ACL token's `Type` field (from GET
	// /v1/acl/token/self — "management" or "client") the last time `abc
	// auth whoami` ran with a reachable cluster. Informational only — the
	// CLI always re-derives the live role from a fresh token lookup when
	// the cluster is reachable; this field exists so the type is visible
	// directly in config.yaml without a network call, for anyone reading
	// the file cold. "management" tokens bypass ALL Nomad ACL policy
	// checks (see Nomad's own docs) — a "group" field showing empty/none
	// on a management-token context is expected and does not indicate a
	// restricted or misconfigured identity. Written by `abc auth whoami`
	// (mirrors the existing Admin.ID auto-generation pattern); never
	// hand-edit this field, it's always overwritten on the next live
	// whoami.
	TokenType string `yaml:"token_type,omitempty"`

	// Deprecated: prefixed forms accepted on read, migrated to short names on save.
	DeprecatedAddr   string `yaml:"nomad_addr,omitempty"`
	DeprecatedToken  string `yaml:"nomad_token,omitempty"`
	DeprecatedRegion string `yaml:"nomad_region,omitempty"`
}

// AdminFloorService holds URLs synced from running Nomad jobs (abc-nodes floor)
// plus operator-supplied credentials (never written by config sync).
// Use endpoint for S3 API bases (MinIO, RustFS) and http for HTTP services (tusd, Grafana, Grafana Alloy, Vault, …).
// access_key/secret_key suit S3-compatible services; user/password suit web UIs.
type AdminFloorService struct {
	HTTP       string                `yaml:"http,omitempty"`
	Endpoint   string                `yaml:"endpoint,omitempty"`
	AccessKey  string                `yaml:"access_key,omitempty"`
	SecretKey  string                `yaml:"secret_key,omitempty"`
	User       string                `yaml:"user,omitempty"`
	Password   string                `yaml:"password,omitempty"`
	CredSource *AdminFloorCredSource `yaml:"cred_source,omitempty"`
	// PingEntryPoint names the entry point used for `traefik healthcheck` static snippets
	// (default in Traefik is "traefik", i.e. the dashboard listener). Only used for traefik.
	PingEntryPoint string `yaml:"ping_entrypoint,omitempty"`
	// Dashboard is an optional direct URL to a service UI page (e.g. a Grafana dashboard).
	// Written by capabilities sync; never overwritten if already set by the operator.
	Dashboard string `yaml:"dashboard,omitempty"`
}

// IsEmpty reports whether all URL and credential fields are unset.
func (a *AdminFloorService) IsEmpty() bool {
	if a == nil {
		return true
	}
	return strings.TrimSpace(a.HTTP) == "" &&
		strings.TrimSpace(a.Endpoint) == "" &&
		strings.TrimSpace(a.AccessKey) == "" &&
		strings.TrimSpace(a.SecretKey) == "" &&
		strings.TrimSpace(a.User) == "" &&
		strings.TrimSpace(a.Password) == "" &&
		isCredSourceEmpty(a.CredSource) &&
		strings.TrimSpace(a.PingEntryPoint) == "" &&
		strings.TrimSpace(a.Dashboard) == ""
}

// AdminFloorCredSource stores per-backend credential values/references for one service.
// local values are literals, while nomad/vault values are backend reference strings.
type AdminFloorCredSource struct {
	Local map[string]string `yaml:"local,omitempty"`
	Nomad map[string]string `yaml:"nomad,omitempty"`
	Vault map[string]string `yaml:"vault,omitempty"`
}

func isCredSourceEmpty(cs *AdminFloorCredSource) bool {
	if cs == nil {
		return true
	}
	return len(cs.Local) == 0 && len(cs.Nomad) == 0 && len(cs.Vault) == 0
}

// PulumiService holds Pulumi deployment settings for one context.
// Nomad and MinIO credentials are inherited from admin.services.nomad /
// admin.services.minio and injected as env vars at runtime; only
// Pulumi-specific knobs belong in this struct.
type PulumiService struct {
	// DeployDir is the path (absolute or relative to CWD) of the Pulumi
	// project directory for this context's abc-nodes deployment. The
	// userspace project lives in github.com/abc-cluster/abc-deployments
	// since 2026-05; e.g. "/path/to/abc-deployments/userspace".
	DeployDir string `yaml:"deploy_dir,omitempty"`

	// Stack is the Pulumi stack name to select when running commands.
	// e.g. "prod"
	Stack string `yaml:"stack,omitempty"`

	// AccessToken is the Pulumi Cloud access token injected as
	// PULUMI_ACCESS_TOKEN.  Required when using the Pulumi Cloud state
	// backend.  Leave empty when using a self-managed (local / S3) backend.
	AccessToken string `yaml:"access_token,omitempty"`

	// ConfigPassphrase is the passphrase used by Pulumi to decrypt encrypted
	// stack config secrets, injected as PULUMI_CONFIG_PASSPHRASE.
	// Required when using the default passphrase secrets provider.
	ConfigPassphrase string `yaml:"config_passphrase,omitempty"`
}

// TerraformService holds Terraform deployment settings for one context.
// Nomad credentials are inherited from admin.services.nomad and do not need
// to be duplicated here; only Terraform-specific knobs belong in this struct.
type TerraformService struct {
	// DeployDir is the path (absolute or relative to CWD) of the Terraform
	// working directory for this context's abc-nodes deployment.
	// e.g. "deployments/abc-nodes/terraform"
	DeployDir string `yaml:"deploy_dir,omitempty"`

	// Workspace is the Terraform workspace to select before running commands.
	// Defaults to "default" when empty.
	Workspace string `yaml:"workspace,omitempty"`

	// Vars holds additional TF_VAR_* overrides injected at runtime alongside
	// the auto-injected Nomad credentials.  Keys are Terraform variable names
	// (without the TF_VAR_ prefix); values are plain strings.
	// Example:
	//   vars:
	//     cluster_public_host: aither.mb.sun.ac.za
	//     deploy_observability_stack: "true"
	Vars map[string]string `yaml:"vars,omitempty"`
}

// AdminServices holds operator-facing integrations under contexts.<name>.admin.services.
type AdminServices struct {
	Nomad        *NomadService      `yaml:"nomad,omitempty"`
	Terraform    *TerraformService  `yaml:"terraform,omitempty"`
	Pulumi       *PulumiService     `yaml:"pulumi,omitempty"`
	MinIO        *AdminFloorService `yaml:"minio,omitempty"`
	Tusd         *AdminFloorService `yaml:"tusd,omitempty"`
	Faasd        *AdminFloorService `yaml:"faasd,omitempty"`
	Grafana      *AdminFloorService `yaml:"grafana,omitempty"`
	GrafanaAlloy *AdminFloorService `yaml:"grafana_alloy,omitempty"`
	Prometheus   *AdminFloorService `yaml:"prometheus,omitempty"`
	Loki         *AdminFloorService `yaml:"loki,omitempty"`
	Ntfy         *AdminFloorService `yaml:"ntfy,omitempty"`
	Rustfs       *AdminFloorService `yaml:"rustfs,omitempty"`
	Vault        *AdminFloorService `yaml:"vault,omitempty"`
	Traefik      *AdminFloorService `yaml:"traefik,omitempty"`
	Uppy         *AdminFloorService `yaml:"uppy,omitempty"`
	Wave         *AdminFloorService `yaml:"wave,omitempty"`
	Apps         *AppsService       `yaml:"apps,omitempty"`
}

// AppsService configures the cluster's per-plane app ingress doors — the
// hostnames (and bare-IP forms) of the TLS termination points that proxy
// `/apps/*` requests to the Traefik shared/private entrypoints. Used by
// `abc app deploy` to bake clickable URLs into the job meta, and by
// `abc app list` to render them. EVERY field is operator-provided per
// deployment; the CLI never assumes cluster-specific defaults.
//
// YAML path: contexts.<name>.admin.services.apps
//
// Example (the seedling-prod context):
//
//	admin:
//	  services:
//	    apps:
//	      public_domain:   apps.seedling.abc-cluster.cloud
//	      private_door:    aither.mb.sun.ac.za
//	      private_door_ip: 146.232.174.77
//	      shared_door:     ""    # Tailscale Serve hostname; empty until wired
//	      shared_door_ip:  ""
//
// Empty fields disable URL composition for the corresponding plane —
// `abc app list` falls through to the bare /apps/<app>/ path; `abc_url`
// meta on the job ends up at the path too.
type AppsService struct {
	// PublicDomain is the wildcard suffix for the public-edge plane. An app
	// named `<sub>` is routed at `https://<sub>.<PublicDomain>/`. Empty means
	// the cluster has no public-edge plane (institution-only deployments).
	PublicDomain string `yaml:"public_domain,omitempty"`

	// PrivateDoor is the campus-LAN TLS door hostname (e.g. a campus DNS name
	// or `aither.mb.sun.ac.za`). Caddy on this host forwards `/apps/*` to
	// Traefik's `private` entrypoint.
	PrivateDoor string `yaml:"private_door,omitempty"`
	// PrivateDoorIP is the bare-IP form of PrivateDoor, for users without the
	// DNS / hosts-file entry. The TLS cert is expected to carry an IP-SAN.
	PrivateDoorIP string `yaml:"private_door_ip,omitempty"`

	// SharedDoor is the overlay-VPN (e.g. Tailscale Serve, WireGuard) hostname
	// that forwards `/apps/*` to Traefik's `shared` entrypoint.
	SharedDoor string `yaml:"shared_door,omitempty"`
	// SharedDoorIP is the bare-IP form of SharedDoor.
	SharedDoorIP string `yaml:"shared_door_ip,omitempty"`
}

// AdminABCNodes holds optional static operator credentials for abc-nodes–style
// contexts (cluster_type: abc-nodes). Used to inject CLI environment when
// talking to Nomad, MinIO mc, RustFS, and S3-compatible tools.
type AdminABCNodes struct {
	NomadNamespace string `yaml:"nomad_namespace,omitempty"`
	S3AccessKey    string `yaml:"s3_access_key,omitempty"`
	S3SecretKey    string `yaml:"s3_secret_key,omitempty"`
	S3Region       string `yaml:"s3_region,omitempty"`
	// S3Endpoint is deprecated: on load it is migrated into admin.services.minio.endpoint
	// and cleared so the next save drops the YAML key. Kept for unmarshaling old files.
	S3Endpoint string `yaml:"s3_endpoint,omitempty"`
	// MinioRootUser and MinioRootPassword mirror MinIO server root credentials;
	// when s3_access_key / s3_secret_key are empty, these are mapped to AWS_* for CLIs.
	MinioRootUser     string `yaml:"minio_root_user,omitempty"`
	MinioRootPassword string `yaml:"minio_root_password,omitempty"`
}

// AdminTools holds operator-side tool management settings for a context.
// These drive `abc admin tools fetch` and `abc admin tools push`.
//
// YAML path: contexts.<name>.admin.tools
// Settable via: abc config set contexts.<name>.admin.tools.<field> <value>
type AdminTools struct {
	// Architectures is the list of os/arch pairs to fetch for each managed tool
	// (e.g. ["linux/amd64", "linux/arm64"]). When empty, defaults to
	// ["linux/amd64", "linux/arm64"]. Add "darwin/arm64", "windows/amd64" etc.
	// to extend coverage to operator machines or non-Linux cluster nodes.
	Architectures []string `yaml:"architectures,omitempty"`

	// ContextService names the admin.services.<name> entry that supplies S3
	// credentials and endpoint for `abc admin tools push`.
	// Typical values: "rustfs" or "minio". Defaults to "rustfs".
	ContextService string `yaml:"context_service,omitempty"`

	// Endpoint is the resolved S3 base URL written back by `abc admin tools push`
	// after the first successful upload. Job definitions can reference this value
	// directly instead of hardcoding the cluster storage URL.
	Endpoint string `yaml:"endpoint,omitempty"`
}

// DefaultToolArchitectures returns the fallback architecture list used when
// admin.tools.architectures is unset in the active context.
func DefaultToolArchitectures() []string {
	return []string{"linux/amd64", "linux/arm64"}
}

// Admin holds optional admin-plane settings for a context.
type Admin struct {
	// Whoami is an optional operator persona label for abc-nodes contexts (e.g. su-mbhg-bioinformatics_admin).
	// When admin.abc_nodes.nomad_namespace is unset, Nomad namespace defaults are derived from known
	// _<role> suffixes on Whoami (see deriveNomadNamespaceFromAdminWhoami).
	Whoami string `yaml:"whoami,omitempty"`

	// ID is a stable per-user identifier generated on first `abc auth whoami`
	// (when absent). Format: ULID (26-char Crockford Base32, lex-sortable,
	// embeds creation time). Used as the canonical user identity for cross-
	// cluster joins, audit logs (abc-jurist + XTDB), and billing —
	// independent of the human-readable Whoami label which can change as
	// personas evolve.
	//
	// Unlike Whoami, ID is opaque, never collides (80 bits of randomness +
	// ms timestamp), and privacy-safer to surface in metadata than email
	// addresses. Set once, never edited. Sortable by creation time.
	ID string `yaml:"id,omitempty"`

	Services AdminServices  `yaml:"services,omitempty"`
	ABCNodes *AdminABCNodes `yaml:"abc_nodes,omitempty"`
	Tools    *AdminTools    `yaml:"tools,omitempty"`
}

// Services is the deprecated YAML shape under contexts.<name>.services (migrated on load).
type Services struct {
	Nomad *NomadService `yaml:"nomad,omitempty"`
}

// NomadAddr returns contexts.<name>.admin.services.nomad.addr.
func (c Context) NomadAddr() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Addr)
}

// NomadToken returns contexts.<name>.admin.services.nomad.token.
func (c Context) NomadToken() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Token)
}

// NomadRegion returns contexts.<name>.admin.services.nomad.region (Nomad RPC region).
// It is intentionally not the same as Context.Region (ABC / datacenter label such as za-cpt).
func (c Context) NomadRegion() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Region)
}

// NomadServiceNamespace returns contexts.<name>.admin.services.nomad.namespace — the
// default Nomad namespace stamped into this context at provision time (e.g. "su-mbhg-bioinformatics").
// Used by NomadNamespace() as the first resolution step for any cluster type.
func (c Context) NomadServiceNamespace() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Namespace)
}

// NomadHeadPool returns contexts.<name>.admin.services.nomad.head_pool —
// the operator-pinned Nomad node-pool name for the pipeline HEAD job.
// Returns "" when unset; callers should apply a build-time fallback
// (currently "platform" for seedling). Trims whitespace.
func (c Context) NomadHeadPool() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.HeadPool)
}

// NomadWorkerPool returns contexts.<name>.admin.services.nomad.worker_pool —
// the operator-pinned Nomad node-pool name for nf-nomad-spawned worker jobs
// (per-process). Returns "" when unset; callers should apply a build-time
// fallback (currently "compute"). Trims whitespace.
//
// Note: --pin-workers bypasses this — workers go to the head's node, not
// the worker-pool default.
func (c Context) NomadWorkerPool() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.WorkerPool)
}

// NomadDatacenters returns contexts.<name>.admin.services.nomad.datacenters —
// the operator-pinned default datacenter list for this context. Returns nil
// when unset; the caller (typically `abc pipeline run`) falls through to a
// command-line --datacenter flag or to the pipeline spec's own "*" default.
//
// Trims whitespace and drops empty entries so YAML accidents like
// `datacenters: ["seedling-prod", ""]` don't propagate. Result is a defensive
// copy so callers can mutate without affecting the cached context.
func (c Context) NomadDatacenters() []string {
	if c.Admin.Services.Nomad == nil {
		return nil
	}
	src := c.Admin.Services.Nomad.Datacenters
	if len(src) == 0 {
		return nil
	}
	out := make([]string, 0, len(src))
	for _, dc := range src {
		if s := strings.TrimSpace(dc); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TerraformDeployDir returns contexts.<name>.admin.services.terraform.deploy_dir.
// Returns "" when unset; callers fall back to CWD or a flag value.
func (c Context) TerraformDeployDir() string {
	if c.Admin.Services.Terraform == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Terraform.DeployDir)
}

// TerraformWorkspace returns contexts.<name>.admin.services.terraform.workspace.
// Returns "" when unset (callers treat "" as "default").
func (c Context) TerraformWorkspace() string {
	if c.Admin.Services.Terraform == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Terraform.Workspace)
}

// TerraformVars returns the extra TF_VAR_* map from
// contexts.<name>.admin.services.terraform.vars.  Never nil.
func (c Context) TerraformVars() map[string]string {
	if c.Admin.Services.Terraform == nil || len(c.Admin.Services.Terraform.Vars) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(c.Admin.Services.Terraform.Vars))
	for k, v := range c.Admin.Services.Terraform.Vars {
		out[k] = v
	}
	return out
}

// PulumiDeployDir returns contexts.<name>.admin.services.pulumi.deploy_dir.
// Returns "" when unset; callers fall back to CWD or a flag value.
func (c Context) PulumiDeployDir() string {
	if c.Admin.Services.Pulumi == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Pulumi.DeployDir)
}

// PulumiStack returns contexts.<name>.admin.services.pulumi.stack.
// Returns "" when unset (callers treat "" as "selecting no stack override").
func (c Context) PulumiStack() string {
	if c.Admin.Services.Pulumi == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Pulumi.Stack)
}

// PulumiAccessToken returns contexts.<name>.admin.services.pulumi.access_token.
// Injected as PULUMI_ACCESS_TOKEN for the Pulumi Cloud state backend.
func (c Context) PulumiAccessToken() string {
	if c.Admin.Services.Pulumi == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Pulumi.AccessToken)
}

// PulumiConfigPassphrase returns contexts.<name>.admin.services.pulumi.config_passphrase.
// Injected as PULUMI_CONFIG_PASSPHRASE to decrypt encrypted stack config secrets.
func (c Context) PulumiConfigPassphrase() string {
	if c.Admin.Services.Pulumi == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Pulumi.ConfigPassphrase)
}

// normalizeContextNomad folds all deprecated / prefixed YAML forms into the
// canonical short-name fields of admin.services.nomad, then clears legacy fields
// so the next save writes only the new shape.
//
// Migration chain (highest to lowest priority for each field):
//   addr:   admin.services.nomad.addr  > nomad_addr (prefixed)  > services.nomad.addr  > top-level nomad_addr
//   token:  admin.services.nomad.token > nomad_token (prefixed) > services.nomad.token > top-level nomad_token
//   region: admin.services.nomad.region > nomad_region (prefixed) > services.nomad.region
func normalizeContextNomad(ctx *Context) {
	var addr, token, region string

	if n := ctx.Admin.Services.Nomad; n != nil {
		// Prefer short canonical names; fall back to prefixed deprecated names.
		addr = strings.TrimSpace(first(n.Addr, n.DeprecatedAddr))
		token = strings.TrimSpace(first(n.Token, n.DeprecatedToken))
		region = strings.TrimSpace(first(n.Region, n.DeprecatedRegion))
	}
	if ctx.ServicesLegacy.Nomad != nil {
		if addr == "" {
			addr = strings.TrimSpace(ctx.ServicesLegacy.Nomad.Addr)
		}
		if token == "" {
			token = strings.TrimSpace(ctx.ServicesLegacy.Nomad.Token)
		}
	}
	if addr == "" {
		addr = strings.TrimSpace(ctx.LegacyNomadAddr)
	}
	if token == "" {
		token = strings.TrimSpace(ctx.LegacyNomadToken)
	}

	// Preserve namespace + datacenters + pool fields — they have no
	// deprecated form, so carry them through the legacy-field flush below.
	var ns, headPool, workerPool string
	var datacenters []string
	if ctx.Admin.Services.Nomad != nil {
		ns = strings.TrimSpace(ctx.Admin.Services.Nomad.Namespace)
		headPool = strings.TrimSpace(ctx.Admin.Services.Nomad.HeadPool)
		workerPool = strings.TrimSpace(ctx.Admin.Services.Nomad.WorkerPool)
		if len(ctx.Admin.Services.Nomad.Datacenters) > 0 {
			datacenters = append([]string(nil), ctx.Admin.Services.Nomad.Datacenters...)
		}
	}

	// Clear all legacy / deprecated fields.
	ctx.ServicesLegacy = Services{}
	ctx.LegacyNomadAddr = ""
	ctx.LegacyNomadToken = ""

	if addr == "" && token == "" && region == "" && ns == "" && len(datacenters) == 0 &&
		headPool == "" && workerPool == "" {
		ctx.Admin.Services.Nomad = nil
		return
	}
	if ctx.Admin.Services.Nomad == nil {
		ctx.Admin.Services.Nomad = &NomadService{}
	}
	if addr != "" {
		addr = CanonicalNomadAPIAddrForYAML(addr)
	}
	ctx.Admin.Services.Nomad = &NomadService{
		Addr:        addr,
		Token:       token,
		Region:      region,
		Namespace:   ns,
		Datacenters: datacenters,
		HeadPool:    headPool,
		WorkerPool:  workerPool,
	}
	if ctx.Admin.Services.Nomad.Addr == "" && ctx.Admin.Services.Nomad.Token == "" &&
		ctx.Admin.Services.Nomad.Region == "" && ctx.Admin.Services.Nomad.Namespace == "" &&
		len(ctx.Admin.Services.Nomad.Datacenters) == 0 &&
		ctx.Admin.Services.Nomad.HeadPool == "" && ctx.Admin.Services.Nomad.WorkerPool == "" {
		ctx.Admin.Services.Nomad = nil
	}
}

// first returns the first non-empty string from the arguments.
func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// splitDatacenterList parses a comma-separated `abc config set ... datacenters`
// value into the trimmed-non-empty slice stored on NomadService.Datacenters.
// Empty input returns nil (which the writer treats as "clear the field").
// Accepts whitespace around commas: "seedling-prod, seedling-canary" →
// ["seedling-prod", "seedling-canary"].
func splitDatacenterList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AppsPublicDomain returns contexts.<name>.admin.services.apps.public_domain
// — the wildcard hostname suffix under which the cluster serves public-plane
// apps (e.g. "apps.seedling.abc-cluster.cloud"). Returns "" when the cluster
// has no public-edge plane configured.
func (c Context) AppsPublicDomain() string {
	if c.Admin.Services.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Apps.PublicDomain)
}

// AppsPrivateDoor returns contexts.<name>.admin.services.apps.private_door —
// the campus-LAN TLS door hostname (Caddy bound to a campus IP) that fronts
// the Traefik `private` entrypoint at `/apps/*`. Returns "" when unset.
func (c Context) AppsPrivateDoor() string {
	if c.Admin.Services.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Apps.PrivateDoor)
}

// AppsPrivateDoorIP returns contexts.<name>.admin.services.apps.private_door_ip
// — the bare-IP form of AppsPrivateDoor, for users without DNS/hosts-file
// resolution. The TLS cert is expected to carry the IP as a SAN.
func (c Context) AppsPrivateDoorIP() string {
	if c.Admin.Services.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Apps.PrivateDoorIP)
}

// AppsSharedDoor returns contexts.<name>.admin.services.apps.shared_door —
// the overlay-VPN (e.g. Tailscale Serve) hostname fronting the Traefik
// `shared` entrypoint at `/apps/*`. Returns "" when unset (Tailscale Serve
// or equivalent not yet wired).
func (c Context) AppsSharedDoor() string {
	if c.Admin.Services.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Apps.SharedDoor)
}

// AppsSharedDoorIP returns contexts.<name>.admin.services.apps.shared_door_ip
// — the bare-IP form of AppsSharedDoor.
func (c Context) AppsSharedDoorIP() string {
	if c.Admin.Services.Apps == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Apps.SharedDoorIP)
}
