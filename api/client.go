package api

import (
"bytes"
"encoding/json"
"fmt"
"io"
"net/http"
"time"
)

// Client is the HTTP client for the abc-cluster API.
type Client struct {
baseURL     string
accessToken string
workspace   string
httpClient  HTTPDoer
}

// HTTPDoer is the interface for executing HTTP requests, allowing test injection.
type HTTPDoer interface {
Do(req *http.Request) (*http.Response, error)
}

// NewClient creates a new abc-cluster API client with the default HTTP client.
func NewClient(baseURL, accessToken, workspace string) *Client {
return &Client{
baseURL:     baseURL,
accessToken: accessToken,
workspace:   workspace,
httpClient: &http.Client{
Timeout: 30 * time.Second,
},
}
}

// PipelineRunRequest holds the parameters for a pipeline run submission.
type PipelineRunRequest struct {
Pipeline   string         `json:"pipeline"`
RunName    string         `json:"runName,omitempty"`
Revision   string         `json:"revision,omitempty"`
Profile    string         `json:"profile,omitempty"`
WorkDir    string         `json:"workDir,omitempty"`
ConfigText string         `json:"config,omitempty"`
Params     map[string]any `json:"params,omitempty"`
}

// PipelineRunResponse holds the API response from a pipeline run submission.
type PipelineRunResponse struct {
RunID      string `json:"runId"`
RunName    string `json:"runName"`
WorkflowID string `json:"workflowId,omitempty"`
}

// SubmitPipelineRun submits a pipeline run to the abc-cluster API.
func (c *Client) SubmitPipelineRun(req *PipelineRunRequest) (*PipelineRunResponse, error) {
body, err := json.Marshal(req)
if err != nil {
return nil, fmt.Errorf("failed to marshal request: %w", err)
}

url := fmt.Sprintf("%s/pipelines/run", c.baseURL)
if c.workspace != "" {
url = fmt.Sprintf("%s?workspaceId=%s", url, c.workspace)
}

httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
if err != nil {
return nil, fmt.Errorf("failed to create HTTP request: %w", err)
}
httpReq.Header.Set("Content-Type", "application/json")
httpReq.Header.Set("Accept", "application/json")
if c.accessToken != "" {
httpReq.Header.Set("Authorization", "Bearer "+c.accessToken)
}

resp, err := c.httpClient.Do(httpReq)
if err != nil {
return nil, fmt.Errorf("request failed: %w", err)
}
defer resp.Body.Close()

respBody, err := io.ReadAll(resp.Body)
if err != nil {
return nil, fmt.Errorf("failed to read response body: %w", err)
}

if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
return nil, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
}

var runResp PipelineRunResponse
if err := json.Unmarshal(respBody, &runResp); err != nil {
return nil, fmt.Errorf("failed to parse response: %w", err)
}

return &runResp, nil
}
