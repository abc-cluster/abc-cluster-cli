package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClusterTypeResponse is the expected JSON shape for GET /api/v1/misc/cluster_type.
// Khan may send cluster_type and/or type.
type ClusterTypeResponse struct {
	ClusterType string `json:"cluster_type"`
	Type        string `json:"type"`
}

// FirstNonEmpty returns the first non-empty cluster type field.
func (r ClusterTypeResponse) FirstNonEmpty() string {
	if strings.TrimSpace(r.ClusterType) != "" {
		return strings.TrimSpace(r.ClusterType)
	}
	return strings.TrimSpace(r.Type)
}

// FetchClusterType calls Khan GET /api/v1/misc/cluster_type with a Bearer token.
func FetchClusterType(ctx context.Context, baseURL, bearerToken string) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", fmt.Errorf("API base URL is empty")
	}
	if strings.TrimSpace(bearerToken) == "" {
		return "", fmt.Errorf("access token is empty")
	}
	reqURL := base + "/api/v1/misc/cluster_type"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearerToken))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cluster_type request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cluster_type: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed ClusterTypeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("cluster_type JSON: %w", err)
	}
	out := parsed.FirstNonEmpty()
	if out == "" {
		return "", fmt.Errorf("cluster_type: empty response")
	}
	return out, nil
}
