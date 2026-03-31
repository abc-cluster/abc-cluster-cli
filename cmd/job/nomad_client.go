// Package job implements the "abc job" command group.
//
// nomad_client.go — minimal Nomad HTTP API client.
//
// This client covers only the endpoints required by "abc job" sub-commands.
// It has zero external dependencies beyond the Go standard library.
//
// Nomad API reference: https://developer.hashicorp.com/nomad/api-docs
package job

import (
	"bufio"
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

// nomadClient is a thin wrapper around Nomad's HTTP API.
// All methods accept a context for cancellation and timeout.
type nomadClient struct {
	addr   string       // e.g. "http://127.0.0.1:4646"
	token  string       // Nomad ACL token (X-Nomad-Token header)
	region string       // Nomad region override (empty = server default)
	http   *http.Client
}

func newNomadClient(addr, token, region string) *nomadClient {
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	return &nomadClient{
		addr:   strings.TrimRight(addr, "/"),
		token:  token,
		region: region,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Wire types (only the fields abc job actually uses) ────────────────────────

// NomadJobStub is one entry from GET /v1/jobs.
type NomadJobStub struct {
	ID          string   `json:"ID"`
	Name        string   `json:"Name"`
	Type        string   `json:"Type"`
	Status      string   `json:"Status"`
	Namespace   string   `json:"Namespace"`
	Region      string   `json:"Region"`
	Datacenters []string `json:"Datacenters"`
	SubmitTime  int64    `json:"SubmitTime"` // Unix nanoseconds
	ModifyTime  int64    `json:"ModifyTime"` // Unix nanoseconds; updated when job stops
}

// NomadJob is the full job object from GET /v1/job/{id}.
type NomadJob struct {
	ID          string            `json:"ID"`
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	Status      string            `json:"Status"`
	StatusDescription string     `json:"StatusDescription"`
	Namespace   string            `json:"Namespace"`
	Region      string            `json:"Region"`
	Priority    int               `json:"Priority"`
	Datacenters []string          `json:"Datacenters"`
	SubmitTime  int64             `json:"SubmitTime"`
	Meta        map[string]string `json:"Meta"`
	TaskGroups  []NomadTaskGroup  `json:"TaskGroups"`
}

// NomadTaskGroup holds the task group summary inside a job.
type NomadTaskGroup struct {
	Name  string      `json:"Name"`
	Count int         `json:"Count"`
	Tasks []NomadTask `json:"Tasks"`
}

// NomadTask holds the minimal task fields used for display.
type NomadTask struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

// NomadAllocStub is one entry from GET /v1/job/{id}/allocations.
type NomadAllocStub struct {
	ID            string            `json:"ID"`
	Name          string            `json:"Name"`
	NodeID        string            `json:"NodeID"`
	NodeName      string            `json:"NodeName"`
	JobID         string            `json:"JobID"`
	TaskGroup     string            `json:"TaskGroup"`
	ClientStatus  string            `json:"ClientStatus"`  // pending, running, complete, failed, lost
	DesiredStatus string            `json:"DesiredStatus"` // run, stop
	CreateTime    int64                     `json:"CreateTime"`  // Unix nanoseconds
	ModifyTime    int64                     `json:"ModifyTime"`  // Unix nanoseconds
	TaskStates    map[string]NomadTaskState `json:"TaskStates"`
}

// NomadTaskState holds per-task state inside an allocation.
type NomadTaskState struct {
	State     string `json:"State"` // pending, running, dead
	StartedAt string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
	Failed    bool   `json:"Failed"`
}

// NomadEvaluation is one entry from GET /v1/job/{id}/evaluations.
type NomadEvaluation struct {
	ID     string `json:"ID"`
	Status string `json:"Status"` // pending, complete, failed, cancelled
}

// NomadRegisterResponse is the response from POST /v1/jobs.
type NomadRegisterResponse struct {
	EvalID  string `json:"EvalID"`
	JobModifyIndex uint64 `json:"JobModifyIndex"`
	Warnings string `json:"Warnings"`
}

// NomadDeregisterResponse is the response from DELETE /v1/job/{id}.
type NomadDeregisterResponse struct {
	EvalID string `json:"EvalID"`
}

// NomadDispatchResponse is the response from POST /v1/job/{id}/dispatch.
type NomadDispatchResponse struct {
	DispatchedJobID string `json:"DispatchedJobID"`
	EvalID          string `json:"EvalID"`
}

// NomadPlanResponse is the response from POST /v1/job/{id}/plan.
type NomadPlanResponse struct {
	Annotations    NomadPlanAnnotations `json:"Annotations"`
	FailedTGAllocs map[string]interface{} `json:"FailedTGAllocs"`
	Diff           NomadJobDiff         `json:"Diff"`
	Warnings       string               `json:"Warnings"`
}

// NomadPlanAnnotations holds placement annotations from a plan.
type NomadPlanAnnotations struct {
	DesiredTGUpdates map[string]NomadDesiredUpdates `json:"DesiredTGUpdates"`
}

// NomadDesiredUpdates holds the counts from a plan for a task group.
type NomadDesiredUpdates struct {
	Place  uint64 `json:"Place"`
	Update uint64 `json:"Update"`
	Stop   uint64 `json:"Stop"`
}

// NomadJobDiff summarises what would change.
type NomadJobDiff struct {
	Type string `json:"Type"` // Added, Deleted, Edited, None
}

// NomadParseResponse is the response from POST /v1/jobs/parse.
// It returns a raw JSON job object which we re-submit as-is.
type NomadParseResponse = json.RawMessage

// NomadLogFrame is one frame from the log streaming endpoint.
type NomadLogFrame struct {
	Data   []byte `json:"Data"`
	File   string `json:"File"`
	Offset int64  `json:"Offset"`
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *nomadClient) url(path string, query url.Values) string {
	if c.region != "" {
		if query == nil {
			query = url.Values{}
		}
		query.Set("region", c.region)
	}
	if query != nil {
		return fmt.Sprintf("%s%s?%s", c.addr, path, query.Encode())
	}
	return fmt.Sprintf("%s%s", c.addr, path)
}

func (c *nomadClient) do(ctx context.Context, method, path string, query url.Values, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.url(path, query), bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-Nomad-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *nomadClient) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *nomadClient) post(ctx context.Context, path string, body, out interface{}) error {
	resp, err := c.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *nomadClient) delete(ctx context.Context, path string, query url.Values, out interface{}) error {
	resp, err := c.do(ctx, http.MethodDelete, path, query, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out interface{}) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("nomad API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── API methods ───────────────────────────────────────────────────────────────

// ListJobs returns job stubs, optionally filtered by prefix.
func (c *nomadClient) ListJobs(ctx context.Context, prefix, namespace string) ([]NomadJobStub, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out []NomadJobStub
	return out, c.get(ctx, "/v1/jobs", q, &out)
}

// GetJob returns a full job object.
func (c *nomadClient) GetJob(ctx context.Context, jobID, namespace string) (*NomadJob, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out NomadJob
	return &out, c.get(ctx, "/v1/job/"+url.PathEscape(jobID), q, &out)
}

// GetJobAllocs returns the allocation stubs for a job.
func (c *nomadClient) GetJobAllocs(ctx context.Context, jobID, namespace string, all bool) ([]NomadAllocStub, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	if all {
		q.Set("all", "true")
	}
	var out []NomadAllocStub
	return out, c.get(ctx, "/v1/job/"+url.PathEscape(jobID)+"/allocations", q, &out)
}

// GetJobEvals returns evaluations for a job.
func (c *nomadClient) GetJobEvals(ctx context.Context, jobID, namespace string) ([]NomadEvaluation, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out []NomadEvaluation
	return out, c.get(ctx, "/v1/job/"+url.PathEscape(jobID)+"/evaluations", q, &out)
}

// ParseHCL sends HCL text to Nomad's server-side parser and returns a raw JSON job spec.
func (c *nomadClient) ParseHCL(ctx context.Context, hcl string) (json.RawMessage, error) {
	body := map[string]interface{}{
		"JobHCL":       hcl,
		"Canonicalize": true,
	}
	var out json.RawMessage
	return out, c.post(ctx, "/v1/jobs/parse", body, &out)
}

// RegisterJob submits a job (raw JSON as returned by ParseHCL).
func (c *nomadClient) RegisterJob(ctx context.Context, jobJSON json.RawMessage) (*NomadRegisterResponse, error) {
	// Nomad expects {"Job": <job object>}
	body := json.RawMessage(fmt.Sprintf(`{"Job":%s}`, string(jobJSON)))
	var out NomadRegisterResponse
	return &out, c.post(ctx, "/v1/jobs", &body, &out)
}

// PlanJob does a dry-run plan for a job.
func (c *nomadClient) PlanJob(ctx context.Context, jobID string, jobJSON json.RawMessage) (*NomadPlanResponse, error) {
	body := json.RawMessage(fmt.Sprintf(`{"Job":%s,"Diff":true}`, string(jobJSON)))
	var out NomadPlanResponse
	return &out, c.post(ctx, "/v1/job/"+url.PathEscape(jobID)+"/plan", &body, &out)
}

// StopJob deregisters a job. Set purge=true to remove the job definition entirely.
func (c *nomadClient) StopJob(ctx context.Context, jobID, namespace string, purge bool) (*NomadDeregisterResponse, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	if purge {
		q.Set("purge", "true")
	}
	var out NomadDeregisterResponse
	return &out, c.delete(ctx, "/v1/job/"+url.PathEscape(jobID), q, &out)
}

// DispatchJob dispatches a parameterized batch job.
func (c *nomadClient) DispatchJob(ctx context.Context, jobID string, meta map[string]string, payload []byte) (*NomadDispatchResponse, error) {
	body := map[string]interface{}{
		"Meta":    meta,
		"Payload": payload,
	}
	var out NomadDispatchResponse
	return &out, c.post(ctx, "/v1/job/"+url.PathEscape(jobID)+"/dispatch", body, &out)
}

// StreamLogs streams log frames for a task in an allocation.
// It writes to w until the context is cancelled or the stream ends.
// logType must be "stdout" or "stderr". origin is "start" or "end".
func (c *nomadClient) StreamLogs(ctx context.Context, allocID, task, logType, origin string, offset int64, w io.Writer) error {
	q := url.Values{
		"task":   {task},
		"type":   {logType},
		"origin": {origin},
		"offset": {fmt.Sprintf("%d", offset)},
		"follow": {"true"},
	}

	// Use a streaming client with no timeout for log following.
	streamClient := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url("/v1/client/fs/logs/"+url.PathEscape(allocID), q), nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-Nomad-Token", c.token)
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("log stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("log stream %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// Nomad streams newline-delimited JSON frames.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var frame NomadLogFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			continue // skip malformed frames
		}
		if len(frame.Data) > 0 {
			w.Write(frame.Data) //nolint:errcheck
		}
	}
	return scanner.Err()
}
