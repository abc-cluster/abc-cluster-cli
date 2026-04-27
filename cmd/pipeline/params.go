package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newParamsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params <name>",
		Short: "Show parameter schema for a saved pipeline",
		Long: `Fetch and display the Nextflow parameter schema for a saved pipeline.

The schema is read from the pipeline's GitHub repository
(nextflow_schema.json at the saved revision, or HEAD if no revision is saved).
Saved parameter overrides are shown alongside the schema defaults.

  abc pipeline params rnaseq
  abc pipeline params rnaseq --json`,
		Args: cobra.ExactArgs(1),
		RunE: runParams,
	}
	cmd.Flags().Bool("json", false, "Output raw nextflow_schema.json")
	return cmd
}

func runParams(cmd *cobra.Command, args []string) error {
	name := args[0]
	ns := namespaceFromCmd(cmd)
	nc := nomadClientFromCmd(cmd)
	asJSON, _ := cmd.Flags().GetBool("json")

	ctx := cmd.Context()

	// Load the saved pipeline spec so we know repository + revision + saved params.
	spec, err := loadPipeline(ctx, nc, name, ns)
	if err != nil {
		return fmt.Errorf("load pipeline %q: %w", name, err)
	}

	repo := spec.Repository
	revision := spec.Revision
	if revision == "" {
		revision = "HEAD"
	}

	schemaURL := buildSchemaURL(repo, revision)
	if schemaURL == "" {
		return fmt.Errorf(
			"cannot derive schema URL for repository %q\n"+
				"  Expected format: owner/repo or https://github.com/owner/repo",
			repo,
		)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "  Fetching %s\n", schemaURL)
	schema, rawBytes, err := fetchSchema(schemaURL)
	if err != nil {
		return fmt.Errorf("fetch schema: %w", err)
	}

	if asJSON {
		_, err = cmd.OutOrStdout().Write(rawBytes)
		return err
	}

	printParamTable(cmd, schema, spec.Params)
	return nil
}

// buildSchemaURL converts a pipeline repository reference to a raw GitHub URL
// for nextflow_schema.json.
func buildSchemaURL(repo, revision string) string {
	// Normalise shorthand (e.g. "nf-core/rnaseq") to a full URL.
	if !strings.Contains(repo, "://") {
		repo = "https://github.com/" + strings.TrimPrefix(repo, "/")
	}
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimRight(repo, "/")

	// Only GitHub is supported for raw content.
	if !strings.Contains(repo, "github.com") {
		return ""
	}

	// https://github.com/owner/repo → https://raw.githubusercontent.com/owner/repo
	raw := strings.Replace(repo, "github.com", "raw.githubusercontent.com", 1)
	return raw + "/" + revision + "/nextflow_schema.json"
}

// nfSchema is a partial representation of nextflow_schema.json (JSON Schema draft-07).
type nfSchema struct {
	Title       string                     `json:"title"`
	Description string                     `json:"description"`
	Definitions map[string]nfSchemaDef     `json:"definitions"`
	Properties  map[string]nfSchemaParam   `json:"properties"`
}

type nfSchemaDef struct {
	Title       string                   `json:"title"`
	Properties  map[string]nfSchemaParam `json:"properties"`
}

type nfSchemaParam struct {
	Type        string `json:"type"`
	Default     any    `json:"default"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	Enum        []any  `json:"enum"`
}

func fetchSchema(schemaURL string) (*nfSchema, []byte, error) {
	cl := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, schemaURL, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, fmt.Errorf("nextflow_schema.json not found at %s (HTTP 404)\n  The pipeline may not use nf-core schema conventions", schemaURL)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d fetching schema", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap
	if err != nil {
		return nil, nil, err
	}
	var schema nfSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, fmt.Errorf("parse schema JSON: %w", err)
	}
	return &schema, raw, nil
}

// printParamTable renders schema parameters as an aligned table,
// annotating params that have a saved override in savedParams.
func printParamTable(cmd *cobra.Command, schema *nfSchema, savedParams map[string]any) {
	out := cmd.OutOrStdout()

	if schema.Title != "" {
		fmt.Fprintf(out, "\n  %s\n", schema.Title)
	}
	if schema.Description != "" {
		fmt.Fprintf(out, "  %s\n", schema.Description)
	}
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  PARAM\tTYPE\tDEFAULT\tSAVED\tDESCRIPTION")
	fmt.Fprintln(w, "  -----\t----\t-------\t-----\t-----------")

	printParams := func(props map[string]nfSchemaParam) {
		// Stable sort: alphabetical within each group.
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		// sort.Strings is available via stdlib but we avoid the import by
		// using a simple insertion sort for the handful of params.
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		for _, k := range keys {
			p := props[k]
			if p.Hidden {
				continue
			}
			defVal := ""
			if p.Default != nil {
				defVal = fmt.Sprintf("%v", p.Default)
			}
			savedVal := ""
			if savedParams != nil {
				if sv, ok := savedParams[k]; ok {
					savedVal = fmt.Sprintf("%v", sv)
				}
			}
			desc := strings.ReplaceAll(p.Description, "\n", " ")
			if len(desc) > 80 {
				desc = desc[:77] + "..."
			}
			fmt.Fprintf(w, "  --%s\t%s\t%s\t%s\t%s\n", k, p.Type, defVal, savedVal, desc)
		}
	}

	// Print top-level properties first (if any).
	if len(schema.Properties) > 0 {
		printParams(schema.Properties)
	}

	// Then each definitions group.
	groups := make([]string, 0, len(schema.Definitions))
	for g := range schema.Definitions {
		groups = append(groups, g)
	}
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0 && groups[j] < groups[j-1]; j-- {
			groups[j], groups[j-1] = groups[j-1], groups[j]
		}
	}
	for _, g := range groups {
		def := schema.Definitions[g]
		if len(def.Properties) == 0 {
			continue
		}
		title := def.Title
		if title == "" {
			title = g
		}
		fmt.Fprintf(w, "\n  [%s]\n", title)
		printParams(def.Properties)
	}

	w.Flush()

	if len(savedParams) > 0 {
		fmt.Fprintf(out, "\n  %d saved parameter(s) are applied on run.\n", len(savedParams))
	}
}
