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
	// authEndpoint is the auth-svc base (e.g. https://workbench.<tier>.<base>).
	// A stamped seedling/v1 endpoint may already carry the /auth/exchange path;
	// strip it back to the base before composing the token path.
	base := strings.TrimRight(authEndpoint, "/")
	base = strings.TrimSuffix(base, "/auth/exchange")
	// The endpoint is served under Caddy's /auth/* route (same host as the
	// workbench), so the path is /auth/workbench/token — NOT /workbench/token,
	// which would fall through to forward_auth and 302 to the login page.
	endpoint := base + "/auth/workbench/token"
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

// NOTE: there is deliberately no DeriveAuthEndpoint here. abc-auth-svc is not a
// sibling `auth.<rest>` host — on seedling no such DNS record exists. It is
// co-located behind the same Caddy as the workbench, served under /auth/*. The
// connect command therefore uses the context's stamped auth_endpoint, or falls
// back to the hub host (see cmd/workbench/token.go). Callers needing a
// different host pass --auth-endpoint explicitly.
