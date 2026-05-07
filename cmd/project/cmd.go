// Package project registers the `abc project` command group.
package project

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/slug"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// NewCmd returns the `abc project` command group.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage research projects (top-level grouping for investigations)",
	}
	cmd.AddCommand(newCreateCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newUseCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newArchiveCmd())
	cmd.AddCommand(newCompleteCmd())
	cmd.AddCommand(newRenameCmd())
	cmd.AddCommand(newTagCmd())
	cmd.AddCommand(newDeleteCmd())
	return cmd
}

func newCreateCmd() *cobra.Command {
	var (
		description string
		tags        []string
		userSlug    string
	)
	c := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a new project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			s := userSlug
			if s == "" {
				s, err = slug.GenerateUnique(func(g string) (bool, error) {
					return state.SlugExistsProject(ctx, db, contextName, g)
				}, 5)
				if err != nil {
					return err
				}
			} else {
				if err := slug.Validate(s); err != nil {
					return err
				}
				exists, err := state.SlugExistsProject(ctx, db, contextName, s)
				if err != nil {
					return err
				}
				if exists {
					return fmt.Errorf("slug %q already in use in context %q", s, contextName)
				}
			}
			p := state.Project{
				ProjectID:   state.NewProjectID(),
				Slug:        s,
				ContextName: contextName,
				Title:       args[0],
				Description: description,
				Status:      "active",
				Tags:        tags,
			}
			p, err = state.CreateProject(ctx, db, p)
			if err != nil {
				return err
			}
			pv := p.ProjectID
			if err := state.SetActivePointer(ctx, db, contextName, state.PointerProject, &pv); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Created project %s (%s) — %q\n", p.ProjectID, p.Slug, p.Title)
			return nil
		},
	}
	c.Flags().StringVar(&description, "description", "", "optional description")
	c.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	c.Flags().StringVar(&userSlug, "slug", "", "user-supplied slug (validated)")
	return c
}

func newListCmd() *cobra.Command {
	var (
		statusFlag string
		all        bool
		output     string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		RunE: func(cm *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			s := statusFlag
			projs, err := state.ListProjects(ctx, db, contextName, s, all)
			if err != nil {
				return err
			}
			return renderProjects(cm, db, projs, output)
		},
	}
	c.Flags().StringVar(&statusFlag, "status", "active", "filter by status (active|archived|completed; empty=all)")
	c.Flags().BoolVar(&all, "all", false, "include all contexts")
	c.Flags().StringVar(&output, "output", "table", "table|json|csv")
	return c
}

func renderProjects(cm *cobra.Command, db *sql.DB, projs []state.Project, format string) error {
	ctx := cm.Context()
	type row struct {
		Slug          string `json:"slug"`
		ID            string `json:"id"`
		Status        string `json:"status"`
		Title         string `json:"title"`
		Investigations int   `json:"investigations"`
		LastActivity  string `json:"last_activity"`
	}
	rows := make([]row, 0, len(projs))
	for _, p := range projs {
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM investigations WHERE project_id = ?`, p.ProjectID).Scan(&n)
		rows = append(rows, row{
			Slug:           p.Slug,
			ID:             p.ProjectID,
			Status:         p.Status,
			Title:          p.Title,
			Investigations: n,
			LastActivity:   time.Unix(p.UpdatedAt, 0).Format(time.RFC3339),
		})
	}
	switch format {
	case "json":
		enc := json.NewEncoder(cm.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "csv":
		w := csv.NewWriter(cm.OutOrStdout())
		w.Write([]string{"slug", "id", "status", "title", "investigations", "last_activity"})
		for _, r := range rows {
			w.Write([]string{r.Slug, r.ID, r.Status, r.Title, fmt.Sprintf("%d", r.Investigations), r.LastActivity})
		}
		w.Flush()
		return nil
	default:
		tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SLUG\tID\tSTATUS\tINV\tLAST ACTIVITY\tTITLE")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", r.Slug, shortID(r.ID), r.Status, r.Investigations, r.LastActivity, r.Title)
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
	c := &cobra.Command{
		Use:   "show <slug-or-id>",
		Short: "Show project details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			p, err := state.FindProject(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			invs, err := state.ListInvestigations(ctx, db, contextName, p.ProjectID, "", false)
			if err != nil {
				return err
			}
			runCount, _ := state.CountRunsForProject(ctx, db, p.ProjectID)
			if output == "json" {
				enc := json.NewEncoder(cm.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"project":         p,
					"investigations":  invs,
					"run_count":       runCount,
				})
			}
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "ID:\t%s\n", p.ProjectID)
			fmt.Fprintf(tw, "Slug:\t%s\n", p.Slug)
			fmt.Fprintf(tw, "Title:\t%s\n", p.Title)
			fmt.Fprintf(tw, "Status:\t%s\n", p.Status)
			fmt.Fprintf(tw, "Description:\t%s\n", p.Description)
			fmt.Fprintf(tw, "Created:\t%s\n", time.Unix(p.CreatedAt, 0).Format(time.RFC3339))
			fmt.Fprintf(tw, "Updated:\t%s\n", time.Unix(p.UpdatedAt, 0).Format(time.RFC3339))
			fmt.Fprintf(tw, "Investigations:\t%d\n", len(invs))
			fmt.Fprintf(tw, "Total runs:\t%d\n", runCount)
			tw.Flush()
			fmt.Fprintln(cm.OutOrStdout(), "")
			fmt.Fprintln(cm.OutOrStdout(), "Investigations:")
			tw2 := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw2, "  SLUG\tSTATUS\tRUNS\tTITLE")
			for _, i := range invs {
				n, _ := state.CountRunsForInvestigation(ctx, db, i.InvestigationID)
				fmt.Fprintf(tw2, "  %s\t%s\t%d\t%s\n", i.Slug, i.Status, n, i.Title)
			}
			return tw2.Flush()
		},
	}
	c.Flags().StringVar(&output, "output", "table", "table|json")
	return c
}

func newUseCmd() *cobra.Command {
	var none bool
	c := &cobra.Command{
		Use:   "use <slug-or-id>",
		Short: "Set the active project for this context",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			if none {
				if err := state.SetActivePointer(ctx, db, contextName, state.PointerProject, nil); err != nil {
					return err
				}
				if err := state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, nil); err != nil {
					return err
				}
				fmt.Fprintln(cm.OutOrStdout(), "Cleared active project and investigation.")
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("supply <slug-or-id> or --none")
			}
			p, err := state.FindProject(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			// Clear active investigation if it doesn't belong to this project.
			currentInvID, _ := state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
			clearInv := true
			if currentInvID != "" {
				inv, err := state.FindInvestigation(ctx, db, contextName, currentInvID)
				if err == nil && inv.ProjectID.Valid && inv.ProjectID.String == p.ProjectID {
					clearInv = false
				}
			}
			pid := p.ProjectID
			if err := state.SetActivePointer(ctx, db, contextName, state.PointerProject, &pid); err != nil {
				return err
			}
			if clearInv && currentInvID != "" {
				if err := state.SetActivePointer(ctx, db, contextName, state.PointerInvestigation, nil); err != nil {
					return err
				}
			}
			// Re-activate archived projects on `use`.
			if p.Status == "archived" {
				if err := state.UpdateProjectFields(ctx, db, p.ProjectID, map[string]any{"status": "active"}); err != nil {
					return err
				}
			}
			fmt.Fprintf(cm.OutOrStdout(), "Active project: %s (%s)\n", p.Slug, p.ProjectID)
			return nil
		},
	}
	c.Flags().BoolVar(&none, "none", false, "clear the active project (and investigation)")
	return c
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show active context, project, investigation",
		RunE: func(cm *cobra.Command, _ []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			fmt.Fprintf(cm.OutOrStdout(), "Context:        %s\n", contextName)
			pid, _ := state.GetActivePointer(ctx, db, contextName, state.PointerProject)
			if pid == "" {
				fmt.Fprintln(cm.OutOrStdout(), "Active project: (none)")
			} else {
				p, err := state.FindProject(ctx, db, contextName, pid)
				if err != nil {
					fmt.Fprintf(cm.OutOrStdout(), "Active project: %s (resolve error: %v)\n", pid, err)
				} else {
					fmt.Fprintf(cm.OutOrStdout(), "Active project: %s — %s\n", p.Slug, p.Title)
				}
			}
			iid, _ := state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
			if iid == "" {
				fmt.Fprintln(cm.OutOrStdout(), "Active inv:     (none)")
			} else {
				i, err := state.FindInvestigation(ctx, db, contextName, iid)
				if err != nil {
					fmt.Fprintf(cm.OutOrStdout(), "Active inv:     %s (resolve error: %v)\n", iid, err)
				} else {
					fmt.Fprintf(cm.OutOrStdout(), "Active inv:     %s — %s\n", i.Slug, i.Title)
				}
			}
			return nil
		},
	}
}

func newArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <slug-or-id>",
		Short: "Archive a project (reversible via `abc project use`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			return setProjectStatus(cm, args[0], "archived")
		},
	}
}

func newCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "complete <slug-or-id>",
		Short: "Mark a project completed (sticky)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			return setProjectStatus(cm, args[0], "completed")
		},
	}
}

func setProjectStatus(cm *cobra.Command, ref, status string) error {
	db, err := state.Open()
	if err != nil {
		return err
	}
	ctx := cm.Context()
	contextName := state.ActiveContextName()
	p, err := state.FindProject(ctx, db, contextName, ref)
	if err != nil {
		return err
	}
	if err := state.UpdateProjectFields(ctx, db, p.ProjectID, map[string]any{"status": status}); err != nil {
		return err
	}
	fmt.Fprintf(cm.OutOrStdout(), "Project %s → %s\n", p.Slug, status)
	return nil
}

func newRenameCmd() *cobra.Command {
	var newSlug, newTitle, newDesc string
	c := &cobra.Command{
		Use:   "rename <slug-or-id>",
		Short: "Rename slug/title/description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if newSlug == "" && newTitle == "" && newDesc == "" {
				return fmt.Errorf("supply at least one of --slug, --title, --description")
			}
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			p, err := state.FindProject(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			fields := map[string]any{}
			if newSlug != "" {
				if err := slug.Validate(newSlug); err != nil {
					return err
				}
				exists, err := state.SlugExistsProject(ctx, db, contextName, newSlug)
				if err != nil {
					return err
				}
				if exists && newSlug != p.Slug {
					return fmt.Errorf("slug %q already in use", newSlug)
				}
				fields["slug"] = newSlug
			}
			if newTitle != "" {
				fields["title"] = newTitle
			}
			if newDesc != "" {
				fields["description"] = newDesc
			}
			if err := state.UpdateProjectFields(ctx, db, p.ProjectID, fields); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Updated project %s.\n", p.ProjectID)
			return nil
		},
	}
	c.Flags().StringVar(&newSlug, "slug", "", "new slug")
	c.Flags().StringVar(&newTitle, "title", "", "new title")
	c.Flags().StringVar(&newDesc, "description", "", "new description")
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
			p, err := state.FindProject(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			tags := append([]string{}, p.Tags...)
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
			return state.UpdateProjectFields(ctx, db, p.ProjectID, map[string]any{"tags_json": string(b)})
		},
	}
	c.Flags().StringVar(&addTag, "add", "", "tag to add")
	c.Flags().StringVar(&removeTag, "remove", "", "tag to remove")
	return c
}

func newDeleteCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "delete <slug-or-id>",
		Short: "Delete a project (cascades to investigations and annotations)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			p, err := state.FindProject(ctx, db, contextName, args[0])
			if err != nil {
				return err
			}
			if !force {
				fmt.Fprintf(cm.OutOrStdout(), "Confirm delete of project %s (%s) [y/N]: ", p.Slug, p.ProjectID)
				var resp string
				fmt.Fscanln(os.Stdin, &resp)
				if resp != "y" && resp != "Y" && resp != "yes" {
					fmt.Fprintln(cm.OutOrStdout(), "Aborted.")
					return nil
				}
			}
			if err := state.DeleteProject(ctx, db, p.ProjectID); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Deleted project %s.\n", p.Slug)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return c
}

// Used to avoid unused import in compactly built file.
var _ = context.Background
