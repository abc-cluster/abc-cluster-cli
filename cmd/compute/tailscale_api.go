package compute

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

const tailscaleCreateKeyURL = "https://api.tailscale.com/api/v2/tailnet/-/keys"

var tailscaleAPIHTTPClient = &http.Client{Timeout: 30 * time.Second}

type TailscaleAuthKeyCreateRequest struct {
	APIKey        string
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	Expiry        time.Duration
	Description   string
}

func CreateTailscaleAuthKey(ctx context.Context, req TailscaleAuthKeyCreateRequest) (string, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return "", fmt.Errorf("empty API key")
	}
	if req.Expiry < 0 {
		return "", fmt.Errorf("expiry must be >= 0")
	}

	var payload struct {
		Capabilities struct {
			Devices struct {
				Create struct {
					Reusable      bool `json:"reusable"`
					Ephemeral     bool `json:"ephemeral"`
					Preauthorized bool `json:"preauthorized"`
				} `json:"create"`
			} `json:"devices"`
		} `json:"capabilities"`
		ExpirySeconds *int64 `json:"expirySeconds,omitempty"`
		Description   string `json:"description,omitempty"`
	}
	payload.Capabilities.Devices.Create.Reusable = req.Reusable
	payload.Capabilities.Devices.Create.Ephemeral = req.Ephemeral
	payload.Capabilities.Devices.Create.Preauthorized = req.Preauthorized
	if req.Expiry > 0 {
		secs := int64(req.Expiry / time.Second)
		if secs < 1 {
			secs = 1
		}
		payload.ExpirySeconds = &secs
	}
	if desc := sanitizeTailscaleKeyDescription(req.Description); desc != "" {
		payload.Description = desc
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tailscaleCreateKeyURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.SetBasicAuth(apiKey, "")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := tailscaleAPIHTTPClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var parsed struct {
		Key     string `json:"key"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(respBody, &parsed)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(respBody))
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("tailscale API returned HTTP %d: %s", resp.StatusCode, msg)
	}
	if strings.TrimSpace(parsed.Key) == "" {
		return "", fmt.Errorf("tailscale API response did not include an auth key")
	}
	return parsed.Key, nil
}

func sanitizeTailscaleKeyDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
		if b.Len() >= 96 {
			break
		}
	}
	return b.String()
}
