package secretsource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

func TestBrokerClient_PutGetRoundTrip(t *testing.T) {
	store := map[string]string{}
	var sawPutGroup string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abco_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/auth/secrets/put":
			var req putReq
			_ = json.Unmarshal(body, &req)
			store[req.Key] = req.Value
			sawPutGroup = req.Group
			w.WriteHeader(http.StatusOK)
		case "/auth/secrets/get":
			var req getReq
			_ = json.Unmarshal(body, &req)
			v, ok := store[req.Key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(getResp{Value: v})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("ABC_SECRETS_BROKER_URL", srv.URL)

	cl, err := NewClient(abccfg.Context{AccessToken: "abco_test"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()

	if err := cl.Put(ctx, "crypt-password", "hunter2", ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := cl.Get(ctx, "crypt-password")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hunter2" {
		t.Fatalf("round-trip mismatch: got %q want %q", got, "hunter2")
	}
	if sawPutGroup != "" {
		t.Fatalf("client-only put should carry empty group, got %q", sawPutGroup)
	}

	// group (job-runtime lane) is forwarded.
	if err := cl.Put(ctx, "db-pw", "s3cr3t", "neuro"); err != nil {
		t.Fatalf("Put with group: %v", err)
	}
	if sawPutGroup != "neuro" {
		t.Fatalf("group not forwarded: got %q want %q", sawPutGroup, "neuro")
	}

	// missing key → ErrNotFound (distinguishable from transport/auth errors).
	if _, err := cl.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: want ErrNotFound, got %v", err)
	}
}

func TestBrokerClient_RejectsMissingOpaque(t *testing.T) {
	if _, err := NewClient(abccfg.Context{AccessToken: ""}); err == nil {
		t.Fatal("expected error when context has no opaque token")
	}
}

func TestBrokerClient_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("ABC_SECRETS_BROKER_URL", srv.URL)

	cl, err := NewClient(abccfg.Context{AccessToken: "abco_bad"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := cl.Get(context.Background(), "k"); err == nil {
		t.Fatal("expected unauthorized error")
	}
}

func TestBrokerBaseURL_Derivation(t *testing.T) {
	// auth_endpoint host wins (base = scheme://host, path dropped).
	b, err := brokerBaseURL(abccfg.Context{AuthEndpoint: "https://workbench.seedling.abc-cluster.cloud"})
	if err != nil || b != "https://workbench.seedling.abc-cluster.cloud" {
		t.Fatalf("auth_endpoint base: got %q err %v", b, err)
	}
	// derive auth.<rest> from the cluster endpoint.
	b, err = brokerBaseURL(abccfg.Context{Endpoint: "https://nomad.seedling.abc-cluster.cloud"})
	if err != nil || b != "https://auth.seedling.abc-cluster.cloud" {
		t.Fatalf("derived base: got %q err %v", b, err)
	}
}
