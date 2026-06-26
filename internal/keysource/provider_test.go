package keysource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// newTestProvider stands up a fake broker that always releases group:demo's age
// keypair, counting hits (to assert the release-once cache).
func newTestProvider(t *testing.T, hits *int) (*Provider, *age.X25519Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kek_id": "group:demo", "version": 1,
			"recipient": id.Recipient().String(),
			"identity":  id.String(),
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ABC_KEYS_BROKER_URL", srv.URL)
	cl, err := NewClient(abccfg.Context{AccessToken: "opaque-xyz"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return NewProvider(context.Background(), cl, ""), id
}

func TestProviderRecipientAndIdentityCached(t *testing.T) {
	hits := 0
	p, want := newTestProvider(t, &hits)

	r, gk, err := p.Recipient()
	if err != nil || gk.KekID != "group:demo" {
		t.Fatalf("Recipient: gk=%+v err=%v", gk, err)
	}
	if r.(*age.X25519Recipient).String() != want.Recipient().String() {
		t.Fatalf("recipient mismatch")
	}
	id, _, err := p.Identity()
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if id.(*age.X25519Identity).String() != want.String() {
		t.Fatalf("identity mismatch")
	}
	if hits != 1 {
		t.Fatalf("server hit %d times; want 1 (group key released once, then cached)", hits)
	}
}

func TestProviderFetchMissingFieldsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"kek_id": "group:demo", "version": 1})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ABC_KEYS_BROKER_URL", srv.URL)
	cl, _ := NewClient(abccfg.Context{AccessToken: "opaque-xyz"})
	if _, err := NewProvider(context.Background(), cl, "").Fetch(); err == nil {
		t.Fatal("expected an error when the broker omits recipient/identity")
	}
}
