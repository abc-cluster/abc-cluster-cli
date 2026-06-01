package workbench

// hubapi.go — minimal JupyterHub user-tokens REST client.
// Wraps:
//   POST   /hub/api/users/<user>/tokens
//   GET    /hub/api/users/<user>/tokens
//   DELETE /hub/api/users/<user>/tokens/<id>
//
// Used by `abc workbench token …` from inside a slot's singleuser session,
// authenticated with the JUPYTERHUB_API_TOKEN env var that SystemdSpawner
// injects. See brainstorms/abc-workbench/2026-06-01-workbench-token-cli.md.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HubTokenClient is a thin wrapper over JupyterHub's user-tokens REST endpoints.
// Distinct from the admin-side HubClient in jupyterhub.go (which is operator-
// tier and stops/inspects user servers); this client is slot-side and acts on
// the slot's own tokens via its JUPYTERHUB_API_TOKEN.
type HubTokenClient struct {
	APIBase string       // e.g. http://127.0.0.1:15001/hub/api (no trailing slash)
	Token   string       // JUPYTERHUB_API_TOKEN
	User    string       // JUPYTERHUB_USER (e.g. slot-calm_dassie)
	HTTP    *http.Client // optional; defaults to a 10s-timeout client
}

// HubToken represents one user-token record as returned by JupyterHub.
//
// The plaintext token value is present ONLY on the response from CreateToken
// — list/get/revoke never return it. Treat Token as the only field with
// "show once and forget" semantics; everything else is metadata.
type HubToken struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind,omitempty"`
	User       string   `json:"user,omitempty"`
	Note       string   `json:"note,omitempty"`
	Token      string   `json:"token,omitempty"`         // populated by Create only
	Created    string   `json:"created,omitempty"`       // RFC3339
	LastActivity *string `json:"last_activity,omitempty"` // RFC3339 or null
	Expires    *string  `json:"expires_at,omitempty"`    // RFC3339 or null
	Scopes     []string `json:"scopes,omitempty"`
	ExpiresIn  int64    `json:"expires_in,omitempty"`    // request-only (Create)
}

// NewHubTokenClient builds a client from explicit values. Caller is responsible
// for non-empty APIBase / Token / User; helpers in cmd/workbench/token.go
// resolve them from env vars (JUPYTERHUB_API_URL / JUPYTERHUB_API_TOKEN /
// JUPYTERHUB_USER) before constructing.
func NewHubTokenClient(apiBase, token, user string) *HubTokenClient {
	return &HubTokenClient{
		APIBase: strings.TrimRight(apiBase, "/"),
		Token:   token,
		User:    user,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateToken issues a new user token. note is the human-readable label
// (`--name`); expiresIn is honoured by JupyterHub as a TTL from now (0 =
// never expires, but our porcelain enforces a bounded default); scopes is
// the token's scope list (nil → JupyterHub default "self").
//
// Returns the HubToken with Token populated. Callers must surface the
// Token to the user exactly once — it is not retrievable later.
func (c *HubTokenClient) CreateToken(note string, expiresIn time.Duration, scopes []string) (*HubToken, error) {
	body := map[string]any{}
	if note != "" {
		body["note"] = note
	}
	if expiresIn > 0 {
		body["expires_in"] = int64(expiresIn.Seconds())
	}
	if len(scopes) > 0 {
		body["scopes"] = scopes
	}
	var out HubToken
	if err := c.do("POST", c.userPath()+"/tokens", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTokens returns all active user tokens (metadata only; no plaintext
// values). JupyterHub returns both OAuth client tokens and API tokens; the
// caller may filter by kind if desired.
func (c *HubTokenClient) ListTokens() ([]HubToken, error) {
	// JupyterHub returns the tokens collection under the user resource.
	// GET /hub/api/users/<user> returns an object whose `tokens` field
	// contains the list; the dedicated /tokens endpoint exists on newer
	// hubs (5.x). Use the dedicated path for clarity.
	var raw json.RawMessage
	if err := c.do("GET", c.userPath()+"/tokens", nil, &raw); err != nil {
		return nil, err
	}
	// JupyterHub may return either an array or an object with a tokens field.
	// Handle both shapes.
	var list []HubToken
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	// JupyterHub 5.x returns {"api_tokens": [...], "oauth_tokens": [...]}.
	// Older versions returned {"tokens": [...]}. Try both.
	var wrapped struct {
		APITokens   []HubToken `json:"api_tokens"`
		OAuthTokens []HubToken `json:"oauth_tokens"`
		Tokens      []HubToken `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		// Filter out the server's own session token (kind=api_token but note
		// "Server at /user/<name>/"). Users don't want to see/revoke that;
		// revoking it would log them out.
		out := make([]HubToken, 0, len(wrapped.APITokens))
		for _, t := range wrapped.APITokens {
			if strings.HasPrefix(t.Note, "Server at ") {
				continue
			}
			out = append(out, t)
		}
		// Append legacy 'tokens' field if present (older hub versions).
		for _, t := range wrapped.Tokens {
			if strings.HasPrefix(t.Note, "Server at ") {
				continue
			}
			out = append(out, t)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected hub tokens response: %s", string(raw))
}

// RevokeToken deletes a specific token by its ID.
func (c *HubTokenClient) RevokeToken(id string) error {
	return c.do("DELETE", c.userPath()+"/tokens/"+id, nil, nil)
}

// userPath returns "/users/<user>" — the segment under APIBase.
func (c *HubTokenClient) userPath() string {
	return "/users/" + c.User
}

// do issues an authenticated request and decodes the response.
// in is JSON-marshalled (skipped if nil); out is JSON-decoded (skipped if nil).
// Non-2xx responses return a structured error including the body.
func (c *HubTokenClient) do(method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	url := c.APIBase + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	if out == nil || len(rb) == 0 {
		return nil
	}
	// out can be *json.RawMessage (preserve raw for shape-ambiguous endpoints)
	// or any concrete type for normal unmarshalling.
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], rb...)
		return nil
	}
	if err := json.Unmarshal(rb, out); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, strings.TrimSpace(string(rb)))
	}
	return nil
}
