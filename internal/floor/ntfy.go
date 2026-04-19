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

// NtfyClient sends messages to a self-hosted ntfy server.
type NtfyClient struct {
	baseURL string
	http    *http.Client
}

// NewNtfyClient creates a client for the ntfy server at baseURL.
func NewNtfyClient(baseURL string) *NtfyClient {
	return &NtfyClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// NtfyMessage is a parsed message from the ntfy JSON feed.
type NtfyMessage struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"-"`
	UnixTime int64     `json:"time"`
	Event    string    `json:"event"`
	Topic    string    `json:"topic"`
	Title    string    `json:"title"`
	Message  string    `json:"message"`
	Priority int       `json:"priority"`
	Tags     []string  `json:"tags"`
}

// Publish sends a message to the given topic.
// title, priority (1–5), and tags are optional.
func (c *NtfyClient) Publish(ctx context.Context, topic, message, title string, priority int, tags []string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/"+topic, strings.NewReader(message))
	if err != nil {
		return err
	}
	if title != "" {
		req.Header.Set("X-Title", title)
	}
	if priority > 0 {
		req.Header.Set("X-Priority", fmt.Sprintf("%d", priority))
	}
	if len(tags) > 0 {
		req.Header.Set("X-Tags", strings.Join(tags, ","))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy publish %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// ListMessages fetches recent messages from a topic (poll mode, since=<duration>).
func (c *NtfyClient) ListMessages(ctx context.Context, topic, since string) ([]NtfyMessage, error) {
	if since == "" {
		since = "1h"
	}
	url := fmt.Sprintf("%s/%s/json?poll=1&since=%s", c.baseURL, topic, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ntfy list: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ntfy list %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// ntfy returns newline-delimited JSON (one object per line).
	var messages []NtfyMessage
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg NtfyMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Event != "message" {
			continue
		}
		msg.Time = time.Unix(msg.UnixTime, 0)
		messages = append(messages, msg)
	}
	return messages, nil
}

// Healthy returns true when ntfy's /v1/health endpoint responds with healthy:true.
func (c *NtfyClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var h struct {
		Healthy bool `json:"healthy"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &h)
	return h.Healthy
}
