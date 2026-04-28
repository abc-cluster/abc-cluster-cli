package module

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const preflightTimeout = 5 * time.Second

// runPreflight executes lightweight reachability/credential checks before
// HCL generation so users see actionable errors instead of cryptic
// downstream failures (Nomad parse/register, prestart task crash).
//
// Fatal failures return an error. Soft warnings are written to stderr and
// do not abort the run.
func runPreflight(ctx context.Context, stderr io.Writer, spec *RunSpec, nomadAddr, nomadToken string) error {
	if err := preflightNomad(ctx, stderr, nomadAddr, nomadToken); err != nil {
		return err
	}
	if spec.PipelineGenURLBase == "" {
		if err := preflightGitHub(ctx, stderr, spec.PipelineGenRepo, spec.GitHubToken); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stderr, "  Preflight  GitHub       skipped (using --pipeline-gen-url-base %s)\n", spec.PipelineGenURLBase)
	}
	preflightOutputCreds(stderr, spec)
	return nil
}

func preflightNomad(ctx context.Context, stderr io.Writer, addr, token string) error {
	if addr == "" {
		return errors.New("preflight: empty Nomad address (set --nomad-addr or ABC_ADDR)")
	}
	c, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(c, http.MethodGet, strings.TrimRight(addr, "/")+"/v1/status/leader", nil)
	if err != nil {
		return fmt.Errorf("preflight nomad: %w", err)
	}
	if token != "" {
		req.Header.Set("X-Nomad-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("preflight: cannot reach Nomad at %s: %w (is the agent running?)", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("preflight: Nomad rejected token (HTTP %d) — check --nomad-token / ABC_TOKEN", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("preflight: Nomad returned HTTP %d at %s", resp.StatusCode, addr)
	}
	fmt.Fprintf(stderr, "  Preflight  Nomad        OK (%s)\n", addr)
	return nil
}

func preflightGitHub(ctx context.Context, stderr io.Writer, repo, token string) error {
	if repo == "" || token == "" {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()

	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(c, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("preflight github: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "  Preflight  GitHub       skipped (network: %v)\n", err)
		return nil
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("preflight: GitHub token rejected (401) — token is invalid or expired")
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("preflight: GitHub token forbidden (403) — token lacks scope or rate-limited")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("preflight: pipeline-gen repo %q not found (check --pipeline-gen-repo or token access)", repo)
	case resp.StatusCode >= 400:
		return fmt.Errorf("preflight: GitHub returned HTTP %d for %s", resp.StatusCode, url)
	}
	fmt.Fprintf(stderr, "  Preflight  GitHub       OK (%s)\n", repo)
	return nil
}

// preflightOutputCreds emits a non-fatal warning when an S3 output prefix is
// configured but no credentials are detectable from the local environment.
// The prestart task may still succeed if credentials are mounted via Nomad
// templates or instance metadata, so this is informational only.
func preflightOutputCreds(stderr io.Writer, spec *RunSpec) {
	if !strings.HasPrefix(spec.OutputPrefix, "s3://") {
		return
	}
	if spec.S3Endpoint != "" {
		return
	}
	hasAWS := os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != "" || os.Getenv("AWS_ROLE_ARN") != ""
	if hasAWS {
		return
	}
	fmt.Fprintf(stderr,
		"  Preflight  Output       warn: --output-prefix is %s but no S3 credentials detected locally\n"+
			"             (the prestart task will rely on cluster-side credentials; set AWS_* or use --s3-endpoint if needed)\n",
		spec.OutputPrefix,
	)
}
