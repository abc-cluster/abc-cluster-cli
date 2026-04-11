package utils

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	// ExperimentalModeEnvVar toggles experimental CLI functionality globally.
	ExperimentalModeEnvVar = "ABC_CLI_EXP_MODE"
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

// ExpFromCmd returns true when experimental mode is active via --exp or
// ABC_CLI_EXP_MODE.
//
// The environment variable takes precedence over the flag.
// Accepted false values are: 0, false, no, off, disabled.
// Any other non-empty value enables experimental mode.
func ExpFromCmd(cmd *cobra.Command) bool {
	if raw, ok := os.LookupEnv(ExperimentalModeEnvVar); ok {
		v := strings.TrimSpace(strings.ToLower(raw))
		if v == "" {
			return false
		}
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
		switch v {
		case "no", "off", "disabled":
			return false
		default:
			return true
		}
	}
	exp, _ := cmd.Root().PersistentFlags().GetBool("exp")
	return exp
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
