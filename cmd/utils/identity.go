package utils

import (
	"runtime/debug"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// UserIdentity is the user / workspace correlation context emitted onto
// Nomad job Meta blocks by both `abc job run` and `abc pipeline run`.
//
// Pipeline runs build a richer hclgen/pipeline.Identity on top of this
// base (adding PipelineURL / PipelineRevision / RunName). The base lives
// here so the two surfaces always agree on:
//
//   - field semantics (whoami vs ULID; tenant defaults to workspace),
//   - meta-key spellings (abc_user_whoami, abc_user_id, abc_workspace,
//     abc_workspace_type, abc_tenant, abc_submitted_at, abc_cli_version),
//   - empty-field elision (partial identity degrades cleanly — no
//     empty-string meta entries).
//
// Downstream consumers (abc-jurist, XTDB audit, Grafana dashboards) can
// pivot across job + pipeline runs by the same fields.
type UserIdentity struct {
	UserWhoami    string // human-readable label, may change as personas evolve
	UserUUID      string // stable ULID minted on first `abc auth whoami`
	Workspace     string // = Nomad namespace = MinIO bucket; opaque flat slug
	WorkspaceType string // "personal" | "shared" (optional)
	Tenant        string // root of workspace parent chain; defaults to Workspace
	SubmittedAt   string // ISO-8601 UTC, set at submission time
	CLIVersion    string // abc CLI semver (or "dev")
}

// ResolveUserIdentity gathers the identity fields from the active config
// context + a target namespace. Missing context fields are left empty.
// SubmittedAt is stamped now (UTC RFC3339).
func ResolveUserIdentity(ctx *config.Context, namespace string) UserIdentity {
	id := UserIdentity{
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
		CLIVersion:  CLIVersion(),
		Workspace:   strings.TrimSpace(namespace),
	}
	if ctx == nil {
		if id.Workspace != "" {
			id.Tenant = id.Workspace
		}
		return id
	}
	id.UserWhoami = strings.TrimSpace(ctx.Admin.Whoami)
	if id.UserWhoami == "" && ctx.Auth != nil {
		id.UserWhoami = strings.TrimSpace(ctx.Auth.Whoami)
	}
	id.UserUUID = strings.TrimSpace(ctx.Admin.ID)
	// Tenant defaults to Workspace; future workspaces.yaml v2 schema work
	// resolves the real parent-chain root from a tenancy graph.
	if id.Tenant == "" && id.Workspace != "" {
		id.Tenant = id.Workspace
	}
	return id
}

// MetaMap returns the identity as a meta-key map suitable for merging
// into a Nomad Job.Meta block. Empty fields are omitted so partial
// identity doesn't pollute the block with empty-string entries.
func (i UserIdentity) MetaMap() map[string]string {
	out := make(map[string]string, 7)
	if i.UserWhoami != "" {
		out["abc_user_whoami"] = i.UserWhoami
	}
	if i.UserUUID != "" {
		out["abc_user_id"] = i.UserUUID
	}
	if i.Workspace != "" {
		out["abc_workspace"] = i.Workspace
	}
	if i.WorkspaceType != "" {
		out["abc_workspace_type"] = i.WorkspaceType
	}
	if i.Tenant != "" {
		out["abc_tenant"] = i.Tenant
	}
	if i.SubmittedAt != "" {
		out["abc_submitted_at"] = i.SubmittedAt
	}
	if i.CLIVersion != "" {
		out["abc_cli_version"] = i.CLIVersion
	}
	return out
}

// CLIVersion reports the abc CLI's own version for stamping into job /
// pipeline meta (abc_cli_version). Falls back to "dev" outside a build
// with embedded version info.
func CLIVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := strings.TrimSpace(info.Main.Version); v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
