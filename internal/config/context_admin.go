package config

import "strings"

// NomadService holds Nomad API / CLI connection details for one context.
type NomadService struct {
	Addr   string `yaml:"nomad_addr,omitempty"`
	Token  string `yaml:"nomad_token,omitempty"`
	Region string `yaml:"nomad_region,omitempty"` // Nomad multi-region ID (e.g. global), not contexts.region
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

// Admin holds optional admin-plane settings for a context.
type Admin struct {
	// Whoami is an optional operator persona label for abc-nodes contexts (e.g. su-mbhg-bioinformatics_admin).
	// When admin.abc_nodes.nomad_namespace is unset, Nomad namespace defaults are derived from known
	// _<role> suffixes on Whoami (see deriveNomadNamespaceFromAdminWhoami).
	Whoami   string         `yaml:"whoami,omitempty"`
	Services AdminServices  `yaml:"services,omitempty"`
	ABCNodes *AdminABCNodes `yaml:"abc_nodes,omitempty"`
}

// Services is the deprecated YAML shape under contexts.<name>.services (migrated on load).
type Services struct {
	Nomad *NomadService `yaml:"nomad,omitempty"`
}

// NomadAddr returns contexts.<name>.admin.services.nomad.nomad_addr.
func (c Context) NomadAddr() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Addr)
}

// NomadToken returns contexts.<name>.admin.services.nomad.nomad_token.
func (c Context) NomadToken() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Token)
}

// NomadRegion returns contexts.<name>.admin.services.nomad.nomad_region (Nomad RPC region).
// It is intentionally not the same as Context.Region (ABC / datacenter label such as za-cpt).
func (c Context) NomadRegion() string {
	if c.Admin.Services.Nomad == nil {
		return ""
	}
	return strings.TrimSpace(c.Admin.Services.Nomad.Region)
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

// normalizeContextNomad folds deprecated YAML (top-level nomad_*, services.nomad)
// into admin.services.nomad and clears legacy fields so the next save writes only admin.
func normalizeContextNomad(ctx *Context) {
	var addr, token string
	if ctx.Admin.Services.Nomad != nil {
		addr = strings.TrimSpace(ctx.Admin.Services.Nomad.Addr)
		token = strings.TrimSpace(ctx.Admin.Services.Nomad.Token)
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

	ctx.ServicesLegacy = Services{}
	ctx.LegacyNomadAddr = ""
	ctx.LegacyNomadToken = ""

	if addr == "" && token == "" {
		ctx.Admin.Services.Nomad = nil
		return
	}
	if ctx.Admin.Services.Nomad == nil {
		ctx.Admin.Services.Nomad = &NomadService{}
	}
	if strings.TrimSpace(ctx.Admin.Services.Nomad.Addr) == "" {
		ctx.Admin.Services.Nomad.Addr = addr
	}
	if strings.TrimSpace(ctx.Admin.Services.Nomad.Token) == "" {
		ctx.Admin.Services.Nomad.Token = token
	}
	if ctx.Admin.Services.Nomad != nil {
		if a := strings.TrimSpace(ctx.Admin.Services.Nomad.Addr); a != "" {
			ctx.Admin.Services.Nomad.Addr = CanonicalNomadAPIAddrForYAML(a)
		}
	}
	if strings.TrimSpace(ctx.Admin.Services.Nomad.Addr) == "" && strings.TrimSpace(ctx.Admin.Services.Nomad.Token) == "" {
		ctx.Admin.Services.Nomad = nil
	}
}
