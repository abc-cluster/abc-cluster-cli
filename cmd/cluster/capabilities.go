package cluster

import (
	"context"
	"fmt"
	"net/http"

	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/debuglog"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// grafanaFloorDashboardPath is the URL path for the dashboard provisioned in
// deployments/abc-nodes/nomad/grafana.nomad.hcl (JSON uid: abc-nodes-nomad-loki-logs).
const grafanaFloorDashboardPath = "/d/abc-nodes-nomad-loki-logs/nomad-allocation-logs"

// nomadClientForCapabilities builds a NomadClient for direct abc-nodes access.
//
// Unlike nomadClientFromCmd, it does NOT fall back to ABC_ADDR / NOMAD_ADDR env
// vars (which typically point to the cloud gateway). It uses only:
//  1. explicitly-passed --nomad-addr / --nomad-token flags (Changed() == true)
//  2. active context admin.services.nomad.* in config
//
// This prevents cloud-gateway tokens from being sent to the wrong host, which
// is the most common cause of 403 on capabilities sync.
func nomadClientForCapabilities(cmd *cobra.Command) (*utils.NomadClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	var addr, token, region string

	// Only use flag values when the user explicitly passed them.
	if cmd.Flags().Changed("nomad-addr") {
		addr, _ = cmd.Flags().GetString("nomad-addr")
	}
	if cmd.Flags().Changed("nomad-token") {
		token, _ = cmd.Flags().GetString("nomad-token")
	}
	if cmd.Flags().Changed("region") {
		region, _ = cmd.Flags().GetString("region")
	}

	// Fall back to context config (not env vars).
	ctx := cfg.ActiveCtx()
	if addr == "" {
		addr = ctx.NomadAddr()
	}
	if token == "" {
		token = ctx.NomadToken()
	}
	if region == "" {
		region = ctx.NomadRegion()
	}

	if addr == "" {
		return nil, fmt.Errorf(
			"no Nomad address for context %q\n"+
				"  Set it with: abc config set admin.services.nomad.addr http://<ip>:4646\n"+
				"  Or pass:     --nomad-addr http://<ip>:4646",
			cfg.ActiveContext,
		)
	}

	return utils.NewNomadClient(addr, token, region), nil
}

func newCapabilitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Inspect cluster capabilities",
		Long: `Commands for inspecting and syncing what services are available on an abc-nodes cluster.

  abc cluster capabilities sync   # Query Nomad and update config
  abc cluster capabilities show   # Print stored capabilities for the active context`,
	}
	cmd.AddCommand(newCapabilitiesSyncCmd(), newCapabilitiesShowCmd())
	return cmd
}

func newCapabilitiesSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync capabilities from abc-controller-svc or Nomad (cascade per active context)",
		Long: `Probes the cluster's capability surface and updates the active context's
capabilities block in config.yaml.

Cascade (per OQ-CAP-4 of the capability brainstorm):

  1. controller_url set in context  → probe abc-controller-svc /v1/capabilities (preferred);
                                 NO fallback to Nomad on failure (preserves
                                 ADR-0019 trust boundary).
  2. controller_url empty           → probe Nomad services API (with job-listing
                                 fallback on 403). This is the pre-controller /
                                 seedling / grove path used today.

Use --source=controller|nomad to force a specific cascade entry point (debug or
testing). Use --source=tier-default to skip the probe and seed from
cluster_type only (useful when both abc-controller-svc and Nomad are unreachable).

Endpoint URLs are populated from service instances when available, or from
allocation port assignments otherwise. Only populates endpoint fields that
are not already set by the operator.`,
		RunE: runCapabilitiesSync,
	}
	cmd.Flags().String("source", "", "force probe source (controller | nomad | tier-default); default = cascade based on context config")
	return cmd
}

func newCapabilitiesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show stored capabilities for the active context",
		RunE:  runCapabilitiesShow,
	}
}

// svcCapMapping maps an abc-nodes service name to an admin.services key + field.
type svcCapMapping struct {
	svcName  string // Nomad service name (abc-nodes-*)
	adminSvc string // key in AdminServices
	field    string // http | endpoint
}

var abcNodesSvcMappings = []svcCapMapping{
	{"abc-nodes-traefik", "traefik", "http"},
	{"abc-nodes-grafana", "grafana", "http"},
	{"abc-nodes-alloy", "grafana_alloy", "http"},
	{"abc-nodes-prometheus", "prometheus", "http"},
	{"abc-nodes-loki", "loki", "http"},
	{"abc-nodes-ntfy", "ntfy", "http"},
	{"abc-nodes-tusd", "tusd", "http"},
	{"abc-nodes-faasd", "faasd", "http"},
	{"abc-nodes-uppy", "uppy", "http"},
	{"abc-nodes-vault", "vault", "http"},
	{"abc-nodes-minio-s3", "minio", "endpoint"},
	{"abc-nodes-rustfs-s3", "rustfs", "endpoint"},
}

// jobToServices maps the Nomad job ID to the service names it registers.
// Used as fallback when the services API is not accessible.
var jobToServices = map[string][]string{
	"abc-nodes-minio":      {"abc-nodes-minio-s3", "abc-nodes-minio-console"},
	"abc-nodes-rustfs":     {"abc-nodes-rustfs-s3"},
	"abc-nodes-traefik":    {"abc-nodes-traefik"},
	"abc-nodes-grafana":    {"abc-nodes-grafana"},
	"abc-nodes-alloy":      {"abc-nodes-alloy"},
	"abc-nodes-prometheus": {"abc-nodes-prometheus"},
	"abc-nodes-loki":       {"abc-nodes-loki"},
	"abc-nodes-ntfy":       {"abc-nodes-ntfy"},
	"abc-nodes-tusd":       {"abc-nodes-tusd"},
	"abc-nodes-faasd":      {"abc-nodes-faasd"},
	"abc-nodes-uppy":       {"abc-nodes-uppy"},
	"abc-nodes-vault":      {"abc-nodes-vault"},
}

func runCapabilitiesSync(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// Resolve the canonical context name — cfg.ActiveContext may be an alias.
	// Writing back to an alias key creates a Contexts/ContextAliases collision.
	ctxName := cfg.ResolveContextName(cfg.ActiveContext)
	if ctxName == "" {
		return fmt.Errorf("cannot resolve active context %q", cfg.ActiveContext)
	}
	ctx := cfg.Contexts[ctxName]
	bg := context.Background()
	source, _ := cmd.Flags().GetString("source")

	// ── Resolve probe source (the discovery cascade) ─────────────────────────
	// Per OQ-CAP-4: when controller_url is configured, abc-controller-svc is the ONLY probe path
	// (no fallback to Nomad on failure — preserves ADR-0019). --source= forces
	// a specific entry point for debug / testing.

	probeVia := source
	if probeVia == "" {
		if strings.TrimSpace(ctx.ControllerURL) != "" {
			probeVia = "controller"
		} else {
			probeVia = "nomad"
		}
	}

	switch probeVia {
	case "controller":
		return runCapabilitiesSyncController(cmd, cfg, ctxName, ctx)
	case "nomad":
		// Continue to the Nomad path below (existing behaviour).
	case "tier-default":
		return runCapabilitiesSyncTierDefault(cmd, cfg, ctxName, ctx)
	default:
		return fmt.Errorf("--source must be one of: controller, nomad, tier-default (got %q)", source)
	}

	nc, err := nomadClientForCapabilities(cmd)
	if err != nil {
		return err
	}

	// ── Step 1: Build service set ─────────────────────────────────────────────

	// Pass the active context's namespace so pool/member tokens (which
	// don't have cluster-wide service:read) succeed against their own
	// namespace. Empty namespace = cluster-wide query, which only mgmt
	// + multi-group-admin tokens can satisfy.
	svcSet, via, err := buildServiceSet(bg, nc, ctx.NomadNamespace())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Detected services via %s.\n", via)

	// ── Step 2: Map services to capabilities ──────────────────────────────────

	caps := &config.Capabilities{
		LastSynced:  time.Now(),
		ProbeSource: "nomad-introspection",
	}

	switch {
	case svcSet["abc-nodes-minio-s3"]:
		caps.Storage = "minio"
	case svcSet["abc-nodes-rustfs-s3"]:
		caps.Storage = "rustfs"
	}
	caps.Uploads = svcSet["abc-nodes-tusd"]
	caps.UploadUI = svcSet["abc-nodes-uppy"]
	caps.Logging = svcSet["abc-nodes-loki"]
	caps.Monitoring = svcSet["abc-nodes-prometheus"]
	caps.Observability = svcSet["abc-nodes-alloy"]
	caps.Notifications = svcSet["abc-nodes-ntfy"]
	caps.Proxy = svcSet["abc-nodes-traefik"]

	if svcSet["abc-nodes-vault"] {
		caps.Secrets = detectVaultSecretsMode(cmd.Context(), nc, &ctx)
	} else {
		caps.Secrets = "nomad"
	}

	// ── Step 3: Sync endpoints (never overwrite existing) ────────────────────

	for _, m := range abcNodesSvcMappings {
		if !svcSet[m.svcName] {
			continue
		}
		existing, ok := config.GetAdminFloorField(&ctx.Admin.Services, m.adminSvc, m.field)
		if ok && existing != "" {
			// Non-empty values are treated as operator-set and not overwritten.
			// EXCEPTION: a MinIO S3 endpoint pointing at the console port is a
			// common misconfiguration (e.g. abc-bootstrap) that makes `abc data`
			// fail with "must be made to API port" (B5). Don't clobber it, but if
			// a fresh resolve disagrees, warn with the exact command to fix it.
			if m.adminSvc == "minio" && m.field == "endpoint" {
				if fresh := resolveServiceEndpoint(bg, nc, m.svcName, ""); fresh != "" && !sameHostPort(fresh, existing) {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"  ⚠ admin.services.minio.endpoint is %s, but the running MinIO S3 API is at %s.\n"+
							"    If `abc data` fails with \"must be made to API port\", update it:\n"+
							"      abc config set admin.services.minio.endpoint %s\n",
						existing, fresh, fresh)
				}
			}
			continue
		}
		if url := resolveServiceEndpoint(bg, nc, m.svcName, ""); url != "" {
			_ = config.SetAdminFloorField(&ctx.Admin.Services, m.adminSvc, m.field, url)
		}
	}

	// ── Step 4: Populate Grafana dashboard URL if not already set ────────────
	if caps.Monitoring {
		grafanaHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "grafana", "http")
		if ok && grafanaHTTP != "" {
			dashboardURL := strings.TrimRight(grafanaHTTP, "/") + grafanaFloorDashboardPath
			_ = config.SetAdminFloorField(&ctx.Admin.Services, "grafana", "dashboard", dashboardURL)
		}
	}

	// ── Step 5: Sync node driver capabilities ─────────────────────────────────
	nodes, nodeErr := syncNodeCapabilities(bg, nc)
	switch {
	case nodeErr != nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: could not sync node capabilities: %v\n", nodeErr)
	case nodes == nil:
		// 403 / no node:read — preserve server-stamped data from config.yaml.
		// caps.Nodes intentionally left as-is (carries the stamped value).
		existing := caps.Nodes
		if len(existing) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Node capabilities not refreshed (no node:read permission) — using %d server-stamped node(s).\n", len(existing))
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Node capabilities not refreshed (no node:read permission) — run 'abc cluster capabilities sync' as admin to populate.\n")
		}
	default:
		caps.Nodes = nodes
		fmt.Fprintf(cmd.OutOrStdout(), "Synced driver capabilities for %d node(s).\n", len(nodes))
	}

	ctx.Capabilities = caps
	if label, err := utils.NomadTokenWhoamiLabel(bg, nc); err == nil && label != "" {
		ctx.SetAuthWhoami(label)
	}
	cfg.Contexts[ctxName] = ctx

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Capabilities synced for context %q:\n", ctxName)
	printCapabilities(cmd, caps)

	// Emit vault sealed warning after sync so it's visible without --verbose.
	if caps.Secrets == "vault+sealed" {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\n  Warning: Vault is running but SEALED — secrets backend unavailable.\n"+
				"  Run: abc admin services vault cli -- operator unseal\n\n")
	}
	return nil
}

// runCapabilitiesSyncController runs the abc-controller-svc probe path (cascade step 1).
// Per OQ-CAP-4: NO fallback to Nomad on abc-controller-svc failure — the trust
// boundary stays preserved even during abc-controller-svc outages. Operators who
// need the CLI to keep working through an abc-controller-svc outage can rerun sync
// with `--source=nomad` (debug) or set `ABC_NODE_NO_PROBE=1` and rely on
// the cached capabilities block + tier-default fallback.
func runCapabilitiesSyncController(cmd *cobra.Command, cfg *config.Config, ctxName string, ctx config.Context) error {
	bg := context.Background()
	controllerURL := strings.TrimSpace(ctx.ControllerURL)
	if controllerURL == "" {
		return fmt.Errorf("--source=controller was forced but controller_url is not set in context %q", ctxName)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Probing abc-controller-svc at %s ...\n", controllerURL)
	resp, err := probeController(bg, controllerURL, ctx.AccessToken, state.CLIVersion)
	if err != nil {
		return fmt.Errorf(
			"abc-controller-svc probe failed: %w\n  ADR-0019 forbids silent fallback to Nomad when controller_url is configured.\n"+
				"  Use 'abc cluster capabilities sync --source=nomad' to bypass this guard for debugging,\n"+
				"  or set ABC_NODE_NO_PROBE=1 and rely on the cached capabilities block.",
			err,
		)
	}

	caps := ctx.Capabilities
	if caps == nil {
		caps = &config.Capabilities{}
	}
	applyControllerResponse(caps, resp)

	ctx.Capabilities = caps
	cfg.Contexts[ctxName] = ctx
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Capabilities synced for context %q via controller-aggregate (%d services, schema v%d):\n",
		ctxName, len(caps.Services), caps.SchemaVersion)
	printCapabilities(cmd, caps)
	return nil
}

// runCapabilitiesSyncTierDefault populates the capabilities block from
// hardcoded tier-default assumptions keyed off ctx.ClusterType. Used as
// the cascade-step-4 fallback when both abc-controller-svc and Nomad are unreachable
// (or via --source=tier-default for testing).
//
// Note: writes only the new Services map (per the brainstorm); the
// abc-nodes shorthand booleans are NOT populated by tier-default —
// those reflect actual Nomad-detected services and stay zero-valued
// when no probe runs.
func runCapabilitiesSyncTierDefault(cmd *cobra.Command, cfg *config.Config, ctxName string, ctx config.Context) error {
	if ctx.ClusterType == "" {
		return fmt.Errorf(
			"--source=tier-default needs cluster_type set in context %q\n"+
				"  set with: abc config set contexts.%s.cluster_type abc-grove",
			ctxName, ctxName,
		)
	}

	caps := ctx.Capabilities
	if caps == nil {
		caps = &config.Capabilities{}
	}
	caps.SchemaVersion = 1
	caps.ProbeSource = "tier-default"
	caps.Services = tierDefaultServices(ctx.ClusterType)
	caps.LastSynced = time.Now()
	caps.ProbeWarnings = []string{
		fmt.Sprintf("seeded from cluster_type=%q (tier-default); no live probe was performed", ctx.ClusterType),
	}

	ctx.Capabilities = caps
	cfg.Contexts[ctxName] = ctx
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"Capabilities seeded for context %q via tier-default (cluster_type=%s, %d services):\n",
		ctxName, ctx.ClusterType, len(caps.Services))
	printCapabilities(cmd, caps)
	return nil
}

// tierDefaultServices returns the seeded Services map for a tier. Mirrors
// the tier-appearance table in design/exploring/service-naming-map.md.
// Keep in lockstep with internal/capability/mock.go's tierDefault().
func tierDefaultServices(clusterType string) map[string]config.ServiceCapability {
	out := map[string]config.ServiceCapability{
		"local-state": {Available: true, Version: "0001_initial"},
	}
	add := func(tech, codename string) {
		out[tech] = config.ServiceCapability{Available: true, Codename: codename}
	}
	switch clusterType {
	case "abc-nodes":
		// seedling — local-state only
	case "abc-grove":
		add("abc-bitemporal-svc", "Chiranjivi")
		add("abc-policy-svc", "Jurist")
	case "abc-grove-tended":
		add("abc-bitemporal-svc", "Chiranjivi")
		add("abc-policy-svc", "Jurist")
		add("abc-controller-svc", "")
		add("abc-accounting-svc", "Kayastha")
		add("abc-fleet-svc", "Veld")
		add("abc-telemetry-svc", "Voron")
		add("abc-chat-svc", "Mimir")
	case "abc-cloud":
		add("abc-bitemporal-svc", "Chiranjivi")
		add("abc-policy-svc", "Jurist")
		add("abc-controller-svc", "")
		add("abc-accounting-svc", "Kayastha")
		add("abc-fleet-svc", "Veld")
		add("abc-telemetry-svc", "Voron")
		add("abc-chat-svc", "Mimir")
		add("abc-client-web", "Khatoon")
		add("abc-marketplace-svc", "Bazaar")
		add("abc-billing-bridge", "Hisaab")
		add("abc-signup-svc", "")
	}
	return out
}

// buildServiceSet returns a set of running abc-nodes service names.
// It tries the Nomad services API first; on 403 it falls back to job listing.
//
// `namespace` is the active context's Nomad namespace; passing it
// scopes the services query so pool/member tokens (which lack
// cluster-wide service:read) succeed against their own namespace.
// Empty string runs the cluster-wide query (mgmt / multi-group-admin
// tokens). Bug history: pre-2026-05-27 the cluster-wide call always
// 403'd for pool tokens, which made `abc cluster capabilities sync`
// unusable from any researcher account.
func buildServiceSet(ctx context.Context, nc *utils.NomadClient, namespace string) (map[string]bool, string, error) {
	services, err := nc.ListServices(ctx, namespace)
	if err == nil {
		set := make(map[string]bool, len(services))
		for _, s := range services {
			set[s.ServiceName] = true
		}
		via := "Nomad service registry"
		if namespace != "" {
			via += " (namespace " + namespace + ")"
		}
		return set, via, nil
	}

	if !isPermissionDenied(err) {
		return nil, "", fmt.Errorf("list services from Nomad: %w", err)
	}

	// 403: fall back to listing jobs with prefix abc-nodes-, scoped to
	// the same namespace so the pool/member-token path still works.
	jobs, jobErr := nc.ListJobs(ctx, "abc-nodes-", namespace)
	if jobErr != nil {
		return nil, "", fmt.Errorf(
			"list services from Nomad: 403 Forbidden (token needs namespace:read-job); "+
				"job listing fallback also failed: %w", jobErr)
	}

	set := make(map[string]bool)
	for _, j := range jobs {
		if j.Status != "running" {
			continue
		}
		for _, svc := range jobToServices[j.ID] {
			set[svc] = true
		}
	}
	return set, "Nomad job listing (services API returned 403)", nil
}

// resolveServiceEndpoint returns "http://ip:port" for a named abc-nodes service.
// It tries the service instances API first; on failure it queries job allocations.
func resolveServiceEndpoint(ctx context.Context, nc *utils.NomadClient, svcName, namespace string) string {
	instances, err := nc.GetServiceInstances(ctx, svcName, namespace)
	if err == nil && len(instances) > 0 {
		inst := instances[0]
		if inst.Address != "" && inst.Port != 0 {
			return fmt.Sprintf("http://%s:%d", inst.Address, inst.Port)
		}
	}

	// Fallback: infer job ID from service name and read alloc ports.
	jobID := serviceNameToJobID(svcName)
	if jobID == "" {
		return ""
	}
	return endpointFromJobAlloc(ctx, nc, jobID, namespace)
}

// serviceNameToJobID maps a service name to its parent job ID.
func serviceNameToJobID(svcName string) string {
	for jobID, svcs := range jobToServices {
		for _, s := range svcs {
			if s == svcName {
				return jobID
			}
		}
	}
	return ""
}

// preferredServicePort selects the port most likely to be a service's API
// endpoint: it de-prioritises console/ui/dashboard/admin ports (never the port
// we want for programmatic access) and prefers an api/s3-labelled port, falling
// back to the first non-zero port. Returns nil when no usable port exists.
func preferredServicePort(ports []utils.NomadDynamicPort) *utils.NomadDynamicPort {
	var best *utils.NomadDynamicPort
	bestRank := -1
	for i := range ports {
		if ports[i].Value == 0 {
			continue
		}
		label := strings.ToLower(ports[i].Label)
		rank := 1 // neutral
		switch {
		case strings.Contains(label, "console"), strings.Contains(label, "ui"),
			strings.Contains(label, "dashboard"), strings.Contains(label, "admin"):
			rank = 0 // avoid — not an API port
		case strings.Contains(label, "s3"), strings.Contains(label, "api"):
			rank = 2 // prefer
		}
		if rank > bestRank {
			bestRank, best = rank, &ports[i]
		}
	}
	return best
}

// sameHostPort reports whether two endpoint URLs point at the same host:port,
// ignoring scheme and a trailing slash.
func sameHostPort(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		s = strings.TrimPrefix(s, "http://")
		s = strings.TrimPrefix(s, "https://")
		return s
	}
	return norm(a) == norm(b)
}

// endpointFromJobAlloc finds the first running alloc for jobID and extracts
// the best-labelled reserved or dynamic port to build an endpoint URL.
func endpointFromJobAlloc(ctx context.Context, nc *utils.NomadClient, jobID, namespace string) string {
	allocs, err := nc.GetJobAllocs(ctx, jobID, namespace, false)
	if err != nil {
		return ""
	}
	for _, stub := range allocs {
		if stub.ClientStatus != "running" {
			continue
		}
		alloc, err := nc.GetAllocation(ctx, stub.ID, namespace)
		if err != nil || alloc.AllocatedResources == nil || alloc.AllocatedResources.Shared == nil {
			continue
		}
		for _, net := range alloc.AllocatedResources.Shared.Networks {
			// Pick the best port by label rather than the first one: never a
			// console/ui port, prefer an api/s3 port. MinIO registers BOTH an S3
			// API port and a console port; returning the console port produced an
			// endpoint the S3 SDK rejects with "must be made to API port" (B5).
			ports := append(append([]utils.NomadDynamicPort{}, net.ReservedPorts...), net.DynamicPorts...)
			p := preferredServicePort(ports)
			if p == nil {
				continue
			}
			ip := p.HostIP
			if ip == "" || ip == "0.0.0.0" {
				// Host-mode jobs: resolve the node's primary IP.
				ip = nodeIP(ctx, nc, stub.NodeID)
			}
			if ip != "" {
				return fmt.Sprintf("http://%s:%d", ip, p.Value)
			}
		}
	}
	return ""
}

// nodeIP returns the primary IP address of a Nomad node.
func nodeIP(ctx context.Context, nc *utils.NomadClient, nodeID string) string {
	if nodeID == "" {
		return ""
	}
	node, err := nc.GetNode(ctx, nodeID)
	if err != nil {
		return ""
	}
	return node.Attributes["unique.network.ip-address"]
}

// detectVaultSecretsMode probes the Vault health endpoint to distinguish
// initialized+unsealed ("vault") from sealed ("vault+sealed").
func detectVaultSecretsMode(reqCtx context.Context, nc *utils.NomadClient, ctx *config.Context) string {
	vaultHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "vault", "http")
	if !ok || vaultHTTP == "" {
		instances, err := nc.GetServiceInstances(context.Background(), "abc-nodes-vault", "")
		if err != nil || len(instances) == 0 {
			return "vault"
		}
		inst := instances[0]
		vaultHTTP = fmt.Sprintf("http://%s:%d", inst.Address, inst.Port)
	}
	healthReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, vaultHTTP+"/v1/sys/health", nil)
	if err != nil {
		return "vault"
	}
	resp, err := debuglog.NewLoggingClient(nil).Do(healthReq) //nolint:gosec
	if err != nil {
		return "vault"
	}
	defer resp.Body.Close()
	// 200 = initialized + unsealed + active
	// 429 = standby
	// 503 = sealed
	if resp.StatusCode == http.StatusServiceUnavailable {
		return "vault+sealed"
	}
	return "vault"
}

func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "403") || strings.Contains(msg, "Permission denied")
}

func runCapabilitiesShow(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()
	if ctx.Capabilities == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No capabilities stored. Run: abc cluster capabilities sync")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Capabilities for context %q:\n", cfg.ActiveContext)
	printCapabilities(cmd, ctx.Capabilities)
	return nil
}

// syncNodeCapabilities queries all ready, eligible Nomad client nodes and
// returns a NodeCapability entry for each, listing healthy+detected drivers.
//
// Returns (nil, nil) when the token lacks node:read permission (403). Callers
// should treat nil results as "keep existing stamped data" rather than "wipe".
func syncNodeCapabilities(ctx context.Context, nc *utils.NomadClient) ([]config.NodeCapability, error) {
	stubs, err := nc.ListNodes(ctx)
	if err != nil {
		if isPermissionDenied(err) {
			// Pool tokens (abc-cluster / seedling tier) do not have node:read.
			// Return nil, nil so the caller preserves any server-stamped node
			// capability data that was embedded in config.yaml at claim time.
			return nil, nil
		}
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	var eligible []utils.NomadNodeStub
	for _, s := range stubs {
		if !strings.EqualFold(s.Status, "ready") {
			continue
		}
		if strings.EqualFold(s.SchedulingEligibility, "ineligible") {
			continue
		}
		if s.Drain {
			continue
		}
		eligible = append(eligible, s)
	}

	var result []config.NodeCapability
	for _, stub := range eligible {
		node, err := nc.GetNode(ctx, stub.ID)
		if err != nil {
			return nil, fmt.Errorf("get node %s: %w", stub.ID, err)
		}

		var drivers []string
		for name, info := range node.Drivers {
			if info.Detected && info.Healthy {
				drivers = append(drivers, name)
			}
		}
		sort.Strings(drivers)

		var volumes []string
		for name, vol := range node.HostVolumes {
			entry := name + ":" + vol.Path
			if vol.ReadOnly {
				entry += " (ro)"
			}
			volumes = append(volumes, entry)
		}
		sort.Strings(volumes)

		result = append(result, config.NodeCapability{
			ID:       node.ID,
			Hostname: node.Name,
			Drivers:  drivers,
			Volumes:  volumes,
		})
	}
	return result, nil
}

func printCapabilities(cmd *cobra.Command, caps *config.Capabilities) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "  storage:       %s\n", orNone(caps.Storage))
	fmt.Fprintf(w, "  uploads:       %v\n", caps.Uploads)
	fmt.Fprintf(w, "  upload_ui:     %v\n", caps.UploadUI)
	fmt.Fprintf(w, "  logging:       %v\n", caps.Logging)
	fmt.Fprintf(w, "  monitoring:    %v\n", caps.Monitoring)
	fmt.Fprintf(w, "  observability: %v\n", caps.Observability)
	fmt.Fprintf(w, "  notifications: %v\n", caps.Notifications)
	fmt.Fprintf(w, "  secrets:       %s\n", orNone(caps.Secrets))
	fmt.Fprintf(w, "  proxy:         %v\n", caps.Proxy)
	if !caps.LastSynced.IsZero() {
		fmt.Fprintf(w, "  last_synced:   %s\n", caps.LastSynced.Format(time.RFC3339))
	}
	// Load config to show dashboard URL if available.
	if cfg, err := config.Load(); err == nil {
		ctx := cfg.ActiveCtx()
		if dash, ok := config.GetAdminFloorField(&ctx.Admin.Services, "grafana", "dashboard"); ok && dash != "" {
			fmt.Fprintf(w, "  dashboard:     %s\n", dash)
		}
	}
	if len(caps.Nodes) > 0 {
		fmt.Fprintf(w, "  nodes:\n")
		for _, n := range caps.Nodes {
			shortID := n.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			driverStr := strings.Join(n.Drivers, ", ")
			if driverStr == "" {
				driverStr = "(none)"
			}
			fmt.Fprintf(w, "    - %s (%s)\n", n.Hostname, shortID)
			fmt.Fprintf(w, "        drivers: %s\n", driverStr)
			if len(n.Volumes) > 0 {
				fmt.Fprintf(w, "        volumes: %s\n", strings.Join(n.Volumes, ", "))
			}
		}
	}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}
