// Package admin implements the "abc admin" command group.
//
// admin groups service health checks and app-level entity management
// (users, workspaces, organizations) under a single administrative surface.
// All write operations require --sudo.
package admin

import (
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/minio"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/nebula"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/nomad"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/rustfs"
	"github.com/abc-cluster/abc-cluster-cli/cmd/admin/tailscale"
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

  abc admin services ping nomad           Check connectivity to a backend service
  abc admin services version api          Show a service version
	abc admin services nomad cli status     Run the preconfigured Nomad CLI
	abc admin services tailscale cli status Run the local Tailscale CLI
	abc admin services minio cli ls local   Run the local MinIO client CLI
	abc admin services nebula cli -version  Run the local Nebula CLI
	abc admin services rustfs cli status    Run the local RustFS CLI
	abc admin services vault cli status     Run the local Vault/OpenBao CLI`,
	}

	// services sub-group — reuses the existing service package.
	svcCmd := service.NewCmd()
	svcCmd.Use = "services"
	svcCmd.Short = "Inspect backend service health and versions"

	// Add nomad sub-group under services (for Nomad-specific operations)
	svcCmd.AddCommand(nomad.NewCmd())
	svcCmd.AddCommand(tailscale.NewCmd())
	svcCmd.AddCommand(minio.NewCmd())
	svcCmd.AddCommand(nebula.NewCmd())
	svcCmd.AddCommand(rustfs.NewCmd())
	svcCmd.AddCommand(vault.NewCmd())
	cmd.AddCommand(svcCmd)

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
`,
	}

	return cmd
}
