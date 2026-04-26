package cluster

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
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
				"  Set it with: abc config set admin.services.nomad.nomad_addr http://<ip>:4646\n"+
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
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync capabilities from Nomad (services API with job-listing fallback)",
		Long: `Queries the Nomad service registry for running abc-nodes services and updates the
active context's capabilities block and admin.services endpoints in config.yaml.

Falls back to job listing if the services API returns 403 (requires only list-jobs
capability rather than read-job). Endpoint URLs are populated from service instances
when available, or from allocation port assignments otherwise.

Only populates endpoint fields that are not already set by the operator.`,
		RunE: runCapabilitiesSync,
	}
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
	nc, err := nomadClientForCapabilities(cmd)
	if err != nil {
		return err
	}
	bg := context.Background()

	// ── Step 1: Build service set ─────────────────────────────────────────────

	svcSet, via, err := buildServiceSet(bg, nc)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Detected services via %s.\n", via)

	// ── Step 2: Map services to capabilities ──────────────────────────────────

	caps := &config.Capabilities{LastSynced: time.Now()}

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
		caps.Secrets = detectVaultSecretsMode(nc, &ctx)
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
	if nodeErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Warning: could not sync node capabilities: %v\n", nodeErr)
	} else {
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

// buildServiceSet returns a set of running abc-nodes service names.
// It tries the Nomad services API first; on 403 it falls back to job listing.
func buildServiceSet(ctx context.Context, nc *utils.NomadClient) (map[string]bool, string, error) {
	services, err := nc.ListServices(ctx, "")
	if err == nil {
		set := make(map[string]bool, len(services))
		for _, s := range services {
			set[s.ServiceName] = true
		}
		return set, "Nomad service registry", nil
	}

	if !isPermissionDenied(err) {
		return nil, "", fmt.Errorf("list services from Nomad: %w", err)
	}

	// 403: fall back to listing jobs with prefix abc-nodes-.
	jobs, jobErr := nc.ListJobs(ctx, "abc-nodes-", "")
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

// endpointFromJobAlloc finds the first running alloc for jobID and extracts
// the first reserved or dynamic port to build an endpoint URL.
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
			// Prefer reserved (static) ports, fall back to dynamic.
			for _, p := range append(net.ReservedPorts, net.DynamicPorts...) {
				if p.Value == 0 {
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
func detectVaultSecretsMode(nc *utils.NomadClient, ctx *config.Context) string {
	vaultHTTP, ok := config.GetAdminFloorField(&ctx.Admin.Services, "vault", "http")
	if !ok || vaultHTTP == "" {
		instances, err := nc.GetServiceInstances(context.Background(), "abc-nodes-vault", "")
		if err != nil || len(instances) == 0 {
			return "vault"
		}
		inst := instances[0]
		vaultHTTP = fmt.Sprintf("http://%s:%d", inst.Address, inst.Port)
	}
	resp, err := http.Get(vaultHTTP + "/v1/sys/health") //nolint:gosec,noctx
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
func syncNodeCapabilities(ctx context.Context, nc *utils.NomadClient) ([]config.NodeCapability, error) {
	stubs, err := nc.ListNodes(ctx)
	if err != nil {
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
		result = append(result, config.NodeCapability{
			ID:       node.ID,
			Hostname: node.Name,
			Drivers:  drivers,
			Volumes:  nil, // Phase 2
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
			fmt.Fprintf(w, "    - %s (%s): %s\n", n.Hostname, n.ID[:8], strings.Join(n.Drivers, ", "))
		}
	}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}
