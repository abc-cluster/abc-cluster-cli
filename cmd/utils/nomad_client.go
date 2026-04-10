// Package utils provides shared utilities for abc-cluster CLI command groups.
//
// nomad_client.go — exported Nomad HTTP API client used across cmd/job and
// cmd/pipeline. Zero external dependencies beyond the Go standard library.
package utils

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

// NomadClient is a thin wrapper around Nomad's HTTP API.
type NomadClient struct {
	addr   string
	token  string
	region string
	sudo   bool
	cloud  bool
	http   *http.Client
}

// Token returns the ACL token configured on this client.
func (c *NomadClient) Token() string { return c.token }

// Addr returns the Nomad API address configured on this client.
func (c *NomadClient) Addr() string { return c.addr }

// WithSudo marks this client to send X-ABC-Sudo: 1 on every request.
// The method returns the receiver so it can be chained with NewNomadClient.
func (c *NomadClient) WithSudo(sudo bool) *NomadClient {
	c.sudo = sudo
	return c
}

// WithCloud marks this client to send X-ABC-Cloud: 1 on every request.
// The method returns the receiver so it can be chained.
func (c *NomadClient) WithCloud(cloud bool) *NomadClient {
	c.cloud = cloud
	return c
}

// NewNomadClient creates a NomadClient. addr defaults to http://127.0.0.1:4646.
func NewNomadClient(addr, token, region string) *NomadClient {
	if addr == "" {
		addr = "http://127.0.0.1:4646"
	}
	return &NomadClient{
		addr:   strings.TrimRight(addr, "/"),
		token:  token,
		region: region,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Wire types ────────────────────────────────────────────────────────────────

type NomadJobStub struct {
	ID          string   `json:"ID"`
	Name        string   `json:"Name"`
	Type        string   `json:"Type"`
	Status      string   `json:"Status"`
	Namespace   string   `json:"Namespace"`
	Region      string   `json:"Region"`
	Datacenters []string `json:"Datacenters"`
	SubmitTime  int64    `json:"SubmitTime"`
	ModifyTime  int64    `json:"ModifyTime"`
}

type NomadJob struct {
	ID                string            `json:"ID"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	Status            string            `json:"Status"`
	StatusDescription string            `json:"StatusDescription"`
	Namespace         string            `json:"Namespace"`
	Region            string            `json:"Region"`
	Priority          int               `json:"Priority"`
	Datacenters       []string          `json:"Datacenters"`
	SubmitTime        int64             `json:"SubmitTime"`
	Meta              map[string]string `json:"Meta"`
	TaskGroups        []NomadTaskGroup  `json:"TaskGroups"`
}

type NomadTaskGroup struct {
	Name  string      `json:"Name"`
	Count int         `json:"Count"`
	Tasks []NomadTask `json:"Tasks"`
}

type NomadTask struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

type NomadAllocStub struct {
	ID            string                    `json:"ID"`
	Name          string                    `json:"Name"`
	NodeID        string                    `json:"NodeID"`
	NodeName      string                    `json:"NodeName"`
	JobID         string                    `json:"JobID"`
	TaskGroup     string                    `json:"TaskGroup"`
	ClientStatus  string                    `json:"ClientStatus"`
	DesiredStatus string                    `json:"DesiredStatus"`
	CreateTime    int64                     `json:"CreateTime"`
	ModifyTime    int64                     `json:"ModifyTime"`
	TaskStates    map[string]NomadTaskState `json:"TaskStates"`
}

type NomadTaskState struct {
	State      string `json:"State"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
	Failed     bool   `json:"Failed"`
}

type NomadEvaluation struct {
	ID     string `json:"ID"`
	Status string `json:"Status"`
}

type NomadRegisterResponse struct {
	EvalID         string `json:"EvalID"`
	JobModifyIndex uint64 `json:"JobModifyIndex"`
	Warnings       string `json:"Warnings"`
}

type NomadDeregisterResponse struct {
	EvalID string `json:"EvalID"`
}

type NomadDispatchResponse struct {
	DispatchedJobID string `json:"DispatchedJobID"`
	EvalID          string `json:"EvalID"`
}

type NomadPlanResponse struct {
	Annotations    NomadPlanAnnotations   `json:"Annotations"`
	FailedTGAllocs map[string]interface{} `json:"FailedTGAllocs"`
	Diff           NomadJobDiff           `json:"Diff"`
	Warnings       string                 `json:"Warnings"`
}

type NomadPlanAnnotations struct {
	DesiredTGUpdates map[string]NomadDesiredUpdates `json:"DesiredTGUpdates"`
}

type NomadDesiredUpdates struct {
	Place  uint64 `json:"Place"`
	Update uint64 `json:"Update"`
	Stop   uint64 `json:"Stop"`
}

type NomadJobDiff struct {
	Type string `json:"Type"`
}

type NomadParseResponse = json.RawMessage

type NomadLogFrame struct {
	Data   []byte `json:"Data"`
	File   string `json:"File"`
	Offset int64  `json:"Offset"`
}

// ── Nomad Variables types ─────────────────────────────────────────────────────

// NomadVariableStub is one entry from GET /v1/vars.
type NomadVariableStub struct {
	Namespace   string `json:"Namespace"`
	Path        string `json:"Path"`
	CreateTime  int64  `json:"CreateTime"`
	ModifyTime  int64  `json:"ModifyTime"`
	CreateIndex uint64 `json:"CreateIndex"`
	ModifyIndex uint64 `json:"ModifyIndex"`
}

// NomadVariable is a full variable with items from GET /v1/var/<path>.
type NomadVariable struct {
	Namespace   string            `json:"Namespace"`
	Path        string            `json:"Path"`
	Items       map[string]string `json:"Items"`
	CreateTime  int64             `json:"CreateTime"`
	ModifyTime  int64             `json:"ModifyTime"`
	CreateIndex uint64            `json:"CreateIndex"`
	ModifyIndex uint64            `json:"ModifyIndex"`
}

// NomadNodeResource holds per-node resource values.
type NomadNodeResource struct {
	CPU      int `json:"CPU"`
	MemoryMB int `json:"MemoryMB"`
	DiskMB   int `json:"DiskMB"`
}

// NomadNodeStub is a lightweight summary returned by GET /v1/nodes.
type NomadNodeStub struct {
	ID                   string `json:"ID"`
	Name                 string `json:"Name"`
	Datacenter           string `json:"Datacenter"`
	NodeClass            string `json:"NodeClass"`
	Status               string `json:"Status"`
	Drain                bool   `json:"Drain"`
	SchedulingEligibility string `json:"SchedulingEligibility"`
}

// NomadNode represents full node details from GET /v1/node/<id>.
type NomadNode struct {
	ID                   string            `json:"ID"`
	Name                 string            `json:"Name"`
	Datacenter           string            `json:"Datacenter"`
	Region               string            `json:"Region"`
	NodeClass            string            `json:"NodeClass"`
	Status               string            `json:"Status"`
	Drain                bool              `json:"Drain"`
	SchedulingEligibility string           `json:"SchedulingEligibility"`
	Attributes           map[string]string `json:"Attributes"`
	NodeResources        *NomadNodeResource `json:"NodeResources"`
	ReservedResources    *NomadNodeResource `json:"ReservedResources"`
	Drivers              map[string]NomadDriverInfo `json:"Drivers"`
}

// NomadDriverInfo is a simplified driver status entry.
type NomadDriverInfo struct {
	Detected bool `json:"Detected"`
	Healthy  bool `json:"Healthy"`
}

// NomadNamespace is a Nomad namespace with optional metadata.
type NomadNamespace struct {
	Name        string            `json:"Name"`
	Description string            `json:"Description"`
	Meta        map[string]string `json:"Meta"`
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *NomadClient) url(path string, query url.Values) string {
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

func (c *NomadClient) do(ctx context.Context, method, path string, query url.Values, body interface{}) (*http.Response, error) {
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
	if c.sudo {
		req.Header.Set("X-ABC-Sudo", "1")
	}
	if c.cloud {
		req.Header.Set("X-ABC-Cloud", "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *NomadClient) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *NomadClient) post(ctx context.Context, path string, body, out interface{}) error {
	resp, err := c.do(ctx, http.MethodPost, path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *NomadClient) put(ctx context.Context, path string, query url.Values, body, out interface{}) error {
	resp, err := c.do(ctx, http.MethodPut, path, query, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *NomadClient) delete(ctx context.Context, path string, query url.Values, out interface{}) error {
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

// ── Job API methods ───────────────────────────────────────────────────────────

func (c *NomadClient) ListJobs(ctx context.Context, prefix, namespace string) ([]NomadJobStub, error) {
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

func (c *NomadClient) GetJob(ctx context.Context, jobID, namespace string) (*NomadJob, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out NomadJob
	return &out, c.get(ctx, "/v1/job/"+url.PathEscape(jobID), q, &out)
}

func (c *NomadClient) GetJobAllocs(ctx context.Context, jobID, namespace string, all bool) ([]NomadAllocStub, error) {
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

func (c *NomadClient) GetJobEvals(ctx context.Context, jobID, namespace string) ([]NomadEvaluation, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out []NomadEvaluation
	return out, c.get(ctx, "/v1/job/"+url.PathEscape(jobID)+"/evaluations", q, &out)
}

func (c *NomadClient) ParseHCL(ctx context.Context, hcl string) (json.RawMessage, error) {
	body := map[string]interface{}{
		"JobHCL":       hcl,
		"Canonicalize": true,
	}
	var out json.RawMessage
	return out, c.post(ctx, "/v1/jobs/parse", body, &out)
}

func (c *NomadClient) RegisterJob(ctx context.Context, jobJSON json.RawMessage) (*NomadRegisterResponse, error) {
	body := json.RawMessage(fmt.Sprintf(`{"Job":%s}`, string(jobJSON)))
	var out NomadRegisterResponse
	return &out, c.post(ctx, "/v1/jobs", &body, &out)
}

func (c *NomadClient) PlanJob(ctx context.Context, jobID string, jobJSON json.RawMessage) (*NomadPlanResponse, error) {
	body := json.RawMessage(fmt.Sprintf(`{"Job":%s,"Diff":true}`, string(jobJSON)))
	var out NomadPlanResponse
	return &out, c.post(ctx, "/v1/job/"+url.PathEscape(jobID)+"/plan", &body, &out)
}

func (c *NomadClient) StopJob(ctx context.Context, jobID, namespace string, purge bool) (*NomadDeregisterResponse, error) {
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

func (c *NomadClient) DispatchJob(ctx context.Context, jobID string, meta map[string]string, payload []byte) (*NomadDispatchResponse, error) {
	body := map[string]interface{}{
		"Meta":    meta,
		"Payload": payload,
	}
	var out NomadDispatchResponse
	return &out, c.post(ctx, "/v1/job/"+url.PathEscape(jobID)+"/dispatch", body, &out)
}

func (c *NomadClient) StreamLogs(ctx context.Context, allocID, task, logType, origin string, offset int64, follow bool, w io.Writer) error {
	q := url.Values{
		"task":   {task},
		"type":   {logType},
		"origin": {origin},
		"offset": {fmt.Sprintf("%d", offset)},
		"follow": {fmt.Sprintf("%t", follow)},
	}
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
			continue
		}
		if len(frame.Data) > 0 {
			w.Write(frame.Data) //nolint:errcheck
		}
	}
	return scanner.Err()
}

// ── Variables API methods ─────────────────────────────────────────────────────

// ListVariables returns variable stubs under the given path prefix.
func (c *NomadClient) ListVariables(ctx context.Context, prefix, namespace string) ([]NomadVariableStub, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out []NomadVariableStub
	return out, c.get(ctx, "/v1/vars", q, &out)
}

// GetVariable fetches a variable by path.
func (c *NomadClient) GetVariable(ctx context.Context, path, namespace string) (*NomadVariable, error) {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	var out NomadVariable
	return &out, c.get(ctx, "/v1/var/"+url.PathEscape(path), q, &out)
}

// ListNodes returns node summaries from GET /v1/nodes.
func (c *NomadClient) ListNodes(ctx context.Context) ([]NomadNodeStub, error) {
	var out []NomadNodeStub
	return out, c.get(ctx, "/v1/nodes", nil, &out)
}

// GetNode returns full node detail from GET /v1/node/<id>.
func (c *NomadClient) GetNode(ctx context.Context, nodeID string) (*NomadNode, error) {
	var out NomadNode
	return &out, c.get(ctx, "/v1/node/"+url.PathEscape(nodeID), nil, &out)
}

// GetNodeAllocs returns the allocations currently assigned to a node.
func (c *NomadClient) GetNodeAllocs(ctx context.Context, nodeID string) ([]NomadAllocStub, error) {
	var out []NomadAllocStub
	return out, c.get(ctx, "/v1/node/"+url.PathEscape(nodeID)+"/allocations", nil, &out)
}

// DrainNode enables (enable=true) or disables drain on a node.
// deadlineSecs ≤ 0 means no deadline (drain until empty).
func (c *NomadClient) DrainNode(ctx context.Context, nodeID string, enable bool, deadlineSecs int) error {
	type drainSpec struct {
		Deadline         int64 `json:"Deadline,omitempty"`
		IgnoreSystemJobs bool  `json:"IgnoreSystemJobs"`
	}
	type drainReq struct {
		DrainSpec     drainSpec `json:"DrainSpec"`
		MarkEligible  bool      `json:"MarkEligible"`
	}
	var body interface{}
	if enable {
		spec := drainSpec{IgnoreSystemJobs: false}
		if deadlineSecs > 0 {
			spec.Deadline = int64(deadlineSecs) * int64(time.Second)
		}
		body = drainReq{DrainSpec: spec}
	} else {
		body = drainReq{MarkEligible: true}
	}
	return c.post(ctx, "/v1/node/"+url.PathEscape(nodeID)+"/drain", body, nil)
}

// SetNodeEligibility marks a node as "eligible" or "ineligible" for scheduling.
func (c *NomadClient) SetNodeEligibility(ctx context.Context, nodeID string, eligible bool) error {
	eligStr := "ineligible"
	if eligible {
		eligStr = "eligible"
	}
	body := map[string]string{"Eligibility": eligStr}
	return c.post(ctx, "/v1/node/"+url.PathEscape(nodeID)+"/eligibility", body, nil)
}

// ── Namespace API methods ─────────────────────────────────────────────────────

// ListNamespaces returns all namespaces.
func (c *NomadClient) ListNamespaces(ctx context.Context) ([]NomadNamespace, error) {
	var out []NomadNamespace
	return out, c.get(ctx, "/v1/namespaces", nil, &out)
}

// GetNamespace returns a namespace by name.
func (c *NomadClient) GetNamespace(ctx context.Context, name string) (*NomadNamespace, error) {
	var out NomadNamespace
	return &out, c.get(ctx, "/v1/namespace/"+url.PathEscape(name), nil, &out)
}

// ApplyNamespace creates or updates a namespace.
func (c *NomadClient) ApplyNamespace(ctx context.Context, ns *NomadNamespace) error {
	return c.post(ctx, "/v1/namespace/"+url.PathEscape(ns.Name), ns, nil)
}

// DeleteNamespace removes a namespace by name.
func (c *NomadClient) DeleteNamespace(ctx context.Context, name string) error {
	return c.delete(ctx, "/v1/namespace/"+url.PathEscape(name), nil, nil)
}

// ── Cloud Gateway API methods ─────────────────────────────────────────────────
//
// These methods call cloud gateway endpoints (routed via X-ABC-Cloud: 1 header).
// The gateway maps /v1/cloud/* to the appropriate cloud provider APIs and
// multi-cluster Nomad operations. The paths below are gateway-defined, not
// Nomad's own API.

// CloudListClusters retrieves all clusters in the fleet.
func (c *NomadClient) CloudListClusters(ctx context.Context, out interface{}) error {
	return c.get(ctx, "/v1/cloud/clusters", nil, out)
}

// CloudGetCluster retrieves detail for a named cluster.
func (c *NomadClient) CloudGetCluster(ctx context.Context, name string, out interface{}) error {
	return c.get(ctx, "/v1/cloud/cluster/"+url.PathEscape(name), nil, out)
}

// CloudProvisionCluster requests provisioning of a new cluster.
func (c *NomadClient) CloudProvisionCluster(ctx context.Context, req, out interface{}) error {
	return c.post(ctx, "/v1/cloud/clusters", req, out)
}

// CloudDecommissionCluster initiates decommission of a named cluster.
func (c *NomadClient) CloudDecommissionCluster(ctx context.Context, name string, req interface{}) error {
	return c.post(ctx, "/v1/cloud/cluster/"+url.PathEscape(name)+"/decommission", req, nil)
}

// CloudAddNode provisions a new VM and registers it as a Nomad client node.
func (c *NomadClient) CloudAddNode(ctx context.Context, req, out interface{}) error {
	return c.post(ctx, "/v1/cloud/nodes", req, out)
}

// CloudTerminateNode destroys the VM backing a named Nomad node.
func (c *NomadClient) CloudTerminateNode(ctx context.Context, nodeID string, req interface{}) error {
	return c.post(ctx, "/v1/cloud/node/"+url.PathEscape(nodeID)+"/terminate", req, nil)
}

// CloudListBudgets retrieves budget allocations for all namespaces.
func (c *NomadClient) CloudListBudgets(ctx context.Context, out interface{}) error {
	return c.get(ctx, "/v1/cloud/budgets", nil, out)
}

// CloudGetBudget retrieves the budget for a specific namespace.
func (c *NomadClient) CloudGetBudget(ctx context.Context, namespace string, out interface{}) error {
	return c.get(ctx, "/v1/cloud/budget/"+url.PathEscape(namespace), nil, out)
}

// CloudSetBudget creates or updates the budget cap for a namespace.
func (c *NomadClient) CloudSetBudget(ctx context.Context, namespace string, req interface{}) error {
	return c.put(ctx, "/v1/cloud/budget/"+url.PathEscape(namespace), nil, req, nil)
}

// CloudGetResidencyPolicy fetches the standing data residency policy for a namespace.
func (c *NomadClient) CloudGetResidencyPolicy(ctx context.Context, namespace string, out interface{}) error {
	return c.get(ctx, "/v1/cloud/residency/"+url.PathEscape(namespace), nil, out)
}

// CloudSetResidencyPolicy creates or updates the residency policy for a namespace.
func (c *NomadClient) CloudSetResidencyPolicy(ctx context.Context, namespace string, req interface{}) error {
	return c.put(ctx, "/v1/cloud/residency/"+url.PathEscape(namespace), nil, req, nil)
}

// CloudRotateSecret rotates a cloud IAM credential associated with a Nomad Variable path.
func (c *NomadClient) CloudRotateSecret(ctx context.Context, req interface{}) error {
	return c.post(ctx, "/v1/cloud/secrets/rotate", req, nil)
}

// CloudListNetworkACLs retrieves the Tailscale/VPN ACL rules.
func (c *NomadClient) CloudListNetworkACLs(ctx context.Context, out interface{}) error {
	return c.get(ctx, "/v1/cloud/network/acls", nil, out)
}

// CloudApplyNetworkACL creates or updates a network ACL rule.
func (c *NomadClient) CloudApplyNetworkACL(ctx context.Context, req interface{}) error {
	return c.post(ctx, "/v1/cloud/network/acls", req, nil)
}

// CloudGetServiceHealth fetches health status for all backend services.
func (c *NomadClient) CloudGetServiceHealth(ctx context.Context, out interface{}) error {
	return c.get(ctx, "/v1/cloud/health", nil, out)
}

// CloudGetServiceVersion fetches version info for a named backend service.
func (c *NomadClient) CloudGetServiceVersion(ctx context.Context, service string, out interface{}) error {
	return c.get(ctx, "/v1/cloud/version/"+url.PathEscape(service), nil, out)
}

// PutVariable creates or updates a variable at the given path.
func (c *NomadClient) PutVariable(ctx context.Context, path, namespace string, items map[string]string) error {
	body := map[string]interface{}{
		"Namespace": namespace,
		"Path":      path,
		"Items":     items,
	}
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	return c.put(ctx, "/v1/var/"+url.PathEscape(path), q, body, nil)
}

// DeleteVariable removes a variable by path.
func (c *NomadClient) DeleteVariable(ctx context.Context, path, namespace string) error {
	q := url.Values{}
	if namespace != "" {
		q.Set("namespace", namespace)
	}
	return c.delete(ctx, "/v1/var/"+url.PathEscape(path), q, nil)
}

// WatchJobLogs waits for the first allocation of jobID to start, then streams
// its logs to w. Intended for post-submit log tailing.
func WatchJobLogs(ctx context.Context, nc *NomadClient, jobID, namespace string,
	w io.Writer, delay, timeout time.Duration) error {
	start := time.Now()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if timeout > 0 && time.Since(start) > timeout {
			return fmt.Errorf("watch timeout after %s", timeout)
		}
		allocs, err := nc.GetJobAllocs(ctx, jobID, namespace, false)
		if err != nil {
			return err
		}
		var chosen *NomadAllocStub
		for _, a := range allocs {
			if a.ClientStatus == "running" {
				chosen = &a
				break
			}
			if chosen == nil || a.CreateTime > chosen.CreateTime {
				chosen = &a
			}
		}
		if chosen != nil {
			task := "main"
			for t := range chosen.TaskStates {
				task = t
				break
			}
			follow := chosen.ClientStatus == "running"
			return nc.StreamLogs(ctx, chosen.ID, task, "stdout", "start", 0, follow, w)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-SleepCh(int(delay.Seconds())):
		}
	}
}
