// Package keysource is the broker CLIENT for the managed per-group KEK
// (ADR-0067 age-envelope, managed mode). It calls abc-auth-svc's
// POST /keys/get, which — for an authenticated slot — releases that slot's own
// group KEK K_G (membership-gated; the broker derives kek_id from the slot's
// group, never trusting it from the client).
//
// Seedling key-delivery tiering (decided 2026-06-12): the broker RELEASES K_G
// once; the client unwraps every file's per-file DEK locally. So a Provider
// fetches K_G at most once per process and caches it — N files = 1 round-trip,
// and there is no per-file broker oracle at seedling.
//
// Auth + URL resolution mirror internal/secretsource (opaque token as Bearer;
// base derived from the context's auth_endpoint, else the cluster endpoint with
// the first DNS label swapped to "auth"). The on-wire path is /auth/keys/get
// (the /auth/* Caddy route strips the prefix before forwarding to auth-svc).
//
// Spec: specs/active/abc-managed-encryption-keys.md
package keysource

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	abccfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// ErrNotProvisioned is returned when the broker has no KEK for the caller's
// group yet (HTTP 404 kek_not_provisioned) — the operator must mint it
// (`provision-kek.sh mint`). Distinguished from transport/auth errors.
var ErrNotProvisioned = errors.New("group KEK not provisioned")

// ErrUnconfigured is returned when managed encryption is not configured on the
// broker (HTTP 503 — no root MK). Distinguished so the CLI can fall back / message.
var ErrUnconfigured = errors.New("managed encryption not configured on the broker")

// Client talks to the seedling-tier broker's /keys/get endpoint.
type Client struct {
	base   string // scheme://host (no path); /auth/keys/get is appended
	opaque string // the context's opaque token, presented as Bearer (never logged)
	http   *http.Client
}

// NewClient builds a broker client bound to the active context. Requires an
// opaque token (the context's access_token) and a resolvable broker base.
func NewClient(ctx abccfg.Context) (*Client, error) {
	opaque := strings.TrimSpace(ctx.AccessToken)
	if opaque == "" {
		return nil, fmt.Errorf("keys broker: context access_token (opaque) is empty — claim a slot or use cred_source: local")
	}
	base, err := brokerBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, opaque: opaque, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

type getReq struct {
	KekID string `json:"kek_id,omitempty"`
}
type getResp struct {
	KekID   string `json:"kek_id"`
	Version int    `json:"version"`
	Kek     string `json:"kek"` // base64 of the raw 32-byte group KEK
}

// get fetches the caller's group KEK. kekID may be "" — the broker then derives
// it from the slot's own group. Returns the canonical kek_id, version, and the
// raw KEK bytes.
func (c *Client) get(ctx context.Context, kekID string) (string, int, []byte, error) {
	body, _ := json.Marshal(getReq{KekID: kekID})
	raw, err := c.do(ctx, "/auth/keys/get", body)
	if err != nil {
		return "", 0, nil, err
	}
	var r getResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", 0, nil, fmt.Errorf("keys broker: parse response: %w", err)
	}
	kek, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Kek))
	if err != nil {
		return "", 0, nil, fmt.Errorf("keys broker: decode kek: %w", err)
	}
	return r.KekID, r.Version, kek, nil
}

// do POSTs body to base+path with the opaque as Bearer.
func (c *Client) do(ctx context.Context, path string, body []byte) ([]byte, error) {
	u := strings.TrimRight(c.base, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("keys broker: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.opaque)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keys broker: POST %s: %w", u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return raw, nil
	case http.StatusNotFound:
		return nil, ErrNotProvisioned
	case http.StatusServiceUnavailable:
		return nil, ErrUnconfigured
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("keys broker: %d — opaque token rejected or not a member of the requested group", resp.StatusCode)
	default:
		return nil, fmt.Errorf("keys broker: %s returned %d: %s", u, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// brokerBaseURL resolves scheme://host for the broker:
//
//	1. ABC_KEYS_BROKER_URL / ABC_SECRETS_BROKER_URL env (operator/test override; full base)
//	2. ctx.AuthEndpoint (server-stamped at claim time) — host taken as base
//	3. derive from ctx.Endpoint by swapping the first DNS label to "auth"
func brokerBaseURL(ctx abccfg.Context) (string, error) {
	for _, k := range []string{"ABC_KEYS_BROKER_URL", "ABC_SECRETS_BROKER_URL"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	if ae := strings.TrimSpace(ctx.AuthEndpoint); ae != "" {
		if u, err := url.Parse(ae); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host, nil
		}
	}
	ep := strings.TrimSpace(ctx.Endpoint)
	u, err := url.Parse(ep)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("keys broker: cannot resolve broker URL from endpoint %q (set auth_endpoint on the context or ABC_KEYS_BROKER_URL)", ep)
	}
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("keys broker: host %q lacks a <subdomain>.<rest> shape", u.Host)
	}
	return fmt.Sprintf("%s://auth.%s", u.Scheme, parts[1]), nil
}
