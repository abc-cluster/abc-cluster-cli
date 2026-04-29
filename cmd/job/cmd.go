package job

import (
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// NewCmd returns the "job" subcommand group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage Nomad batch jobs",
		Long:  `Commands for submitting and managing batch jobs on the ABC-cluster platform.`,
	}
	cmd.PersistentFlags().String("nomad-addr", utils.EnvOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR; defaults to http://127.0.0.1:4646)")
	cmd.PersistentFlags().String("nomad-token", utils.EnvOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", utils.EnvOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(
		newRunCmd(),
		newTranslateCmd(),
		newListCmd(),
		newShowCmd(),
		newStopCmd(),
		newDispatchCmd(),
		newLogsCmd(),
		newStatusCmd(),
		newTraceCmd(),
	)
	return cmd
}

// NewLogsCmd exports a top-level "logs" command variant.
func NewLogsCmd() *cobra.Command {
	return newLogsCmd()
}

// nomadClientFromCmd builds a nomadClient from persistent flags.
func nomadClientFromCmd(cmd *cobra.Command) *nomadClient {
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
	if addr == "" || token == "" || region == "" {
		cfgAddr, cfgToken, cfgRegion := utils.NomadDefaultsFromConfig()
		if addr == "" {
			addr = cfgAddr
		}
		if token == "" {
			token = cfgToken
		}
		if region == "" {
			region = cfgRegion
		}
	}
	return newNomadClient(addr, token, region).
		WithSudo(utils.SudoFromCmd(cmd)).
		WithCloud(utils.CloudFromCmd(cmd))
}

// nomadAddrFromCmd returns the Nomad address string for display.
func nomadAddrFromCmd(cmd *cobra.Command) string {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	if addr == "" {
		cfgAddr, _, _ := utils.NomadDefaultsFromConfig()
		addr = cfgAddr
	}
	if addr == "" {
		return "http://127.0.0.1:4646"
	}
	return addr
}

// sleepCh delegates to the shared utility.
func sleepCh(n int) <-chan struct{} { return utils.SleepCh(n) }

// namespaceFromCmd returns the Nomad namespace to use for a command:
// 1. --namespace flag (explicit)
// 2. Active abc context admin.abc_nodes.nomad_namespace (config fallback)
// 3. Empty string (Nomad server default)
func namespaceFromCmd(cmd *cobra.Command) string {
	ns, _ := cmd.Flags().GetString("namespace")
	if strings.TrimSpace(ns) != "" {
		return ns
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return ""
	}
	ctx := cfg.ActiveCtx()
	// Prefer explicit admin.abc_nodes.nomad_namespace (works regardless of cluster_type).
	if ctx.Admin.ABCNodes != nil {
		if v := strings.TrimSpace(ctx.Admin.ABCNodes.NomadNamespace); v != "" {
			return v
		}
	}
	// Fall back to the derived namespace for abc-nodes cluster types.
	return strings.TrimSpace(ctx.AbcNodesNomadNamespaceForCLI())
}
