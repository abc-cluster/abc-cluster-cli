// Annotation management subgroup: `abc project investigation annotation`.
//
// Verbs (per spec §D.5): list, show, edit, tag, move, withdraw, restore.
// Every mutation writes a row to annotation_revisions capturing the previous
// state for the field(s) that changed (per `edit_kind`).
package investigation

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/editor"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newAnnotationCmd registers the `annotation` subgroup.
func newAnnotationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "annotation",
		Short: "Manage annotations on an investigation (list, show, edit, tag, move, withdraw, restore)",
	}
	cmd.AddCommand(newAnnListCmd())
	cmd.AddCommand(newAnnShowCmd())
	cmd.AddCommand(newAnnEditCmd())
	cmd.AddCommand(newAnnTagCmd())
	cmd.AddCommand(newAnnMoveCmd())
	cmd.AddCommand(newAnnWithdrawCmd())
	cmd.AddCommand(newAnnRestoreCmd())
	return cmd
}

func newAnnListCmd() *cobra.Command {
	var (
		includeWithdrawn bool
		tagFilter        string
		output           string
	)
	c := &cobra.Command{
		Use:   "list <inv-slug-or-id>",
		Short: "List annotations on an investigation",
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
			anns, err := state.ListAnnotationsFiltered(ctx, db, inv.InvestigationID, includeWithdrawn)
			if err != nil {
				return err
			}
			if tagFilter != "" {
				filtered := make([]state.Annotation, 0, len(anns))
				lc := strings.ToLower(tagFilter)
				for _, a := range anns {
					for _, t := range a.Tags {
						if strings.ToLower(t) == lc {
							filtered = append(filtered, a)
							break
						}
					}
				}
				anns = filtered
			}
			if output == "json" {
				enc := json.NewEncoder(cm.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(anns)
			}
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID-PREFIX\tCREATED\tUPDATED\tTAGS\tEXCERPT\tWITHDRAWN")
			for _, a := range anns {
				prefix := a.AnnotationID
				if len(prefix) > 12 {
					prefix = prefix[:12]
				}
				excerpt := a.Body
				if len(excerpt) > 80 {
					excerpt = excerpt[:80] + "…"
				}
				excerpt = strings.ReplaceAll(excerpt, "\n", " ")
				wd := ""
				if a.WithdrawnAt.Valid {
					wd = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					prefix,
					time.Unix(a.CreatedAt, 0).Format("2006-01-02 15:04"),
					time.Unix(a.UpdatedAt, 0).Format("2006-01-02 15:04"),
					strings.Join(a.Tags, ","), excerpt, wd)
			}
			tw.Flush()
			return nil
		},
	}
	c.Flags().BoolVar(&includeWithdrawn, "include-withdrawn", false, "include soft-deleted annotations")
	c.Flags().StringVar(&tagFilter, "tag", "", "filter to annotations whose tags contain this tag (case-insensitive)")
	c.Flags().StringVar(&output, "output", "table", "table|json")
	return c
}

func newAnnShowCmd() *cobra.Command {
	var (
		history bool
		output  string
	)
	c := &cobra.Command{
		Use:   "show <ann-id>",
		Short: "Show full annotation, optionally with revision history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			var revs []state.AnnotationRevision
			if history {
				revs, _ = state.ListAnnotationRevisions(ctx, db, a.AnnotationID)
			}
			if output == "json" {
				enc := json.NewEncoder(cm.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"annotation": a,
					"revisions":  revs,
				})
			}
			tw := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "ID:\t%s\n", a.AnnotationID)
			fmt.Fprintf(tw, "Investigation:\t%s\n", a.InvestigationID)
			fmt.Fprintf(tw, "Tags:\t%s\n", strings.Join(a.Tags, ", "))
			fmt.Fprintf(tw, "Created:\t%s\n", time.Unix(a.CreatedAt, 0).Format(time.RFC3339))
			fmt.Fprintf(tw, "Updated:\t%s\n", time.Unix(a.UpdatedAt, 0).Format(time.RFC3339))
			if a.WithdrawnAt.Valid {
				fmt.Fprintf(tw, "Withdrawn:\t%s\n", time.Unix(a.WithdrawnAt.Int64, 0).Format(time.RFC3339))
				if a.WithdrawnReason.Valid {
					fmt.Fprintf(tw, "Withdraw reason:\t%s\n", a.WithdrawnReason.String)
				}
			}
			tw.Flush()
			fmt.Fprintln(cm.OutOrStdout(), "")
			fmt.Fprintln(cm.OutOrStdout(), a.Body)
			if history {
				fmt.Fprintln(cm.OutOrStdout(), "")
				fmt.Fprintln(cm.OutOrStdout(), "Revisions (oldest first):")
				tw2 := tabwriter.NewWriter(cm.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw2, "  EDITED-AT\tKIND\tREASON")
				for _, r := range revs {
					reason := ""
					if r.EditReason.Valid {
						reason = r.EditReason.String
					}
					fmt.Fprintf(tw2, "  %s\t%s\t%s\n",
						time.Unix(r.EditedAt, 0).Format(time.RFC3339), r.EditKind, reason)
				}
				tw2.Flush()
			}
			return nil
		},
	}
	c.Flags().BoolVar(&history, "history", false, "include the full annotation_revisions chain")
	c.Flags().StringVar(&output, "output", "table", "table|json")
	return c
}

func newAnnEditCmd() *cobra.Command {
	var (
		note   string
		reason string
	)
	c := &cobra.Command{
		Use:   "edit <ann-id>",
		Short: "Edit annotation body (writes a revision row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			if a.WithdrawnAt.Valid {
				return fmt.Errorf("annotation %s is withdrawn — restore it first", a.AnnotationID)
			}

			newBody := note
			newTags := a.Tags
			tagsChanged := false
			if newBody == "" {
				seedTag := ""
				if len(a.Tags) > 0 {
					seedTag = strings.Join(a.Tags, ", ")
				}
				body, tag, err := editor.AnnotationEditExisting(seedTag, a.Body)
				if err != nil {
					return err
				}
				if strings.TrimSpace(body) == "" {
					return fmt.Errorf("body is empty after edit; aborted")
				}
				newBody = body
				if tag != "" {
					parsed := []string{}
					for _, t := range strings.Split(tag, ",") {
						if t = strings.TrimSpace(t); t != "" {
							parsed = append(parsed, t)
						}
					}
					if !sameStrings(parsed, a.Tags) {
						newTags = parsed
						tagsChanged = true
					}
				}
			}
			if strings.TrimSpace(newBody) == "" {
				return fmt.Errorf("body is empty; aborted")
			}

			// Body always changes when this verb runs (the gate above catches
			// "edit but unchanged" implicitly — we still write the body
			// revision so reasoning chain is preserved).
			rsn := sql.NullString{}
			if reason != "" {
				rsn = sql.NullString{String: reason, Valid: true}
			}
			if newBody != a.Body {
				if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
					AnnotationID: a.AnnotationID,
					PrevBody:     sql.NullString{String: a.Body, Valid: true},
					EditKind:     "body",
					EditReason:   rsn,
				}); err != nil {
					return err
				}
				if err := state.UpdateAnnotationBody(ctx, db, a.AnnotationID, newBody); err != nil {
					return err
				}
			}
			if tagsChanged {
				prevJSON, _ := json.Marshal(a.Tags)
				if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
					AnnotationID: a.AnnotationID,
					PrevTagsJSON: sql.NullString{String: string(prevJSON), Valid: true},
					EditKind:     "tag",
					EditReason:   rsn,
				}); err != nil {
					return err
				}
				if err := state.UpdateAnnotationTags(ctx, db, a.AnnotationID, newTags); err != nil {
					return err
				}
			}
			fmt.Fprintf(cm.OutOrStdout(), "Updated annotation %s.\n", a.AnnotationID)
			return nil
		},
	}
	c.Flags().StringVar(&note, "note", "", "new body (skips editor)")
	c.Flags().StringVar(&reason, "reason", "", "rationale recorded with the revision (recommended)")
	return c
}

func newAnnTagCmd() *cobra.Command {
	var (
		add     []string
		remove  []string
		replace []string
		reason  string
	)
	c := &cobra.Command{
		Use:   "tag <ann-id>",
		Short: "Modify annotation tags (writes a revision row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if len(add) == 0 && len(remove) == 0 && len(replace) == 0 {
				return fmt.Errorf("supply at least one of --add, --remove, --replace")
			}
			if len(replace) > 0 && (len(add) > 0 || len(remove) > 0) {
				return fmt.Errorf("--replace is mutually exclusive with --add / --remove")
			}
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			var newTags []string
			if len(replace) > 0 {
				newTags = append(newTags, replace...)
			} else {
				newTags = append(newTags, a.Tags...)
				for _, t := range remove {
					newTags = removeString(newTags, t)
				}
				for _, t := range add {
					if !containsString(newTags, t) {
						newTags = append(newTags, t)
					}
				}
			}
			if sameStrings(newTags, a.Tags) {
				fmt.Fprintln(cm.OutOrStdout(), "No tag change; nothing to do.")
				return nil
			}
			prevJSON, _ := json.Marshal(a.Tags)
			rsn := sql.NullString{}
			if reason != "" {
				rsn = sql.NullString{String: reason, Valid: true}
			}
			if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
				AnnotationID: a.AnnotationID,
				PrevTagsJSON: sql.NullString{String: string(prevJSON), Valid: true},
				EditKind:     "tag",
				EditReason:   rsn,
			}); err != nil {
				return err
			}
			if err := state.UpdateAnnotationTags(ctx, db, a.AnnotationID, newTags); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Tags now: %s\n", strings.Join(newTags, ", "))
			return nil
		},
	}
	c.Flags().StringArrayVar(&add, "add", nil, "tag to add (repeatable)")
	c.Flags().StringArrayVar(&remove, "remove", nil, "tag to remove (repeatable)")
	c.Flags().StringSliceVar(&replace, "replace", nil, "comma-separated full tag set (overwrites)")
	c.Flags().StringVar(&reason, "reason", "", "rationale recorded with the revision")
	return c
}

func newAnnMoveCmd() *cobra.Command {
	var (
		toRef  string
		reason string
	)
	c := &cobra.Command{
		Use:   "move <ann-id>",
		Short: "Move annotation to a different investigation (writes a revision row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			if toRef == "" {
				return fmt.Errorf("--to=<inv-slug-or-id> is required")
			}
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			dest, err := state.FindInvestigation(ctx, db, contextName, toRef)
			if err != nil {
				return fmt.Errorf("destination investigation %q: %w", toRef, err)
			}
			if dest.InvestigationID == a.InvestigationID {
				fmt.Fprintln(cm.OutOrStdout(), "No-op: source and destination are the same.")
				return nil
			}
			rsn := sql.NullString{}
			if reason != "" {
				rsn = sql.NullString{String: reason, Valid: true}
			}
			if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
				AnnotationID:        a.AnnotationID,
				PrevInvestigationID: sql.NullString{String: a.InvestigationID, Valid: true},
				EditKind:            "move",
				EditReason:          rsn,
			}); err != nil {
				return err
			}
			if err := state.MoveAnnotation(ctx, db, a.AnnotationID, dest.InvestigationID); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Moved annotation %s → %s\n", a.AnnotationID, dest.Slug)
			return nil
		},
	}
	c.Flags().StringVar(&toRef, "to", "", "destination investigation (slug or ID)")
	c.Flags().StringVar(&reason, "reason", "", "rationale recorded with the revision")
	return c
}

func newAnnWithdrawCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use:   "withdraw <ann-id>",
		Short: "Soft-delete an annotation (writes a revision row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			if a.WithdrawnAt.Valid {
				fmt.Fprintln(cm.OutOrStdout(), "Already withdrawn; no-op.")
				return nil
			}
			rsn := sql.NullString{}
			if reason != "" {
				rsn = sql.NullString{String: reason, Valid: true}
			}
			if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
				AnnotationID: a.AnnotationID,
				EditKind:     "withdraw",
				EditReason:   rsn,
			}); err != nil {
				return err
			}
			if err := state.WithdrawAnnotation(ctx, db, a.AnnotationID, reason); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Withdrew annotation %s.\n", a.AnnotationID)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "", "withdraw reason")
	return c
}

func newAnnRestoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "restore <ann-id>",
		Short: "Restore a withdrawn annotation (writes a revision row)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			a, err := state.FindAnnotationGlobal(ctx, db, args[0])
			if err != nil {
				return err
			}
			if !a.WithdrawnAt.Valid {
				fmt.Fprintln(cm.OutOrStdout(), "Not withdrawn; no-op.")
				return nil
			}
			if err := state.AddAnnotationRevision(ctx, db, state.AnnotationRevision{
				AnnotationID: a.AnnotationID,
				EditKind:     "restore",
			}); err != nil {
				return err
			}
			if err := state.RestoreAnnotation(ctx, db, a.AnnotationID); err != nil {
				return err
			}
			fmt.Fprintf(cm.OutOrStdout(), "Restored annotation %s.\n", a.AnnotationID)
			return nil
		},
	}
	return c
}

// ----- helpers -----

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
