// Package config manages the abc-cluster CLI configuration file.
//
// The config file is stored at ~/.abc/config.yaml by default.
// The location can be overridden with the ABC_CONFIG_FILE environment variable.
// After load, ABC_ACTIVE_CONTEXT (if set) overrides active_context in memory only
// when contexts.<name> exists (the file on disk is unchanged). If the name is not
// defined, the env var is ignored so minimal configs in unit tests still load.
// Example: ABC_ACTIVE_CONTEXT=aither go test -tags integration -v ./cmd/job/...
//
// Schema versioning:
// The config file includes a version field for forward/backward compatibility.
// Currently at version "1.0". Future versions will support schema migrations.
//
// Schema (v1):
//
//	version: "1.0"
//	active_context: "org-a-za-cpt"
//	contexts:
//	  primary: aither              # optional top-level redirect (alias name -> target context name)
//	  org-a-za-cpt:
//	    endpoint:        "https://api.abc-cluster.io"
//	    upload_endpoint: "https://api.abc-cluster.io/files/"  // defaults from endpoint + /files/
//	    upload_token:    "s.abc123..."
//	    access_token:    "eyJ..."
//	    organization_id: "org-dev"
//	    workspace_id:    ""
//	    region:          ""
//	    cluster_type:    "abc-nodes"  # optional: abc-nodes | abc-cluster | abc-cloud
//	    aliases:         ["lab"]      # optional: alternate names for abc context use (alias: "x" is also accepted)
//	    crypt:           # optional; local rclone crypt + abc secrets key material
//	      password: "..."
//	      salt:     "..."
//	    secrets:         # encrypted map; managed via abc secrets
//	      my-key: "base64..."
//	    auth: root              # optional shorthand: Nomad bootstrap / management token (same as auth.root: true)
//	    auth:
//	      whoami: "lab-admin"   # optional; Nomad ACL token label from GET /v1/acl/token/self (token Name, policies, or management)
//	      root: true            # optional; marks bootstrap-token contexts (can combine with whoami)
//	    admin:
//	      services:
//	        nomad:
//	          nomad_addr:  "http://100.70.185.46:4646"  # http must use an explicit :PORT on write; bare http://host is rewritten to :4646 on load/set
//	          nomad_token: "s.123..."
//	          nomad_region: "global"   # optional; Nomad RPC region (not the same as contexts.region)
//	      abc_nodes:              # optional; static operator creds when cluster_type is abc-nodes
//	        nomad_namespace: "default"
//	        s3_access_key: "..."
//	        s3_secret_key: "..."
//	        s3_region: "us-east-1"
//	        minio_root_user: "minioadmin"
//	        minio_root_password: "..."
//	        # S3 API bases live under admin.services (minio vs rustfs are separate):
//	        #   admin.services.minio.endpoint
//	        #   admin.services.rustfs.endpoint
//	        #   admin.services.traefik.http / endpoint  (Nomad dashboard vs web entry; from config sync)
//	defaults:
//	  output: "table"
//	  region: ""
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CurrentVersion is the current config file schema version (written first in YAML).
const CurrentVersion = "1.0"

// DefaultContextName is the placeholder context created by config init for first-time users.
const DefaultContextName = "default"

// DefaultPublicAPIEndpoint matches the auth login prompt default (ABC control plane).
const DefaultPublicAPIEndpoint = "https://api.abc-cluster.io"

// DefaultConfigPath returns the path to the config file, honouring the
// ABC_CONFIG_FILE environment variable.
func DefaultConfigPath() string {
	if v := os.Getenv("ABC_CONFIG_FILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".abc/config.yaml"
	}
	return filepath.Join(home, ".abc", "config.yaml")
}

// Context holds connection details for one named context.
type Context struct {
	Endpoint       string `yaml:"endpoint"`
	UploadEndpoint string `yaml:"upload_endpoint,omitempty"`
	UploadToken    string `yaml:"upload_token,omitempty"`
	AccessToken    string `yaml:"access_token"`
	OrgID          string `yaml:"organization_id,omitempty"`
	WorkspaceID    string `yaml:"workspace_id,omitempty"`
	Region         string `yaml:"region,omitempty"`
	// ClusterType is one of abc-nodes | abc-cluster | abc-cloud (platform tier).
	ClusterType  string        `yaml:"cluster_type,omitempty"`
	Admin        Admin         `yaml:"admin,omitempty"`
	Capabilities *Capabilities `yaml:"capabilities,omitempty"`
	// Job holds context-level job submission policy (driver priority, etc.).
	Job *ContextJob `yaml:"job,omitempty"`

	// Per-context encrypted secrets (abc secrets) and local crypt key material (rclone crypt / secrets).
	Secrets map[string]string `yaml:"secrets,omitempty"`
	Crypt   ContextCrypt      `yaml:"crypt,omitempty"`
	// Deprecated flat YAML keys; normalized into Crypt on load (see normalizeContextCrypt).
	FlatCryptPassword string `yaml:"crypt_password,omitempty"`
	FlatCryptSalt     string `yaml:"crypt_salt,omitempty"`

	// Deprecated YAML keys; normalized into admin on load (see normalizeContextNomad).
	ServicesLegacy   Services `yaml:"services,omitempty"`
	LegacyNomadAddr  string   `yaml:"nomad_addr,omitempty"`
	LegacyNomadToken string   `yaml:"nomad_token,omitempty"`

	// Aliases are alternate names for this context (abc context use <name>).
	// Singular "alias" in YAML is merged into Aliases on load.
	Aliases []string `yaml:"aliases,omitempty"`
	Alias   string   `yaml:"alias,omitempty"`

	// Auth holds derived operator identity (e.g. Nomad ACL token self).
	Auth *ContextAuth `yaml:"auth,omitempty"`
}

// Defaults holds user-level default values.
type Defaults struct {
	Output string `yaml:"output,omitempty"`
	Region string `yaml:"region,omitempty"`
}

// Config is the in-memory representation of ~/.abc/config.yaml.
type Config struct {
	Version        string             `yaml:"version,omitempty"`
	ActiveContext  string             `yaml:"active_context,omitempty"`
	Contexts       map[string]Context `yaml:"-"` // full definitions; YAML under contexts: is custom-parsed
	ContextAliases map[string]string  `yaml:"-"` // alias name -> target context name (YAML: contexts.<alias>: <target>)
	Defaults       Defaults           `yaml:"defaults,omitempty"`
}

// Load reads the config file. If the file does not exist, an empty Config is
// returned (no error). Any other read/parse error is returned as-is.
func Load() (*Config, error) {
	return LoadFrom(DefaultConfigPath())
}

// Create ensures the config file exists, creating it if necessary, and that a
// placeholder context named DefaultContextName is present with ActiveContext
// pointing at it when no active context is set. Existing files are loaded,
// merged, and rewritten (idempotent).
// Returns the path to the config file.
func Create() (string, error) {
	path := DefaultConfigPath()
	var cfg *Config
	if _, err := os.Stat(path); err == nil {
		loaded, err := LoadFrom(path)
		if err != nil {
			return "", err
		}
		cfg = loaded
	} else if errors.Is(err, os.ErrNotExist) {
		cfg = &Config{
			Version:        CurrentVersion,
			Contexts:       map[string]Context{},
			ContextAliases: map[string]string{},
		}
	} else {
		return "", fmt.Errorf("stat config %q: %w", path, err)
	}
	cfg.EnsureDefaultContext()
	if err := cfg.SaveTo(path); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureDefaultContext guarantees contexts.default exists (placeholder). It does
// not replace an existing default definition.
//
// If active_context is empty, it is set to DefaultContextName only when there were
// no contexts before this call (typical first-time init), or when the only defined
// context is DefaultContextName (repair hand-edited files).
func (c *Config) EnsureDefaultContext() {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	hadAny := len(c.Contexts) > 0
	if _, ok := c.Contexts[DefaultContextName]; !ok {
		ctx := Context{Endpoint: DefaultPublicAPIEndpoint}
		if up, err := DeriveUploadEndpointFromAPI(DefaultPublicAPIEndpoint); err == nil {
			ctx.UploadEndpoint = up
		}
		c.Contexts[DefaultContextName] = ctx
	}
	if strings.TrimSpace(c.ActiveContext) != "" {
		return
	}
	if !hadAny || len(c.Contexts) == 1 {
		c.ActiveContext = DefaultContextName
	}
}

// LoadFrom reads the config file at path. If the file does not exist, an
// empty Config is returned (no error). Missing version field defaults to CurrentVersion.
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{
				Version:        CurrentVersion,
				Contexts:       map[string]Context{},
				ContextAliases: map[string]string{},
			}, nil
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg, err := parseConfigYAML(data)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.Version == "" {
		cfg.Version = CurrentVersion
	}
	for name, ctx := range cfg.Contexts {
		normalizeContextNomad(&ctx)
		migrateAbcNodesLegacyS3Endpoint(&ctx)
		normalizeContextAbcNodes(&ctx)
		NormalizeFloorServices(&ctx)
		normalizeContextCrypt(&ctx)
		cfg.Contexts[name] = ctx
	}
	migrateLegacySecretsAndCrypt(data, cfg)
	if err := applyActiveContextEnvOverride(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyActiveContextEnvOverride(cfg *Config) error {
	name := strings.TrimSpace(os.Getenv("ABC_ACTIVE_CONTEXT"))
	if name == "" {
		return nil
	}
	if !cfg.HasDefinedContext(name) {
		return nil
	}
	cfg.ActiveContext = name
	return nil
}

// Save writes the config to the default path, creating parent directories as
// needed.
func (c *Config) Save() error {
	return c.SaveTo(DefaultConfigPath())
}

// Validate checks alias rules, active_context resolution, Nomad addresses, and cluster_type values.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if err := validateNoAliasKeyCollidesWithPrimaryName(c); err != nil {
		return err
	}
	if err := validateContextAliases(c); err != nil {
		return err
	}
	if ac := strings.TrimSpace(c.ActiveContext); ac != "" && !c.HasDefinedContext(ac) {
		return fmt.Errorf("active_context %q is not a defined context or alias", ac)
	}
	for _, name := range sortedContextNames(c.Contexts) {
		ctx := c.Contexts[name]
		if err := ValidateNomadAddrForContext(ctx.NomadAddr()); err != nil {
			return fmt.Errorf("contexts.%s.admin.services.nomad.nomad_addr: %w", name, err)
		}
		if err := ValidateAdminServicesFloorCredSource(ctx.Admin.Services); err != nil {
			return fmt.Errorf("contexts.%s: %w", name, err)
		}
		if tier := strings.TrimSpace(ctx.ClusterType); tier != "" {
			if _, ok := NormalizeClusterType(tier); !ok {
				return fmt.Errorf("contexts.%s.cluster_type: invalid value %q (want %s, %s, or %s)", name, tier, ClusterTypeABCNodes, ClusterTypeABCCluster, ClusterTypeABCCloud)
			}
		}
	}
	return nil
}

// MarshalDocumentYAML returns canonical YAML (sorted keys at each mapping level) as written by Save/SaveTo.
// It normalizes the file version field the same way SaveTo does.
func (c *Config) MarshalDocumentYAML() ([]byte, error) {
	c.Version = normalizeConfigFileVersionForSave(c.Version)
	return c.marshalConfigDocumentYAML()
}

// SaveTo writes the config to path, creating parent directories as needed.
// Ensures version is set to current version before writing.
func (c *Config) SaveTo(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := c.MarshalDocumentYAML()
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

// ActiveCtx returns the Context for the active_context name, or an empty
// Context if no context is set.
func (c *Config) ActiveCtx() Context {
	if c.ActiveContext == "" {
		return Context{}
	}
	ctx, _ := c.ContextNamed(c.ActiveContext)
	return ctx
}

// SetContext upserts a named context and marks it active.
func (c *Config) SetContext(name string, ctx Context) error {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	if c.ContextAliases == nil {
		c.ContextAliases = map[string]string{}
	}
	delete(c.ContextAliases, name)
	removeAliasKeysForCanonical(c, name)
	normalizeContextAliasList(&ctx)
	c.Contexts[name] = ctx
	if err := integratePerContextAliasesIntoMap(c); err != nil {
		return err
	}
	c.ActiveContext = name
	return nil
}

// ClearContext removes the named context. If it was the active context,
// active_context is cleared.
func (c *Config) ClearContext(name string) {
	if c.ContextAliases != nil {
		delete(c.ContextAliases, name)
	}
	if _, ok := c.Contexts[name]; ok {
		removeAliasKeysForCanonical(c, name)
		delete(c.Contexts, name)
	}
	if c.ActiveContext == name || (c.ActiveContext != "" && !c.HasDefinedContext(c.ActiveContext)) {
		c.ActiveContext = ""
	}
}

// Get returns a config value by dot-separated key path.
// Supported paths: active_context, defaults.output, defaults.region,
// contexts.<name>.endpoint, contexts.<name>.access_token, etc.
//
// Nomad: contexts.<name>.admin.services.nomad.nomad_addr / nomad_token / nomad_region
// (nomad_addr for http:// must include an explicit :PORT when set via config.Set).
// Auth: contexts.<name>.auth.whoami | auth.root | shorthand auth: root (bootstrap token).
// Admin: contexts.<name>.admin.whoami (optional persona label; Nomad namespace can be derived for abc-nodes).
// abc-nodes floor: contexts.<name>.admin.abc_nodes.<field> (see AdminABCNodes).
func (c *Config) Get(key string) (string, bool) {
	parts := strings.Split(key, ".")
	switch parts[0] {
	case "active_context":
		return c.ActiveContext, true
	case "defaults":
		if len(parts) < 2 {
			return "", false
		}
		switch parts[1] {
		case "output":
			return c.Defaults.Output, true
		case "region":
			return c.Defaults.Region, true
		}
	case "contexts":
		if len(parts) < 3 {
			return "", false
		}
		canon := c.ResolveContextName(parts[1])
		if canon == "" {
			return "", false
		}
		ctx := c.Contexts[canon]
		// contexts.<name>.crypt.password | contexts.<name>.crypt.salt
		if len(parts) == 4 && parts[2] == "crypt" {
			switch parts[3] {
			case "password":
				return ctx.Crypt.Password, true
			case "salt":
				return ctx.Crypt.Salt, true
			}
			return "", false
		}
		if len(parts) == 4 && parts[2] == "auth" {
			if ctx.Auth == nil {
				return "", false
			}
			switch parts[3] {
			case "whoami":
				if strings.TrimSpace(ctx.Auth.Whoami) == "" {
					return "", false
				}
				return ctx.Auth.Whoami, true
			case "root":
				if !ctx.Auth.Root {
					return "", false
				}
				return "true", true
			}
			return "", false
		}
		if len(parts) == 4 && parts[2] == "admin" && parts[3] == "whoami" {
			if v := strings.TrimSpace(ctx.Admin.Whoami); v != "" {
				return v, true
			}
			return "", false
		}
		// contexts.<name>.admin.services.<svc>.<field>
		if len(parts) == 6 && parts[2] == "admin" && parts[3] == "services" {
			if parts[4] == "nomad" {
				if ctx.Admin.Services.Nomad == nil {
					return "", false
				}
				switch parts[5] {
				case "nomad_addr":
					return ctx.Admin.Services.Nomad.Addr, true
				case "nomad_token":
					return ctx.Admin.Services.Nomad.Token, true
				case "nomad_region":
					return ctx.Admin.Services.Nomad.Region, true
				}
				return "", false
			}
			v, ok := GetAdminFloorField(&ctx.Admin.Services, parts[4], parts[5])
			return v, ok
		}
		// contexts.<name>.admin.tools.<field>
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "tools" {
			if ctx.Admin.Tools == nil {
				return "", false
			}
			switch parts[4] {
			case "context_service":
				return ctx.Admin.Tools.ContextService, true
			case "endpoint":
				return ctx.Admin.Tools.Endpoint, true
			}
			return "", false
		}
		// contexts.<name>.admin.abc_nodes.<field>
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "abc_nodes" {
			if parts[4] == "nomad_namespace" {
				if v := strings.TrimSpace(ctx.resolvedAbcNodesNomadNamespace()); v != "" {
					return v, true
				}
				return "", false
			}
			n := ctx.abcNodes()
			if n == nil {
				return "", false
			}
			switch parts[4] {
			case "s3_access_key":
				return n.S3AccessKey, true
			case "s3_secret_key":
				return n.S3SecretKey, true
			case "s3_region":
				return n.S3Region, true
			case "s3_endpoint":
				// Deprecated alias for admin.services.minio.endpoint.
				if v, ok := GetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint"); ok {
					return v, true
				}
				if ep := strings.TrimSpace(n.S3Endpoint); ep != "" {
					return ep, true
				}
				return "", false
			case "minio_root_user":
				return n.MinioRootUser, true
			case "minio_root_password":
				return n.MinioRootPassword, true
			}
			return "", false
		}
		if len(parts) != 3 {
			return "", false
		}
		switch parts[2] {
		case "endpoint":
			return ctx.Endpoint, true
		case "upload_endpoint":
			return ctx.UploadEndpoint, true
		case "upload_token":
			return ctx.UploadToken, true
		case "access_token":
			return ctx.AccessToken, true
		case "organization_id":
			return ctx.OrgID, true
		case "workspace_id":
			return ctx.WorkspaceID, true
		case "region":
			return ctx.Region, true
		case "cluster_type":
			return ctx.ClusterType, true
		case "aliases":
			a := AliasesResolvingToCanon(c, canon)
			if len(a) == 0 {
				return "", false
			}
			return strings.Join(a, ","), true
		}
	}
	return "", false
}

// Set sets a config value by dot-separated key path. Returns an error for
// unknown keys.
func (c *Config) Set(key, value string) error {
	parts := strings.SplitN(key, ".", 3)
	switch parts[0] {
	case "active_context":
		c.ActiveContext = value
		return nil
	case "defaults":
		if len(parts) < 2 {
			return fmt.Errorf("unknown config key %q", key)
		}
		switch parts[1] {
		case "output":
			c.Defaults.Output = value
		case "region":
			c.Defaults.Region = value
		default:
			return fmt.Errorf("unknown config key %q", key)
		}
		return nil
	case "contexts":
		parts := strings.Split(key, ".")
		if len(parts) < 3 {
			return fmt.Errorf("unknown config key %q; use contexts.<name>.<field>", key)
		}
		name := parts[1]
		canon := c.ResolveContextName(name)
		if canon == "" {
			return fmt.Errorf("unknown context %q", name)
		}
		ctx := c.Contexts[canon]
		if len(parts) == 6 && parts[2] == "admin" && parts[3] == "services" {
			if parts[4] == "nomad" {
				if ctx.Admin.Services.Nomad == nil {
					ctx.Admin.Services.Nomad = &NomadService{}
				}
				switch parts[5] {
				case "nomad_addr":
					v := CanonicalNomadAPIAddrForYAML(strings.TrimSpace(value))
					if err := ValidateNomadAddrForContext(v); err != nil {
						return err
					}
					ctx.Admin.Services.Nomad.Addr = v
				case "nomad_token":
					ctx.Admin.Services.Nomad.Token = value
					if strings.TrimSpace(value) == "" {
						ctx.SetAuthWhoami("")
					}
				case "nomad_region":
					ctx.Admin.Services.Nomad.Region = value
				default:
					return fmt.Errorf("unknown admin.services.nomad field %q", parts[5])
				}
				c.Contexts[canon] = ctx
				return nil
			}
			if err := SetAdminFloorField(&ctx.Admin.Services, parts[4], parts[5], value); err != nil {
				return err
			}
			NormalizeFloorServices(&ctx)
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "tools" {
			if ctx.Admin.Tools == nil {
				ctx.Admin.Tools = &AdminTools{}
			}
			switch parts[4] {
			case "context_service":
				ctx.Admin.Tools.ContextService = strings.TrimSpace(value)
			case "endpoint":
				ctx.Admin.Tools.Endpoint = strings.TrimSpace(value)
			default:
				return fmt.Errorf("unknown admin.tools field %q (supported: context_service, endpoint)", parts[4])
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "abc_nodes" {
			if ctx.Admin.ABCNodes == nil {
				ctx.Admin.ABCNodes = &AdminABCNodes{}
			}
			n := ctx.Admin.ABCNodes
			switch parts[4] {
			case "nomad_namespace":
				n.NomadNamespace = value
			case "s3_access_key":
				n.S3AccessKey = value
			case "s3_secret_key":
				n.S3SecretKey = value
			case "s3_region":
				n.S3Region = value
			case "s3_endpoint":
				// Deprecated: sets admin.services.minio.endpoint (same as floor key).
				if err := SetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint", value); err != nil {
					return err
				}
				n.S3Endpoint = ""
				NormalizeFloorServices(&ctx)
			case "minio_root_user":
				n.MinioRootUser = value
			case "minio_root_password":
				n.MinioRootPassword = value
			default:
				return fmt.Errorf("unknown admin.abc_nodes field %q", parts[4])
			}
			normalizeContextAbcNodes(&ctx)
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "admin" && parts[3] == "whoami" {
			ctx.Admin.Whoami = strings.TrimSpace(value)
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "crypt" {
			switch parts[3] {
			case "password":
				ctx.Crypt.Password = value
			case "salt":
				ctx.Crypt.Salt = value
			default:
				return fmt.Errorf("unknown crypt field %q (expected password or salt)", parts[3])
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "auth" {
			switch parts[3] {
			case "whoami":
				ctx.SetAuthWhoami(value)
			case "root":
				v := strings.TrimSpace(strings.ToLower(value))
				truth := v == "true" || v == "1" || v == "yes"
				if !truth && v != "" && v != "false" && v != "0" && v != "no" {
					return fmt.Errorf("contexts.%s.auth.root: expected true or false", canon)
				}
				if truth {
					if ctx.Auth == nil {
						ctx.Auth = &ContextAuth{}
					}
					ctx.Auth.Root = true
				} else {
					if ctx.Auth != nil {
						ctx.Auth.Root = false
					}
					ctx.clearAuthIfEmpty()
				}
			default:
				return fmt.Errorf("unknown auth field %q (expected whoami or root)", parts[3])
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) != 3 {
			return fmt.Errorf("unknown config key %q", key)
		}
		switch parts[2] {
		case "endpoint":
			ctx.Endpoint = value
		case "upload_endpoint":
			ctx.UploadEndpoint = value
		case "upload_token":
			ctx.UploadToken = value
		case "access_token":
			ctx.AccessToken = value
		case "organization_id":
			ctx.OrgID = value
		case "workspace_id":
			ctx.WorkspaceID = value
		case "region":
			ctx.Region = value
		case "cluster_type":
			norm, ok := NormalizeClusterType(value)
			if !ok {
				return fmt.Errorf("invalid cluster_type %q (want %s, %s, or %s)", value, ClusterTypeABCNodes, ClusterTypeABCCluster, ClusterTypeABCCloud)
			}
			ctx.ClusterType = norm
		case "aliases":
			removeAliasKeysForCanonical(c, canon)
			ctx.Aliases = nil
			ctx.Alias = ""
			for _, p := range strings.Split(value, ",") {
				a := strings.TrimSpace(p)
				if a == "" || a == canon {
					continue
				}
				if _, exists := c.Contexts[a]; exists {
					return fmt.Errorf("alias %q conflicts with an existing context name", a)
				}
				if prev, ok := c.ContextAliases[a]; ok && prev != canon {
					return fmt.Errorf("alias %q already maps to context %q", a, prev)
				}
				if c.ContextAliases == nil {
					c.ContextAliases = map[string]string{}
				}
				c.ContextAliases[a] = canon
				ctx.Aliases = append(ctx.Aliases, a)
			}
			sort.Strings(ctx.Aliases)
		default:
			return fmt.Errorf("unknown context field %q", parts[2])
		}
		c.Contexts[canon] = ctx
		return nil
	}
	return fmt.Errorf("unknown config key %q", key)
}

// Unset removes a config value by dot-separated key path.
func (c *Config) Unset(key string) error {
	parts := strings.SplitN(key, ".", 3)
	switch parts[0] {
	case "active_context":
		c.ActiveContext = ""
		return nil
	case "defaults":
		if len(parts) < 2 {
			return fmt.Errorf("unknown config key %q", key)
		}
		switch parts[1] {
		case "output":
			c.Defaults.Output = ""
		case "region":
			c.Defaults.Region = ""
		default:
			return fmt.Errorf("unknown config key %q", key)
		}
		return nil
	case "contexts":
		parts := strings.Split(key, ".")
		if len(parts) < 3 {
			return fmt.Errorf("use 'abc config unset contexts.<name>' to remove an entire context")
		}
		name := parts[1]
		canon := c.ResolveContextName(name)
		if canon == "" {
			return fmt.Errorf("context %q not found", name)
		}
		ctx := c.Contexts[canon]
		if len(parts) == 6 && parts[2] == "admin" && parts[3] == "services" {
			if parts[4] == "nomad" {
				if ctx.Admin.Services.Nomad == nil {
					return nil
				}
				switch parts[5] {
				case "nomad_addr":
					ctx.Admin.Services.Nomad.Addr = ""
				case "nomad_token":
					ctx.Admin.Services.Nomad.Token = ""
					ctx.ClearAuth()
				case "nomad_region":
					ctx.Admin.Services.Nomad.Region = ""
				default:
					return fmt.Errorf("unknown admin.services.nomad field %q", parts[5])
				}
				if ctx.Admin.Services.Nomad.Addr == "" && ctx.Admin.Services.Nomad.Token == "" && ctx.Admin.Services.Nomad.Region == "" {
					ctx.Admin.Services.Nomad = nil
				}
				c.Contexts[canon] = ctx
				return nil
			}
			if err := UnsetAdminFloorField(&ctx.Admin.Services, parts[4], parts[5]); err != nil {
				return err
			}
			NormalizeFloorServices(&ctx)
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "tools" {
			if ctx.Admin.Tools != nil {
				switch parts[4] {
				case "context_service":
					ctx.Admin.Tools.ContextService = ""
				case "endpoint":
					ctx.Admin.Tools.Endpoint = ""
				default:
					return fmt.Errorf("unknown admin.tools field %q (supported: context_service, endpoint)", parts[4])
				}
				if ctx.Admin.Tools.ContextService == "" && ctx.Admin.Tools.Endpoint == "" && len(ctx.Admin.Tools.Architectures) == 0 {
					ctx.Admin.Tools = nil
				}
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 5 && parts[2] == "admin" && parts[3] == "abc_nodes" {
			if ctx.Admin.ABCNodes == nil {
				return nil
			}
			n := ctx.Admin.ABCNodes
			switch parts[4] {
			case "nomad_namespace":
				n.NomadNamespace = ""
			case "s3_access_key":
				n.S3AccessKey = ""
			case "s3_secret_key":
				n.S3SecretKey = ""
			case "s3_region":
				n.S3Region = ""
			case "s3_endpoint":
				if err := UnsetAdminFloorField(&ctx.Admin.Services, "minio", "endpoint"); err != nil {
					return err
				}
				n.S3Endpoint = ""
				NormalizeFloorServices(&ctx)
			case "minio_root_user":
				n.MinioRootUser = ""
			case "minio_root_password":
				n.MinioRootPassword = ""
			default:
				return fmt.Errorf("unknown admin.abc_nodes field %q", parts[4])
			}
			normalizeContextAbcNodes(&ctx)
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "admin" && parts[3] == "whoami" {
			ctx.Admin.Whoami = ""
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "crypt" {
			switch parts[3] {
			case "password":
				ctx.Crypt.Password = ""
			case "salt":
				ctx.Crypt.Salt = ""
			default:
				return fmt.Errorf("unknown crypt field %q (expected password or salt)", parts[3])
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 4 && parts[2] == "auth" {
			switch parts[3] {
			case "whoami":
				ctx.SetAuthWhoami("")
			case "root":
				if ctx.Auth != nil {
					ctx.Auth.Root = false
				}
				ctx.clearAuthIfEmpty()
			default:
				return fmt.Errorf("unknown auth field %q (expected whoami or root)", parts[3])
			}
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) == 3 && parts[2] == "auth" {
			ctx.ClearAuth()
			c.Contexts[canon] = ctx
			return nil
		}
		if len(parts) != 3 {
			return fmt.Errorf("unknown config key %q", key)
		}
		switch parts[2] {
		case "endpoint":
			ctx.Endpoint = ""
		case "upload_endpoint":
			ctx.UploadEndpoint = ""
		case "upload_token":
			ctx.UploadToken = ""
		case "access_token":
			ctx.AccessToken = ""
		case "organization_id":
			ctx.OrgID = ""
		case "workspace_id":
			ctx.WorkspaceID = ""
		case "region":
			ctx.Region = ""
		case "cluster_type":
			ctx.ClusterType = ""
		case "aliases":
			removeAliasKeysForCanonical(c, canon)
			ctx.Aliases = nil
			ctx.Alias = ""
		default:
			return fmt.Errorf("unknown context field %q", parts[2])
		}
		c.Contexts[canon] = ctx
		return nil
	}
	return fmt.Errorf("unknown config key %q", key)
}

// AllKeys returns all key-value pairs in the config as a flat slice of [key, value] pairs,
// masking access tokens so only the first 8 characters are shown.
func (c *Config) AllKeys() [][2]string {
	var out [][2]string
	out = append(out, [2]string{"active_context", c.ActiveContext})
	out = append(out, [2]string{"defaults.output", c.Defaults.Output})
	out = append(out, [2]string{"defaults.region", c.Defaults.Region})
	for _, name := range c.AllContextEntryNames() {
		if target, ok := c.ContextAliases[name]; ok {
			out = append(out, [2]string{"contexts." + name, "-> " + target})
			continue
		}
		ctx := c.Contexts[name]
		out = append(out, [2]string{"contexts." + name + ".endpoint", ctx.Endpoint})
		if ctx.UploadEndpoint != "" {
			out = append(out, [2]string{"contexts." + name + ".upload_endpoint", ctx.UploadEndpoint})
		}
		if ctx.UploadToken != "" {
			out = append(out, [2]string{"contexts." + name + ".upload_token", maskToken(ctx.UploadToken)})
		}
		out = append(out, [2]string{"contexts." + name + ".access_token", maskToken(ctx.AccessToken)})
		if ctx.OrgID != "" {
			out = append(out, [2]string{"contexts." + name + ".organization_id", ctx.OrgID})
		}
		if ctx.WorkspaceID != "" {
			out = append(out, [2]string{"contexts." + name + ".workspace_id", ctx.WorkspaceID})
		}
		if ctx.Region != "" {
			out = append(out, [2]string{"contexts." + name + ".region", ctx.Region})
		}
		if ctx.ClusterType != "" {
			out = append(out, [2]string{"contexts." + name + ".cluster_type", ctx.ClusterType})
		}
		if ctx.Auth != nil {
			if strings.TrimSpace(ctx.Auth.Whoami) != "" {
				out = append(out, [2]string{"contexts." + name + ".auth.whoami", ctx.Auth.Whoami})
			}
			if ctx.Auth.Root {
				out = append(out, [2]string{"contexts." + name + ".auth.root", "true"})
			}
		}
		if v := strings.TrimSpace(ctx.Admin.Whoami); v != "" {
			out = append(out, [2]string{"contexts." + name + ".admin.whoami", v})
		}
		if als := AliasesResolvingToCanon(c, name); len(als) > 0 {
			out = append(out, [2]string{"contexts." + name + ".aliases", strings.Join(als, ",")})
		}
		if ctx.Admin.Services.Nomad != nil {
			if ctx.Admin.Services.Nomad.Addr != "" {
				out = append(out, [2]string{"contexts." + name + ".admin.services.nomad.nomad_addr", ctx.Admin.Services.Nomad.Addr})
			}
			if ctx.Admin.Services.Nomad.Token != "" {
				out = append(out, [2]string{"contexts." + name + ".admin.services.nomad.nomad_token", maskToken(ctx.Admin.Services.Nomad.Token)})
			}
			if ctx.Admin.Services.Nomad.Region != "" {
				out = append(out, [2]string{"contexts." + name + ".admin.services.nomad.nomad_region", ctx.Admin.Services.Nomad.Region})
			}
		}
		out = AppendAdminFloorAllKeys("contexts."+name, ctx.Admin.Services, out)
		if n := ctx.Admin.ABCNodes; n != nil {
			pfx := "contexts." + name + ".admin.abc_nodes."
			if n.NomadNamespace != "" {
				out = append(out, [2]string{pfx + "nomad_namespace", n.NomadNamespace})
			}
			if n.S3AccessKey != "" {
				out = append(out, [2]string{pfx + "s3_access_key", maskToken(n.S3AccessKey)})
			}
			if n.S3SecretKey != "" {
				out = append(out, [2]string{pfx + "s3_secret_key", maskToken(n.S3SecretKey)})
			}
			if n.S3Region != "" {
				out = append(out, [2]string{pfx + "s3_region", n.S3Region})
			}
			if n.MinioRootUser != "" {
				out = append(out, [2]string{pfx + "minio_root_user", maskToken(n.MinioRootUser)})
			}
			if n.MinioRootPassword != "" {
				out = append(out, [2]string{pfx + "minio_root_password", maskToken(n.MinioRootPassword)})
			}
		}
		if ctx.Crypt.Password != "" {
			out = append(out, [2]string{"contexts." + name + ".crypt.password", maskToken(ctx.Crypt.Password)})
		}
		if ctx.Crypt.Salt != "" {
			out = append(out, [2]string{"contexts." + name + ".crypt.salt", maskToken(ctx.Crypt.Salt)})
		}
		for sk := range ctx.Secrets {
			out = append(out, [2]string{"contexts." + name + ".secrets." + sk, maskToken(ctx.Secrets[sk])})
		}
	}
	return out
}

func maskToken(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return strings.Repeat("•", len(tok))
	}
	return tok[:8] + strings.Repeat("•", 12)
}
