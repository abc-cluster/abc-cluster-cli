package config

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// ContextForSecrets returns the context used for abc secrets and stored rclone crypt
// material (contexts.<name>.secrets, contexts.<name>.crypt.password / crypt.salt).
//
// It uses active_context when set and valid; otherwise the sole context if exactly
// one exists.
func (c *Config) ContextForSecrets() (name string, ctx Context, err error) {
	if c.ActiveContext != "" {
		ctx, ok := c.Contexts[c.ActiveContext]
		if !ok {
			return "", Context{}, fmt.Errorf("active_context %q is not defined under contexts; fix config or run: abc context use <name>", c.ActiveContext)
		}
		return c.ActiveContext, ctx, nil
	}
	if len(c.Contexts) == 1 {
		for n, ctx := range c.Contexts {
			return n, ctx, nil
		}
	}
	return "", Context{}, fmt.Errorf("no active_context set; choose the context for secrets and crypt with: abc context use <name>")
}

// ActiveContextCrypt returns stored contexts.<name>.crypt.password and .salt for the context
// selected by ContextForSecrets.
func (c *Config) ActiveContextCrypt() (password, salt string, err error) {
	_, ctx, err := c.ContextForSecrets()
	if err != nil {
		return "", "", err
	}
	return ctx.Crypt.Password, ctx.Crypt.Salt, nil
}

func sortedContextNames(m map[string]Context) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// migrationTargetForLegacy picks where to merge legacy root secrets: and defaults.crypt_* on load.
func migrationTargetForLegacy(c *Config) string {
	if c.ActiveContext != "" {
		if _, ok := c.Contexts[c.ActiveContext]; ok {
			return c.ActiveContext
		}
	}
	if len(c.Contexts) == 1 {
		for n := range c.Contexts {
			return n
		}
	}
	names := sortedContextNames(c.Contexts)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

type legacyConfigFragment struct {
	Secrets  map[string]string `yaml:"secrets"`
	Defaults struct {
		CryptPassword string `yaml:"crypt_password"`
		CryptSalt     string `yaml:"crypt_salt"`
	} `yaml:"defaults"`
}

// migrateLegacySecretsAndCrypt merges legacy top-level secrets and defaults.crypt_*
// from raw YAML into one context. Does not overwrite existing per-context keys or crypt.
func migrateLegacySecretsAndCrypt(raw []byte, cfg *Config) {
	var leg legacyConfigFragment
	if err := yaml.Unmarshal(raw, &leg); err != nil {
		return
	}
	target := migrationTargetForLegacy(cfg)
	if target == "" {
		return
	}
	ctx := cfg.Contexts[target]
	if len(leg.Secrets) > 0 {
		if ctx.Secrets == nil {
			ctx.Secrets = map[string]string{}
		}
		for k, v := range leg.Secrets {
			if _, ok := ctx.Secrets[k]; !ok {
				ctx.Secrets[k] = v
			}
		}
	}
	if leg.Defaults.CryptPassword != "" && ctx.Crypt.Password == "" {
		ctx.Crypt.Password = leg.Defaults.CryptPassword
		ctx.Crypt.Salt = leg.Defaults.CryptSalt
	} else if leg.Defaults.CryptSalt != "" && ctx.Crypt.Salt == "" && ctx.Crypt.Password != "" {
		ctx.Crypt.Salt = leg.Defaults.CryptSalt
	}
	cfg.Contexts[target] = ctx
}
