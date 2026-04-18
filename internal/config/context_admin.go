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
// Use endpoint for S3 API bases (MinIO, RustFS) and http for HTTP services (tusd, Grafana, …).
// traefik_http / traefik_endpoint mirror http / endpoint but hold Host()-style public
// bases from Traefik (config sync when abc-nodes-traefik is running); Nomad dynamic
// ports stay in http / endpoint.
// access_key/secret_key suit S3-compatible services; user/password suit web UIs.
type AdminFloorService struct {
	HTTP     string `yaml:"http,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
	// TraefikHTTP / TraefikEndpoint are public router bases (scheme + hostname), not Nomad host:port.
	TraefikHTTP     string `yaml:"traefik_http,omitempty"`
	TraefikEndpoint string `yaml:"traefik_endpoint,omitempty"`
	AccessKey       string `yaml:"access_key,omitempty"`
	SecretKey       string `yaml:"secret_key,omitempty"`
	User            string `yaml:"user,omitempty"`
	Password        string `yaml:"password,omitempty"`
	// PingEntryPoint names the entry point used for `traefik healthcheck` static snippets
	// (default in Traefik is "traefik", i.e. the dashboard listener). Only used for traefik.
	PingEntryPoint string `yaml:"ping_entrypoint,omitempty"`
}

// IsEmpty reports whether all URL and credential fields are unset.
func (a *AdminFloorService) IsEmpty() bool {
	if a == nil {
		return true
	}
	return strings.TrimSpace(a.HTTP) == "" &&
		strings.TrimSpace(a.Endpoint) == "" &&
		strings.TrimSpace(a.TraefikHTTP) == "" &&
		strings.TrimSpace(a.TraefikEndpoint) == "" &&
		strings.TrimSpace(a.AccessKey) == "" &&
		strings.TrimSpace(a.SecretKey) == "" &&
		strings.TrimSpace(a.User) == "" &&
		strings.TrimSpace(a.Password) == "" &&
		strings.TrimSpace(a.PingEntryPoint) == ""
}

// AdminServices holds operator-facing integrations under contexts.<name>.admin.services.
type AdminServices struct {
	Nomad      *NomadService      `yaml:"nomad,omitempty"`
	MinIO      *AdminFloorService `yaml:"minio,omitempty"`
	Tusd       *AdminFloorService `yaml:"tusd,omitempty"`
	Grafana    *AdminFloorService `yaml:"grafana,omitempty"`
	Prometheus *AdminFloorService `yaml:"prometheus,omitempty"`
	Loki       *AdminFloorService `yaml:"loki,omitempty"`
	Ntfy       *AdminFloorService `yaml:"ntfy,omitempty"`
	Rustfs     *AdminFloorService `yaml:"rustfs,omitempty"`
	Traefik    *AdminFloorService `yaml:"traefik,omitempty"`
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
	if strings.TrimSpace(ctx.Admin.Services.Nomad.Addr) == "" && strings.TrimSpace(ctx.Admin.Services.Nomad.Token) == "" {
		ctx.Admin.Services.Nomad = nil
	}
}
