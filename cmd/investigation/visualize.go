package investigation

import (
	"bytes"
	"compress/zlib"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
		openBrowserFlg bool
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

			if err := writeOutput(cm.OutOrStdout(), src, output, render); err != nil {
				return err
			}
			if openBrowserFlg {
				url, err := mermaidLiveURL(src)
				if err != nil {
					return fmt.Errorf("build mermaid.live URL: %w", err)
				}
				if err := openInDefaultBrowser(url); err != nil {
					fmt.Fprintf(os.Stderr, "[abc] could not open browser: %v\n", err)
					fmt.Fprintf(os.Stderr, "[abc] paste this URL manually:\n  %s\n", url)
					return nil
				}
				fmt.Fprintln(os.Stderr, "[abc] opened in default browser (mermaid.live).")
			}
			return nil
		},
	}
	c.Flags().StringVar(&projectRef, "project", "", "render a project rollup instead of a single investigation")
	c.Flags().StringVar(&vizType, "type", "branches", "branches|timeline|flow|lineage|kanban|gantt")
	c.Flags().StringVar(&output, "output", "", "write to path instead of stdout")
	c.Flags().StringVar(&render, "render", "", "svg|png — invokes mmdc if present (soft dependency)")
	c.Flags().StringVar(&since, "since", "", "filter entries newer than YYYY-MM-DD")
	c.Flags().StringVar(&branchesFilter, "branches", "all", "alive|dead|all")
	c.Flags().BoolVar(&noRuns, "no-runs", false, "annotation-only diagrams")
	c.Flags().StringVar(&mermaidVersion, "mermaid-version", "v1", "v1|v2 — gitGraph syntax compatibility")
	c.Flags().BoolVar(&openBrowserFlg, "browser", false, "open the diagram in the default browser via mermaid.live")
	return c
}

// mermaidLiveURL builds a mermaid.live shareable URL with the diagram embedded
// in the URL hash via pako (zlib) compression — the same encoding mermaid.live
// uses for its own "Share" button. Works for diagrams up to several KB; larger
// diagrams may exceed browser URL-length limits and should use --output instead.
func mermaidLiveURL(source string) (string, error) {
	payload := struct {
		Code    string `json:"code"`
		Mermaid string `json:"mermaid"`
	}{
		Code:    source,
		Mermaid: `{"theme":"default"}`,
	}
	j, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(j); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	encoded := base64.URLEncoding.EncodeToString(buf.Bytes())
	return "https://mermaid.live/edit#pako:" + encoded, nil
}

// openInDefaultBrowser opens a URL in the OS default browser. Cross-platform:
// macOS, Linux (incl. WSL), Windows, and the major BSDs.
func openInDefaultBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux", "freebsd", "openbsd", "netbsd":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		// rundll32 handles URL protocol via FileProtocolHandler.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
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

// ----- Mermaid R|G|B|N colour palette -----------------------------------
//
// Single source of truth for all diagram types. Apply via classDef + class
// directives in flowchart/gitGraph renderers.
//
//	R (red)     dead-end · issue · failed · crit
//	G (green)   active · run · success
//	B (blue)    merged · hypothesis · insight · citation · inv
//	N (neutral) archived
const (
	mermaidR = "fill:#fee2e2,stroke:#dc2626"
	mermaidG = "fill:#dcfce7,stroke:#16a34a"
	mermaidB = "fill:#dbeafe,stroke:#3b82f6"
	mermaidN = "fill:#f3f4f6,stroke:#6b7280"
)

// mermaidClassDefs writes the standard classDef block for all named classes
// used across the flowchart renderers. Call once near the end of each diagram.
func mermaidClassDefs(b *strings.Builder) {
	b.WriteString("   classDef active " + mermaidG + "\n")
	b.WriteString("   classDef merged " + mermaidB + "\n")
	b.WriteString("   classDef deadend " + mermaidR + ",stroke-dasharray:3 3\n")
	b.WriteString("   classDef archived " + mermaidN + "\n")
	b.WriteString("   classDef run " + mermaidG + "\n")
	b.WriteString("   classDef hyp " + mermaidB + "\n")
	b.WriteString("   classDef issue " + mermaidR + "\n")
	b.WriteString("   classDef insight " + mermaidB + "\n")
	b.WriteString("   classDef inv " + mermaidB + "\n")
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
	case "gantt":
		return renderInvestigationGantt(ctx, db, inv, opts)
	case "kanban":
		return "", fmt.Errorf("--type=kanban requires --project=<slug> (kanban is project-level only)")
	default:
		return "", fmt.Errorf("unknown --type %q", opts.vizType)
	}
}

func renderProject(ctx context.Context, db *sql.DB, p state.Project, opts vizOptions) (string, error) {
	switch opts.vizType {
	case "kanban":
		return renderProjectKanban(ctx, db, p, opts)
	case "gantt":
		return renderProjectGantt(ctx, db, p, opts)
	}
	// Default project rollup: flowchart-shaped lineage.
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
	mermaidClassDefs(&b)
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
			fmt.Fprintf(&b, "   %%%% branch %s abandoned: %q\n", branchName(child), child.DeadEndReason.String)
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
	mermaidClassDefs(&b)
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
	mermaidClassDefs(&b)
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

// ----- Tier A: Kanban + Gantt (project-level, primarily) -----

// renderProjectKanban groups investigations under a project by status into a
// Mermaid kanban board. Each column is a status; each card is one investigation.
func renderProjectKanban(ctx context.Context, db *sql.DB, p state.Project, opts vizOptions) (string, error) {
	invs, err := state.ListInvestigations(ctx, db, p.ContextName, p.ProjectID, "", false)
	if err != nil {
		return "", err
	}
	// Bucket by status, preserving creation order within each bucket.
	buckets := map[string][]state.Investigation{
		"active":   {},
		"merged":   {},
		"dead-end": {},
		"archived": {},
	}
	for _, i := range invs {
		if !branchPasses(i, opts) {
			continue
		}
		buckets[i.Status] = append(buckets[i.Status], i)
	}
	for k := range buckets {
		sort.Slice(buckets[k], func(a, b int) bool {
			return buckets[k][a].CreatedAt < buckets[k][b].CreatedAt
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s — kanban\n---\n", p.Slug)
	b.WriteString("kanban\n")
	// Column order: Active, Merged, Dead-end, Archived
	for _, status := range []string{"active", "merged", "dead-end", "archived"} {
		col := strings.Title(status)
		if status == "dead-end" {
			col = "Dead-end"
		}
		fmt.Fprintf(&b, "  %s\n", col)
		for _, i := range buckets[status] {
			cardID := nodeIDFor("I", i.InvestigationID)
			fmt.Fprintf(&b, "    %s[%s — %s]\n",
				cardID, i.Slug, escapeLabel(truncate(i.Title, 60)))
		}
	}
	return b.String(), nil
}

// renderProjectGantt builds a Gantt chart for all investigations under a
// project. One bar per investigation from CreatedAt to UpdatedAt (or now if
// active). Sections group by status.
func renderProjectGantt(ctx context.Context, db *sql.DB, p state.Project, opts vizOptions) (string, error) {
	invs, err := state.ListInvestigations(ctx, db, p.ContextName, p.ProjectID, "", false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s — investigation timeline\n---\n", p.Slug)
	b.WriteString("gantt\n")
	fmt.Fprintf(&b, "   title %s\n", p.Title)
	b.WriteString("   dateFormat YYYY-MM-DD\n")
	b.WriteString("   axisFormat %Y-%m-%d\n")

	// Group by status; emit each non-empty section with status-typed task lines.
	groups := map[string][]state.Investigation{}
	for _, i := range invs {
		if !branchPasses(i, opts) {
			continue
		}
		groups[i.Status] = append(groups[i.Status], i)
	}
	now := time.Now().Unix()
	for _, status := range []string{"active", "merged", "dead-end", "archived"} {
		list := groups[status]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(a, b int) bool { return list[a].CreatedAt < list[b].CreatedAt })
		section := strings.Title(status)
		if status == "dead-end" {
			section = "Dead-end"
		}
		fmt.Fprintf(&b, "   section %s\n", section)
		for _, i := range list {
			start, end := ganttRange(i.CreatedAt, i.UpdatedAt, status, now)
			fmt.Fprintf(&b, "   %s :%s%s, %s\n", ganttLabel(i), statusGanttModifier(status), start, end)
		}
	}
	return b.String(), nil
}

// renderInvestigationGantt builds a Gantt chart for a single investigation
// showing its branches over time (each child branch as one bar). Useful when
// the user has multiple parallel branches and wants to see when each was alive.
func renderInvestigationGantt(ctx context.Context, db *sql.DB, root state.Investigation, opts vizOptions) (string, error) {
	tree, err := investigationSubtree(ctx, db, root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s — branches over time\n---\n", root.Slug)
	b.WriteString("gantt\n")
	fmt.Fprintf(&b, "   title %s\n", escapeLabel(root.Title))
	b.WriteString("   dateFormat YYYY-MM-DD\n")
	b.WriteString("   axisFormat %Y-%m-%d\n")

	// Sort all investigations in the subtree by created_at.
	all := append([]state.Investigation{}, tree...)
	sort.Slice(all, func(a, b int) bool { return all[a].CreatedAt < all[b].CreatedAt })

	now := time.Now().Unix()
	b.WriteString("   section Main\n")
	start, end := ganttRange(root.CreatedAt, root.UpdatedAt, root.Status, now)
	fmt.Fprintf(&b, "   %s :%s%s, %s\n", ganttLabel(root), statusGanttModifier(root.Status), start, end)
	b.WriteString("   section Branches\n")
	for _, i := range all {
		if i.InvestigationID == root.InvestigationID {
			continue
		}
		s, e := ganttRange(i.CreatedAt, i.UpdatedAt, i.Status, now)
		fmt.Fprintf(&b, "   %s :%s%s, %s\n", ganttLabel(i), statusGanttModifier(i.Status), s, e)
	}
	return b.String(), nil
}

// ganttLabel renders an investigation as a label safe for Mermaid gantt syntax.
// Mermaid uses ':' to separate task name from id+modifier+dates, so we never
// emit a colon in the label. Use an em-dash separator instead.
func ganttLabel(i state.Investigation) string {
	return escapeLabel(truncate(i.Slug+" — "+i.Title, 60))
}

// ganttRange computes start + end dates for a gantt bar. If start == end (same-
// day investigations, common for fresh test data), pad end by 1 day so the bar
// is visible.
func ganttRange(createdAt, updatedAt int64, status string, now int64) (string, string) {
	endTs := updatedAt
	if status == "active" {
		endTs = now
	}
	if endTs <= createdAt {
		endTs = createdAt + 86400 // 1 day
	}
	start := time.Unix(createdAt, 0).UTC().Format("2006-01-02")
	end := time.Unix(endTs, 0).UTC().Format("2006-01-02")
	if start == end {
		end = time.Unix(createdAt+86400, 0).UTC().Format("2006-01-02")
	}
	return start, end
}

func statusGanttModifier(status string) string {
	switch status {
	case "active":
		return "active, "
	case "merged", "archived":
		return "done, "
	case "dead-end":
		return "crit, "
	}
	return ""
}
