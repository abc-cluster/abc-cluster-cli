package submit

import (
	"fmt"
	"os"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/spf13/cobra"
)

// nomadAddrFromCmd reads --nomad-addr from the command or its root.
func nomadAddrFromCmd(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("nomad-addr"); v != "" {
		return v
	}
	if v, _ := cmd.Root().PersistentFlags().GetString("nomad-addr"); v != "" {
		return v
	}
	if cfgAddr, _, _ := utils.NomadDefaultsFromConfig(); cfgAddr != "" {
		return cfgAddr
	}
	return "http://127.0.0.1:4646"
}

// nomadTokenFromCmd reads --nomad-token from the command or its root.
func nomadTokenFromCmd(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("nomad-token"); v != "" {
		return v
	}
	if v, _ := cmd.Root().PersistentFlags().GetString("nomad-token"); v != "" {
		return v
	}
	if _, cfgToken, _ := utils.NomadDefaultsFromConfig(); cfgToken != "" {
		return cfgToken
	}
	return ""
}

// namespaceFromCmd reads --namespace from the command or its root.
func namespaceFromCmd(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("namespace"); v != "" {
		return v
	}
	if v, _ := cmd.Root().PersistentFlags().GetString("namespace"); v != "" {
		return v
	}
	return ""
}

// readFile is a thin wrapper around os.ReadFile.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// parseMemoryMBStr parses a human memory string (e.g. "4G", "512M") into MB.
// Returns 0 on empty input or parse error.
func parseMemoryMBStr(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	s = strings.ToUpper(s)
	var multiplier int
	var numStr string
	switch {
	case strings.HasSuffix(s, "G") || strings.HasSuffix(s, "GB"):
		multiplier = 1024
		numStr = strings.TrimRight(s, "GB")
	case strings.HasSuffix(s, "M") || strings.HasSuffix(s, "MB"):
		multiplier = 1
		numStr = strings.TrimRight(s, "MB")
	case strings.HasSuffix(s, "K") || strings.HasSuffix(s, "KB"):
		multiplier = 0 // <1 MB, clamp to 1
		numStr = strings.TrimRight(s, "KB")
	default:
		numStr = s
		multiplier = 1
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(numStr), "%d", &n); err != nil {
		return 0, fmt.Errorf("cannot parse memory %q", s)
	}
	if multiplier == 0 {
		return 1, nil
	}
	return n * multiplier, nil
}
