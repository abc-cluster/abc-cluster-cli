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
	"github.com/abc-cluster/abc-cluster-cli/internal/style"
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
		userName    string
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
			s := userName
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
					return fmt.Errorf("name %q already in use in context %q", s, contextName)
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
			fmt.Fprintf(cm.OutOrStdout(), "%s %s (%s) — %q\n",
				style.G("Created project"), p.ProjectID, p.Slug, p.Title)
			return nil
		},
	}
	c.Flags().StringVar(&description, "description", "", "optional description")
	c.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	c.Flags().StringVar(&userName, "name", "", "user-supplied name (validated; auto-generated if omitted)")
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
		Name           string `json:"name"`
		ID             string `json:"id"`
		Status         string `json:"status"`
		Title          string `json:"title"`
		Investigations int    `json:"investigations"`
		LastActivity   string `json:"last_activity"`
	}
	rows := make([]row, 0, len(projs))
	for _, p := range projs {
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM investigations WHERE project_id = ?`, p.ProjectID).Scan(&n)
		rows = append(rows, row{
			Name:           p.Slug,
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
		w.Write([]string{"name", "id", "status", "title", "investigations", "last_activity"})
		for _, r := range rows {
			w.Write([]string{r.Name, r.ID, r.Status, r.Title, fmt.Sprintf("%d", r.Investigations), r.LastActivity})
		}
		w.Flush()
		return nil
	default:
		tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, style.Header("NAME", "ID", "STATUS", "INV", "LAST ACTIVITY", "TITLE"))
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
				r.Name, shortID(r.ID), style.Status(r.Status),
				r.Investigations, style.Dim(r.LastActivity), r.Title)
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
		Use:   "show <name-or-id>",
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
					"project":        p,
					"investigations": invs,
					"run_count":      runCount,
				})
			}
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "%s\t%s\n", style.B("ID:"), p.ProjectID)
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Name:"), p.Slug)
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Title:"), p.Title)
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Status:"), style.Status(p.Status))
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Description:"), p.Description)
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Created:"), style.Dim(time.Unix(p.CreatedAt, 0).Format(time.RFC3339)))
			fmt.Fprintf(tw, "%s\t%s\n", style.B("Updated:"), style.Dim(time.Unix(p.UpdatedAt, 0).Format(time.RFC3339)))
			fmt.Fprintf(tw, "%s\t%d\n", style.B("Investigations:"), len(invs))
			fmt.Fprintf(tw, "%s\t%d\n", style.B("Total runs:"), runCount)
			tw.Flush()
			fmt.Fprintln(cm.OutOrStdout(), "")
			fmt.Fprintln(cm.OutOrStdout(), style.B("Investigations:"))
			tw2 := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw2, "  "+style.Header("NAME", "STATUS", "RUNS", "TITLE"))
			for _, i := range invs {
				n, _ := state.CountRunsForInvestigation(ctx, db, i.InvestigationID)
				fmt.Fprintf(tw2, "  %s\t%s\t%d\t%s\n", i.Slug, style.Status(i.Status), n, i.Title)
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
		Use:   "use <name-or-id>",
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
				return fmt.Errorf("supply <name-or-id> or --none")
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
			fmt.Fprintf(cm.OutOrStdout(), "%s  %s\n", style.B("Context:"), contextName)
			pid, _ := state.GetActivePointer(ctx, db, contextName, state.PointerProject)
			if pid == "" {
				fmt.Fprintf(cm.OutOrStdout(), "%s  %s\n", style.B("Project:"), style.Dim("(none)"))
			} else {
				p, err := state.FindProject(ctx, db, contextName, pid)
				if err != nil {
					fmt.Fprintf(cm.OutOrStdout(), "%s  %s (resolve error: %v)\n", style.B("Project:"), pid, err)
				} else {
					fmt.Fprintf(cm.OutOrStdout(), "%s  %s — %s\n", style.B("Project:"), style.G(p.Slug), p.Title)
				}
			}
			iid, _ := state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
			if iid == "" {
				fmt.Fprintf(cm.OutOrStdout(), "%s  %s\n", style.B("Investigation:"), style.Dim("(none)"))
			} else {
				i, err := state.FindInvestigation(ctx, db, contextName, iid)
				if err != nil {
					fmt.Fprintf(cm.OutOrStdout(), "%s  %s (resolve error: %v)\n", style.B("Investigation:"), iid, err)
				} else {
					fmt.Fprintf(cm.OutOrStdout(), "%s  %s — %s\n", style.B("Investigation:"), style.G(i.Slug), i.Title)
				}
			}
			return nil
		},
	}
}

func newArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <name-or-id>",
		Short: "Archive a project (reversible via `abc project use`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			return setProjectStatus(cm, args[0], "archived")
		},
	}
}

func newCompleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "complete <name-or-id>",
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
	fmt.Fprintf(cm.OutOrStdout(), "Project %s → %s\n", p.Slug, style.Status(status))
	return nil
}

func newRenameCmd() *cobra.Command {
	var newName, newTitle, newDesc string
	c := &cobra.Command{
		Use:   "rename <name-or-id>",
		Short: "Rename name/title/description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if newName == "" && newTitle == "" && newDesc == "" {
				return fmt.Errorf("supply at least one of --name, --title, --description")
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
			if newName != "" {
				if err := slug.Validate(newName); err != nil {
					return err
				}
				exists, err := state.SlugExistsProject(ctx, db, contextName, newName)
				if err != nil {
					return err
				}
				if exists && newName != p.Slug {
					return fmt.Errorf("name %q already in use", newName)
				}
				fields["slug"] = newName
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
			fmt.Fprintf(cm.OutOrStdout(), "%s %s.\n", style.G("Updated project"), p.ProjectID)
			return nil
		},
	}
	c.Flags().StringVar(&newName, "name", "", "new name")
	c.Flags().StringVar(&newTitle, "title", "", "new title")
	c.Flags().StringVar(&newDesc, "description", "", "new description")
	return c
}

func newTagCmd() *cobra.Command {
	var addTag, removeTag string
	c := &cobra.Command{
		Use:   "tag <name-or-id>",
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
		Use:   "delete <name-or-id>",
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
			fmt.Fprintf(cm.OutOrStdout(), "%s %s.\n", style.R("Deleted project"), p.Slug)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return c
}

// Used to avoid unused import in compactly built file.
var _ = context.Background
