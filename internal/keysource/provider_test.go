package keysource

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func fixedKEK() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// newTestProvider stands up a fake broker that always releases group:demo's KEK
// at the given version, counting how many times it is hit (to assert caching).
func newTestProvider(t *testing.T, version int, hits *int) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kek_id":  "group:demo",
			"version": version,
			"kek":     base64.StdEncoding.EncodeToString(fixedKEK()),
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ABC_KEYS_BROKER_URL", srv.URL)
	cl, err := NewClient(abccfg.Context{AccessToken: "opaque-xyz"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return NewProvider(context.Background(), cl)
}

func TestProviderOwnKekIDThenWrapIsCached(t *testing.T) {
	hits := 0
	p := newTestProvider(t, 1, &hits)

	id, err := p.OwnKekID()
	if err != nil || id != "group:demo" {
		t.Fatalf("OwnKekID = %q, %v; want group:demo", id, err)
	}
	kek, v, err := p.WrapKEK("group:demo")
	if err != nil || v != 1 || !bytes.Equal(kek, fixedKEK()) {
		t.Fatalf("WrapKEK = (%x, %d, %v); want fixedKEK, 1, nil", kek, v, err)
	}
	if hits != 1 {
		t.Fatalf("server hit %d times; want 1 (K_G released once, then cached)", hits)
	}
}

func TestProviderUnwrapVersionMatch(t *testing.T) {
	hits := 0
	p := newTestProvider(t, 3, &hits)
	kek, err := p.UnwrapKEK("group:demo", 3)
	if err != nil || !bytes.Equal(kek, fixedKEK()) {
		t.Fatalf("UnwrapKEK(v3) = (%x, %v); want fixedKEK, nil", kek, err)
	}
}

func TestProviderUnwrapVersionMismatchFailsClosed(t *testing.T) {
	hits := 0
	p := newTestProvider(t, 5, &hits) // broker now holds v5
	_, err := p.UnwrapKEK("group:demo", 2) // file was wrapped under v2
	if err == nil {
		t.Fatal("expected a version-mismatch error (G3 fail-closed), got nil")
	}
	if !strings.Contains(err.Error(), "rotated") {
		t.Fatalf("want a rotation-explaining error, got: %v", err)
	}
}
