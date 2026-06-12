package app

import (
	"testing"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
)

// fixtureDoors is the AppsDoors a typical seedling-tier deployment populates
// from its context (admin.services.apps). Tests use these as illustrative
// values — they are NOT package consts, mirroring the v0.1.57 design that
// keeps cluster-specific hosts out of source.
func fixtureDoors() appgen.AppsDoors {
	return appgen.AppsDoors{
		PublicDomain:  "apps.seedling.abc-cluster.cloud",
		PrivateDoor:   "aither.mb.sun.ac.za",
		PrivateDoorIP: "146.232.174.77",
		// SharedDoor: "" — Tailscale Serve not yet wired in seedling-prod;
		// shared-only apps fall back to PrivateDoor.
	}
}

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
	planes, url := appExpose(job, fixtureDoors())
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
	doors := fixtureDoors()
	planes, url := appExpose(job, doors)
	if planes != "private" {
		t.Errorf("planes = %q, want %q", planes, "private")
	}
	want := "https://" + doors.PrivateDoor + "/apps/abc-platform-seedling-v1-docs/"
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
	doors := fixtureDoors()
	planes, url := appExpose(job, doors)
	if planes != "shared,private" {
		t.Errorf("planes = %q, want %q", planes, "shared,private")
	}
	want := "https://" + doors.PrivateDoor + "/apps/abc-platform-titanic-survival/"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAppExpose_SharedOnly_FallsBackToPrivateDoorWhenSharedUnwired(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-foo-internal.rule=PathPrefix(`/apps/abc-platform-foo`)",
		"traefik.http.routers.app-abc-platform-foo-internal.entrypoints=shared",
	)
	doors := fixtureDoors() // SharedDoor is "" in the fixture
	planes, url := appExpose(job, doors)
	if planes != "shared" {
		t.Errorf("planes = %q, want %q", planes, "shared")
	}
	want := "https://" + doors.PrivateDoor + "/apps/abc-platform-foo/"
	if url != want {
		t.Errorf("url = %q, want %q (shared fall-back to private door)", url, want)
	}
}

func TestAppExpose_SharedOnly_UsesSharedDoorWhenConfigured(t *testing.T) {
	doors := fixtureDoors()
	doors.SharedDoor = "aither.tailnet.example.ts.net"
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-foo-internal.rule=PathPrefix(`/apps/abc-platform-foo`)",
		"traefik.http.routers.app-abc-platform-foo-internal.entrypoints=shared",
	)
	_, url := appExpose(job, doors)
	want := "https://" + doors.SharedDoor + "/apps/abc-platform-foo/"
	if url != want {
		t.Errorf("url = %q, want %q (shared door takes precedence)", url, want)
	}
}

func TestAppExpose_PrivatePath_BareWhenNoDoorConfigured(t *testing.T) {
	// Operator hasn't set admin.services.apps.private_door in their context
	// — URL falls through to the bare /apps/<app>/ path (a hint, not clickable).
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-abc-platform-foo-internal.rule=PathPrefix(`/apps/abc-platform-foo`)",
		"traefik.http.routers.app-abc-platform-foo-internal.entrypoints=private",
	)
	_, url := appExpose(job, appgen.AppsDoors{}) // zero value — nothing configured
	const want = "/apps/abc-platform-foo/"
	if url != want {
		t.Errorf("url = %q, want %q (bare path fallback)", url, want)
	}
}

func TestAppExpose_PublicAndPrivate_PublicWins(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-p-name-public.rule=Host(`p-name.apps.seedling.abc-cluster.cloud`)",
		"traefik.http.routers.app-p-name-public.entrypoints=web",
		"traefik.http.routers.app-p-name-internal.rule=PathPrefix(`/apps/p-name`)",
		"traefik.http.routers.app-p-name-internal.entrypoints=private,shared",
	)
	planes, url := appExpose(job, fixtureDoors())
	if planes != "public,shared,private" {
		t.Errorf("planes = %q, want %q", planes, "public,shared,private")
	}
	const want = "https://p-name.apps.seedling.abc-cluster.cloud/"
	if url != want {
		t.Errorf("url = %q, want %q (public-wins)", url, want)
	}
}

func TestAppExpose_LegacyHostRouted(t *testing.T) {
	job := withTags(
		"traefik.enable=true",
		"traefik.http.routers.app-lan-streamlit-test.rule=Host(`146.232.174.77`) || Host(`streamlit-test.internal`)",
		"traefik.http.routers.app-lan-streamlit-test.entrypoints=web",
	)
	planes, url := appExpose(job, fixtureDoors())
	if planes != "internal*" {
		t.Errorf("planes = %q, want %q", planes, "internal*")
	}
	const want = "https://146.232.174.77/"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAppExpose_NoTags(t *testing.T) {
	planes, url := appExpose(withTags(), fixtureDoors())
	if planes != "—" || url != "—" {
		t.Errorf("planes,url = %q,%q ; want dashes", planes, url)
	}
}

func TestAppExpose_NilJob(t *testing.T) {
	planes, url := appExpose(nil, fixtureDoors())
	if planes != "—" || url != "—" {
		t.Errorf("planes,url = %q,%q ; want dashes for nil job", planes, url)
	}
}
