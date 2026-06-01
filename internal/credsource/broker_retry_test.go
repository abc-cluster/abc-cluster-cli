package credsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastBrokerRetry shortens the backoffs so retry tests don't add real wait
// time. Restores defaults via t.Cleanup.
func fastBrokerRetry(t *testing.T) {
	t.Helper()
	origMin, origMax := BrokerRetryMinBackoff, BrokerRetryMaxBackoff
	BrokerRetryMinBackoff = 1 * time.Millisecond
	BrokerRetryMaxBackoff = 5 * time.Millisecond
	t.Cleanup(func() {
		BrokerRetryMinBackoff = origMin
		BrokerRetryMaxBackoff = origMax
	})
}

// TestBrokerRetry_503ThenSuccess: a flapping broker returns 503 twice then
// recovers — fetch() must succeed on the 3rd attempt without surfacing
// the transient failure to the caller.
func TestBrokerRetry_503ThenSuccess(t *testing.T) {
	fastBrokerRetry(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			http.Error(w, `{"error":"upstream_down"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okBrokerResponse("calm_dassie"))
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_x", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	creds, err := cs.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if creds.Whoami != "calm_dassie" {
		t.Errorf("Whoami = %q", creds.Whoami)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d; want 3 (2 retryable failures + 1 success)", got)
	}
}

// TestBrokerRetry_401NoRetry: a 401 (revoked opaque) must fail on the FIRST
// attempt and not be retried. This is the load-bearing security invariant —
// admin revocations must be visible to the user immediately, not masked by
// retry latency.
func TestBrokerRetry_401NoRetry(t *testing.T) {
	fastBrokerRetry(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, `{"error":"invalid_or_inactive_token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_revoked", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error from 401 broker response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v; want error to mention 401", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d; want 1 (no retry on 401)", got)
	}
}

// TestBrokerRetry_409NoRetry: 409 slot_not_on_seedling_v1 is also a
// deliberate refusal from the broker (defensive check when an opaque hash
// matches a slot whose cred_source has been flipped to local). Same
// invariant — fail loud immediately.
func TestBrokerRetry_409NoRetry(t *testing.T) {
	fastBrokerRetry(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, `{"error":"slot_not_on_seedling_v1"}`, http.StatusConflict)
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_x", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("expected 409 error; got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d; want 1 (no retry on 409)", got)
	}
}

// TestBrokerRetry_500Exhausts: persistent 5xx exhausts the retry budget
// and surfaces a clear error mentioning the status.
func TestBrokerRetry_500Exhausts(t *testing.T) {
	fastBrokerRetry(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_x", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected error after retry budget exhausted")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v; want it to mention 500", err)
	}
	if got := atomic.LoadInt32(&calls); int(got) != int(BrokerRetryMaxAttempts) {
		t.Errorf("calls = %d; want %d (max attempts)", got, BrokerRetryMaxAttempts)
	}
}

// TestBrokerRetry_429Retried: rate-limit (429) is retried — the broker
// might have temporarily refused due to backpressure; the next attempt
// after backoff may succeed.
func TestBrokerRetry_429Retried(t *testing.T) {
	fastBrokerRetry(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			http.Error(w, `{"error":"slow_down"}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okBrokerResponse("calm_dassie"))
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_x", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	_, err := cs.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d; want 2 (1 retry then success)", got)
	}
}

// TestBrokerRetry_ContextCancellation: a cancelled context aborts the
// retry loop promptly, even if the broker is still flapping.
func TestBrokerRetry_ContextCancellation(t *testing.T) {
	// Keep default delays here so the cancellation has work to interrupt.
	BrokerRetryMinBackoff = 200 * time.Millisecond
	BrokerRetryMaxBackoff = 1 * time.Second
	t.Cleanup(func() {
		BrokerRetryMinBackoff = 200 * time.Millisecond
		BrokerRetryMaxBackoff = 2 * time.Second
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"upstream_down"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cs, _ := NewSeedlingV1(mkSeedlingCtx("abco_x", "calm_dassie"))
	cs.ExchangeURL = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	t0 := time.Now()
	_, err := cs.Resolve(ctx)
	elapsed := time.Since(t0)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// Should bail well before the full retry budget (which would be ~seconds).
	if elapsed > 1*time.Second {
		t.Errorf("Resolve took %v; want it to abort within ~50ms of context cancellation", elapsed)
	}
}

// TestIsRetryableHTTPStatus: status-code allowlist invariant.
func TestIsRetryableHTTPStatus(t *testing.T) {
	retryable := []int{500, 502, 503, 504, 429}
	nonRetryable := []int{200, 201, 301, 302, 400, 401, 403, 404, 409, 410, 422}
	for _, code := range retryable {
		if !isRetryableHTTPStatus(code) {
			t.Errorf("isRetryableHTTPStatus(%d) = false; want true", code)
		}
	}
	for _, code := range nonRetryable {
		if isRetryableHTTPStatus(code) {
			t.Errorf("isRetryableHTTPStatus(%d) = true; want false", code)
		}
	}
}
