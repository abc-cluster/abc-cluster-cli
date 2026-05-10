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
		Short: "Show cluster status",
		Long: `Show the status of the active cluster.

Without --cloud, probes the Nomad endpoint configured in the active context
directly — works on bare seedling and grove deployments without abc-cloud.

With --cloud, delegates to the abc-cloud gateway for multi-cluster fleet
operations (requires an infrastructure-tier token).`,
		Args: cobra.MaximumNArgs(1),
		RunE: runClusterStatus,
	}
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	if utils.CloudFromCmd(cmd) {
		return runClusterStatusCloud(cmd, args)
	}
	return runClusterStatusLocal(cmd)
}

// runClusterStatusCloud is the original cloud-gateway path.
func runClusterStatusCloud(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)

	name := utils.ClusterFromCmd(cmd)
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("specify a cluster name as argument or via --cluster / ABC_CLUSTER")
	}

	var detail ClusterDetail
	if err := nc.CloudGetCluster(cmd.Context(), name, &detail); err != nil {
		return fmt.Errorf("fetching cluster %q: %w", name, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Name         %s\n", detail.Name)
	fmt.Fprintf(out, "  Region       %s\n", detail.Region)
	fmt.Fprintf(out, "  Status       %s\n", detail.Status)
	fmt.Fprintf(out, "  Nodes        %d\n", detail.NodeCount)
	fmt.Fprintf(out, "  Nomad        %s\n", detail.NomadVersion)
	if len(detail.Datacenters) > 0 {
		fmt.Fprintf(out, "  Datacenters  %v\n", detail.Datacenters)
	}
	if len(detail.Meta) > 0 {
		fmt.Fprintf(out, "\n  Metadata:\n")
		keys := make([]string, 0, len(detail.Meta))
		for k := range detail.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "    %-16s %s\n", k, detail.Meta[k])
		}
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

	fmt.Fprintf(out, "  Tip: run 'abc admin health' for a full service health breakdown.\n\n")
	return nil
}
