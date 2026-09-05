package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Nomad's default region is "global". "default" is the default NAMESPACE name,
// and asking for it as a region fails every request:
//
//	nomad API 500 Internal Server Error: No path to region
//
// terminalOnce treats any error as "not terminal yet", so callers that coerced
// an empty region to "default" waited out the full 24h cap on a job that had
// already finished. An empty region must reach the API as no region parameter
// at all, so Nomad answers for its own region.
func TestWaitTerminal_EmptyRegionSendsNoRegionParam(t *testing.T) {
	var sawRegion string
	var sawParam bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.URL.Query()["region"]; ok {
			sawParam = true
			sawRegion = v[0]
		}
		// A terminal allocation.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ID":"aaaaaaaa-0000-0000-0000-000000000001","ClientStatus":"complete","TaskStates":{}}]`))
	}))
	defer srv.Close()

	res, err := WaitTerminal(context.Background(), srv.URL, "tok", "",
		WatchTarget{JobID: "j", Namespace: "default"}, 50*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if res.Status != "complete" {
		t.Errorf("Status = %q, want complete", res.Status)
	}
	if sawParam {
		t.Errorf("region=%q was sent; an empty region must send no region parameter, "+
			"otherwise Nomad answers 500 'No path to region'", sawRegion)
	}
}

// A region that is explicitly configured must still be honoured.
func TestWaitTerminal_ExplicitRegionIsSent(t *testing.T) {
	var sawRegion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRegion = r.URL.Query().Get("region")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ID":"aaaaaaaa-0000-0000-0000-000000000001","ClientStatus":"complete","TaskStates":{}}]`))
	}))
	defer srv.Close()

	if _, err := WaitTerminal(context.Background(), srv.URL, "tok", "global",
		WatchTarget{JobID: "j", Namespace: "default"}, 50*time.Millisecond, 5*time.Second); err != nil {
		t.Fatalf("WaitTerminal: %v", err)
	}
	if sawRegion != "global" {
		t.Errorf("region = %q, want global", sawRegion)
	}
}
