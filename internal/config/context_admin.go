package config

import "strings"

// NomadService holds Nomad API / CLI connection details for one context.
type NomadService struct {
	Addr   string `yaml:"nomad_addr,omitempty"`
	Token  string `yaml:"nomad_token,omitempty"`
	Region string `yaml:"nomad_region,omitempty"` // Nomad multi-region ID (e.g. global), not contexts.region
}

// AdminServices holds operator-facing integrations under contexts.<name>.admin.services.
type AdminServices struct {
	Nomad *NomadService `yaml:"nomad,omitempty"`
}

// Admin holds optional admin-plane settings for a context.
type Admin struct {
	Services AdminServices `yaml:"services,omitempty"`
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
