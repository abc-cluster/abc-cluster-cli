// Package floor provides lightweight HTTP clients for the abc-nodes enhanced
// floor services: Loki, Prometheus, Grafana, ntfy, and MinIO/S3.
// All clients use only the Go standard library — no external SDK is required.
package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LokiClient queries a Grafana Loki instance via the HTTP API.
type LokiClient struct {
	baseURL string
	http    *http.Client
}

// NewLokiClient creates a client for the Loki instance at baseURL.
func NewLokiClient(baseURL string) *LokiClient {
	return &LokiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// LokiEntry is a single log line with its nanosecond timestamp.
type LokiEntry struct {
	Timestamp time.Time
	Line      string
	Labels    map[string]string
}

// lokiQueryRangeResponse is the subset of Loki's query_range JSON we care about.
type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [["<ns_epoch>", "<line>"], ...]
		} `json:"result"`
	} `json:"data"`
}

// QueryRange queries Loki with a LogQL expression over the given time window.
// Entries are returned in ascending timestamp order (oldest first).
func (c *LokiClient) QueryRange(ctx context.Context, logql, since, until string, limit int) ([]LokiEntry, error) {
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

	q := url.Values{}
	q.Set("query", logql)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("direction", "forward")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/loki/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki query_range: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki query_range %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out lokiQueryRangeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("loki response parse: %w", err)
	}

	var entries []LokiEntry
	for _, stream := range out.Data.Result {
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			entries = append(entries, LokiEntry{
				Timestamp: time.Unix(0, ns),
				Line:      v[1],
				Labels:    stream.Stream,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	return entries, nil
}

// Healthy returns true when Loki's /ready endpoint responds 200.
func (c *LokiClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/ready", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// parseDuration parses "1h", "30m", "2h30m" — subset of Go's time.ParseDuration
// plus bare integers treated as seconds.
func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	// Try plain integer seconds.
	if n, e := strconv.ParseInt(s, 10, 64); e == nil {
		return time.Duration(n) * time.Second, nil
	}
	return 0, fmt.Errorf("cannot parse duration %q", s)
}
