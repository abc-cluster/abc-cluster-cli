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
