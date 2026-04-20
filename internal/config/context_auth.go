package config

import "strings"

// ContextAuth holds operator identity derived from the control plane or Nomad.
// Populated from Nomad GET /v1/acl/token/self when a nomad_token is present (see cmd/utils).
type ContextAuth struct {
	Whoami string `yaml:"whoami,omitempty"`
}

// SetAuthWhoami stores a human-readable Nomad operator label (token name, policies, or management).
func (ctx *Context) SetAuthWhoami(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
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
	ctx.Auth.Whoami = ""
	if strings.TrimSpace(ctx.Auth.Whoami) == "" {
		ctx.Auth = nil
	}
}

// ClearAuth removes all auth-derived fields for this context.
func (ctx *Context) ClearAuth() {
	ctx.Auth = nil
}
