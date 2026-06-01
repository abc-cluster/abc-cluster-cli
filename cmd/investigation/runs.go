package investigation

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newRunsCmd implements `abc project investigation runs <inv> [--compare] [--by <tag-key>]`.
//
// Lists the runs attributed to an investigation (the many-runs-per-investigation
// model — runs from several notebooks/scripts that share a dataset/question), and
// with --by <tag-key> groups/compares them by a run tag (e.g. --by model over runs
// tagged model=knn|rf|svm). Run tags come from `abc job run --tag k=v` (which also
// auto-tags notebook=<stem>). Costed (ZAR / kgCO2e) figures live in `abc report runs`.
func newRunsCmd() *cobra.Command {
	var byKey string
	var compare bool
	c := &cobra.Command{
		Use:     "runs [<slug-or-id>]",
		Short:   "List runs in an investigation; compare them by tag",
		Aliases: []string{"run-list"},
		Long: `List the runs attributed to an investigation and, with --by, group and
compare them by a run tag.

Runs are attributed automatically by ` + "`abc job run`" + ` (via the active
investigation / ABC_INVESTIGATION) and tagged with ` + "`--tag k=v`" + ` plus an
auto ` + "`notebook=<stem>`" + ` tag. Many runs across different notebooks can share
one investigation when they share a dataset/question.

Examples:
  abc project investigation runs species-classification
  abc project investigation runs species-classification --by model
  abc project inv runs species-classification --compare --by model

For costed (ZAR / kgCO2e) per-run figures, see ` + "`abc report runs`" + `.`,
		Args: cobra.MaximumNArgs(1),
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
			runs, err := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
			if err != nil {
				return err
			}
			out := cm.OutOrStdout()
			if len(runs) == 0 {
				fmt.Fprintf(out, "No runs attributed to investigation %s.\n", inv.Slug)
				return nil
			}

			if byKey != "" {
				printGroupedByTag(out, inv.Slug, byKey, runs)
			} else {
				if compare {
					fmt.Fprintf(out, "Runs in investigation %s (%d):\n", inv.Slug, len(runs))
				}
				printRunTable(out, runs)
			}
			fmt.Fprintf(out, "\nCosted (ZAR / kgCO2e) per-run figures: abc report runs\n")
			return nil
		},
	}
	c.Flags().StringVar(&byKey, "by", "", "Group/compare runs by this tag key (e.g. --by model)")
	c.Flags().BoolVar(&compare, "compare", false, "Show the comparison view (with --by, group by that tag)")
	return c
}

// printGroupedByTag groups runs by the value of tag key `key` and prints each
// group with its run table. Runs lacking the key fall into a "(no <key> tag)" group.
func printGroupedByTag(out io.Writer, invSlug, key string, runs []state.Run) {
	groups := map[string][]state.Run{}
	order := []string{}
	for _, r := range runs {
		v := tagValue(r.Tags, key)
		if _, ok := groups[v]; !ok {
			order = append(order, v)
		}
		groups[v] = append(groups[v], r)
	}
	sort.Strings(order)
	fmt.Fprintf(out, "Investigation %s — %d run(s) by %s:\n\n", invSlug, len(runs), key)
	for _, v := range order {
		label := v
		if label == "" {
			label = "(no " + key + " tag)"
		} else {
			label = key + "=" + v
		}
		fmt.Fprintf(out, "%s  (%d run(s))\n", label, len(groups[v]))
		printRunTable(out, groups[v])
		fmt.Fprintln(out)
	}
}

// printRunTable renders run id, status, walltime, cpu-hours, mem-GB-hours, and tags.
func printRunTable(out io.Writer, runs []state.Run) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  RUN\tSTATUS\tWALL(s)\tCPU(h)\tMEM(GBh)\tTAGS")
	for _, r := range runs {
		wall := "-"
		if r.WalltimeSeconds.Valid {
			wall = fmt.Sprintf("%d", r.WalltimeSeconds.Int64)
		}
		cpu := "-"
		if r.CPUHours.Valid {
			cpu = fmt.Sprintf("%.3g", r.CPUHours.Float64)
		}
		mem := "-"
		if r.MemoryGBHours.Valid {
			mem = fmt.Sprintf("%.3g", r.MemoryGBHours.Float64)
		}
		shortID := r.RunID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", shortID, r.Status, wall, cpu, mem, strings.Join(r.Tags, ","))
	}
	tw.Flush()
}

// tagValue returns the value of the first "key=value" tag matching key, or "".
func tagValue(tags []string, key string) string {
	pfx := key + "="
	for _, t := range tags {
		if strings.HasPrefix(t, pfx) {
			return strings.TrimPrefix(t, pfx)
		}
	}
	return ""
}
