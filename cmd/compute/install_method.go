package compute

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	packageInstallMethodStatic         = "static"
	packageInstallMethodPackageManager = "package-manager"
)

func normalizePackageInstallMethod(v string) (string, error) {
	method := strings.ToLower(strings.TrimSpace(v))
	switch method {
	case "", packageInstallMethodStatic:
		return packageInstallMethodStatic, nil
	case "package", "packages", "script", packageInstallMethodPackageManager:
		return packageInstallMethodPackageManager, nil
	default:
		return "", fmt.Errorf("invalid --package-install-method %q (supported: %s, %s)", v, packageInstallMethodStatic, packageInstallMethodPackageManager)
	}
}

func resolvePackageInstallMethodFlag(cmd *cobra.Command) (string, error) {
	method, _ := cmd.Flags().GetString("package-install-method")
	legacyMethod, _ := cmd.Flags().GetString("tailscale-install-method")

	if cmd.Flags().Changed("tailscale-install-method") {
		if cmd.Flags().Changed("package-install-method") && method != legacyMethod {
			return "", fmt.Errorf("conflicting flags: --package-install-method=%q and --tailscale-install-method=%q", method, legacyMethod)
		}
		method = legacyMethod
	}

	return normalizePackageInstallMethod(method)
}
