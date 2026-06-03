package floor

// victorialogs.go — query client for VictoriaLogs (LogSQL).
//
// VictoriaLogs implements its own native query API at `/select/logsql/query`
// (LogSQL syntax) and does NOT expose Grafana Loki's `/loki/api/v1/query_range`.
// The seedling tier uses VictoriaLogs as the log archive (Alloy ships to its
// Loki-compat *insert* endpoint at `/insert/loki/api/v1/push`, but querying
// is LogSQL-only).
//
// Docs: https://docs.victoriametrics.com/victorialogs/logsql/
//
// LogsClient (a sibling of LokiClient) speaks LogSQL. NewLogsClient(url) auto-
// selects between Loki and VictoriaLogs based on a cheap probe of the URL:
//   - any host whose port is the VictoriaLogs default (9428) → VictoriaLogs.
//   - paths containing /select/logsql or /select/loki → VictoriaLogs.
//   - everything else → Loki (today's behaviour, unchanged).
//
// This keeps the abc-nodes tier (which deploys real Loki) working untouched
// while the seedling tier (which deploys VictoriaLogs) is finally queryable.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// VictoriaLogsClient queries a VictoriaLogs instance via its native LogSQL API.
type VictoriaLogsClient struct {
	baseURL string
	http    *http.Client
}

// NewVictoriaLogsClient creates a VictoriaLogs LogSQL client for baseURL.
// The base may or may not include a trailing slash; either works.
func NewVictoriaLogsClient(baseURL string) *VictoriaLogsClient {
	return &VictoriaLogsClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// vlEntry mirrors the JSON-line shape VictoriaLogs returns from
// /select/logsql/query. We only consume the fields the CLI display layer
// needs; everything else is dropped.
type vlEntry struct {
	Time   string `json:"_time"` // RFC3339Nano, e.g. "2026-06-03T15:30:45.123456789Z"
	Stream string `json:"_stream"` // "{alloc_id=\"…\",task=\"…\",…}" — the labels
	Msg    string `json:"_msg"`    // the actual log line text
}

// QueryStream is a VictoriaLogs-flavoured QueryRange. Selectors take the
// form expected by LogSQL's `_stream:{…}` filter (label="value" or
// label=~"regex"); they are joined and emitted as a single stream selector.
// Extra free-text filters can be appended via `extra`.
//
// Returned entries are sorted in ascending time order to match LokiClient.
//
// Times are emitted in RFC3339Nano to LogSQL's `_time:[start, end)` filter.
func (c *VictoriaLogsClient) QueryStream(ctx context.Context, streamSelector string, extra string, since, until string, limit int) ([]LokiEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	end := time.Now()
	start := end.Add(-1 * time.Hour)
	if since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			start = t
		} else if d, err := parseDuration(since); err == nil {
			start = end.Add(-d)
		}
	}
	if until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			end = t
		}
	}

	// LogSQL query: `_stream:{...} _time:[start, end) | <extra> | limit N`
	// VictoriaLogs accepts `[start TO end]` style brackets, but the cleaner
	// form is to filter both sides with date strings via _time:>=X _time:<Y.
	var b strings.Builder
	b.WriteString("_stream:")
	b.WriteString(streamSelector)
	if extra != "" {
		b.WriteByte(' ')
		b.WriteString(extra)
	}
	b.WriteString(fmt.Sprintf(` _time:[%s, %s)`,
		start.UTC().Format(time.RFC3339Nano),
		end.UTC().Format(time.RFC3339Nano)))

	q := url.Values{}
	q.Set("query", b.String())
	q.Set("limit", fmt.Sprintf("%d", limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/select/logsql/query?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("victorialogs query: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		preview := strings.TrimSpace(string(body))
		if len(preview) > 400 {
			preview = preview[:400] + "…"
		}
		return nil, fmt.Errorf("victorialogs query %d: %s", resp.StatusCode, preview)
	}

	// VictoriaLogs returns one JSON object per line.
	var entries []LokiEntry
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e vlEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil {
			continue
		}
		entries = append(entries, LokiEntry{
			Timestamp: ts,
			Line:      e.Msg,
			Labels:    parseVLStream(e.Stream),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	return entries, nil
}

// parseVLStream parses VictoriaLogs's "{k1=\"v1\",k2=\"v2\"}" label-set
// notation into a map. Best-effort: malformed input yields a partial map.
func parseVLStream(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	// Pairs are "k=\"v\"" separated by commas. Values may contain commas
	// inside quotes — handle by walking with a quote-aware scanner.
	inQ := false
	start := 0
	emit := func(end int) {
		pair := s[start:end]
		start = end + 1
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			return
		}
		k := strings.TrimSpace(pair[:eq])
		v := strings.TrimSpace(pair[eq+1:])
		v = strings.TrimPrefix(v, "\"")
		v = strings.TrimSuffix(v, "\"")
		out[k] = v
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQ = !inQ
		case ',':
			if !inQ {
				emit(i)
			}
		}
	}
	if start < len(s) {
		emit(len(s))
	}
	return out
}

// LogsBackend names which historical-logs API the cluster speaks.
type LogsBackend int

const (
	BackendLoki LogsBackend = iota
	BackendVictoriaLogs
)

// DetectLogsBackend picks Loki vs VictoriaLogs from the configured URL.
//   - port 9428 OR path containing /select/logsql or /select/loki → VictoriaLogs
//   - else → Loki (the historical default for abc-nodes)
func DetectLogsBackend(rawURL string) LogsBackend {
	u, err := url.Parse(rawURL)
	if err != nil {
		return BackendLoki
	}
	if u.Port() == "9428" {
		return BackendVictoriaLogs
	}
	if strings.Contains(u.Path, "/select/logsql") || strings.Contains(u.Path, "/select/loki") {
		return BackendVictoriaLogs
	}
	return BackendLoki
}
