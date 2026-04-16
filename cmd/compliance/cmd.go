// Package compliance implements "abc compliance" — compliance posture from the ABC API.
package compliance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
)

// NewCmd returns the top-level "abc compliance" command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compliance",
		Short: "Compliance status and reports from the ABC API",
		Long: `Fetches GET /v1/compliance from the API base configured with --url (or ABC_API_ENDPOINT).

Uses --access-token / ABC_ACCESS_TOKEN and optional --workspace / ABC_WORKSPACE_ID
(workspaceId query parameter when set).`,
		RunE: runCompliance,
	}
	cmd.Flags().String("scope", "", "optional scope filter (query: scope), e.g. workspace or pipeline")
	return cmd
}

func runCompliance(cmd *cobra.Command, _ []string) error {
	u, _ := cmd.Root().PersistentFlags().GetString("url")
	tok, _ := cmd.Root().PersistentFlags().GetString("access-token")
	ws, _ := cmd.Root().PersistentFlags().GetString("workspace")

	q := url.Values{}
	if ws != "" {
		q.Set("workspaceId", ws)
	}
	if v, _ := cmd.Flags().GetString("scope"); strings.TrimSpace(v) != "" {
		q.Set("scope", strings.TrimSpace(v))
	}

	client := api.NewClient(u, tok, ws)
	body, err := client.GetV1("/v1/compliance", q)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		_, _ = cmd.OutOrStdout().Write(body)
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		return nil
	}
	_, _ = buf.WriteTo(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
	return nil
}
