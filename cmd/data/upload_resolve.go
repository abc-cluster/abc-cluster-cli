package data

import (
	"fmt"
	"os"
	"strings"

	cfg "github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/spf13/cobra"
)

// resolveUploadEndpoint picks the tus endpoint in priority order:
// 1) explicit --endpoint when non-empty (including when user cleared default to use context)
// 2) ABC_UPLOAD_ENDPOINT
// 3) active context upload_endpoint
// 4) abc-nodes context: admin.services.tusd.http + "/files/" (when capabilities.uploads is true)
// 5) derived from active context API endpoint (<endpoint>/files/)
// 6) derived from server URL / --url (<url>/files/)
func resolveUploadEndpoint(cmd *cobra.Command, flagEndpoint, serverURL string) (string, error) {
	if cmd.Flags().Changed("endpoint") {
		if v := strings.TrimSpace(flagEndpoint); v != "" {
			return v, nil
		}
	} else if v := strings.TrimSpace(flagEndpoint); v != "" {
		return v, nil
	}

	if v := strings.TrimSpace(os.Getenv("ABC_UPLOAD_ENDPOINT")); v != "" {
		return v, nil
	}

	c, err := cfg.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	actx := c.ActiveCtx()
	if v := strings.TrimSpace(actx.UploadEndpoint); v != "" {
		return v, nil
	}

	if actx.Capabilities != nil && actx.Capabilities.Uploads {
		if h, ok := cfg.GetAdminFloorField(&actx.Admin.Services, "tusd", "http"); ok && h != "" {
			return strings.TrimRight(h, "/") + "/files/", nil
		}
	}

	if ep := strings.TrimSpace(actx.Endpoint); ep != "" {
		if derived, err := cfg.DeriveUploadEndpointFromAPI(ep); err == nil {
			return derived, nil
		}
	}

	return resolveEndpoint("", serverURL)
}

// resolveUploadToken picks the bearer token for tus in priority order:
// 1) explicit --upload-token when non-empty
// 2) ABC_UPLOAD_TOKEN
// 3) active context upload_token
// 4) ABC_TOKEN / NOMAD_TOKEN
// 5) active context admin.services.nomad.nomad_token
// 6) root --access-token / ABC_ACCESS_TOKEN (passed as accessToken)
func resolveUploadToken(cmd *cobra.Command, flagToken, accessToken string) string {
	if cmd.Flags().Changed("upload-token") {
		if v := strings.TrimSpace(flagToken); v != "" {
			return v
		}
	} else if v := strings.TrimSpace(flagToken); v != "" {
		return v
	}

	if v := strings.TrimSpace(os.Getenv("ABC_UPLOAD_TOKEN")); v != "" {
		return v
	}

	if v := strings.TrimSpace(os.Getenv("ABC_TOKEN")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("NOMAD_TOKEN")); v != "" {
		return v
	}

	c, err := cfg.Load()
	if err == nil {
		actx := c.ActiveCtx()
		if v := strings.TrimSpace(actx.UploadToken); v != "" {
			return v
		}
		if v := strings.TrimSpace(actx.NomadToken()); v != "" {
			return v
		}
	}

	return strings.TrimSpace(accessToken)
}
