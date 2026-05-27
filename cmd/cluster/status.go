package cluster

import (
	"fmt"
	"sort"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// ClusterDetail is a full cluster description from the cloud gateway.
type ClusterDetail struct {
	Name         string            `json:"Name"`
	Region       string            `json:"Region"`
	Status       string            `json:"Status"`
	NodeCount    int               `json:"NodeCount"`
	NomadVersion string            `json:"NomadVersion"`
	Datacenters  []string          `json:"Datacenters"`
	Meta         map[string]string `json:"Meta"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show cluster and service status",
		Long: `Show the status of the active cluster and its backend services.

Without --cloud, probes the Nomad endpoint configured in the active context
directly — works on bare seedling and grove deployments without abc-cloud.
Shows: Nomad health, node count, running jobs, active context.

With --cloud, delegates to the abc-cloud gateway: shows cluster detail
(name, region, datacenters, node count) plus a full backend service health
table (nomad, jurist, minio, tus, etc. with version and latency).

This command was formerly accessible as top-level 'abc status'; that alias
is deprecated — use 'abc cluster status' going forward.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runClusterStatus,
	}
}

// NewStatusCmd exports a top-level "status" command variant for the
// deprecated root-level alias. New callers should use NewCmd() and
// invoke 'abc cluster status' directly.
func NewStatusCmd() *cobra.Command {
	return newStatusCmd()
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	if utils.CloudFromCmd(cmd) {
		return runClusterStatusCloud(cmd, args)
	}
	return runClusterStatusLocal(cmd)
}

// ServiceHealth is the health summary for one backend service (returned by
// the abc-cloud gateway). Mirrors the type in cmd/service but defined here
// to avoid a cross-package import cycle.
type ServiceHealth struct {
	Name      string `json:"Name"`
	Status    string `json:"Status"`
	Version   string `json:"Version"`
	LatencyMs int    `json:"LatencyMs"`
}

// runClusterStatusCloud is the cloud-gateway path. It shows cluster detail
// (name, region, node count, datacenters) followed by the full backend
// service health table (formerly top-level 'abc status').
func runClusterStatusCloud(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	out := cmd.OutOrStdout()

	// ── Context header ────────────────────────────────────────────────────────
	if cfg, err := config.Load(); err == nil && cfg != nil && strings.TrimSpace(cfg.ActiveContext) != "" {
		fmt.Fprintf(out, "  Context      %s\n", cfg.ActiveContext)
		if canon := cfg.ResolveContextName(cfg.ActiveContext); canon != "" && canon != cfg.ActiveContext {
			fmt.Fprintf(out, "  Canonical    %s\n", canon)
		}
		fmt.Fprintln(out)
	}

	// ── Cluster detail (optional — requires --cluster or ABC_CLUSTER) ─────────
	name := utils.ClusterFromCmd(cmd)
	if len(args) > 0 {
		name = args[0]
	}
	if name != "" {
		var detail ClusterDetail
		if err := nc.CloudGetCluster(cmd.Context(), name, &detail); err != nil {
			fmt.Fprintf(out, "  Cluster      %s (unavailable: %v)\n\n", name, err)
		} else {
			fmt.Fprintf(out, "  Cluster      %s\n", detail.Name)
			fmt.Fprintf(out, "  Region       %s\n", detail.Region)
			fmt.Fprintf(out, "  Status       %s\n", detail.Status)
			fmt.Fprintf(out, "  Nodes        %d\n", detail.NodeCount)
			fmt.Fprintf(out, "  Nomad        %s\n", detail.NomadVersion)
			if len(detail.Datacenters) > 0 {
				fmt.Fprintf(out, "  Datacenters  %s\n", strings.Join(detail.Datacenters, ", "))
			}
			if len(detail.Meta) > 0 {
				keys := make([]string, 0, len(detail.Meta))
				for k := range detail.Meta {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(out, "    %-16s %s\n", k, detail.Meta[k])
				}
			}
			fmt.Fprintln(out)
		}
	}

	// ── Backend service health table (formerly 'abc status') ─────────────────
	var services []ServiceHealth
	if err := nc.CloudGetServiceHealth(cmd.Context(), &services); err != nil {
		fmt.Fprintf(out, "  Service health unavailable: %v\n", err)
		return fmt.Errorf("fetching service health: %w", err)
	}

	fmt.Fprintf(out, "  %-22s %-10s %-12s %-10s\n", "SERVICE", "STATUS", "VERSION", "LATENCY")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 58))

	unhealthy := 0
	for _, s := range services {
		status := s.Status
		if status == "" {
			status = "unknown"
		}
		latency := "—"
		if s.LatencyMs > 0 {
			latency = fmt.Sprintf("%dms", s.LatencyMs)
		}
		fmt.Fprintf(out, "  %-22s %-10s %-12s %-10s\n", s.Name, status, s.Version, latency)
		if status != "healthy" {
			unhealthy++
		}
	}
	fmt.Fprintln(out)

	if unhealthy > 0 {
		return fmt.Errorf("%d service(s) are not healthy", unhealthy)
	}
	return nil
}

// runClusterStatusLocal probes the Nomad endpoint directly. Works on bare
// seedling and grove deployments without abc-cloud (no controller-svc needed).
func runClusterStatusLocal(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()
	nomadAddr := ctx.NomadAddr()
	nomadToken := ctx.NomadToken()
	if nomadAddr == "" {
		return fmt.Errorf("no Nomad address configured\n" +
			"  Run: abc context add <name> --endpoint http://<ip>:4646")
	}

	nc := nomadClientFromCmd(cmd)
	out := cmd.OutOrStdout()

	// ── Nomad health (version + leader) ──────────────────────────────────────
	nomadHealth := floor.ProbeNomad(cmd.Context(), nomadAddr, nomadToken)
	nomadStatus := "healthy"
	if !nomadHealth.Healthy {
		nomadStatus = "UNREACHABLE"
	}
	fmt.Fprintf(out, "\n  Nomad        %s  (%s)\n", nomadStatus, nomadAddr)
	if nomadHealth.Detail != "" && nomadHealth.Healthy {
		fmt.Fprintf(out, "  Version      %s\n", nomadHealth.Detail)
	}
	if !nomadHealth.Healthy {
		fmt.Fprintf(out, "\n  Cannot reach Nomad — check the address and token.\n")
		return fmt.Errorf("nomad unreachable")
	}

	// ── Node list ─────────────────────────────────────────────────────────────
	nodes, nodeErr := nc.ListNodes(cmd.Context())
	if nodeErr == nil {
		ready, draining := 0, 0
		dcs := map[string]struct{}{}
		for _, n := range nodes {
			if strings.EqualFold(n.Status, "ready") {
				ready++
			}
			if n.Drain {
				draining++
			}
			if n.Datacenter != "" {
				dcs[n.Datacenter] = struct{}{}
			}
		}
		fmt.Fprintf(out, "  Nodes        %d ready", ready)
		if draining > 0 {
			fmt.Fprintf(out, ", %d draining", draining)
		}
		fmt.Fprintln(out)
		if len(dcs) > 0 {
			dcList := make([]string, 0, len(dcs))
			for dc := range dcs {
				dcList = append(dcList, dc)
			}
			sort.Strings(dcList)
			fmt.Fprintf(out, "  Datacenters  %s\n", strings.Join(dcList, ", "))
		}
	}

	// ── Running job count ─────────────────────────────────────────────────────
	jobs, jobErr := nc.ListJobs(cmd.Context(), "", "")
	if jobErr == nil {
		running, total := 0, 0
		for _, j := range jobs {
			total++
			if strings.EqualFold(j.Status, "running") {
				running++
			}
		}
		fmt.Fprintf(out, "  Jobs         %d running / %d total\n", running, total)
	}

	// ── Active context info ───────────────────────────────────────────────────
	fmt.Fprintf(out, "  Context      %s\n", cfg.ActiveContext)
	if cfg.ActiveContext != "" {
		if region := ctx.Region; region != "" {
			fmt.Fprintf(out, "  Region       %s\n", region)
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintf(out, "  Tip: run 'abc doctor' for a full end-to-end config and workload health check.\n\n")
	return nil
}
