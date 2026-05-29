package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// HubClient is a minimal JupyterHub admin API client.
// Requires an admin service token stored in config under
// admin.services.workbench.hub_token (generate via `tljh-config generate-admin-token`).
type HubClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewHubClient creates a client for the given hub base URL and admin token.
func NewHubClient(baseURL, token string) *HubClient {
	return &HubClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// HubURLFromCtx returns the JupyterHub URL from the active context config,
// falling back to the seedling default.
func HubURLFromCtx(actx config.Context) string {
	if u, ok := config.GetAdminFloorField(&actx.Admin.Services, "workbench", "hub_url"); ok && u != "" {
		return strings.TrimRight(u, "/")
	}
	return "https://workbench.seedling.abc-cluster.cloud"
}

// HubTokenFromCtx returns the JupyterHub admin token from config, and whether
// it was set. Looks at admin.services.workbench.hub_token.
func HubTokenFromCtx(actx config.Context) (string, bool) {
	tok, ok := config.GetAdminFloorField(&actx.Admin.Services, "workbench", "hub_token")
	return tok, ok && strings.TrimSpace(tok) != ""
}

// HubServer holds the server state for one JupyterHub single-user server.
type HubServer struct {
	Name         string     `json:"name"`
	LastActivity *time.Time `json:"last_activity"`
	Started      *time.Time `json:"started"`
	Ready        bool       `json:"ready"`
	Stopped      bool       `json:"stopped"`
	Pending      *string    `json:"pending"`
}

// HubUser is a single user record from the JupyterHub API.
type HubUser struct {
	Name         string     `json:"name"`
	LastActivity *time.Time `json:"last_activity"`
	Server       *HubServer `json:"server"` // nil when no server running
}

// IsRunning reports whether the user has an active server.
func (u HubUser) IsRunning() bool {
	return u.Server != nil && u.Server.Ready && !u.Server.Stopped
}

// ServerIdleSince returns how long the server has been idle (no activity).
// Returns 0 when the server is not running or last_activity is unknown.
func (u HubUser) ServerIdleSince() time.Duration {
	if !u.IsRunning() || u.Server.LastActivity == nil {
		return 0
	}
	return time.Since(*u.Server.LastActivity)
}

// GetUser fetches a single user's record from the JupyterHub API.
func (c *HubClient) GetUser(ctx context.Context, username string) (HubUser, error) {
	var u HubUser
	if err := c.get(ctx, "/hub/api/users/"+username, &u); err != nil {
		return HubUser{}, err
	}
	return u, nil
}

// ListActiveUsers returns all users that currently have a running server.
func (c *HubClient) ListActiveUsers(ctx context.Context) ([]HubUser, error) {
	var all []HubUser
	if err := c.get(ctx, "/hub/api/users?include_stopped_servers=false", &all); err != nil {
		return nil, err
	}
	var active []HubUser
	for _, u := range all {
		if u.IsRunning() {
			active = append(active, u)
		}
	}
	return active, nil
}

// StopServer sends DELETE /hub/api/users/<username>/server.
// 204, 202, 200, and 404 are all treated as success.
func (c *HubClient) StopServer(ctx context.Context, username string) error {
	req, err := http.NewRequestWithContext(ctx,
		http.MethodDelete,
		c.baseURL+"/hub/api/users/"+username+"/server",
		nil,
	)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stop server request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusAccepted, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("hub API returned %d for DELETE /hub/api/users/%s/server",
			resp.StatusCode, username)
	}
}

func (c *HubClient) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hub API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("hub API: not found (404) for %s", path)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hub API returned %d for %s: %s", resp.StatusCode, path, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
