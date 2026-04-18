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

type syncKV struct{ key, val string }

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
	{"abc-nodes-rustfs", "rustfs", "console", "http", ""},
	{"abc-nodes-tusd", "tusd", "http", "http", "/files/"},
	{"abc-nodes-grafana", "grafana", "http", "http", ""},
	{"abc-nodes-alloy", "grafana_alloy", "ui", "http", ""},
	{"abc-nodes-prometheus", "prometheus", "http", "http", ""},
	{"abc-nodes-loki", "loki", "http", "http", ""},
	{"abc-nodes-ntfy", "ntfy", "http", "http", ""},
	{"abc-nodes-vault", "vault", "http", "http", ""},
	// Traefik: dashboard (:8888) → http; entry web (:80) → endpoint (see traefik.nomad.hcl).
	{"abc-nodes-traefik", "traefik", "dashboard", "http", ""},
	{"abc-nodes-traefik", "traefik", "http", "endpoint", ""},
}

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync admin.services URLs from running abc-nodes Nomad jobs into ~/.abc",
		Long: strings.TrimSpace(`
For a context with cluster_type abc-nodes, queries Nomad for floor jobs named
abc-nodes-* (MinIO, tusd, Grafana, Grafana Alloy, …), reads each running allocation's published
ports, and writes:

  contexts.<name>.admin.services.<service>.http|endpoint
    (RustFS: endpoint = S3 API, http = web console when the job exposes a console port)
    (Traefik: http = dashboard port, endpoint = web entrypoint port)
  contexts.<name>.upload_endpoint              (from tusd, …/files/)

The hostname is taken from admin.services.nomad.nomad_addr (same rule as when
the allocation has no concrete HostIP on the port). Scheme follows nomad_addr
(http vs https).

Credentials are not read from task logs (would require exec). Existing
admin.services.<svc>.access_key, secret_key, user, password (except vault lab
token below) and admin.abc_nodes credentials are left unchanged unless noted.
Nomad sync updates http / endpoint / upload_endpoint only. For job abc-nodes-vault
when running, sync also reads VAULT_DEV_ROOT_TOKEN_ID from the registered job
spec and sets admin.services.vault.access_key (lab -dev Vault only).
Requires nomad_addr and nomad_token on the context.
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

	var updates []syncKV

	scheme := "http"
	if u, err := url.Parse(addr); err == nil && strings.EqualFold(u.Scheme, "https") {
		scheme = "https"
	}

	vaultTokUpdates, err := vaultSyncUpdatesFromNomad(ctxNomad, out, nc, ns, canon, jobByID)
	if err != nil {
		return err
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
			updates = append(updates, syncKV{pfx + "endpoint", base})
		case "http":
			updates = append(updates, syncKV{pfx + "http", base})
			if rule.UploadRel != "" {
				upload := strings.TrimSuffix(base, "/") + rule.UploadRel
				updates = append(updates, syncKV{"contexts." + canon + ".upload_endpoint", upload})
			}
		default:
			return fmt.Errorf("internal: unknown kind %q", rule.Kind)
		}
	}

	merged := make(map[string]string)
	for _, u := range updates {
		merged[u.key] = u.val
	}
	for _, u := range vaultTokUpdates {
		merged[u.key] = u.val
	}
	updates = updates[:0]
	for k, v := range merged {
		updates = append(updates, syncKV{k, v})
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
		for _, dp := range nw.ReservedPorts {
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
