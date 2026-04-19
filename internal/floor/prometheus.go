package floor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PrometheusClient queries a Prometheus instance via the HTTP API.
type PrometheusClient struct {
	baseURL string
	http    *http.Client
}

// NewPrometheusClient creates a client for the Prometheus instance at baseURL.
func NewPrometheusClient(baseURL string) *PrometheusClient {
	return &PrometheusClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// PrometheusMetric holds one label-set and its most recent value.
type PrometheusMetric struct {
	Labels map[string]string
	Value  float64
}

type promInstantResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"` // [<unix_ts>, "<value_str>"]
		} `json:"result"`
	} `json:"data"`
}

// Query executes an instant PromQL query and returns the result vector.
func (c *PrometheusClient) Query(ctx context.Context, promql string) ([]PrometheusMetric, error) {
	q := url.Values{}
	q.Set("query", promql)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/query?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus query %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out promInstantResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("prometheus response parse: %w", err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("prometheus: status=%s", out.Status)
	}

	metrics := make([]PrometheusMetric, 0, len(out.Data.Result))
	for _, r := range out.Data.Result {
		if len(r.Value) < 2 {
			continue
		}
		valStr, _ := r.Value[1].(string)
		val, _ := strconv.ParseFloat(valStr, 64)
		metrics = append(metrics, PrometheusMetric{Labels: r.Metric, Value: val})
	}
	return metrics, nil
}

// QueryScalar executes an instant PromQL query expected to return a single
// scalar value (e.g. a sum/count). Returns the value or an error.
func (c *PrometheusClient) QueryScalar(ctx context.Context, promql string) (float64, error) {
	metrics, err := c.Query(ctx, promql)
	if err != nil {
		return 0, err
	}
	if len(metrics) == 0 {
		return 0, nil
	}
	return metrics[0].Value, nil
}

// Healthy returns true when Prometheus responds to /-/healthy.
func (c *PrometheusClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/-/healthy", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
