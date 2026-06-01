package credsource

import (
	"context"
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestLocalCredSource_PrefersAdminServicesThenFallsBack(t *testing.T) {
	t.Run("admin.services.nomad populated → uses those", func(t *testing.T) {
		ctx := abccfg.Context{
			Endpoint:    "https://fallback-endpoint",
			AccessToken: "fallback-token",
			Namespace:   "fallback-ns",
		}
		ctx.Admin.Services.Nomad = &abccfg.NomadService{
			Addr:        "https://admin-nomad",
			Token:       "admin-token",
			Namespace:   "admin-ns",
			Datacenters: []string{"seedling-prod"},
			HeadPool:    "platform",
			WorkerPool:  "compute",
		}
		ctx.Admin.Services.MinIO = &abccfg.AdminFloorService{
			Endpoint:  "https://minio",
			AccessKey: "AK",
			SecretKey: "SK",
		}
		ctx.SetAuthWhoami("abhi")

		got, err := NewLocal(ctx).Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Source != "local" {
			t.Errorf("Source = %q; want local", got.Source)
		}
		if got.Whoami != "abhi" {
			t.Errorf("Whoami = %q; want abhi", got.Whoami)
		}
		if got.Nomad.Addr != "https://admin-nomad" {
			t.Errorf("Nomad.Addr = %q; want admin-nomad (admin.services wins)", got.Nomad.Addr)
		}
		if got.Nomad.Token != "admin-token" {
			t.Errorf("Nomad.Token = %q; want admin-token", got.Nomad.Token)
		}
		if got.Nomad.Namespace != "admin-ns" {
			t.Errorf("Nomad.Namespace = %q; want admin-ns", got.Nomad.Namespace)
		}
		if len(got.Nomad.Datacenters) != 1 || got.Nomad.Datacenters[0] != "seedling-prod" {
			t.Errorf("Nomad.Datacenters = %v; want [seedling-prod]", got.Nomad.Datacenters)
		}
		if got.Nomad.HeadPool != "platform" || got.Nomad.WorkerPool != "compute" {
			t.Errorf("pools = (%q, %q); want (platform, compute)", got.Nomad.HeadPool, got.Nomad.WorkerPool)
		}
		if got.Minio.Endpoint != "https://minio" || got.Minio.AccessKey != "AK" || got.Minio.SecretKey != "SK" {
			t.Errorf("Minio mismatch: %+v", got.Minio)
		}
	})

	t.Run("no admin.services → falls back to context fields (slot-config shape)", func(t *testing.T) {
		ctx := abccfg.Context{
			Endpoint:    "https://slot-endpoint",
			AccessToken: "slot-token",
			Namespace:   "su-group",
		}
		ctx.SetAuthWhoami("calm_dassie")

		got, err := NewLocal(ctx).Resolve(context.Background())
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Whoami != "calm_dassie" {
			t.Errorf("Whoami = %q", got.Whoami)
		}
		if got.Nomad.Addr != "https://slot-endpoint" {
			t.Errorf("Nomad.Addr = %q; want slot-endpoint", got.Nomad.Addr)
		}
		if got.Nomad.Token != "slot-token" {
			t.Errorf("Nomad.Token = %q; want slot-token", got.Nomad.Token)
		}
		if got.Nomad.Namespace != "su-group" {
			t.Errorf("Nomad.Namespace = %q; want su-group", got.Nomad.Namespace)
		}
	})
}

func TestSelect_DispatchesByCredSource(t *testing.T) {
	cases := []struct {
		name      string
		credSrc   string
		wantErr   bool
		wantName  string // when no error
	}{
		{"empty → local", "", false, "local"},
		{"local", "local", false, "local"},
		{"LOCAL → local (case-insensitive)", "LOCAL", false, "local"},
		// seedling/v1 with an opaque token + parseable endpoint succeeds now
		// that NewSeedlingV1 is wired in. See seedling_v1_test.go for the
		// network-mocked Resolve flow.
		{"grove/v1 → not-yet-implemented error", "grove/v1", true, ""},
		{"cloud/v1 → not-yet-implemented error", "cloud/v1", true, ""},
		{"garbage → unknown-cred_source error", "lolwat", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs, err := Select(abccfg.Context{CredSource: tc.credSrc})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Select(%q) = nil err; want error", tc.credSrc)
				}
				return
			}
			if err != nil {
				t.Fatalf("Select(%q) err: %v", tc.credSrc, err)
			}
			if cs.Name() != tc.wantName {
				t.Errorf("Select(%q).Name() = %q; want %q", tc.credSrc, cs.Name(), tc.wantName)
			}
		})
	}
}
