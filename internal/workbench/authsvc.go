package workbench

// authsvc.go — thin client for abc-auth-svc broker endpoints.
//
// Today this is just MintHubToken (POST /workbench/token). The endpoint
// authenticates the caller via the active context's access token (the
// slot's Nomad token), validates it server-side, and mints a JupyterHub
// user token using the JH admin token that only auth-svc holds.
//
// Design: brainstorms/abc-workbench/2026-06-01-workbench-connect-laptop-flow.md
// Endpoint: brainstorms/abc-workbench/2026-06-01-workbench-token-cli.md §"new abc-auth-svc endpoint"

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MintHubTokenRequest is the JSON body for POST /workbench/token.
// Both fields are optional — auth-svc derives sensible defaults when empty.
type MintHubTokenRequest struct {
	Note      string `json:"note,omitempty"`
	ExpiresIn int64  `json:"expires_in,omitempty"` // seconds; 0 → server default (7d)
}

// MintHubTokenResponse mirrors the JSON returned by /workbench/token.
type MintHubTokenResponse struct {
	Token     string   `json:"token"`
	ID        string   `json:"id"`
	ExpiresAt string   `json:"expires_at"`
	Scopes    []string `json:"scopes"`
	Note      string   `json:"note"`
	Slot      string   `json:"slot"`    // e.g. "slot-calm_dassie" — the JH username for URL composition
	HubURL    string   `json:"hub_url"` // e.g. "https://workbench.seedling.abc-cluster.cloud"
}

// MintHubToken calls abc-auth-svc to mint a fresh JupyterHub user token for
// the slot identified by the bearer token. authEndpoint is the auth-svc
// base URL (e.g. "https://auth.seedling.abc-cluster.cloud"); bearer is the
// active context's access token; note + expiresIn are optional.
//
// Errors carry the HTTP status + the auth-svc-returned message so the user
// sees actionable text.
func MintHubToken(
	ctx context.Context,
	authEndpoint string,
	bearer string,
	req MintHubTokenRequest,
) (*MintHubTokenResponse, error) {
	endpoint := strings.TrimRight(authEndpoint, "/") + "/workbench/token"
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("invalid auth endpoint %q: %w", authEndpoint, err)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to surface auth-svc's structured error message.
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rb, &errBody)
		if errBody.Error != "" {
			return nil, fmt.Errorf("auth-svc %d: %s", resp.StatusCode, errBody.Error)
		}
		return nil, fmt.Errorf("auth-svc %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}

	var out MintHubTokenResponse
	if err := json.Unmarshal(rb, &out); err != nil {
		return nil, fmt.Errorf("decode auth-svc response: %w (body: %s)", err, strings.TrimSpace(string(rb)))
	}
	if out.Token == "" || out.Slot == "" {
		return nil, fmt.Errorf("auth-svc response missing required fields (token / slot)")
	}
	return &out, nil
}

// knownServiceLabels are first-DNS-label values that indicate the endpoint is a
// per-service host (<svc>.<tier>.<base>). For these we replace the label with
// "auth"; for anything else we treat the host as the bare tier gateway
// (<tier>.<base>) and prepend "auth.".
var knownServiceLabels = map[string]bool{
	"nomad": true, "workbench": true, "api": true, "auth": true,
	"grafana": true, "upload": true, "minio": true, "vault": true,
}

// DeriveAuthEndpoint converts a cluster endpoint URL into its auth-svc sibling.
//
// Two endpoint shapes occur in the wild:
//
//	<svc>.<tier>.<base>   e.g. https://nomad.seedling.abc-cluster.cloud
//	<tier>.<base>         e.g. https://seedling.abc-cluster.cloud   (the API gateway)
//
// Both must map to the SAME auth host: https://auth.seedling.abc-cluster.cloud.
// We distinguish by the first DNS label: a known service label is replaced
// with "auth"; otherwise the whole host is treated as the tier gateway and
// "auth." is prepended.
//
// An earlier version always replaced the first label, which turned the gateway
// form (seedling.abc-cluster.cloud) into auth.abc-cluster.cloud — a host that
// does not exist. If the heuristic is wrong for a deployment, callers should
// pass --auth-endpoint explicitly.
func DeriveAuthEndpoint(clusterEndpoint string) (string, error) {
	clusterEndpoint = strings.TrimSpace(clusterEndpoint)
	if clusterEndpoint == "" {
		return "", fmt.Errorf("active context has no endpoint")
	}
	u, err := url.Parse(clusterEndpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute URL: %q", clusterEndpoint)
	}
	first, rest, found := strings.Cut(u.Host, ".")
	if !found || rest == "" {
		return "", fmt.Errorf("host %q has no domain part", u.Host)
	}
	if knownServiceLabels[strings.ToLower(first)] {
		// <svc>.<tier>.<base> → auth.<tier>.<base>
		return fmt.Sprintf("%s://auth.%s", u.Scheme, rest), nil
	}
	// <tier>.<base> (bare gateway) → auth.<tier>.<base>
	return fmt.Sprintf("%s://auth.%s", u.Scheme, u.Host), nil
}
