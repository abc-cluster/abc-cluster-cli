package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
)

// controllerCapabilitiesPath is the canonical endpoint abc-controller-svc exposes for
// capability aggregation. Per OQ-CAP-2, abc-controller-svc fans out to per-service
// /_/capabilities internal endpoints with sub-30s server-side cache.
const controllerCapabilitiesPath = "/v1/capabilities"

// controllerProbeTimeout caps a single HTTP attempt against abc-controller-svc. Probes are
// supposed to be fast (abc-controller-svc caches sub-30s); 10 s is generous.
const controllerProbeTimeout = 10 * time.Second

// controllerCapabilityResponse is the shape abc-controller-svc returns from /v1/capabilities.
// Mirrors the proposed schema in the brainstorm's "Capability map schema"
// section. All fields optional — abc-controller-svc may omit any when unknown.
type controllerCapabilityResponse struct {
	SchemaVersion int                                            `json:"schema_version"`
	ProbeSource   string                                         `json:"probe_source,omitempty"`
	Services      map[string]controllerCapabilityResponseService `json:"services,omitempty"`
	Warnings      []string                                       `json:"probe_warnings,omitempty"`
}

type controllerCapabilityResponseService struct {
	Codename           string                               `json:"codename,omitempty"`
	Available          bool                                 `json:"available"`
	Version            string                               `json:"version,omitempty"`
	Features           []string                             `json:"features,omitempty"`
	DeprecatedFeatures map[string]controllerDeprecationInfo `json:"deprecated_features,omitempty"`
	Endpoints          map[string]string                    `json:"endpoints,omitempty"`
	Reason             string                               `json:"reason,omitempty"`
	Fallback           string                               `json:"fallback,omitempty"`
}

type controllerDeprecationInfo struct {
	RemovedIn   string    `json:"removed_in,omitempty"`
	Replacement string    `json:"replacement,omitempty"`
	SunsetDate  time.Time `json:"sunset_date,omitempty"`
}

// probeController hits <controllerURL>/v1/capabilities and returns the parsed
// response. The bearer token from the active context is forwarded as
// Authorization: Bearer <token>; the User-Agent identifies the CLI
// version per the convention in the brainstorm.
//
// Per OQ-CAP-4: when controller_url is configured, this is the ONLY probe path.
// Failure does NOT fall back to Nomad introspection (preserves ADR-0019
// trust boundary). Caller surfaces a clear "abc-controller-svc unreachable; cache
// fallback" message.
func probeController(ctx context.Context, controllerURL, accessToken, cliVersion string) (*controllerCapabilityResponse, error) {
	url := strings.TrimRight(controllerURL, "/") + controllerCapabilitiesPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/abc.capability.v1+json")
	req.Header.Set("User-Agent", fmt.Sprintf("abc-cluster-cli/%s", cliVersion))
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	cli := debuglog.NewLoggingClient(&http.Client{Timeout: controllerProbeTimeout})
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("abc-controller-svc probe %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read abc-controller-svc response: %w", err)
	}

	if resp.StatusCode == http.StatusNotAcceptable {
		// Server doesn't support our schema version. Surface the error
		// per the brainstorm's negotiation strategy — operator must
		// upgrade the CLI.
		return nil, fmt.Errorf(
			"abc-controller-svc returned 406 (schema version mismatch); upgrade abc CLI: %s",
			truncate(string(body), 200),
		)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("abc-controller-svc probe returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var out controllerCapabilityResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode abc-controller-svc response: %w", err)
	}
	return &out, nil
}

// applyControllerResponse copies the abc-controller-svc probe payload into the active
// context's Capabilities block. Preserves the abc-nodes-tier shorthand
// booleans (those are populated by Nomad introspection only).
func applyControllerResponse(caps *cfg.Capabilities, resp *controllerCapabilityResponse) {
	caps.SchemaVersion = resp.SchemaVersion
	caps.ProbeSource = resp.ProbeSource
	if caps.ProbeSource == "" {
		caps.ProbeSource = "controller-aggregate"
	}
	caps.ProbeWarnings = append([]string(nil), resp.Warnings...)
	if len(resp.Services) > 0 {
		caps.Services = make(map[string]cfg.ServiceCapability, len(resp.Services))
		for name, svc := range resp.Services {
			deprecated := map[string]cfg.DeprecationInfo{}
			for fname, di := range svc.DeprecatedFeatures {
				deprecated[fname] = cfg.DeprecationInfo{
					RemovedIn:   di.RemovedIn,
					Replacement: di.Replacement,
					SunsetDate:  di.SunsetDate,
				}
			}
			endpoints := map[string]string{}
			for k, v := range svc.Endpoints {
				endpoints[k] = v
			}
			caps.Services[name] = cfg.ServiceCapability{
				Codename:           svc.Codename,
				Available:          svc.Available,
				Version:            svc.Version,
				Features:           append([]string(nil), svc.Features...),
				DeprecatedFeatures: deprecated,
				Endpoints:          endpoints,
				Reason:             svc.Reason,
				Fallback:           svc.Fallback,
			}
		}
	}
	caps.LastSynced = time.Now()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
