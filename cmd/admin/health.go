package admin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Aggregate health check across all configured floor services",
		Long: `Probes each floor service configured for the active abc-nodes context
and prints a status table. Services not configured are skipped.

  abc admin health

Exit codes:
  0  All probed services healthy
  1  One or more services unhealthy or unreachable`,
		RunE: runHealth,
	}
	cmd.Flags().Bool("all", false, "Show skipped (unconfigured) services as well")
	return cmd
}

type healthResult struct {
	floor.ServiceHealth
	skipped bool
}

func runHealth(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := cfg.ActiveCtx()
	showAll, _ := cmd.Flags().GetBool("all")

	type probe struct {
		name string
		run  func(context.Context) floor.ServiceHealth
	}

	svc := func(id, field string) string {
		v, _ := config.GetAdminFloorField(&ctx.Admin.Services, id, field)
		return v
	}
	nomadAddr := ctx.NomadAddr()
	nomadToken := ctx.NomadToken()

	probes := []probe{
		{"nomad", func(c context.Context) floor.ServiceHealth {
			if nomadAddr == "" {
				return floor.ServiceHealth{Name: "nomad", Detail: "not configured"}
			}
			return floor.ProbeNomad(c, nomadAddr, nomadToken)
		}},
		{"minio", func(c context.Context) floor.ServiceHealth {
			u := svc("minio", "endpoint")
			if u == "" {
				u = svc("minio", "http")
			}
			if u == "" {
				return floor.ServiceHealth{Name: "minio", Detail: "not configured"}
			}
			return floor.ProbeMinIO(c, u)
		}},
		{"rustfs", func(c context.Context) floor.ServiceHealth {
			u := svc("rustfs", "endpoint")
			if u == "" {
				u = svc("rustfs", "http")
			}
			if u == "" {
				return floor.ServiceHealth{Name: "rustfs", Detail: "not configured"}
			}
			return floor.ProbeRustFS(c, u)
		}},
		{"tusd", func(c context.Context) floor.ServiceHealth {
			u := svc("tusd", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "tusd", Detail: "not configured"}
			}
			return floor.ProbeTusd(c, u)
		}},
		{"prometheus", func(c context.Context) floor.ServiceHealth {
			u := svc("prometheus", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "prometheus", Detail: "not configured"}
			}
			return floor.ProbePrometheus(c, u)
		}},
		{"loki", func(c context.Context) floor.ServiceHealth {
			u := svc("loki", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "loki", Detail: "not configured"}
			}
			return floor.ProbeLoki(c, u)
		}},
		{"grafana", func(c context.Context) floor.ServiceHealth {
			u := svc("grafana", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "grafana", Detail: "not configured"}
			}
			return floor.ProbeGrafana(c, u)
		}},
		{"alloy", func(c context.Context) floor.ServiceHealth {
			u := svc("grafana_alloy", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "alloy", Detail: "not configured"}
			}
			return floor.ProbeAlloy(c, u)
		}},
		{"ntfy", func(c context.Context) floor.ServiceHealth {
			u := svc("ntfy", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "ntfy", Detail: "not configured"}
			}
			return floor.ProbeNtfy(c, u)
		}},
		{"vault", func(c context.Context) floor.ServiceHealth {
			u := svc("vault", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "vault", Detail: "not configured"}
			}
			return floor.ProbeVault(c, u)
		}},
		{"traefik", func(c context.Context) floor.ServiceHealth {
			u := svc("traefik", "http")
			if u == "" {
				return floor.ServiceHealth{Name: "traefik", Detail: "not configured"}
			}
			return floor.ProbeTraefik(c, u)
		}},
	}

	// Run all probes concurrently.
	results := make([]healthResult, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(idx int, pr probe) {
			defer wg.Done()
			h := pr.run(cmd.Context())
			results[idx] = healthResult{
				ServiceHealth: h,
				skipped:       h.Detail == "not configured",
			}
		}(i, p)
	}
	wg.Wait()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\n  %-14s %-10s %-36s %s\n", "SERVICE", "STATUS", "URL", "DETAIL")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 72))

	allHealthy := true
	for _, r := range results {
		if r.skipped && !showAll {
			continue
		}
		status := "healthy"
		if r.skipped {
			status = "—"
		} else if !r.Healthy {
			status = "UNHEALTHY"
			allHealthy = false
		}
		url := r.URL
		if len(url) > 34 {
			url = url[:31] + "..."
		}
		detail := r.Detail
		if r.skipped {
			detail = "not configured"
		}
		fmt.Fprintf(out, "  %-14s %-10s %-36s %s\n", r.Name, status, url, detail)
	}
	fmt.Fprintln(out)

	if !allHealthy {
		return fmt.Errorf("one or more services are unhealthy")
	}

	// Emit vault-sealed warning separately so exit code stays 0 for "reachable but sealed".
	for _, r := range results {
		if r.Name == "vault" && r.Detail == "sealed" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"  Warning: Vault is sealed — secrets backend unavailable.\n"+
					"  Run: abc admin services vault cli -- operator unseal\n\n")
		}
	}
	return nil
}
