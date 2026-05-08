package report

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/capability"
	rep "github.com/abc-cluster/abc-cluster-cli/internal/report"
)

// TestRejectAllContexts: --all-contexts when the controller service
// isn't in the active context's capability map must reject with the
// standard capability.Require() failure message. Spec §F.
func TestRejectAllContexts(t *testing.T) {
	flagsSet := map[string]bool{"all-contexts": true}
	d := capability.Require(reportCapabilities, nil, flagsSet)
	if !d.Failed() {
		t.Fatal("expected --all-contexts to fail when controller service unavailable")
	}
	err := d.AsError()
	if err == nil {
		t.Fatal("AsError() returned nil")
	}
	if !strings.Contains(err.Error(), "all-contexts") {
		t.Errorf("error doesn't reference the flag: %v", err)
	}
	if !strings.Contains(err.Error(), "abc-controller-svc") {
		t.Errorf("error doesn't reference the required service: %v", err)
	}
}

// TestLocalStateAlwaysSatisfies: with a nil capabilities map (the
// common-today seed-tier case), the verb's required local-state need
// resolves through the migration-derived feature path. No controller
// service required for the personal summary.
func TestLocalStateAlwaysSatisfies(t *testing.T) {
	d := capability.Require(reportCapabilities, nil, map[string]bool{})
	if d.Failed() {
		t.Fatalf("local-state should satisfy AllOf at seed: %v", d.AsError())
	}
}

// TestByWithoutJSON_DefersWithMessage: --by=<axis> without --json
// returns the deferral error verbatim (per-spec clarification: text
// mode grouping ships in v2; v1 supports JSON --by only).
func TestByWithoutJSON_DefersWithMessage(t *testing.T) {
	cmd := NewCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--by=investigation"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error from --by= without --json")
	}
	want := "deferred to v2"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q missing %q", err.Error(), want)
	}
}

// TestNoNetworkOnRender: the report verb must not make any outbound
// HTTP calls. We swap http.DefaultTransport for a tripwire that
// records every request and then run RenderText / RenderJSON over an
// in-memory fixture DB. Spec §D acceptance.
func TestNoNetworkOnRender(t *testing.T) {
	t.Setenv("ABC_NO_PROBE", "1")

	var hits int64
	originalRT := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = originalRT })
	http.DefaultTransport = &tripwireRT{hits: &hits}

	db := openTestFixtureDB(t)
	results := rep.Compute(context.Background(), db, rep.QueryOptions{
		Window:      rep.DefaultWindowYTD(time.Now()),
		ContextName: "ctx",
	})
	var bufText, bufJSON bytes.Buffer
	if err := rep.RenderText(context.Background(), db, &bufText, rep.TextOptions{Window: rep.DefaultWindowYTD(time.Now())}, results); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	card, err := rep.LoadRateCard("ctx")
	if err != nil {
		t.Fatalf("LoadRateCard: %v", err)
	}
	if err := rep.RenderJSON(&bufJSON, rep.DefaultWindowYTD(time.Now()), "ctx", card, results, nil); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Errorf("report rendering made %d outbound HTTP calls; want zero", got)
	}
}

// tripwireRT counts every RoundTrip and refuses to actually transport.
type tripwireRT struct{ hits *int64 }

func (t *tripwireRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	atomic.AddInt64(t.hits, 1)
	return nil, errors.New("network forbidden in this test")
}
