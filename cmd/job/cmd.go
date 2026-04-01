package job

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

// NewCmd returns the "job" subcommand group.
func envOrDefault(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage Nomad batch jobs",
		Long:  `Commands for submitting and managing batch jobs on the ABC-cluster platform.`,
	}

	// Persistent flags shared across all job sub-commands that call Nomad.
	cmd.PersistentFlags().String("nomad-addr", envOrDefault("ABC_ADDR", "NOMAD_ADDR"),
		"Nomad API address (or set ABC_ADDR/NOMAD_ADDR; defaults to http://127.0.0.1:4646)")
	cmd.PersistentFlags().String("nomad-token", envOrDefault("ABC_TOKEN", "NOMAD_TOKEN"),
		"Nomad ACL token (or set ABC_TOKEN/NOMAD_TOKEN)")
	cmd.PersistentFlags().String("region", envOrDefault("ABC_REGION", "NOMAD_REGION"),
		"Nomad region (or set ABC_REGION/NOMAD_REGION)")

	cmd.AddCommand(
		newRunCmd(),
		newListCmd(),
		newShowCmd(),
		newStopCmd(),
		newDispatchCmd(),
		newLogsCmd(),
		newStatusCmd(),
	)
	return cmd
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
	return newNomadClient(addr, token, region)
}

// nomadAddrFromCmd returns the Nomad address string for display.
func nomadAddrFromCmd(cmd *cobra.Command) string {
	addr, _ := cmd.Flags().GetString("nomad-addr")
	if addr == "" {
		addr, _ = cmd.Root().PersistentFlags().GetString("nomad-addr")
	}
	if addr == "" {
		return "http://127.0.0.1:4646"
	}
	return addr
}

// sleepCh returns a channel that fires after n seconds. Used for polling loops.
func sleepCh(n int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(n) * time.Second)
		close(ch)
	}()
	return ch
}
