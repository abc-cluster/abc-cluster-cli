package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/appgen"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

const (
	healthPollInterval   = 5 * time.Second
	defaultHealthTimeout = 180 * time.Second // heavy images (JVM, large layers) need >1m to first-respond
)

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy an app from abc-app.yaml",
		Long: `Deploy a scientific web app described by abc-app.yaml.

Reads ./abc-app.yaml by default. The deploy is strictly ordered:
  1. validate the spec + confirm every data bucket exists
  2. provision the per-app MinIO service account scoped to those buckets
  3. submit the Nomad service job
  4. poll the Nomad-native health check until it reports success

If submission fails after step 2, the service account is revoked so nothing
orphans. If the health check times out, the job is left running for diagnosis
(abc app logs <name>) and deploy exits non-zero.`,
		RunE: runDeploy,
	}
	cmd.Flags().StringP("file", "f", appgen.DefaultSpecFile, "Path to the app descriptor")
	cmd.Flags().String("image", "", "Override the image in abc-app.yaml without editing the file")
	cmd.Flags().String("exposure", "", "Network reach: internal|public|both (overrides abc-app.yaml; default public)")
	cmd.Flags().Bool("dry-run", false, "Print the templated Nomad HCL and exit; submit nothing")
	cmd.Flags().Bool("no-wait", false, "Return after submission without polling health")
	cmd.Flags().String("node-pool", "", "Nomad node pool to place the app in (overrides the context's admin.services.nomad.head_pool)")
	cmd.Flags().Duration("health-timeout", 0, "How long to wait for the app to become healthy, e.g. 3m or 90s (overrides abc-app.yaml health_timeout; default 3m)")
	return cmd
}

func runDeploy(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()

	file, _ := cmd.Flags().GetString("file")
	imageOverride, _ := cmd.Flags().GetString("image")
	exposureOverride, _ := cmd.Flags().GetString("exposure")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	noWait, _ := cmd.Flags().GetBool("no-wait")

	// ── Parse + validate (step 1, format-only) ──────────────────────────────
	spec, err := appgen.Load(file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(imageOverride) != "" {
		spec.Image = strings.TrimSpace(imageOverride)
	}
	if strings.TrimSpace(exposureOverride) != "" {
		spec.Exposure = strings.TrimSpace(exposureOverride)
	}
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("invalid %s: %w", file, err)
	}
	spec.ApplyDefaults()

	// Resolve platform params (namespace, datacenters, node pool, MinIO endpoint).
	cfg, _ := config.Load()
	var activeCtx config.Context
	if cfg != nil {
		activeCtx = cfg.ActiveCtx()
	}
	params := appgen.JobParams{
		Namespace:   appNamespace(cmd),
		Datacenters: activeCtx.NomadDatacenters(),
		NodePool:    activeCtx.NomadHeadPool(),
	}
	// --node-pool overrides the context's head_pool (parallels --namespace).
	if np, _ := cmd.Flags().GetString("node-pool"); strings.TrimSpace(np) != "" {
		params.NodePool = strings.TrimSpace(np)
	}

	// MinIO endpoint + creds for bucket checks + SA provisioning. Only required
	// when the app declares data buckets; a no-data app skips MinIO entirely.
	var (
		provisioner *appgen.DataProvisioner
		minioCreds  *appgen.MinIOCreds
	)
	if len(spec.Data) > 0 {
		minioCreds, err = appgen.ResolveMinIOCreds(activeCtx)
		if err != nil {
			return err
		}
		params.MinIOEndpoint = minioCreds.Endpoint
		provisioner, err = appgen.NewDataProvisioner(minioCreds)
		if err != nil {
			return err
		}
		// Bucket existence — fail before any side effect.
		if err := provisioner.CheckBuckets(ctx, spec); err != nil {
			return err
		}
	}

	// ── dry-run: render HCL and stop (no side effects) ──────────────────────
	if dryRun {
		fmt.Fprint(out, appgen.Generate(spec, params))
		return nil
	}

	// ── Provision data SA (step 2) ──────────────────────────────────────────
	var appCreds *appgen.AppCredentials = &appgen.AppCredentials{}
	if provisioner != nil {
		appCreds, err = provisioner.Provision(ctx, spec)
		if err != nil {
			return fmt.Errorf("provision data access: %w", err)
		}
	}
	params.AWSAccessKey = appCreds.AccessKey
	params.AWSSecretKey = appCreds.SecretKey

	// ── Submit the Nomad job (step 3) ───────────────────────────────────────
	nc := nomadClientFromCmd(cmd)
	hcl := appgen.Generate(spec, params)
	jobJSON, err := nc.ParseHCL(ctx, hcl)
	if err != nil {
		rollback(ctx, provisioner, spec)
		return fmt.Errorf("translate job HCL: %w", err)
	}
	if _, err := nc.RegisterJob(ctx, jobJSON); err != nil {
		// Step-3 failure after step-2 provisioning: rollback the SA so nothing
		// orphans, then surface the Nomad error verbatim.
		rollback(ctx, provisioner, spec)
		return fmt.Errorf("submit app job: %w", err)
	}

	fmt.Fprintf(out, "Submitted %s\n", spec.JobName())
	fmt.Fprintf(out, "  URL: %s\n", spec.URL())
	if spec.NormExposure() != appgen.ExposurePublic {
		// Internal apps are off the public edge — reachable only via Tailscale +
		// campus LAN, and the apps.internal host resolves only where the operator
		// has wired internal DNS. Surface that so the user isn't left guessing.
		fmt.Fprintf(out, "  exposure: %s — institution-only (not on the public edge).\n", spec.NormExposure())
		fmt.Fprintf(out, "    Internal host: %s (needs internal DNS: Tailscale MagicDNS / campus resolver).\n", spec.InternalHost())
		fmt.Fprintln(out, "    See abc-deployments .../docs/internal-app-exposure.md for resolution + Tailscale Serve.")
	}

	if noWait {
		fmt.Fprintln(out, "  (--no-wait: not polling health)")
		return nil
	}

	// ── Poll Nomad-native health (step 4) ───────────────────────────────────
	healthTO := resolveHealthTimeout(cmd, spec)
	if err := waitHealthy(ctx, out, nc, spec.JobName(), spec.Health, healthTO); err != nil {
		// Health timeout: leave the job in place for diagnosis.
		return healthTimeoutErr(spec, healthTO)
	}
	fmt.Fprintf(out, "  Healthy. %s\n", spec.URL())
	return nil
}

// rollback revokes the per-app MinIO service account. Called when Nomad
// submission fails after the SA was provisioned. Best-effort: a rollback error
// is reported but does not mask the original failure.
func rollback(ctx context.Context, p *appgen.DataProvisioner, spec *appgen.Spec) {
	if p == nil {
		return
	}
	if err := p.Revoke(ctx, spec); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: rollback of MinIO service account failed: %v\n", err)
	}
}

// errHealthTimeout sentinels a health-poll timeout so callers can wrap it with
// their own (deploy vs restart) context-specific guidance.
var errHealthTimeout = fmt.Errorf("health check did not reach success before timeout")

// resolveHealthTimeout picks the health-poll budget, most-specific first:
// --health-timeout flag > abc-app.yaml `health_timeout` > defaultHealthTimeout.
func resolveHealthTimeout(cmd *cobra.Command, spec *appgen.Spec) time.Duration {
	if f, _ := cmd.Flags().GetDuration("health-timeout"); f > 0 {
		return f
	}
	if spec.HealthTimeout != "" {
		if d, err := time.ParseDuration(spec.HealthTimeout); err == nil && d > 0 {
			return d
		}
	}
	return defaultHealthTimeout
}

// waitHealthy polls a job's alloc checks (Nomad-native, Consul-free) every
// healthPollInterval until a check reports "success", or defaultHealthTimeout elapses.
// It uses /v1/client/allocation/<id>/checks, not the Consul health endpoint.
// Shared by `deploy` and `restart`; callers map errHealthTimeout to their own
// guidance message.
func waitHealthy(ctx context.Context, out io.Writer, nc *utils.NomadClient, jobName, health string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	fmt.Fprintf(out, "  Waiting for health check (%s, timeout %s)...\n", health, timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Find a running alloc for this job.
		allocs, err := nc.GetJobAllocs(ctx, jobName, nc.DefaultNamespace(), false)
		if err == nil {
			for _, a := range allocs {
				if a.ClientStatus != "running" {
					continue
				}
				checks, cerr := nc.GetAllocChecks(ctx, a.ID)
				if cerr != nil {
					continue
				}
				for _, c := range checks {
					if strings.EqualFold(c.Status, "success") {
						return nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return errHealthTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPollInterval):
		}
	}
}

// healthTimeoutErr returns the health-check-timeout error with the bind-contract
// hint (the most common standalone-Shiny / framework failure mode) and a pointer
// at --health-timeout for slow-booting (JVM/large) images.
func healthTimeoutErr(spec *appgen.Spec, timeout time.Duration) error {
	return fmt.Errorf(
		"app %q did not become healthy within %s\n"+
			"  health check: %s%s (expected the container to respond on 0.0.0.0:%d)\n"+
			"  • confirm the container binds 0.0.0.0:%d, not localhost/127.0.0.1\n"+
			"  • slow image (JVM/large)? raise the budget: --health-timeout 5m (or health_timeout in abc-app.yaml)\n"+
			"  • inspect logs: abc app logs %s\n"+
			"  the job was left running for diagnosis (not auto-rolled-back)",
		spec.Name, timeout, spec.URL(), spec.Health, spec.Port, spec.Port, spec.Name)
}
