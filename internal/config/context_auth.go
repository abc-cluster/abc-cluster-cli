package config

import "strings"

// ContextAuth holds operator identity derived from the control plane or Nomad.
// Populated from Nomad GET /v1/acl/token/self when a nomad_token is present (see cmd/utils).
type ContextAuth struct {
	// Root marks contexts that use the Nomad bootstrap / initial management token
	// (no ACL policies; often paired with auth: root in YAML — see normalizeContextAuthScalarInMap).
	Root   bool   `yaml:"root,omitempty"`
	Whoami string `yaml:"whoami,omitempty"`
}

// SetAuthWhoami stores a human-readable Nomad operator label (token name, policies, or management).
func (ctx *Context) SetAuthWhoami(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		if ctx.Auth != nil {
			ctx.Auth.Whoami = ""
		}
		ctx.clearAuthIfEmpty()
		return
	}
	if ctx.Auth == nil {
		ctx.Auth = &ContextAuth{}
	}
	ctx.Auth.Whoami = label
}

func (ctx *Context) clearAuthIfEmpty() {
	if ctx.Auth == nil {
		return
	}
	if strings.TrimSpace(ctx.Auth.Whoami) == "" && !ctx.Auth.Root {
		ctx.Auth = nil
	}
}

// ClearAuth removes all auth-derived fields for this context.
func (ctx *Context) ClearAuth() {
	ctx.Auth = nil
}
