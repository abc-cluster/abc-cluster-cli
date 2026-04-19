package floor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GrafanaClient interacts with a Grafana instance via the HTTP API.
type GrafanaClient struct {
	baseURL  string
	user     string
	password string
	http     *http.Client
}

// NewGrafanaClient creates a client for the Grafana instance at baseURL.
// Pass user/password for basic auth (typically "admin"/"admin" in lab setups).
func NewGrafanaClient(baseURL, user, password string) *GrafanaClient {
	if user == "" {
		user = "admin"
	}
	if password == "" {
		password = "admin"
	}
	return &GrafanaClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// annotationRequest is the payload for POST /api/annotations.
type annotationRequest struct {
	Text    string   `json:"text"`
	Tags    []string `json:"tags"`
	Time    int64    `json:"time"`    // ms since epoch
	TimeEnd int64    `json:"timeEnd"` // ms; equals Time for point annotations
}

// Annotate writes a point annotation to all dashboards.
// text is the message body; tags are optional filter labels.
func (c *GrafanaClient) Annotate(ctx context.Context, text string, tags []string) error {
	now := time.Now().UnixMilli()
	payload := annotationRequest{
		Text:    text,
		Tags:    tags,
		Time:    now,
		TimeEnd: now,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/annotations", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("grafana annotate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grafana annotate %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Healthy returns true when Grafana's /api/health endpoint responds 200.
func (c *GrafanaClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/health", nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth(c.user, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
