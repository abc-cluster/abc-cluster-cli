// envvars_lookup.go — bridges the config package's active context into the
// pluggable ContextLookup function expected by internal/envvars.Resolver.
//
// Spec: $ABC_UNIVERSE/specs/active/abc-cli-env-resolution.md §A.2 + §E
//
// Keep this file thin: only the key-to-field mapping lives here. Adding a
// new field to Context goes in config.go; adding a registry entry that
// reads from a context field goes in internal/envvars/registry.go; here we
// just wire the dot-path string the Entry declares (ContextKey) to the
// matching Context struct field.

package config

import (
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/internal/envvars"
)

// ContextLookupFor returns an envvars.ContextLookup over the given Context.
// Unknown keys return ("", false). Empty Context fields return ("", false)
// so the resolver falls through to defaults (not "" treated as set).
func ContextLookupFor(ctx Context) envvars.ContextLookup {
	return func(key string) (string, bool) {
		v := lookupContextField(ctx, key)
		if v == "" {
			return "", false
		}
		return v, true
	}
}

// ActiveContextLookup builds a ContextLookup from the active context of the
// given Config. Returns NoContext when no active context is set.
func ActiveContextLookup(cfg *Config) envvars.ContextLookup {
	if cfg == nil || cfg.ActiveContext == "" {
		return envvars.NoContext
	}
	return ContextLookupFor(cfg.ActiveCtx())
}

func lookupContextField(ctx Context, key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "url":
		return ctx.Endpoint
	case "access_token":
		return ctx.AccessToken
	case "workspace_id":
		return ctx.WorkspaceID
	case "region":
		return ctx.Region
	case "namespace":
		return ctx.Namespace
	case "org_id":
		return ctx.OrgID
	case "output_format":
		return ctx.OutputFormat
	case "upload_endpoint":
		return ctx.UploadEndpoint
	case "upload_token":
		return ctx.UploadToken
	case "controller_url":
		return ctx.ControllerURL
	case "cluster_type":
		return ctx.ClusterType
	}
	return ""
}
