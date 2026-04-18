package serviceconfig

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

type floorSyncRule struct {
	JobName     string // Nomad job ID, e.g. abc-nodes-minio
	ServiceYAML string // admin.services.<name>
	PortLabel   string // group network port label
	Kind        string // "http" | "endpoint"
	// UploadRel, when Kind is http, also sets contexts.<ctx>.upload_endpoint to base+UploadRel (e.g. "/files/").
	UploadRel string
}

// Order matches typical dependency / operator interest; skipped if the job is missing or not running.
var floorSyncRules = []floorSyncRule{
	{"abc-nodes-minio", "minio", "api", "endpoint", ""},
	{"abc-nodes-rustfs", "rustfs", "s3", "endpoint", ""},
	{"abc-nodes-tusd", "tusd", "http", "http", "/files/"},
	{"abc-nodes-grafana", "grafana", "http", "http", ""},
	{"abc-nodes-prometheus", "prometheus", "http", "http", ""},
	{"abc-nodes-loki", "loki", "http", "http", ""},
	{"abc-nodes-ntfy", "ntfy", "http", "http", ""},
}

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync admin.services URLs from running abc-nodes Nomad jobs into ~/.abc",
		Long: strings.TrimSpace(`
For a context with cluster_type abc-nodes, queries Nomad for floor jobs named
abc-nodes-* (MinIO, tusd, Grafana, …), reads each running allocation's dynamic
host ports, and writes:

  contexts.<name>.admin.services.<service>.http|endpoint
  contexts.<name>.upload_endpoint              (from tusd, …/files/)

Host names follow admin.services.nomad.nomad_addr (same host as the Nomad API
unless the allocation publishes a concrete HostIP on the port). Scheme is http
when nomad_addr is http, otherwise https.

Credentials are not read from tasks (would require exec); existing admin.abc_nodes
keys are left unchanged. Requires nomad_addr and nomad_token on the context.
`),
		RunE: runConfigSync,
	}
	cmd.Flags().String("context", "", "Context name (default: active_context)")
	cmd.Flags().Bool("dry-run", false, "Print keys that would be set without saving")
	cmd.Flags().Bool("skip-cluster-type-check", false, "Run even if cluster_type is not abc-nodes")
	return cmd
}

func runConfigSync(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	c, err := cfg.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctxName, _ := cmd.Flags().GetString("context")
	if strings.TrimSpace(ctxName) == "" {
		ctxName = c.ActiveContext
	}
	canon := c.ResolveContextName(ctxName)
	if canon == "" {
		return fmt.Errorf("unknown context %q", ctxName)
	}
	ctx := c.Contexts[canon]

	skipCheck, _ := cmd.Flags().GetBool("skip-cluster-type-check")
	if !skipCheck && !ctx.IsABCNodesCluster() {
		return fmt.Errorf("context %q has cluster_type %q (want abc-nodes); use --skip-cluster-type-check to override",
			canon, strings.TrimSpace(ctx.ClusterType))
	}

	addr := strings.TrimSpace(ctx.NomadAddr())
	tok := strings.TrimSpace(ctx.NomadToken())
	region := strings.TrimSpace(ctx.NomadRegion())
	if addr == "" || tok == "" {
		return fmt.Errorf("context %q needs admin.services.nomad.nomad_addr and nomad_token", canon)
	}
	if region == "" {
		region = "global"
	}

	ns := ctx.AbcNodesNomadNamespaceOrDefault()

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	nc := utils.NewNomadClient(addr, tok, region)

	ctxNomad := context.Background()
	jobs, err := nc.ListJobs(ctxNomad, "abc-nodes-", ns)
	if err != nil {
		return fmt.Errorf("list Nomad jobs: %w", err)
	}
	jobByID := make(map[string]utils.NomadJobStub, len(jobs))
	for _, j := range jobs {
		jobByID[j.ID] = j
	}

	type update struct{ key, val string }
	var updates []update

	scheme := "http"
	if u, err := url.Parse(addr); err == nil && strings.EqualFold(u.Scheme, "https") {
		scheme = "https"
	}

	for _, rule := range floorSyncRules {
		stub, ok := jobByID[rule.JobName]
		if !ok {
			fmt.Fprintf(out, "skip %s: job not found in namespace %q\n", rule.JobName, ns)
			continue
		}
		if !strings.EqualFold(stub.Status, "running") {
			fmt.Fprintf(out, "skip %s: job status %q (want running)\n", rule.JobName, stub.Status)
			continue
		}
		allocs, err := nc.GetJobAllocs(ctxNomad, rule.JobName, ns, false)
		if err != nil {
			return fmt.Errorf("list allocations for %s: %w", rule.JobName, err)
		}
		allocID, ok := pickRunningAllocID(allocs)
		if !ok {
			fmt.Fprintf(out, "skip %s: no running allocation\n", rule.JobName)
			continue
		}
		alloc, err := nc.GetAllocation(ctxNomad, allocID, ns)
		if err != nil {
			return fmt.Errorf("get allocation %s: %w", allocID, err)
		}
		port, hostIP, ok := lookupDynamicHostPort(alloc, rule.PortLabel)
		if !ok {
			fmt.Fprintf(out, "skip %s: no dynamic port label %q on allocation\n", rule.JobName, rule.PortLabel)
			continue
		}
		base, err := publicBaseURL(scheme, addr, hostIP, port)
		if err != nil {
			return fmt.Errorf("%s: %w", rule.JobName, err)
		}
		base = strings.TrimSuffix(strings.TrimSpace(base), "/")

		pfx := "contexts." + canon + ".admin.services." + rule.ServiceYAML + "."
		switch rule.Kind {
		case "endpoint":
			updates = append(updates, update{pfx + "endpoint", base})
		case "http":
			updates = append(updates, update{pfx + "http", base})
			if rule.UploadRel != "" {
				upload := strings.TrimSuffix(base, "/") + rule.UploadRel
				updates = append(updates, update{"contexts." + canon + ".upload_endpoint", upload})
			}
		default:
			return fmt.Errorf("internal: unknown kind %q", rule.Kind)
		}
	}

	sort.Slice(updates, func(i, j int) bool { return updates[i].key < updates[j].key })

	if dryRun {
		fmt.Fprintf(out, "Dry run (%d keys):\n", len(updates))
		for _, u := range updates {
			fmt.Fprintf(out, "  %s = %s\n", u.key, u.val)
		}
		return nil
	}

	for _, u := range updates {
		if err := c.Set(u.key, u.val); err != nil {
			return fmt.Errorf("set %s: %w", u.key, err)
		}
	}
	if err := c.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintf(out, "Updated %d keys on context %q.\n", len(updates), canon)
	return nil
}

func pickRunningAllocID(allocs []utils.NomadAllocStub) (string, bool) {
	var bestID string
	var bestMod int64
	for _, a := range allocs {
		if !strings.EqualFold(a.ClientStatus, "running") {
			continue
		}
		if a.ModifyTime >= bestMod {
			bestMod = a.ModifyTime
			bestID = a.ID
		}
	}
	return bestID, bestID != ""
}

func lookupDynamicHostPort(alloc *utils.NomadAllocation, label string) (port int, hostIP string, ok bool) {
	if alloc == nil || alloc.AllocatedResources == nil || alloc.AllocatedResources.Shared == nil {
		return 0, "", false
	}
	for _, nw := range alloc.AllocatedResources.Shared.Networks {
		for _, dp := range nw.DynamicPorts {
			if dp.Label == label && dp.Value > 0 {
				return dp.Value, strings.TrimSpace(dp.HostIP), true
			}
		}
	}
	return 0, "", false
}

func publicBaseURL(scheme, nomadAPIAddr, hostIP string, port int) (string, error) {
	host := strings.TrimSpace(hostIP)
	if host == "" || host == "0.0.0.0" {
		u, err := url.Parse(nomadAPIAddr)
		if err != nil {
			return "", err
		}
		host = u.Hostname()
		if host == "" {
			return "", fmt.Errorf("nomad_addr has no hostname")
		}
	}
	if scheme == "" {
		scheme = "http"
	}
	h := net.JoinHostPort(host, strconv.Itoa(port))
	return scheme + "://" + h, nil
}
