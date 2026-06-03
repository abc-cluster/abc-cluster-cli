package floor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectLogsBackend(t *testing.T) {
	cases := map[string]LogsBackend{
		"http://localhost:3100":                                 BackendLoki,
		"http://loki.abc-nodes.cloud":                           BackendLoki,
		"http://loki.abc-nodes.cloud:3100":                      BackendLoki,
		"https://grafana.example.cloud/loki":                    BackendLoki,
		"http://100.70.185.46:9428":                             BackendVictoriaLogs,
		"https://vl.example.cloud:9428/select/logsql/query":     BackendVictoriaLogs,
		"http://example/select/loki/api/v1/push":                BackendVictoriaLogs,
		"":                                                      BackendLoki, // safe default
	}
	for url, want := range cases {
		if got := DetectLogsBackend(url); got != want {
			t.Errorf("DetectLogsBackend(%q) = %v; want %v", url, got, want)
		}
	}
}

func TestVictoriaLogsClient_QueryStream(t *testing.T) {
	// Server emits JSON-lines as VictoriaLogs does.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		q := r.URL.Query().Get("query")
		// We expect the query to contain the _stream selector + the time
		// filter we passed in.
		if !strings.Contains(q, `_stream:{alloc_id="a1",task="nf-task",stream="stdout"}`) {
			http.Error(w, "missing stream selector in: "+q, http.StatusBadRequest)
			return
		}
		if !strings.Contains(q, "_time:[") {
			http.Error(w, "missing _time filter in: "+q, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/stream+json")
		_, _ = w.Write([]byte(
			`{"_time":"2026-06-03T14:44:59.809Z","_stream":"{alloc_id=\"a1\",task=\"nf-task\",stream=\"stdout\"}","_msg":"hello"}` + "\n" +
				`{"_time":"2026-06-03T14:45:00.561Z","_stream":"{alloc_id=\"a1\",task=\"nf-task\",stream=\"stdout\"}","_msg":"world"}` + "\n"))
	}))
	defer srv.Close()

	c := NewVictoriaLogsClient(srv.URL)
	entries, err := c.QueryStream(
		context.Background(),
		`{alloc_id="a1",task="nf-task",stream="stdout"}`, // streamSelector
		"",                                               // extra
		"",                                               // since (defaults to -1h)
		"",                                               // until (defaults to now)
		10,
	)
	if err != nil {
		t.Fatalf("QueryStream: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Line != "hello" || entries[1].Line != "world" {
		t.Errorf("entries[*].Line = %q,%q; want hello,world", entries[0].Line, entries[1].Line)
	}
	// Labels should be parsed from the _stream string.
	if entries[0].Labels["alloc_id"] != "a1" {
		t.Errorf("entries[0].Labels[alloc_id] = %q; want a1", entries[0].Labels["alloc_id"])
	}
	if entries[0].Labels["task"] != "nf-task" {
		t.Errorf("entries[0].Labels[task] = %q; want nf-task", entries[0].Labels["task"])
	}
}

func TestParseVLStream(t *testing.T) {
	got := parseVLStream(`{alloc_id="a-1",task="nf-task",stream="stdout"}`)
	if got["alloc_id"] != "a-1" || got["task"] != "nf-task" || got["stream"] != "stdout" {
		t.Errorf("parseVLStream = %+v", got)
	}
	// Value containing a comma (quoted).
	got2 := parseVLStream(`{a="hello, world",b="x"}`)
	if got2["a"] != "hello, world" || got2["b"] != "x" {
		t.Errorf("parseVLStream with quoted comma = %+v", got2)
	}
}
