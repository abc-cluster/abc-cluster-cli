package floor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// ServiceHealth is the result of a single floor service health probe.
type ServiceHealth struct {
	Name    string
	URL     string
	Healthy bool
	Detail  string // e.g. "sealed" for Vault, version string, etc.
}

// probeHTTP performs a GET to url and returns (ok, detail).
func probeHTTP(ctx context.Context, rawURL string) (bool, string) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "unreachable"
	}
	defer resp.Body.Close()
	return resp.StatusCode < 400, ""
}

// ProbeNomad checks Nomad /v1/agent/self and returns version.
func ProbeNomad(ctx context.Context, addr, token string) ServiceHealth {
	client := &http.Client{Timeout: 5 * time.Second}
	url := strings.TrimRight(addr, "/") + "/v1/agent/self"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ServiceHealth{Name: "nomad", URL: addr, Detail: "unreachable"}
	}
	if token != "" {
		req.Header.Set("X-Nomad-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ServiceHealth{Name: "nomad", URL: addr, Detail: "unreachable"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	healthy := resp.StatusCode == http.StatusOK

	var info struct {
		Config struct {
			Version struct {
				Version string `json:"Version"`
			} `json:"Version"`
		} `json:"config"`
	}
	detail := ""
	if json.Unmarshal(body, &info) == nil {
		detail = info.Config.Version.Version
	}
	return ServiceHealth{Name: "nomad", URL: addr, Healthy: healthy, Detail: detail}
}

// ProbeVault checks Vault /v1/sys/health and returns sealed/active state.
func ProbeVault(ctx context.Context, baseURL string) ServiceHealth {
	h := ServiceHealth{Name: "vault", URL: baseURL}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/v1/sys/health", nil)
	if err != nil {
		h.Detail = "unreachable"
		return h
	}
	resp, err := client.Do(req)
	if err != nil {
		h.Detail = "unreachable"
		return h
	}
	defer resp.Body.Close()
	// Vault uses non-200 for sealed/standby but that's still "reachable".
	switch resp.StatusCode {
	case 200:
		h.Healthy = true
		h.Detail = "active"
	case 429:
		h.Healthy = true
		h.Detail = "standby"
	case 473:
		h.Healthy = true
		h.Detail = "performance standby"
	case 503:
		h.Healthy = false
		h.Detail = "sealed"
	case 501:
		h.Healthy = false
		h.Detail = "not initialized"
	default:
		h.Detail = "unknown"
	}
	return h
}

// ProbeTraefik checks Traefik via /ping.
func ProbeTraefik(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/ping")
	return ServiceHealth{Name: "traefik", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeGrafana checks Grafana /api/health.
func ProbeGrafana(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/api/health")
	return ServiceHealth{Name: "grafana", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbePrometheus checks Prometheus /-/healthy.
func ProbePrometheus(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/-/healthy")
	return ServiceHealth{Name: "prometheus", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeLoki checks Loki /ready.
func ProbeLoki(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/ready")
	return ServiceHealth{Name: "loki", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeNtfy checks ntfy /v1/health.
func ProbeNtfy(ctx context.Context, baseURL string) ServiceHealth {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/v1/health", nil)
	if err != nil {
		return ServiceHealth{Name: "ntfy", URL: baseURL, Detail: "unreachable"}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ServiceHealth{Name: "ntfy", URL: baseURL, Detail: "unreachable"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var h struct {
		Healthy bool `json:"healthy"`
	}
	_ = json.Unmarshal(body, &h)
	return ServiceHealth{Name: "ntfy", URL: baseURL, Healthy: h.Healthy}
}

// ProbeMinIO checks MinIO /minio/health/live.
func ProbeMinIO(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/minio/health/live")
	return ServiceHealth{Name: "minio", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeRustFS checks RustFS /minio/health/live (RustFS mirrors MinIO endpoints).
func ProbeRustFS(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/minio/health/live")
	return ServiceHealth{Name: "rustfs", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeTusd checks tusd / for a 200 or known response.
func ProbeTusd(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/"))
	return ServiceHealth{Name: "tusd", URL: baseURL, Healthy: ok, Detail: detail}
}

// ProbeAlloy checks Grafana Alloy's HTTP UI endpoint /-/healthy.
func ProbeAlloy(ctx context.Context, baseURL string) ServiceHealth {
	ok, detail := probeHTTP(ctx, strings.TrimRight(baseURL, "/")+"/-/healthy")
	return ServiceHealth{Name: "alloy", URL: baseURL, Healthy: ok, Detail: detail}
}
