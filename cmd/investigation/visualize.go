package investigation

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/spf13/cobra"
)

// newVisualizeCmd registers `abc investigation visualize`.
func newVisualizeCmd() *cobra.Command {
	var (
		projectRef     string
		vizType        string
		output         string
		render         string
		since          string
		branchesFilter string
		noRuns         bool
		mermaidVersion string
	)
	c := &cobra.Command{
		Use:   "visualize [<slug-or-id>]",
		Short: "Emit a Mermaid diagram of an investigation or project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cm *cobra.Command, args []string) error {
			db, err := state.Open()
			if err != nil {
				return err
			}
			ctx := cm.Context()
			contextName := state.ActiveContextName()

			opts := vizOptions{
				vizType:        vizType,
				since:          parseSince(since),
				branchesFilter: branchesFilter,
				noRuns:         noRuns,
				mermaidVersion: mermaidVersion,
			}

			var src string
			if projectRef != "" {
				p, err := state.FindProject(ctx, db, contextName, projectRef)
				if err != nil {
					return err
				}
				src, err = renderProject(ctx, db, p, opts)
				if err != nil {
					return err
				}
			} else {
				ref := ""
				if len(args) == 1 {
					ref = args[0]
				} else {
					ref, _ = state.GetActivePointer(ctx, db, contextName, state.PointerInvestigation)
					if ref == "" {
						return fmt.Errorf("no active investigation — supply <slug-or-id> or --project=<slug>")
					}
				}
				inv, err := state.FindInvestigation(ctx, db, contextName, ref)
				if err != nil {
					return err
				}
				src, err = renderInvestigation(ctx, db, inv, opts)
				if err != nil {
					return err
				}
			}

			return writeOutput(cm.OutOrStdout(), src, output, render)
		},
	}
	c.Flags().StringVar(&projectRef, "project", "", "render a project rollup instead of a single investigation")
	c.Flags().StringVar(&vizType, "type", "branches", "branches|timeline|flow|lineage")
	c.Flags().StringVar(&output, "output", "", "write to path instead of stdout")
	c.Flags().StringVar(&render, "render", "", "svg|png — invokes mmdc if present (soft dependency)")
	c.Flags().StringVar(&since, "since", "", "filter entries newer than YYYY-MM-DD")
	c.Flags().StringVar(&branchesFilter, "branches", "all", "alive|dead|all")
	c.Flags().BoolVar(&noRuns, "no-runs", false, "annotation-only diagrams")
	c.Flags().StringVar(&mermaidVersion, "mermaid-version", "v1", "v1|v2 — gitGraph syntax compatibility")
	return c
}

type vizOptions struct {
	vizType        string
	since          int64
	branchesFilter string
	noRuns         bool
	mermaidVersion string
}

func parseSince(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func writeOutput(stdout io.Writer, src, outputPath, render string) error {
	if outputPath == "" && render == "" {
		_, err := fmt.Fprintln(stdout, src)
		return err
	}
	target := outputPath
	if target == "" {
		target = "investigation.mmd"
	}
	// First write Mermaid source.
	mmdPath := target
	if render != "" {
		mmdPath = strings.TrimSuffix(target, "."+render) + ".mmd"
		if mmdPath == target {
			mmdPath = target + ".mmd"
		}
	}
	if err := os.WriteFile(mmdPath, []byte(src), 0o644); err != nil {
		return err
	}
	if render != "" {
		if _, err := exec.LookPath("mmdc"); err != nil {
			fmt.Fprintf(os.Stderr, "[abc] mmdc not on PATH; wrote %s instead of %s\n", mmdPath, target)
			return nil
		}
		cmd := exec.Command("mmdc", "-i", mmdPath, "-o", target)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[abc] mmdc render failed: %v; .mmd source kept at %s\n", err, mmdPath)
			return nil
		}
	}
	return nil
}

// ----- Rendering -----

func renderInvestigation(ctx context.Context, db *sql.DB, inv state.Investigation, opts vizOptions) (string, error) {
	switch opts.vizType {
	case "", "branches":
		return renderBranches(ctx, db, inv, opts)
	case "timeline":
		return renderTimeline(ctx, db, inv, opts)
	case "flow":
		return renderFlow(ctx, db, inv, opts)
	case "lineage":
		return renderLineage(ctx, db, inv, opts)
	default:
		return "", fmt.Errorf("unknown --type %q", opts.vizType)
	}
}

func renderProject(ctx context.Context, db *sql.DB, p state.Project, opts vizOptions) (string, error) {
	// Project rollup is implemented for lineage; other types fall back to a
	// flowchart of investigations under the project.
	invs, err := state.ListInvestigations(ctx, db, p.ContextName, p.ProjectID, "", false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s — %s\n---\n", p.Slug, p.Title)
	b.WriteString("flowchart TB\n")
	fmt.Fprintf(&b, "   P[%s\\n%s]\n", p.Slug, escapeLabel(p.Title))
	for _, i := range invs {
		if !branchPasses(i, opts) {
			continue
		}
		nodeID := nodeIDFor("I", i.InvestigationID)
		fmt.Fprintf(&b, "   %s[%s\\n%s]\n", nodeID, i.Slug, escapeLabel(i.Title))
		fmt.Fprintf(&b, "   P --> %s\n", nodeID)
	}
	// Status colour classes
	b.WriteString("   classDef active fill:#dcfce7,stroke:#16a34a\n")
	b.WriteString("   classDef merged fill:#dbeafe,stroke:#3b82f6\n")
	b.WriteString("   classDef deadend fill:#fee2e2,stroke:#dc2626,stroke-dasharray:3 3\n")
	b.WriteString("   classDef archived fill:#f3f4f6,stroke:#6b7280\n")
	for _, i := range invs {
		if !branchPasses(i, opts) {
			continue
		}
		nodeID := nodeIDFor("I", i.InvestigationID)
		switch i.Status {
		case "active":
			fmt.Fprintf(&b, "   class %s active\n", nodeID)
		case "merged":
			fmt.Fprintf(&b, "   class %s merged\n", nodeID)
		case "dead-end":
			fmt.Fprintf(&b, "   class %s deadend\n", nodeID)
		case "archived":
			fmt.Fprintf(&b, "   class %s archived\n", nodeID)
		}
	}
	return b.String(), nil
}

func branchPasses(i state.Investigation, opts vizOptions) bool {
	switch opts.branchesFilter {
	case "alive":
		return i.Status == "active" || i.Status == "merged"
	case "dead":
		return i.Status == "dead-end"
	default:
		return true
	}
}

// renderBranches builds a gitGraph: parent investigation = main, children = branches,
// runs + annotations interleaved chronologically as commits.
func renderBranches(ctx context.Context, db *sql.DB, root state.Investigation, opts vizOptions) (string, error) {
	tree, err := investigationSubtree(ctx, db, root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s — %s\n---\n", root.Slug, escapeLabel(root.Title))
	b.WriteString("gitGraph\n")
	// Walk in topo order from root; main = root.
	branchName := func(inv state.Investigation) string {
		if inv.InvestigationID == root.InvestigationID {
			return "main"
		}
		return inv.Slug
	}
	// Render main first.
	if err := emitBranchCommits(ctx, db, &b, root, "main", opts); err != nil {
		return "", err
	}
	// Then each child branch in creation order.
	for _, child := range tree {
		if child.InvestigationID == root.InvestigationID {
			continue
		}
		if !branchPasses(child, opts) {
			continue
		}
		fmt.Fprintf(&b, "   branch %s\n", branchName(child))
		if err := emitBranchCommits(ctx, db, &b, child, branchName(child), opts); err != nil {
			return "", err
		}
		if child.MergedInto.Valid {
			parentID := child.MergedInto.String
			parent, err := state.FindInvestigation(ctx, db, root.ContextName, parentID)
			if err == nil {
				fmt.Fprintf(&b, "   checkout %s\n", branchName(parent))
				fmt.Fprintf(&b, "   merge %s\n", branchName(child))
			}
		} else if child.DeadEndReason.Valid {
			fmt.Fprintf(&b, "   %% branch %s abandoned: %q\n", branchName(child), child.DeadEndReason.String)
			fmt.Fprintf(&b, "   checkout main\n")
		} else {
			fmt.Fprintf(&b, "   checkout main\n")
		}
	}
	return b.String(), nil
}

func emitBranchCommits(ctx context.Context, db *sql.DB, b *strings.Builder, inv state.Investigation, _ string, opts vizOptions) error {
	type entry struct {
		ts   int64
		kind string // "A" or "R"
		ann  state.Annotation
		run  state.Run
	}
	annsRaw, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
	runsRaw, _ := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
	var entries []entry
	for _, a := range annsRaw {
		if opts.since > 0 && a.CreatedAt < opts.since {
			continue
		}
		entries = append(entries, entry{ts: a.CreatedAt, kind: "A", ann: a})
	}
	if !opts.noRuns {
		for _, r := range runsRaw {
			if opts.since > 0 && r.SubmittedAt < opts.since {
				continue
			}
			entries = append(entries, entry{ts: r.SubmittedAt, kind: "R", run: r})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	for _, e := range entries {
		if e.kind == "A" {
			tag := ""
			if e.ann.Tag.Valid {
				tag = e.ann.Tag.String
			}
			ctype := commitTypeForTag(tag)
			label := tag
			if label == "" {
				label = "annotation"
			}
			fmt.Fprintf(b, "   commit id: %q tag: %q", e.ann.AnnotationID, label)
			if ctype != "NORMAL" {
				fmt.Fprintf(b, " type: %s", ctype)
			}
			b.WriteString("\n")
		} else {
			label := e.run.WorkloadRef
			if e.run.WorkloadVersion.Valid && e.run.WorkloadVersion.String != "" {
				label = label + "@" + e.run.WorkloadVersion.String
			}
			ctype := "NORMAL"
			if e.run.Status == "failed" {
				ctype = "REVERSE"
			}
			fmt.Fprintf(b, "   commit id: %q tag: %q", e.run.RunID, label)
			if ctype != "NORMAL" {
				fmt.Fprintf(b, " type: %s", ctype)
			}
			b.WriteString("\n")
		}
	}
	return nil
}

func commitTypeForTag(tag string) string {
	switch tag {
	case "hypothesis", "insight", "decision":
		return "HIGHLIGHT"
	case "issue", "dead-end":
		return "REVERSE"
	default:
		return "NORMAL"
	}
}

// renderTimeline emits a Mermaid timeline directive.
func renderTimeline(ctx context.Context, db *sql.DB, inv state.Investigation, opts vizOptions) (string, error) {
	type entry struct {
		ts   int64
		text string
	}
	var entries []entry
	annsRaw, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
	for _, a := range annsRaw {
		if opts.since > 0 && a.CreatedAt < opts.since {
			continue
		}
		tag := ""
		if a.Tag.Valid {
			tag = a.Tag.String
		}
		body := truncate(strings.ReplaceAll(a.Body, "\n", " "), 64)
		entries = append(entries, entry{ts: a.CreatedAt, text: fmt.Sprintf("%s %q", tag, body)})
	}
	if !opts.noRuns {
		runsRaw, _ := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
		for _, r := range runsRaw {
			if opts.since > 0 && r.SubmittedAt < opts.since {
				continue
			}
			label := r.WorkloadRef
			if r.WorkloadVersion.Valid && r.WorkloadVersion.String != "" {
				label = label + "@" + r.WorkloadVersion.String
			}
			st := r.Status
			if st == "" {
				st = "running"
			}
			entries = append(entries, entry{ts: r.SubmittedAt, text: fmt.Sprintf("%s %s (%s)", r.RunID, label, st)})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	var b strings.Builder
	b.WriteString("timeline\n")
	fmt.Fprintf(&b, "   title %s timeline\n", inv.Slug)
	curDay := ""
	for _, e := range entries {
		day := time.Unix(e.ts, 0).UTC().Format("2006-01-02")
		if day != curDay {
			fmt.Fprintf(&b, "   %s : %s\n", day, e.text)
			curDay = day
		} else {
			fmt.Fprintf(&b, "              : %s\n", e.text)
		}
	}
	return b.String(), nil
}

// renderFlow emits a flowchart TD with annotation→run→annotation chains.
func renderFlow(ctx context.Context, db *sql.DB, inv state.Investigation, opts vizOptions) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s flow\n---\n", inv.Slug)
	b.WriteString("flowchart TD\n")
	type entry struct {
		ts    int64
		kind  string
		ann   state.Annotation
		run   state.Run
		nodeI string
	}
	annsRaw, _ := state.ListAnnotations(ctx, db, inv.InvestigationID)
	runsRaw, _ := state.ListRunsForInvestigation(ctx, db, inv.InvestigationID)
	var entries []entry
	for _, a := range annsRaw {
		if opts.since > 0 && a.CreatedAt < opts.since {
			continue
		}
		entries = append(entries, entry{ts: a.CreatedAt, kind: "A", ann: a, nodeI: nodeIDFor("A", a.AnnotationID)})
	}
	if !opts.noRuns {
		for _, r := range runsRaw {
			if opts.since > 0 && r.SubmittedAt < opts.since {
				continue
			}
			entries = append(entries, entry{ts: r.SubmittedAt, kind: "R", run: r, nodeI: nodeIDFor("R", r.RunID)})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	prev := ""
	prevRunStatus := ""
	for _, e := range entries {
		switch e.kind {
		case "A":
			tag := ""
			if e.ann.Tag.Valid {
				tag = e.ann.Tag.String
			}
			label := fmt.Sprintf("%s: %s", tag, truncate(strings.ReplaceAll(e.ann.Body, "\n", " "), 48))
			fmt.Fprintf(&b, "   %s[%q]\n", e.nodeI, label)
		case "R":
			label := e.run.WorkloadRef
			if e.run.WorkloadVersion.Valid && e.run.WorkloadVersion.String != "" {
				label = label + "@" + e.run.WorkloadVersion.String
			}
			fmt.Fprintf(&b, "   %s[%q]\n", e.nodeI, fmt.Sprintf("%s %s", e.run.RunID, label))
			prevRunStatus = e.run.Status
		}
		if prev != "" {
			edgeLabel := ""
			if prevRunStatus != "" {
				edgeLabel = fmt.Sprintf("|%s|", prevRunStatus)
				prevRunStatus = ""
			}
			fmt.Fprintf(&b, "   %s -->%s %s\n", prev, edgeLabel, e.nodeI)
		}
		prev = e.nodeI
	}
	// Class styling
	b.WriteString("   classDef hyp fill:#e8f4fd,stroke:#3b82f6\n")
	b.WriteString("   classDef run fill:#dcfce7,stroke:#16a34a\n")
	b.WriteString("   classDef issue fill:#fee2e2,stroke:#dc2626\n")
	b.WriteString("   classDef insight fill:#fef9c3,stroke:#ca8a04\n")
	for _, e := range entries {
		switch e.kind {
		case "A":
			tag := ""
			if e.ann.Tag.Valid {
				tag = e.ann.Tag.String
			}
			cls := classFor(tag)
			if cls != "" {
				fmt.Fprintf(&b, "   class %s %s\n", e.nodeI, cls)
			}
		case "R":
			fmt.Fprintf(&b, "   class %s run\n", e.nodeI)
		}
	}
	return b.String(), nil
}

func classFor(tag string) string {
	switch tag {
	case "hypothesis":
		return "hyp"
	case "issue", "dead-end":
		return "issue"
	case "insight":
		return "insight"
	}
	return ""
}

// renderLineage emits a flowchart LR with citations as dotted arrows.
func renderLineage(ctx context.Context, db *sql.DB, inv state.Investigation, opts vizOptions) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s lineage\n---\n", inv.Slug)
	b.WriteString("flowchart LR\n")
	rootID := nodeIDFor("I", inv.InvestigationID)
	fmt.Fprintf(&b, "   %s[%s\\n%s]\n", rootID, inv.Slug, escapeLabel(inv.Title))
	// Outgoing citations from this investigation's annotations.
	cites, _ := state.ListCitationsFromInvestigation(ctx, db, inv.InvestigationID)
	seen := map[string]bool{}
	for _, c := range cites {
		tinv, err := state.FindInvestigation(ctx, db, inv.ContextName, c.TargetInvestigation)
		if err != nil {
			continue
		}
		tID := nodeIDFor("I", tinv.InvestigationID)
		if !seen[tID] {
			fmt.Fprintf(&b, "   %s[%s\\n%s]\n", tID, tinv.Slug, escapeLabel(tinv.Title))
			seen[tID] = true
		}
		// Dotted "cites" edge.
		label := "cites"
		if c.TargetAnnotationID.Valid {
			label = "cites " + c.TargetAnnotationID.String
		}
		fmt.Fprintf(&b, "   %s -.->|%s| %s\n", rootID, label, tID)
	}
	// Incoming citations (other investigations citing this one).
	incoming, _ := state.ListCitationsToInvestigation(ctx, db, inv.InvestigationID)
	for _, c := range incoming {
		// Resolve the source investigation via the source annotation.
		var srcInvID string
		err := db.QueryRowContext(ctx,
			`SELECT investigation_id FROM annotations WHERE annotation_id = ?`,
			c.SourceAnnotationID).Scan(&srcInvID)
		if err != nil {
			continue
		}
		sinv, err := state.FindInvestigation(ctx, db, inv.ContextName, srcInvID)
		if err != nil {
			continue
		}
		sID := nodeIDFor("I", sinv.InvestigationID)
		if !seen[sID] {
			fmt.Fprintf(&b, "   %s[%s\\n%s]\n", sID, sinv.Slug, escapeLabel(sinv.Title))
			seen[sID] = true
		}
		label := "cites"
		if c.TargetAnnotationID.Valid {
			label = "cites " + c.TargetAnnotationID.String
		}
		fmt.Fprintf(&b, "   %s -.->|%s| %s\n", sID, label, rootID)
	}
	b.WriteString("   classDef inv fill:#e8f4fd,stroke:#3b82f6\n")
	fmt.Fprintf(&b, "   class %s inv\n", rootID)
	for id := range seen {
		fmt.Fprintf(&b, "   class %s inv\n", id)
	}
	return b.String(), nil
}

// investigationSubtree returns root + all descendants in BFS order.
func investigationSubtree(ctx context.Context, db *sql.DB, root state.Investigation) ([]state.Investigation, error) {
	out := []state.Investigation{root}
	queue := []state.Investigation{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		children, err := state.ChildInvestigations(ctx, db, cur.InvestigationID)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out, nil
}

func nodeIDFor(prefix, raw string) string {
	clean := strings.ReplaceAll(raw, "-", "_")
	clean = strings.ReplaceAll(clean, ".", "_")
	if len(clean) > 16 {
		clean = clean[len(clean)-12:]
	}
	return prefix + "_" + clean
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 64 {
		s = s[:61] + "..."
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
