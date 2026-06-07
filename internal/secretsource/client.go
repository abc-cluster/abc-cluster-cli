// Package secretsource is the broker CLIENT for the user-secret store (the
// `broker` / `seedling/v1` backend of `abc secrets`, and the store `abc data
// encrypt/decrypt` use for the crypt password when secret_source is set).
//
// It is a *client*, not a second secrets system: `abc secrets` still owns the
// command surface + the local/nomad/vault backends. This package only adds the
// HTTP calls to abc-auth-svc's /secrets/{get,put} endpoints, where the broker
// writes/reads a Nomad Variable on the user's behalf using its own management
// token — so an ordinary user who holds no Nomad ACL can store and retrieve
// their own secret portably.
//
// Auth + URL resolution mirror internal/credsource's broker (opaque token as
// Bearer; base derived from the context's auth_endpoint, else the cluster
// endpoint with the first DNS label swapped to "auth"). The /secrets/* paths
// ride the existing `/auth/*` Caddy route (which strips the /auth prefix before
// forwarding to auth-svc), so the on-wire path is /auth/secrets/{get,put}.
//
// Spec: specs/active/abc-user-secret-portability.md
package secretsource

import (
	"bytes"
	"context"
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

// ErrNotFound is returned by Get when the broker has no value for the key
// (HTTP 404). Callers distinguish "no such secret" from a transport/auth error.
var ErrNotFound = errors.New("secret not found")

// Client talks to the seedling-tier broker's /secrets/* endpoints.
type Client struct {
	base   string // scheme://host (no path); /auth/secrets/{get,put} is appended
	opaque string // the context's opaque token, presented as Bearer (never logged)
	http   *http.Client
}

// NewClient builds a broker client bound to the active context. Requires an
// opaque token (the context's access_token) and a resolvable broker base.
func NewClient(ctx abccfg.Context) (*Client, error) {
	opaque := strings.TrimSpace(ctx.AccessToken)
	if opaque == "" {
		return nil, fmt.Errorf("secret broker: context access_token (opaque) is empty — claim a slot or set secret_source: local")
	}
	base, err := brokerBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, opaque: opaque, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Put stores value under key. group is optional: empty for client-only secrets
// (the broker picks the path), or a namespace for the job-runtime lane so a
// job's workload identity can later read the Variable natively.
func (c *Client) Put(ctx context.Context, key, value, group string) error {
	body, _ := json.Marshal(putReq{Key: key, Value: value, Group: group})
	_, err := c.do(ctx, "/auth/secrets/put", body)
	return err
}

// Get returns the value stored under key, or ErrNotFound if the broker has none.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	body, _ := json.Marshal(getReq{Key: key})
	raw, err := c.do(ctx, "/auth/secrets/get", body)
	if err != nil {
		return "", err
	}
	var r getResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("secret broker: parse response: %w", err)
	}
	return r.Value, nil
}

type putReq struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Group string `json:"group,omitempty"`
}
type getReq struct {
	Key string `json:"key"`
}
type getResp struct {
	Value string `json:"value"`
}

// do POSTs body to base+path with the opaque as Bearer, returning the response
// body on 200, ErrNotFound on 404, and a clear error otherwise.
func (c *Client) do(ctx context.Context, path string, body []byte) ([]byte, error) {
	u := strings.TrimRight(c.base, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("secret broker: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.opaque)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("secret broker: POST %s: %w", u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		return raw, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("secret broker: %d unauthorized — opaque token rejected (re-claim your slot?)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("secret broker: %s returned %d: %s", u, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// brokerBaseURL resolves scheme://host for the broker:
//
//	1. ABC_SECRETS_BROKER_URL env (operator/test override; full base)
//	2. ctx.AuthEndpoint (server-stamped at claim time) — host taken as base
//	3. derive from ctx.Endpoint by swapping the first DNS label to "auth"
func brokerBaseURL(ctx abccfg.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("ABC_SECRETS_BROKER_URL")); v != "" {
		return v, nil
	}
	if ae := strings.TrimSpace(ctx.AuthEndpoint); ae != "" {
		if u, err := url.Parse(ae); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host, nil
		}
	}
	ep := strings.TrimSpace(ctx.Endpoint)
	u, err := url.Parse(ep)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("secret broker: cannot resolve broker URL from endpoint %q (set auth_endpoint on the context or ABC_SECRETS_BROKER_URL)", ep)
	}
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", fmt.Errorf("secret broker: host %q lacks a <subdomain>.<rest> shape", u.Host)
	}
	return fmt.Sprintf("%s://auth.%s", u.Scheme, parts[1]), nil
}
