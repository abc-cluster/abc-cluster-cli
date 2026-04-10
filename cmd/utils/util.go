package utils

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

// EnvOrDefault returns the value of the first environment variable in keys
// that is non-empty, or "" if none are set.
func EnvOrDefault(keys ...string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// SudoFromCmd returns true when the caller has requested elevated admin scope,
// either via the --sudo flag or the ABC_CLI_SUDO_MODE environment variable.
// The env var takes priority over the flag.
func SudoFromCmd(cmd *cobra.Command) bool {
	if os.Getenv("ABC_CLI_SUDO_MODE") != "" {
		return true
	}
	sudo, _ := cmd.Root().PersistentFlags().GetBool("sudo")
	return sudo
}

// CloudFromCmd returns true when the caller has requested infrastructure-level
// elevation, either via the --cloud flag or the ABC_CLI_CLOUD_MODE environment
// variable. The env var takes priority over the flag.
func CloudFromCmd(cmd *cobra.Command) bool {
	if os.Getenv("ABC_CLI_CLOUD_MODE") != "" {
		return true
	}
	cloud, _ := cmd.Root().PersistentFlags().GetBool("cloud")
	return cloud
}

// ClusterFromCmd returns the --cluster flag value, falling back to the
// ABC_CLUSTER environment variable.
func ClusterFromCmd(cmd *cobra.Command) string {
	if v := os.Getenv("ABC_CLUSTER"); v != "" {
		return v
	}
	cluster, _ := cmd.Root().PersistentFlags().GetString("cluster")
	return cluster
}

// SleepCh returns a channel that closes after n seconds. Use in select
// statements within polling loops to allow context cancellation.
func SleepCh(n int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(n) * time.Second)
		close(ch)
	}()
	return ch
}
