// Package investigation registers `abc project investigation` verbs (canonical
// path; relocated from the standalone `abc investigation` group on 2026-05-08).
package investigation

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/slug"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// NewCmd returns the canonical `investigation` Cobra tree. Registered as a
// subcommand of `abc project` (also as the `inv` alias via NewInvAlias).
func NewCmd() *cobra.Command {
	return buildCmd("investigation",
		"Manage research investigations (branchable, mergeable explorations)")
}

// NewInvAlias returns the same Cobra tree under the short name `inv`. Wired
// as a sibling of `investigation` under `abc project`.
func NewInvAlias() *cobra.Command {
	return buildCmd("inv", "Alias for `abc project investigation`")
}

// NewDeprecatedRootAlias returns the tree wired under the legacy root-level
// path `abc investigation`, and emits a one-line deprecation note at
// invocation. Kept for one release after the move under `abc project`.
func NewDeprecatedRootAlias() *cobra.Command {
	cmd := buildCmd("investigation",
		"(deprecated) moved to `abc project investigation`")
	cmd.PersistentPreRun = func(c *cobra.Command, _ []string) {
		fmt.Fprintln(os.Stderr, "[abc] note: 'abc investigation' has moved to 'abc project investigation'; alias kept for one release")
	}
	return cmd
}

// buildCmd constructs the investigation Cobra tree. Re-invoked per
// registration so each parent gets its own subtree (Cobra does not allow
// command sharing across multiple parents).
func buildCmd(use, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
	}
	cmd.AddCommand(newCreateCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newUseCmd())
	cmd.AddCommand(newBranchCmd())
	cmd.AddCommand(newAnnotateCmd())
	cmd.AddCommand(newDeadEndCmd())
	cmd.AddCommand(newMergeCmd())
	cmd.AddCommand(newTreeCmd())
	cmd.AddCommand(newRenameCmd())
	cmd.AddCommand(newTagCmd())
	cmd.AddCommand(newVisualizeCmd())
	cmd.AddCommand(newExportCmd())
	cmd.AddCommand(newDiffCmd())
	cmd.AddCommand(newAnnotationCmd())
	return cmd
}

func newCreateCmd() *cobra.Command {
	var (
		projectRef string
		tags       []string
		userSlug   string
		noProject  bool
	)
	c := &cobra.Command{
		Use:   "create <title>",
		Short: "Create an investigation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			var projectID sql.NullString
			if !noProject {
				ref := projectRef
				if ref == "" {
					pid, _ := state.GetActivePointer(ctx, db, contextName, state.PointerProject)
					ref = pid
				}
				if ref == "" {
					return fmt.Errorf("no active project — pass --project=<slug> or --no-project to create a project-less investigation")
				}
				p, err := state.FindProject(ctx, db, contextName, ref)
				if err != nil {
					return err
				}
				projectID = sql.NullString{String: p.ProjectID, Valid: true}
			}
			s := userSlug
			if s == "" {
				s, err = slug.GenerateUnique(func(g string) (bool, error) {
					return state.SlugExistsInvestigation(ctx, db, contextName, g)
				}, 5)
				if err != nil {
					return err
				}
			} else {
				if err := slug.Validate(s); err != nil {
					return err
				}
				exists, err := state.SlugExistsInvestigation(ctx, db, contextName, s)
				if err != nil {
					return err
				}
				if exists {
					return fmt.Errorf("name %q already in use", s)
				}
			}
			inv := state.Investigation{
				InvestigationID: state.NewInvestigationID(),
				Slug:            s,
				ContextName:     contextName,
				ProjectID:       projectID,
				Title:           args[0],
				Status:          "active",
				Tags:            tags,
			}
			inv, err = state.CreateInvestigation(ctx, db, inv)
			if err != nil {
				return err
			}
			iv := inv.InvestigationID
			if err := state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, &iv); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Created investigation %s (%s) — %q\n", inv.InvestigationID, inv.Slug, inv.Title)
			return nil
		},
	}
	c.Flags().StringVar(&projectRef, "project", "", "project slug or ID (defaults to active project)")
	c.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	c.Flags().StringVar(&userSlug, "name", "", "user-supplied name (validated; auto-generated if omitted)")
	c.Flags().BoolVar(&noProject, "no-project", false, "create a project-less investigation")
	return c
}

func newListCmd() *cobra.Command {
	var (
		projectRef  string
		statusFlag  string
		allProjects bool
		output      string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List investigations",
		RunE: func(cm *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			pid := ""
			if !allProjects {
				ref := projectRef
				if ref == "" {
					ref, _ = state.GetActivePointer(ctx, db, contextName, state.PointerProject)
				}
				if ref != "" {
					p, err := state.FindProject(ctx, db, contextName, ref)
					if err != nil {
						return err
					}
					pid = p.ProjectID
				}
			}
			invs, err := state.ListInvestigations(ctx, db, contextName, pid, statusFlag, allProjects)
			if err != nil {
				return err
			}
			return renderInvestigations(cm, db, invs, output)
		},
	}
	c.Flags().StringVar(&projectRef, "project", "", "project slug or ID")
	c.Flags().StringVar(&statusFlag, "status", "", "filter by status (active|dead-end|merged|archived)")
	c.Flags().BoolVar(&allProjects, "all-projects", false, "include all projects in this context")
	c.Flags().StringVar(&output, "output", "table", "table|json|csv")
	return c
}

func renderInvestigations(cm *cobra.Command, db *sql.DB, invs []state.Investigation, format string) error {
	ctx := cm.Context()
	type row struct {
		Slug      string `json:"slug"`
		ID        string `json:"id"`
		Status    string `json:"status"`
		ProjectID string `json:"project_id,omitempty"`
		ParentID  string `json:"parent_id,omitempty"`
		Runs      int    `json:"runs"`
		Title     string `json:"title"`
	}
	rows := make([]row, 0, len(invs))
	for _, i := range invs {
		n, _ := state.CountRunsForInvestigation(ctx, db, i.InvestigationID)
		r := row{Slug: i.Slug, ID: i.InvestigationID, Status: i.Status, Runs: n, Title: i.Title}
		if i.ProjectID.Valid {
			r.ProjectID = i.ProjectID.String
		}
		if i.ParentID.Valid {
			r.ParentID = i.ParentID.String
		}
		rows = append(rows, r)
	}
	switch format {
	case "json":
		enc := json.NewEncoder(cm.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "csv":
		w := csv.NewWriter(cm.OutOrStdout())
		w.Write([]string{"slug", "id", "status", "project_id", "parent_id", "runs", "title"})
		for _, r := range rows {
			w.Write([]string{r.Slug, r.ID, r.Status, r.ProjectID, r.ParentID, fmt.Sprintf("%d", r.Runs), r.Title})
		}
		w.Flush()
		return nil
	default:
		tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SLUG\tID\tSTATUS\tRUNS\tTITLE")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.Slug, shortID(r.ID), r.Status, r.Runs, r.Title)
		}
		return tw.Flush()
	}
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func newShowCmd() *cobra.Command {
	var output string
	var tuiFlag bool
	c := &cobra.Command{
		Use:   "show [<slug-or-id>]",
		Short: "Show investigation details",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			} else {
				ref, _ = state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
				if ref == "" {
					return fmt.Errorf("no active investigation — supply <slug-or-id>")
				}
			}
			inv, err := state.FindInvestigation(ctx, db, contextName, ref)
			if err != nil {
				return err
			}
			runs, _ := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
			anns, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
			children, _ := state.ChildInvestigations(ctx, db, inv.InvestigationID)
			if output == "json" {
				enc := json.NewEncoder(cm.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"investigation": inv,
					"runs":          runs,
					"annotations":   anns,
					"children":      children,
				})
			}
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "ID:\t%s\n", inv.InvestigationID)
			fmt.Fprintf(tw, "Slug:\t%s\n", inv.Slug)
			fmt.Fprintf(tw, "Title:\t%s\n", inv.Title)
			fmt.Fprintf(tw, "Status:\t%s\n", inv.Status)
			if inv.ParentID.Valid {
				fmt.Fprintf(tw, "Parent:\t%s\n", inv.ParentID.String)
			}
			if inv.MergedInto.Valid {
				fmt.Fprintf(tw, "Merged into:\t%s\n", inv.MergedInto.String)
			}
			if inv.DeadEndReason.Valid {
				fmt.Fprintf(tw, "Dead-end reason:\t%s\n", inv.DeadEndReason.String)
			}
			fmt.Fprintf(tw, "Created:\t%s\n", time.Unix(inv.CreatedAt, 0).Format(time.RFC3339))
			fmt.Fprintf(tw, "Children:\t%d\n", len(children))
			fmt.Fprintf(tw, "Runs:\t%d\n", len(runs))
			fmt.Fprintf(tw, "Annotations:\t%d\n", len(anns))
			tw.Flush()
			if tuiFlag {
				fmt.Fprintln(cm.OutOrStdout(), "(TUI deferred to a follow-up commit; --tui currently a no-op.)")
			}
			return nil
		},
	}
	c.Flags().StringVar(&output, "output", "table", "table|json")
	c.Flags().BoolVar(&tuiFlag, "tui", false, "open the interactive TUI (deferred)")
	return c
}

func newUseCmd() *cobra.Command {
	var none bool
	c := &cobra.Command{
		Use:   "use [<slug-or-id>]",
		Short: "Set the active investigation",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			if none {
				return state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, nil)
			}
			if len(args) == 0 {
				return fmt.Errorf("supply <slug-or-id> or --none")
			}
			inv, err := state.FindInvestigation(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			iv := inv.InvestigationID
			if err := state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, &iv); err != nil {
				return err
			}
			if inv.ProjectID.Valid {
				pid := inv.ProjectID.String
				if err := state.SetActivePointer(ctx, db, contextName, state.PointerProject, &pid); err != nil {
					return err
				}
			}
			fmt.Fprintf(cm.OutOrStdout(), "Active investigation: %s (%s)\n", inv.Slug, inv.InvestigationID)
			return nil
		},
	}
	c.Flags().BoolVar(&none, "none", false, "clear the active investigation")
	return c
}

func newBranchCmd() *cobra.Command {
	var tags []string
	c := &cobra.Command{
		Use:   "branch <parent-slug-or-id> <title>",
		Short: "Create a child investigation branched from a parent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			parent, err := state.FindInvestigation(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			s, err := slug.GenerateUnique(func(g string) (bool, error) {
				return state.SlugExistsInvestigation(ctx, db, contextName, g)
			}, 5)
			if err != nil {
				return err
			}
			child := state.Investigation{
				InvestigationID: state.NewInvestigationID(),
				Slug:            s,
				ContextName:     contextName,
				ProjectID:       parent.ProjectID,
				ParentID:        sql.NullString{String: parent.InvestigationID, Valid: true},
				Title:           args[1],
				Status:          "active",
				Tags:            tags,
			}
			if _, err := state.CreateInvestigation(ctx, db, child); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Branched %s → %s (%s)\n", parent.Slug, child.Slug, child.InvestigationID)
			fmt.Fprintln(cm.OutOrStdout(), "Active investigation unchanged. Run `abc investigation use "+child.Slug+"` to switch.")
			return nil
		},
	}
	c.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	return c
}

func newAnnotateCmd() *cobra.Command {
	var (
		tag      string
		note     string
		citesArr []string
	)
	c := &cobra.Command{
		Use:   "annotate <slug-or-id>",
		Short: "Add an annotation to an investigation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
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
			body := note
			if body == "" {
				edited, editTag, err := openEditorTemplate(tag)
				if err != nil {
					return err
				}
				if strings.TrimSpace(edited) == "" {
					fmt.Fprintln(cm.OutOrStdout(), "Empty body; aborted.")
					return nil
				}
				body = edited
				if editTag != "" && tag == "" {
					tag = editTag
				}
			}
			a := state.Annotation{
				AnnotationID:    state.NewAnnotationID(),
				InvestigationID: inv.InvestigationID,
				Body:            body,
			}
			if tag != "" {
				a.Tag = sql.NullString{String: tag, Valid: true}
			}
			if _, err := state.AddAnnotation(ctx, db, a); err != nil {
				return err
			}
			// Process citations.
			for _, cstr := range citesArr {
				parts := strings.SplitN(cstr, ":", 2)
				targetInv := parts[0]
				targetAnn := ""
				if len(parts) == 2 {
					targetAnn = parts[1]
				}
				tinv, err := state.FindInvestigation(ctx, db, contextName, targetInv)
				if err != nil {
					return fmt.Errorf("--cites target investigation %q: %w", targetInv, err)
				}
				cit := state.Citation{
					SourceAnnotationID:  a.AnnotationID,
					TargetInvestigation: tinv.InvestigationID,
				}
				if targetAnn != "" {
					ta, err := state.FindAnnotation(ctx, db, tinv.InvestigationID, targetAnn)
					if err != nil {
						return fmt.Errorf("--cites target annotation %q: %w", targetAnn, err)
					}
					cit.TargetAnnotationID = sql.NullString{String: ta.AnnotationID, Valid: true}
				}
				if err := state.AddCitation(ctx, db, cit); err != nil {
					return err
				}
			}
			fmt.Fprintf(cm.OutOrStdout(), "Added annotation %s to %s.\n", a.AnnotationID, inv.Slug)
			return nil
		},
	}
	c.Flags().StringVar(&tag, "tag", "", "annotation tag (e.g. hypothesis, insight, decision)")
	c.Flags().StringVar(&note, "note", "", "one-shot annotation body (skips editor)")
	c.Flags().StringArrayVar(&citesArr, "cites", nil, "<inv-slug>:<annotation-id> citation, repeatable")
	return c
}

func newDeadEndCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use:   "dead-end <slug-or-id>",
		Short: "Mark an investigation as a dead-end",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if reason == "" {
				return fmt.Errorf("--reason is required")
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
			if inv.Status != "active" {
				return fmt.Errorf("can only dead-end an active investigation; current status: %s", inv.Status)
			}
			if err := state.UpdateInvestigationFields(ctx, db, inv.InvestigationID, map[string]any{
				"status":          "dead-end",
				"dead_end_reason": reason,
			}); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "%s marked dead-end: %s\n", inv.Slug, reason)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", "why this branch is closing (required)")
	return c
}

func newMergeCmd() *cobra.Command {
	var (
		intoRef string
		note    string
	)
	c := &cobra.Command{
		Use:   "merge <child-slug-or-id>",
		Short: "Merge a child investigation back into a parent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if intoRef == "" {
				return fmt.Errorf("--into <parent> is required")
			}
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			child, err := state.FindInvestigation(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			parent, err := state.FindInvestigation(ctx, db, contextName, intoRef)
			if err != nil {
				return err
			}
			anns, err := state.ListAnnotations(ctx, db, child.InvestigationID)
			if err != nil {
				return err
			}
			for _, a := range anns {
				copy := state.Annotation{
					AnnotationID:    state.NewAnnotationID(),
					InvestigationID: parent.InvestigationID,
					Tag:             a.Tag,
					Body:            a.Body,
					CarriedFrom:     sql.NullString{String: a.AnnotationID, Valid: true},
				}
				if _, err := state.AddAnnotation(ctx, db, copy); err != nil {
					return err
				}
			}
			if note != "" {
				_, err := state.AddAnnotation(ctx, db, state.Annotation{
					AnnotationID:    state.NewAnnotationID(),
					InvestigationID: parent.InvestigationID,
					Tag:             sql.NullString{String: "merge-note", Valid: true},
					Body:            note,
				})
				if err != nil {
					return err
				}
			}
			if err := state.UpdateInvestigationFields(ctx, db, child.InvestigationID, map[string]any{
				"status":      "merged",
				"merged_into": parent.InvestigationID,
			}); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Merged %s into %s (%d annotations carried).\n", child.Slug, parent.Slug, len(anns))
			return nil
		},
	}
	c.Flags().StringVar(&intoRef, "into", "", "parent investigation slug or ID")
	c.Flags().StringVar(&note, "note", "", "optional merge note (added as merge-note annotation on parent)")
	return c
}

func newRenameCmd() *cobra.Command {
	var newName, newTitle string
	c := &cobra.Command{
		Use:   "rename <name-or-id>",
		Short: "Rename name/title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if newName == "" && newTitle == "" {
				return fmt.Errorf("supply --name or --title")
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
			fields := map[string]any{}
			if newName != "" {
				if err := slug.Validate(newName); err != nil {
					return err
				}
				exists, err := state.SlugExistsInvestigation(ctx, db, contextName, newName)
				if err != nil {
					return err
				}
				if exists && newName != inv.Slug {
					return fmt.Errorf("name %q already in use", newName)
				}
				fields["slug"] = newName
			}
			if newTitle != "" {
				fields["title"] = newTitle
			}
			return state.UpdateInvestigationFields(ctx, db, inv.InvestigationID, fields)
		},
	}
	c.Flags().StringVar(&newName, "name", "", "new name")
	c.Flags().StringVar(&newTitle, "title", "", "new title")
	return c
}

func newTagCmd() *cobra.Command {
	var addTag, removeTag string
	c := &cobra.Command{
		Use:   "tag <slug-or-id>",
		Short: "Add or remove a tag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if addTag == "" && removeTag == "" {
				return fmt.Errorf("supply --add or --remove")
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
			tags := append([]string{}, inv.Tags...)
			if removeTag != "" {
				out := []string{}
				for _, t := range tags {
					if t != removeTag {
						out = append(out, t)
					}
				}
				tags = out
			}
			if addTag != "" {
				present := false
				for _, t := range tags {
					if t == addTag {
						present = true
						break
					}
				}
				if !present {
					tags = append(tags, addTag)
				}
			}
			b, _ := json.Marshal(tags)
			return state.UpdateInvestigationFields(ctx, db, inv.InvestigationID, map[string]any{"tags_json": string(b)})
		},
	}
	c.Flags().StringVar(&addTag, "add", "", "tag to add")
	c.Flags().StringVar(&removeTag, "remove", "", "tag to remove")
	return c
}

func newTreeCmd() *cobra.Command {
	var maxDepth int
	c := &cobra.Command{
		Use:   "tree [<root-slug-or-id>]",
		Short: "ASCII tree of an investigation and its branches",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			} else {
				ref, _ = state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
				if ref == "" {
					return fmt.Errorf("no active investigation — supply <root>")
				}
			}
			root, err := state.FindInvestigation(ctx, db, contextName, ref)
			if err != nil {
				return err
			}
			// Walk to the topmost ancestor.
			cur := root
			for cur.ParentID.Valid {
				next, err := state.FindInvestigation(ctx, db, contextName, cur.ParentID.String)
				if err != nil {
					break
				}
				cur = next
			}
			return printTree(cm, db, cur, "", maxDepth, 0)
		},
	}
	c.Flags().IntVar(&maxDepth, "max-depth", 0, "max depth (0 = unlimited)")
	return c
}

func printTree(cm *cobra.Command, db *sql.DB, inv state.Investigation, prefix string, maxDepth, depth int) error {
	ctx := cm.Context()
	runs, _ := state.CountRunsForInvestigation(ctx, db, inv.InvestigationID)
	anns, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
	carriedN := 0
	for _, a := range anns {
		if a.CarriedFrom.Valid {
			carriedN++
		}
	}
	statusBadge := inv.Status
	if inv.MergedInto.Valid {
		statusBadge = "merged→" + inv.MergedInto.String[:min(12, len(inv.MergedInto.String))]
	}
	fmt.Fprintf(cm.OutOrStdout(), "%s%s [%s] runs=%d ann=%d carried=%d  %s\n",
		prefix, inv.Slug, statusBadge, runs, len(anns), carriedN, inv.Title)
	if maxDepth > 0 && depth+1 >= maxDepth {
		return nil
	}
	children, err := state.ChildInvestigations(ctx, db, inv.InvestigationID)
	if err != nil {
		return err
	}
	for i, c := range children {
		branch := "├── "
		next := "│   "
		if i == len(children)-1 {
			branch = "└── "
			next = "    "
		}
		if err := printTree(cm, db, c, prefix+next[:0]+branch, maxDepth, depth+1); err != nil {
			return err
		}
		_ = next
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Diff is intentionally a small implementation to satisfy spec coverage.
func newDiffCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "diff <a> <b>",
		Short: "Side-by-side comparison of two investigations",
		Args:  cobra.ExactArgs(2),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			a, err := state.FindInvestigation(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			b, err := state.FindInvestigation(ctx, db, contextName, args[1])
			if err != nil {
				return err
			}
			runsA, _ := state.ListRunsForInvestigation(ctx, db, a.InvestigationID)
			runsB, _ := state.ListRunsForInvestigation(ctx, db, b.InvestigationID)
			annsA, _ := state.ListAnnotations(ctx, db, a.InvestigationID)
			annsB, _ := state.ListAnnotations(ctx, db, b.InvestigationID)
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "FIELD\t%s\t%s\n", a.Slug, b.Slug)
			fmt.Fprintf(tw, "title\t%s\t%s\n", a.Title, b.Title)
			fmt.Fprintf(tw, "status\t%s\t%s\n", a.Status, b.Status)
			fmt.Fprintf(tw, "runs\t%d\t%d\n", len(runsA), len(runsB))
			fmt.Fprintf(tw, "annotations\t%d\t%d\n", len(annsA), len(annsB))
			tw.Flush()
			// Workload-set diff
			setA := map[string]bool{}
			for _, r := range runsA {
				setA[r.WorkloadRef] = true
			}
			setB := map[string]bool{}
			for _, r := range runsB {
				setB[r.WorkloadRef] = true
			}
			fmt.Fprintln(cm.OutOrStdout(), "")
			fmt.Fprintln(cm.OutOrStdout(), "Workloads:")
			for w := range setA {
				if !setB[w] {
					fmt.Fprintf(cm.OutOrStdout(), "  - only in %s: %s\n", a.Slug, w)
				}
			}
			for w := range setB {
				if !setA[w] {
					fmt.Fprintf(cm.OutOrStdout(), "  - only in %s: %s\n", b.Slug, w)
				}
			}
			return nil
		},
	}
	return c
}
