package app

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
)

// withTags builds a single-group, single-service NomadJob whose service tags are
// the ones passed in. Mirrors the shape `abc app list` sees from Nomad for any
// app deployed via `abc app deploy`.
func withTags(tags ...string) *utils.NomadJob {
	return &utils.NomadJob{
		TaskGroups: []utils.NomadTaskGroup{{
			Services: []utils.NomadJobService{{Tags: tags}},
		}},
	}
}

func TestAppExpose_PublicOnly(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-h2o-penguins.rule=Host(`abc-platform-h2o-penguins.apps.seedling.abc-cluster.cloud`)",
		"traefik.http.routers.app-abc-platform-h2o-penguins.entrypoints=web",
	)
	planes, url := appExpose(job)
	if planes != "public" {
		t.Errorf("planes = %q, want %q", planes, "public")
	}
	const wantURL = "https://abc-platform-h2o-penguins.apps.seedling.abc-cluster.cloud/"
	if url != wantURL {
		t.Errorf("url = %q, want %q", url, wantURL)
	}
}

func TestAppExpose_PrivateOnly_UsesPrivateDoor(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-seedling-v1-docs-internal.rule=PathPrefix(`/apps/abc-platform-seedling-v1-docs`)",
		"traefik.http.routers.app-abc-platform-seedling-v1-docs-internal.entrypoints=private",
	)
	planes, url := appExpose(job)
	if planes != "private" {
		t.Errorf("planes = %q, want %q", planes, "private")
	}
	want := "https://" + appgen.PrivateAppsDoor + "/apps/abc-platform-seedling-v1-docs/"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAppExpose_PrivateAndShared_PrefersPrivateDoor(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-titanic-survival-internal.rule=PathPrefix(`/apps/abc-platform-titanic-survival`)",
		"traefik.http.routers.app-abc-platform-titanic-survival-internal.entrypoints=private,shared",
	)
	planes, url := appExpose(job)
	// Plane label order is canonical: shared before private alphabetically by
	// our normalisation (public, shared, private), so:
	if planes != "shared,private" {
		t.Errorf("planes = %q, want %q", planes, "shared,private")
	}
	want := "https://" + appgen.PrivateAppsDoor + "/apps/abc-platform-titanic-survival/"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAppExpose_SharedOnly_FallsBackToPrivateDoorWhenSharedUnwired(t *testing.T) {
	// SharedAppsDoor is "" in this build; shared-only should fall back to the
	// private door (Traefik routes the path on either entrypoint).
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-foo-internal.rule=PathPrefix(`/apps/abc-platform-foo`)",
		"traefik.http.routers.app-abc-platform-foo-internal.entrypoints=shared",
	)
	planes, url := appExpose(job)
	if planes != "shared" {
		t.Errorf("planes = %q, want %q", planes, "shared")
	}
	if appgen.SharedAppsDoor != "" {
		t.Skip("SharedAppsDoor is configured in this build; fall-back path not exercised")
	}
	want := "https://" + appgen.PrivateAppsDoor + "/apps/abc-platform-foo/"
	if url != want {
		t.Errorf("url = %q, want %q (shared fall-back to private door)", url, want)
	}
}

func TestAppExpose_PublicAndPrivate_PublicWins(t *testing.T) {
	// `expose: [public, private]` — emit two routers (public Host + internal
	// PathPrefix). URL preference is public.
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-p-name-public.rule=Host(`p-name.apps.seedling.abc-cluster.cloud`)",
		"traefik.http.routers.app-p-name-public.entrypoints=web",
		"traefik.http.routers.app-p-name-internal.rule=PathPrefix(`/apps/p-name`)",
		"traefik.http.routers.app-p-name-internal.entrypoints=private,shared",
	)
	planes, url := appExpose(job)
	if planes != "public,shared,private" {
		t.Errorf("planes = %q, want %q", planes, "public,shared,private")
	}
	const want = "https://p-name.apps.seedling.abc-cluster.cloud/"
	if url != want {
		t.Errorf("url = %q, want %q (public-wins)", url, want)
	}
}

func TestAppExpose_LegacyHostRouted(t *testing.T) {
	// Pre-`expose:` apps used Host(...) on the web entrypoint with a non-public
	// hostname (campus DNS / tailscale machine name). The list output keeps the
	// `internal*` label to flag these.
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-lan-streamlit-test.rule=Host(`146.232.174.77`) || Host(`streamlit-test.internal`)",
		"traefik.http.routers.app-lan-streamlit-test.entrypoints=web",
	)
	planes, url := appExpose(job)
	if planes != "internal*" {
		t.Errorf("planes = %q, want %q", planes, "internal*")
	}
	const want = "https://146.232.174.77/"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAppExpose_NoTags(t *testing.T) {
	planes, url := appExpose(withTags()) // no Traefik tags at all
	if planes != "—" || url != "—" {
		t.Errorf("planes,url = %q,%q ; want dashes", planes, url)
	}
}

func TestAppExpose_NilJob(t *testing.T) {
	planes, url := appExpose(nil)
	if planes != "—" || url != "—" {
		t.Errorf("planes,url = %q,%q ; want dashes for nil job", planes, url)
	}
}
