package credsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// mkSeedlingCtx builds a context whose endpoint points at the given test
// server and whose access_token is an opaque-shape value. We then *override*
// the resolver's ExchangeURL to hit the test server directly, since the
// derive helper requires a `<subdomain>.<rest>` shape and httptest URLs
// don't have one.
func mkSeedlingCtx(opaque, whoami string) abccfg.Context {
	ctx := abccfg.Context{
		Endpoint:    "https://nomad.seedling.example.test",
		AccessToken: opaque,
		CredSource:  "seedling/v1",
	}
	ctx.SetAuthWhoami(whoami)
	return ctx
}

func okBrokerResponse(slotName string) wireCreds {
	w := wireCreds{
		Whoami: slotName,
		Source: "seedling/v1",
	}
	w.Nomad.Addr = "https://nomad.seedling.example.test"
	w.Nomad.Token = "real-nomad-token-from-broker"
	w.Nomad.Namespace = "su-test-group"
	w.Nomad.Datacenters = []string{"seedling-prod"}
	w.Nomad.HeadPool = "platform"
	w.Nomad.WorkerPool = "compute"
	w.Minio.Endpoint = "https://s3.seedling.example.test"
	w.Minio.AccessKey = "slot-" + slotName
	w.Minio.SecretKey = "real-minio-secret-from-broker"
	return w
}

func TestSeedlingV1_HappyPath(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s; want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer abco_test-opaque" {
			t.Errorf("Authorization = %q; want Bearer abco_test-opaque", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okBrokerResponse("calm_dassie"))
	}))
	defer srv.Close()

	cs, err := NewSeedlingV1(mkSeedlingCtx("abco_test-opaque", "calm_dassie"))
	if err != nil {
		t.Fatalf("NewSeedlingV1: %v", err)
	}
	cs.ExchangeURL = srv.URL // override the derived URL with the test server

	creds, err := cs.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if creds.Source != "seedling/v1" {
		t.Errorf("Source = %q", creds.Source)
	}
	if creds.Whoami != "calm_dassie" {
		t.Errorf("Whoami = %q", creds.Whoami)
	}
	if creds.Nomad.Token != "real-nomad-token-from-broker" {
		t.Errorf("Nomad.Token = %q", creds.Nomad.Token)
	}
	if creds.Minio.SecretKey != "real-minio-secret-from-broker" {
		t.Errorf("Minio.SecretKey = %q", creds.Minio.SecretKey)
	}

	// Second Resolve must hit the cache — no extra HTTP call.
	if _, err := cs.Resolve(context.Background()); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("broker calls = %d; want 1 (cache miss + cache hit)", got)
	}

	// After InvalidateCache the next Resolve must hit the broker again.
	cs.InvalidateCache()
	if _, err := cs.Resolve(context.Background()); err != nil {
		t.Fatalf("post-invalidate Resolve: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("broker calls = %d; want 2 after invalidate", got)
	}
}

func TestSeedlingV1_BrokerErrorClearsCache(t *testing.T) {
	var calls int32
	var failNow atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if failNow.Load() {
			http.Error(w, `{"error":"invalid_or_inactive_token"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okBrokerResponse("calm_dassie"))
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_test-opaque", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	if _, err := cs.Resolve(context.Background()); err != nil {
		t.Fatalf("initial Resolve: %v", err)
	}

	// Even with a fresh cache hit available, an InvalidateCache + 401
	// must clear the cache so future success calls actually re-fetch.
	cs.InvalidateCache()
	failNow.Store(true)
	if _, err := cs.Resolve(context.Background()); err == nil {
		t.Fatalf("expected 401 error from broker")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v; want it to mention 401", err)
	}

	// After failure, recovery: broker comes back, next Resolve must hit it
	// (not serve from the old cache).
	failNow.Store(false)
	if _, err := cs.Resolve(context.Background()); err != nil {
		t.Fatalf("recovery Resolve: %v", err)
	}
}

func TestSeedlingV1_WhoamiMismatchRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Broker says the opaque resolves to a DIFFERENT slot than the
		// context claims. Refuse rather than silently dispatch as someone else.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okBrokerResponse("OTHER_USER"))
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_test-opaque", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err == nil {
		t.Fatalf("expected whoami-mismatch error")
	}
	if !strings.Contains(err.Error(), "whoami") {
		t.Errorf("err = %v; want it to mention whoami", err)
	}
}

func TestSeedlingV1_SourceMismatchRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Broker accidentally returns a grove/v1 bundle for a seedling/v1
		// request — possible misrouted proxy. Reject loudly.
		bundle := okBrokerResponse("calm_dassie")
		bundle.Source = "grove/v1"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bundle)
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_test-opaque", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "source=") {
		t.Fatalf("expected source-mismatch error; got %v", err)
	}
}

func TestSeedlingV1_NoOpaque(t *testing.T) {
	ctx := mkSeedlingCtx("", "calm_dassie")
	_, err := NewSeedlingV1(ctx)
	if err == nil || !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("expected access_token-empty error; got %v", err)
	}
}

func TestSeedlingV1_DeriveBrokerURL(t *testing.T) {
	cases := []struct {
		endpoint string
		want     string
		wantErr  bool
	}{
		{"https://nomad.seedling.abc-cluster.cloud", "https://auth.seedling.abc-cluster.cloud/auth/exchange", false},
		{"http://nomad.local.example", "http://auth.local.example/auth/exchange", false},
		{"https://just-host-no-dots", "", true},
		{"", "", true},
		{"not a url", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			got, err := deriveBrokerExchangeURL(tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error; got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestSelect_SeedlingV1_NowDispatches(t *testing.T) {
	ctx := mkSeedlingCtx("abco_test", "calm_dassie")
	cs, err := Select(ctx)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if cs.Name() != "seedling/v1" {
		t.Errorf("Name = %q; want seedling/v1", cs.Name())
	}
}
