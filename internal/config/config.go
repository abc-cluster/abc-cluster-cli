// Package config manages the abc-cluster CLI configuration file.
//
// The config file is stored at ~/.abc/config.yaml by default.
// The location can be overridden with the ABC_CONFIG_FILE environment variable.
//
// Schema versioning:
// The config file includes a version field for forward/backward compatibility.
// Currently at version "1". Future versions will support schema migrations.
//
// Schema (v1):
//
//	version: "1"
//	active_context: "org-a-za-cpt"
//	contexts:
//	  org-a-za-cpt:
//	    endpoint:        "https://api.abc-cluster.io"
//	    upload_endpoint: "https://uploads.abc-cluster.io/files/"
//	    upload_token:    "s.abc123..."
//	    access_token:    "eyJ..."
//	    cluster:         "dev-cluster"
//	    organization_id: "org-dev"
//	    workspace_id:    ""
//	    region:          ""
//	    nomad_addr:      "http://100.70.185.46:4646"
//	    nomad_token:     "s.123..."
//	defaults:
//	  output: "table"
//	  region: ""
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentVersion is the current config file schema version.
const CurrentVersion = "1"

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
	Cluster        string `yaml:"cluster,omitempty"`
	OrgID          string `yaml:"organization_id,omitempty"`
	WorkspaceID    string `yaml:"workspace_id,omitempty"`
	Region         string `yaml:"region,omitempty"`
	NomadAddr      string `yaml:"nomad_addr,omitempty"`
	NomadToken     string `yaml:"nomad_token,omitempty"`
}

// Defaults holds user-level default values.
type Defaults struct {
	Output        string `yaml:"output,omitempty"`
	Region        string `yaml:"region,omitempty"`
	CryptPassword string `yaml:"crypt_password,omitempty"`
	CryptSalt     string `yaml:"crypt_salt,omitempty"`
}

// Config is the in-memory representation of ~/.abc/config.yaml.
type Config struct {
	Version       string             `yaml:"version,omitempty"`
	ActiveContext string             `yaml:"active_context,omitempty"`
	Contexts      map[string]Context `yaml:"contexts,omitempty"`
	Defaults      Defaults           `yaml:"defaults,omitempty"`
	Secrets       map[string]string  `yaml:"secrets,omitempty"` // Encrypted secrets managed via 'abc secrets'
}

// Load reads the config file. If the file does not exist, an empty Config is
// returned (no error). Any other read/parse error is returned as-is.
func Load() (*Config, error) {
	return LoadFrom(DefaultConfigPath())
}

// Create ensures the config file exists, creating it if necessary.
// Returns the path to the config file.
func Create() (string, error) {
	path := DefaultConfigPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil // Already exists
	}
	cfg := &Config{
		Version:  CurrentVersion,
		Contexts: map[string]Context{},
	}
	if err := cfg.SaveTo(path); err != nil {
		return "", err
	}
	return path, nil
}

// LoadFrom reads the config file at path. If the file does not exist, an
// empty Config is returned (no error). Missing version field defaults to "1".
func LoadFrom(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{
				Version:  CurrentVersion,
				Contexts: map[string]Context{},
				Secrets:  map[string]string{},
			}, nil
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]string{}
	}
	if cfg.Version == "" {
		cfg.Version = CurrentVersion
	}
	return &cfg, nil
}

// Save writes the config to the default path, creating parent directories as
// needed.
func (c *Config) Save() error {
	return c.SaveTo(DefaultConfigPath())
}

// SaveTo writes the config to path, creating parent directories as needed.
// Ensures version is set to current version before writing.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if c.Version == "" {
		c.Version = CurrentVersion
	}
	data, err := yaml.Marshal(c)
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
	return c.Contexts[c.ActiveContext]
}

// SetContext upserts a named context and marks it active.
func (c *Config) SetContext(name string, ctx Context) {
	if c.Contexts == nil {
		c.Contexts = map[string]Context{}
	}
	c.Contexts[name] = ctx
	c.ActiveContext = name
}

// ClearContext removes the named context. If it was the active context,
// active_context is cleared.
func (c *Config) ClearContext(name string) {
	delete(c.Contexts, name)
	if c.ActiveContext == name {
		c.ActiveContext = ""
	}
}

// Get returns a config value by dot-separated key path.
// Supported paths: active_context, defaults.output, defaults.region,
// contexts.<name>.endpoint, contexts.<name>.access_token, etc.
func (c *Config) Get(key string) (string, bool) {
	parts := strings.SplitN(key, ".", 3)
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
		ctx, ok := c.Contexts[parts[1]]
		if !ok {
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
		case "cluster":
			return ctx.Cluster, true
		case "organization_id":
			return ctx.OrgID, true
		case "workspace_id":
			return ctx.WorkspaceID, true
		case "region":
			return ctx.Region, true
		case "nomad_addr":
			return ctx.NomadAddr, true
		case "nomad_token":
			return ctx.NomadToken, true
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
		if len(parts) < 3 {
			return fmt.Errorf("unknown config key %q; use contexts.<name>.<field>", key)
		}
		ctx := c.Contexts[parts[1]]
		switch parts[2] {
		case "endpoint":
			ctx.Endpoint = value
		case "upload_endpoint":
			ctx.UploadEndpoint = value
		case "upload_token":
			ctx.UploadToken = value
		case "access_token":
			ctx.AccessToken = value
		case "cluster":
			ctx.Cluster = value
		case "organization_id":
			ctx.OrgID = value
		case "workspace_id":
			ctx.WorkspaceID = value
		case "region":
			ctx.Region = value
		case "nomad_addr":
			ctx.NomadAddr = value
		case "nomad_token":
			ctx.NomadToken = value
		default:
			return fmt.Errorf("unknown context field %q", parts[2])
		}
		c.Contexts[parts[1]] = ctx
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
		if len(parts) < 3 {
			return fmt.Errorf("use 'abc config unset contexts.<name>' to remove an entire context")
		}
		ctx, ok := c.Contexts[parts[1]]
		if !ok {
			return fmt.Errorf("context %q not found", parts[1])
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
		case "cluster":
			ctx.Cluster = ""
		case "organization_id":
			ctx.OrgID = ""
		case "workspace_id":
			ctx.WorkspaceID = ""
		case "region":
			ctx.Region = ""
		case "nomad_addr":
			ctx.NomadAddr = ""
		case "nomad_token":
			ctx.NomadToken = ""
		default:
			return fmt.Errorf("unknown context field %q", parts[2])
		}
		c.Contexts[parts[1]] = ctx
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
	for name, ctx := range c.Contexts {
		out = append(out, [2]string{"contexts." + name + ".endpoint", ctx.Endpoint})
		if ctx.UploadEndpoint != "" {
			out = append(out, [2]string{"contexts." + name + ".upload_endpoint", ctx.UploadEndpoint})
		}
		if ctx.UploadToken != "" {
			out = append(out, [2]string{"contexts." + name + ".upload_token", maskToken(ctx.UploadToken)})
		}
		out = append(out, [2]string{"contexts." + name + ".access_token", maskToken(ctx.AccessToken)})
		if ctx.Cluster != "" {
			out = append(out, [2]string{"contexts." + name + ".cluster", ctx.Cluster})
		}
		if ctx.OrgID != "" {
			out = append(out, [2]string{"contexts." + name + ".organization_id", ctx.OrgID})
		}
		if ctx.WorkspaceID != "" {
			out = append(out, [2]string{"contexts." + name + ".workspace_id", ctx.WorkspaceID})
		}
		if ctx.Region != "" {
			out = append(out, [2]string{"contexts." + name + ".region", ctx.Region})
		}
		if ctx.NomadAddr != "" {
			out = append(out, [2]string{"contexts." + name + ".nomad_addr", ctx.NomadAddr})
		}
		if ctx.NomadToken != "" {
			out = append(out, [2]string{"contexts." + name + ".nomad_token", maskToken(ctx.NomadToken)})
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
