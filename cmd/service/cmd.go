// Package service implements "abc service" and "abc status" backend health commands.
package service

import (
	"fmt"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// ServiceHealth is the health summary for one backend service.
type ServiceHealth struct {
	Name    string `json:"Name"`
	Status  string `json:"Status"`
	Version string `json:"Version"`
	LatencyMs int  `json:"LatencyMs"`
}

// NewCmd returns the "service" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Inspect backend service health and versions",
		Long: `Commands for checking the health and version of individual abc-cluster backend services.

Valid service names: nomad, jurist, minio, api, tus, cloud-gateway, xtdb, supabase, tailscale, khan

  abc admin services ping nomad
  abc admin services ping jurist
  abc admin services version api`,
	}

	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Cloud gateway or API address (or set ABC_ADDR/NOMAD_ADDR)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Auth token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(newPingCmd(), newVersionCmd())
	cmd.AddCommand(newCLICmd())
	return cmd
}

// NewStatusCmd returns the top-level "abc status" command.
func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show health of all backend services",
		RunE:  runAllStatus,
	}
}

func nomadClientFromCmd(cmd *cobra.Command) *utils.NomadClient {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	token, _ := cmd.Flags().GetString("nomad-token")
	if token == "" {
		token, _ = cmd.Root().PersistentFlags().GetString("nomad-token")
	}
	region, _ := cmd.Flags().GetString("region")
	if region == "" {
		region, _ = cmd.Root().PersistentFlags().GetString("region")
	}
	return utils.NewNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

func newPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping <service>",
		Short: "Check connectivity to a specific backend service",
		Long: `Valid service names: nomad, jurist, minio, api, tus, cloud-gateway, xtdb, supabase, tailscale, khan

  abc admin services ping nomad
  abc admin services ping jurist`,
		Args: cobra.ExactArgs(1),
		RunE: runServicePing,
	}
}

func runServicePing(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	svcName := args[0]

	var result ServiceHealth
	if err := nc.CloudGetServiceVersion(cmd.Context(), svcName, &result); err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "  ✗ %s  unreachable: %v\n", svcName, err)
		return fmt.Errorf("service %q is unreachable", svcName)
	}

	status := result.Status
	if status == "" {
		status = "healthy"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %-20s %s\n", svcName, status)
	return nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version <service>",
		Short: "Show the version of a specific backend service",
		Args:  cobra.ExactArgs(1),
		RunE:  runServiceVersion,
	}
}

func runServiceVersion(cmd *cobra.Command, args []string) error {
	nc := nomadClientFromCmd(cmd)
	svcName := args[0]

	var result ServiceHealth
	if err := nc.CloudGetServiceVersion(cmd.Context(), svcName, &result); err != nil {
		return fmt.Errorf("fetching version for %q: %w", svcName, err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "  Service  %s\n", svcName)
	if result.Version != "" {
		fmt.Fprintf(out, "  Version  %s\n", result.Version)
	}
	if result.Status != "" {
		fmt.Fprintf(out, "  Status   %s\n", result.Status)
	}
	return nil
}

func runAllStatus(cmd *cobra.Command, _ []string) error {
	nc := nomadClientFromCmd(cmd)

	var services []ServiceHealth
	if err := nc.CloudGetServiceHealth(cmd.Context(), &services); err != nil {
		return fmt.Errorf("fetching service health: %w", err)
	}

	out := cmd.OutOrStdout()
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

	if unhealthy > 0 {
		return fmt.Errorf("%d service(s) are not healthy", unhealthy)
	}
	return nil
}

func newCLICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cli",
		Short: "Porcelain wrappers for local service CLIs",
		Long: `Convenience wrappers for setting up local service CLIs used by abc wrappers.

  abc admin services cli setup`,
	}
	cmd.AddCommand(newCLISetupCmd())
	return cmd
}

func newCLISetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Download all wrapped CLI binaries if missing",
		Long: `Download wrapped service binaries into ~/.abc/binaries (or ABC_BINARIES_DIR).

This checks PATH first and skips download when a binary is already available.
Current managed binaries:
  - nomad
  - abc-node-probe
  - tailscale`,
		RunE: runCLISetup,
	}
}

func runCLISetup(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	dir, err := utils.ManagedBinaryDir()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Setting up wrapped binaries in %s\n", dir)
	fmt.Fprintln(out, "Checking PATH first, then downloading missing binaries...")

	if _, err := utils.SetupNomadAndProbeBinaries(out); err != nil {
		return err
	}
	if _, err := utils.SetupTailscaleBinary(out); err != nil {
		return err
	}

	fmt.Fprintln(out, "Setup complete.")
	fmt.Fprintf(out, "Tip: prepend %s to PATH to prefer managed binaries.\n", dir)
	return nil
}
