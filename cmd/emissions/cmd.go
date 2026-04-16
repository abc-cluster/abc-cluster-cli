// Package emissions implements "abc emissions" — carbon emissions from the ABC API.
package emissions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/abc-cluster/abc-cluster-cli/api"
	"github.com/spf13/cobra"
)

// NewCmd returns the top-level "abc emissions" command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emissions",
		Short: "Carbon emissions data from the ABC API",
		Long: `Fetches GET /v1/emissions from the API base configured with --url (or ABC_API_ENDPOINT).

Uses --access-token / ABC_ACCESS_TOKEN and optional --workspace / ABC_WORKSPACE_ID
(workspaceId query parameter when set).`,
		RunE: runEmissions,
	}
	cmd.Flags().String("from", "", "optional reporting window start (query: from)")
	cmd.Flags().String("to", "", "optional reporting window end (query: to)")
	return cmd
}

func runEmissions(cmd *cobra.Command, _ []string) error {
	u, _ := cmd.Root().PersistentFlags().GetString("url")
	tok, _ := cmd.Root().PersistentFlags().GetString("access-token")
	ws, _ := cmd.Root().PersistentFlags().GetString("workspace")

	q := url.Values{}
	if ws != "" {
		q.Set("workspaceId", ws)
	}
	if v, _ := cmd.Flags().GetString("from"); strings.TrimSpace(v) != "" {
		q.Set("from", strings.TrimSpace(v))
	}
	if v, _ := cmd.Flags().GetString("to"); strings.TrimSpace(v) != "" {
		q.Set("to", strings.TrimSpace(v))
	}

	client := api.NewClient(u, tok, ws)
	body, err := client.GetV1("/v1/emissions", q)
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
