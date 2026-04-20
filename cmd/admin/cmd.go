// Package admin implements the "abc admin" command group.
//
// admin groups service health checks and app-level entity management
// (users, workspaces, organizations) under a single administrative surface.
// All write operations require --sudo.
package admin

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/loki"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/minio"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/nebula"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/nomad"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/ntfy"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/probe"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/prometheus"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/rclone"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/rustfs"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/serviceconfig"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tailscale"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/traefik"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/vault"
	"github.com/abc-cluster/abc-cluster-cli/cmd/service"
	"github.com/spf13/cobra"
)

// NewCmd returns the "admin" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands: services and app-level entity management",
		Long: `Commands for cluster administrators.

  abc admin health                                    Aggregate health check across all floor services
  abc admin services ping nomad                       Check connectivity to a backend service
  abc admin services nomad cli -- job status -short   Run the preconfigured Nomad CLI
  abc admin services minio cli ls local               Run the local MinIO client CLI
  abc admin services vault cli status                 Run the local Vault or OpenBao (bao) CLI
  abc admin services loki query '{task="minio"}'      Query Loki logs directly
  abc admin services loki cli -- query '{job="nomad"}'  Run logcli passthrough
  abc admin services prometheus query 'nomad_client_allocated_cpu'  Instant PromQL query
  abc admin services ntfy send abc-jobs "Maintenance at 22:00"      Send push notification
  abc admin services ntfy list abc-jobs               List recent ntfy messages
  abc admin services traefik cli version              Run the local Traefik CLI
  abc admin services config sync                      Sync ~/.abc admin.services.* from Nomad (abc-nodes)`,
	}

	// services sub-group — reuses the existing service package.
	svcCmd := service.NewCmd()
	svcCmd.Use = "services"
	svcCmd.Short = "Inspect backend service health and versions"

	// Add service sub-commands.
	svcCmd.AddCommand(nomad.NewCmd())
	svcCmd.AddCommand(probe.NewCmd())
	svcCmd.AddCommand(tailscale.NewCmd())
	svcCmd.AddCommand(minio.NewCmd())
	svcCmd.AddCommand(nebula.NewCmd())
	svcCmd.AddCommand(rustfs.NewCmd())
	svcCmd.AddCommand(vault.NewCmd())
	svcCmd.AddCommand(rclone.NewCmd())
	svcCmd.AddCommand(traefik.NewCmd())
	svcCmd.AddCommand(serviceconfig.NewCmd())
	// Floor observability & notifications.
	svcCmd.AddCommand(loki.NewCmd())
	svcCmd.AddCommand(prometheus.NewCmd())
	svcCmd.AddCommand(ntfy.NewCmd())
	cmd.AddCommand(svcCmd)

	// Aggregate health check.
	cmd.AddCommand(newHealthCmd())

	// app sub-group — application-level entity management.
	cmd.AddCommand(newAppCmd())

	return cmd
}

// newAppCmd returns the "app" subcommand group for application-level entities.
func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage application-level entities: workspaces, organizations",
		Long: `Commands for managing application-level entities on the ABC-cluster platform.

(Application-level namespaces are managed via: abc admin services nomad namespace)`,
	}

	return cmd
}
