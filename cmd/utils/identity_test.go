package utils

import (
	"strings"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestResolveUserIdentity_FullContext(t *testing.T) {
	ctx := &config.Context{
		Admin: config.Admin{Whoami: "anel", ID: "01KS7XXZBRNKRJJ0TR7PW9FWV9"},
	}
	id := ResolveUserIdentity(ctx, "su-mbhg-hostgen")
	if id.UserWhoami != "anel" {
		t.Errorf("UserWhoami=%q, want anel", id.UserWhoami)
	}
	if id.UserUUID != "01KS7XXZBRNKRJJ0TR7PW9FWV9" {
		t.Errorf("UserUUID=%q, want ULID", id.UserUUID)
	}
	if id.Workspace != "su-mbhg-hostgen" {
		t.Errorf("Workspace=%q, want su-mbhg-hostgen", id.Workspace)
	}
	if id.Tenant != "su-mbhg-hostgen" {
		t.Errorf("Tenant=%q, want su-mbhg-hostgen (defaults to Workspace)", id.Tenant)
	}
	if id.SubmittedAt == "" {
		t.Errorf("SubmittedAt should be stamped")
	}
	if _, err := time.Parse(time.RFC3339, id.SubmittedAt); err != nil {
		t.Errorf("SubmittedAt %q is not RFC3339: %v", id.SubmittedAt, err)
	}
	if id.CLIVersion == "" {
		t.Errorf("CLIVersion should be set (got empty)")
	}
	if id.UserKind != "named" {
		t.Errorf("UserKind=%q, want named (anel is not a slot-* pseudonym)", id.UserKind)
	}
	if id.RunOrigin != "cluster" {
		t.Errorf("RunOrigin=%q, want cluster (abc CLI head IS a Nomad job)", id.RunOrigin)
	}
}

// Slot-pool pseudonyms (`slot-<animal>`) are classified as UserKind="slot"
// so downstream rollups (`abc report`, Kayastha) can separate training
// activity from research activity.
func TestResolveUserIdentity_SlotPseudonym(t *testing.T) {
	ctx := &config.Context{
		Admin: config.Admin{Whoami: "slot-calm_dassie", ID: "01KSBOOTSTRAP000000000000"},
	}
	id := ResolveUserIdentity(ctx, "su-demo")
	if id.UserKind != "slot" {
		t.Errorf("UserKind=%q, want slot (whoami starts with slot-)", id.UserKind)
	}
	if id.RunOrigin != "cluster" {
		t.Errorf("RunOrigin=%q, want cluster", id.RunOrigin)
	}
}

// Empty context → only the ambient fields (SubmittedAt + CLIVersion + Workspace
// + UserKind + RunOrigin) land. UserWhoami / UserUUID stay empty so MetaMap
// omits them; UserKind defaults to "named" and RunOrigin to "cluster"
// (since abc CLI never sets origin=external — only nf-nomad's fallback does).
func TestResolveUserIdentity_NilContext(t *testing.T) {
	id := ResolveUserIdentity(nil, "su-foo")
	if id.UserWhoami != "" || id.UserUUID != "" {
		t.Errorf("nil ctx → expected empty user fields, got whoami=%q id=%q", id.UserWhoami, id.UserUUID)
	}
	if id.Workspace != "su-foo" {
		t.Errorf("Workspace should be set from namespace even with nil ctx, got %q", id.Workspace)
	}
	if id.Tenant != "su-foo" {
		t.Errorf("Tenant should still default to Workspace with nil ctx, got %q", id.Tenant)
	}
	if id.UserKind != "named" {
		t.Errorf("UserKind=%q, want named (default)", id.UserKind)
	}
	if id.RunOrigin != "cluster" {
		t.Errorf("RunOrigin=%q, want cluster", id.RunOrigin)
	}
}

// Whoami falls back from Admin.Whoami to Auth.Whoami if Admin's is empty.
func TestResolveUserIdentity_AuthWhoamiFallback(t *testing.T) {
	ctx := &config.Context{
		Auth: &config.ContextAuth{Whoami: "abhi"},
	}
	id := ResolveUserIdentity(ctx, "")
	if id.UserWhoami != "abhi" {
		t.Errorf("expected fallback to auth.whoami=abhi, got %q", id.UserWhoami)
	}
}

func TestUserIdentity_MetaMap_FullPopulation(t *testing.T) {
	id := UserIdentity{
		UserWhoami:    "anel",
		UserUUID:      "01KS7XXZBRNKRJJ0TR7PW9FWV9",
		Workspace:     "su-mbhg-hostgen",
		WorkspaceType: "shared",
		Tenant:        "su-mbhg-hostgen",
		SubmittedAt:   "2026-05-22T10:00:00Z",
		CLIVersion:    "v1.2.3",
		UserKind:      "named",
		RunOrigin:     "cluster",
	}
	got := id.MetaMap()
	want := map[string]string{
		"abc_user_whoami":    "anel",
		"abc_user_id":        "01KS7XXZBRNKRJJ0TR7PW9FWV9",
		"abc_workspace":      "su-mbhg-hostgen",
		"abc_workspace_type": "shared",
		"abc_tenant":         "su-mbhg-hostgen",
		"abc_submitted_at":   "2026-05-22T10:00:00Z",
		"abc_cli_version":    "v1.2.3",
		"abc_user_kind":      "named",
		"abc_run_origin":     "cluster",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("MetaMap[%q]=%q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("MetaMap key count=%d, want %d (extra keys: %v)", len(got), len(want), got)
	}
}

// Partial identity → empty fields are omitted, not emitted as "".
func TestUserIdentity_MetaMap_OmitsEmptyFields(t *testing.T) {
	id := UserIdentity{
		UserWhoami:  "anel",
		SubmittedAt: "2026-05-22T10:00:00Z",
		CLIVersion:  "dev",
	}
	got := id.MetaMap()
	for _, k := range []string{"abc_user_id", "abc_workspace", "abc_workspace_type", "abc_tenant", "abc_user_kind", "abc_run_origin"} {
		if _, ok := got[k]; ok {
			t.Errorf("MetaMap should omit empty field %q, but got %q", k, got[k])
		}
	}
	if got["abc_user_whoami"] != "anel" {
		t.Errorf("MetaMap should keep populated abc_user_whoami, got %q", got["abc_user_whoami"])
	}
}

func TestUserIdentity_MetaMap_EmptyIdentity(t *testing.T) {
	got := UserIdentity{}.MetaMap()
	if len(got) != 0 {
		t.Errorf("zero-value identity → empty map, got %v", got)
	}
}

func TestCLIVersion_NonEmpty(t *testing.T) {
	v := CLIVersion()
	if strings.TrimSpace(v) == "" {
		t.Errorf("CLIVersion returned empty string")
	}
}
