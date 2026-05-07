package investigation

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	var format, outputPath string
	c := &cobra.Command{
		Use:   "export <slug-or-id>",
		Short: "Export an investigation to ro-crate, markdown, or json",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if outputPath == "" {
				return fmt.Errorf("--output is required")
			}
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			inv, err := state.FindInvestigation(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			runs, _ := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
			anns, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
			children, _ := state.ChildInvestigations(ctx, db, inv.InvestigationID)
			payload := map[string]any{
				"investigation": inv,
				"runs":          runs,
				"annotations":   anns,
				"children":      children,
				"exported_at":   time.Now().UTC().Format(time.RFC3339),
			}
			switch format {
			case "json":
				b, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					return err
				}
				return os.WriteFile(outputPath, b, 0o644)
			case "markdown":
				return os.WriteFile(outputPath, []byte(renderMarkdown(inv, runs, anns, children)), 0o644)
			case "ro-crate":
				return writeRoCrateZip(outputPath, inv, runs, anns, children)
			default:
				return fmt.Errorf("--format must be one of ro-crate|markdown|json")
			}
		},
	}
	c.Flags().StringVar(&format, "format", "markdown", "ro-crate|markdown|json")
	c.Flags().StringVar(&outputPath, "output", "", "destination path")
	return c
}

func renderMarkdown(inv state.Investigation, runs []state.Run, anns []state.Annotation, children []state.Investigation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Investigation %s — %s\n\n", inv.Slug, inv.Title)
	fmt.Fprintf(&b, "- ID: `%s`\n- Status: %s\n- Created: %s\n",
		inv.InvestigationID, inv.Status, time.Unix(inv.CreatedAt, 0).Format(time.RFC3339))
	if inv.ParentID.Valid {
		fmt.Fprintf(&b, "- Parent: `%s`\n", inv.ParentID.String)
	}
	if inv.MergedInto.Valid {
		fmt.Fprintf(&b, "- Merged into: `%s`\n", inv.MergedInto.String)
	}
	if inv.DeadEndReason.Valid {
		fmt.Fprintf(&b, "- Dead-end reason: %s\n", inv.DeadEndReason.String)
	}
	fmt.Fprintf(&b, "\n## Annotations\n\n")
	for _, a := range anns {
		tag := ""
		if a.Tag.Valid {
			tag = " — " + a.Tag.String
		}
		carried := ""
		if a.CarriedFrom.Valid {
			carried = " (carried from " + a.CarriedFrom.String + ")"
		}
		fmt.Fprintf(&b, "### %s %s%s%s\n\n%s\n\n",
			time.Unix(a.CreatedAt, 0).Format(time.RFC3339), a.AnnotationID, tag, carried, a.Body)
	}
	fmt.Fprintf(&b, "## Runs\n\n")
	for _, r := range runs {
		ver := ""
		if r.WorkloadVersion.Valid {
			ver = "@" + r.WorkloadVersion.String
		}
		fmt.Fprintf(&b, "- `%s` %s%s — submitted %s — status %s\n", r.RunID, r.WorkloadRef, ver,
			time.Unix(r.SubmittedAt, 0).Format(time.RFC3339), r.Status)
	}
	if len(children) > 0 {
		fmt.Fprintf(&b, "\n## Branches\n\n")
		for _, c := range children {
			fmt.Fprintf(&b, "- `%s` %s — %s\n", c.InvestigationID, c.Slug, c.Status)
		}
	}
	return b.String()
}

func writeRoCrateZip(path string, inv state.Investigation, runs []state.Run, anns []state.Annotation, children []state.Investigation) error {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	// Minimal RO-Crate metadata.
	graph := []map[string]any{
		{
			"@type":      "CreativeWork",
			"@id":        "ro-crate-metadata.json",
			"about":      map[string]string{"@id": "./"},
			"conformsTo": map[string]string{"@id": "https://w3id.org/ro/crate/1.1"},
		},
		{
			"@id":         "./",
			"@type":       "Dataset",
			"name":        inv.Title,
			"identifier":  inv.InvestigationID,
			"description": fmt.Sprintf("Investigation %s exported by abc-cluster-cli", inv.Slug),
		},
	}
	for _, r := range runs {
		graph = append(graph, map[string]any{
			"@id":          r.RunID + "/",
			"@type":        "ComputationalWorkflow",
			"name":         r.WorkloadRef,
			"dateCreated":  time.Unix(r.SubmittedAt, 0).Format(time.RFC3339),
			"runStatus":    r.Status,
		})
	}
	meta := map[string]any{
		"@context": "https://w3id.org/ro/crate/1.1/context",
		"@graph":   graph,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	mf, err := zw.Create("ro-crate-metadata.json")
	if err != nil {
		return err
	}
	mf.Write(metaBytes)

	// annotations.md
	af, err := zw.Create("annotations.md")
	if err != nil {
		return err
	}
	af.Write([]byte(renderMarkdown(inv, runs, anns, children)))

	// per-run folders with params.json + status.txt
	for _, r := range runs {
		paramsName := fmt.Sprintf("%s/params.json", r.RunID)
		f, err := zw.Create(paramsName)
		if err != nil {
			return err
		}
		params := "{}"
		if r.ParamsJSON.Valid {
			params = r.ParamsJSON.String
		}
		f.Write([]byte(params))
		sf, err := zw.Create(fmt.Sprintf("%s/status.txt", r.RunID))
		if err != nil {
			return err
		}
		fmt.Fprintf(sf, "run_id=%s\nstatus=%s\nworkload=%s\n", r.RunID, r.Status, r.WorkloadRef)
	}

	// branch tree ascii
	tf, err := zw.Create("tree.txt")
	if err != nil {
		return err
	}
	fmt.Fprintf(tf, "%s [%s]\n", inv.Slug, inv.Status)
	for _, c := range children {
		fmt.Fprintf(tf, "├── %s [%s]\n", c.Slug, c.Status)
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
