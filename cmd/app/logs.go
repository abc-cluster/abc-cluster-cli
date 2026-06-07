package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/config"
	"github.com/abc-cluster/abc-cluster-cli/internal/floor"
	"github.com/spf13/cobra"
)

// logsFollowInterval is the poll cadence for `abc app logs -f` against the log
// archive (VictoriaLogs has no server-side live-tail; we poll for new lines).
const logsFollowInterval = 2 * time.Second

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Stream an app's container logs",
		Long: `Stream a deployed app's container logs from the cluster log archive
(VictoriaLogs). Filters to the app's allocations.

With --follow, polls the archive for new lines until interrupted (Ctrl-C).`,
		Args: cobra.ExactArgs(1),
		RunE: runLogs,
	}
	cmd.Flags().Int("tail", 200, "Number of recent log lines to show")
	cmd.Flags().String("since", "1h", "Show logs since this duration/time (e.g. 30m, 2h, RFC3339)")
	cmd.Flags().BoolP("follow", "f", false, "Stream new log lines live until interrupted")
	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	nc := nomadClientFromCmd(cmd)

	r, err := resolveApp(ctx, nc, args[0])
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	active := cfg.ActiveCtx()
	logsHTTP, ok := config.GetAdminFloorField(&active.Admin.Services, "loki", "http")
	if !ok || logsHTTP == "" {
		return fmt.Errorf(
			"log archive URL not configured for context %q\n"+
				"  Run: abc cluster capabilities sync",
			cfg.ActiveContext)
	}

	tail, _ := cmd.Flags().GetInt("tail")
	since, _ := cmd.Flags().GetString("since")
	follow, _ := cmd.Flags().GetBool("follow")

	// Resolve this app's alloc IDs (all, including completed, for history).
	allocs, _ := nc.GetJobAllocs(ctx, r.JobID, nc.DefaultNamespace(), true)
	var allocIDs []string
	for i := range allocs {
		allocIDs = append(allocIDs, allocs[i].ID)
	}
	if len(allocIDs) == 0 {
		return fmt.Errorf("no allocations found for app %q — it may not have been scheduled yet", r.Name)
	}

	selectors := []string{
		fmt.Sprintf(`alloc_id=~"^(%s)$"`, strings.Join(allocIDs, "|")),
		`stream=~"stdout|stderr"`,
	}
	backend := floor.DetectLogsBackend(logsHTTP)

	// queryLogs runs one range query against whichever backend is configured.
	queryLogs := func(sinceArg string, limit int) ([]floor.LokiEntry, error) {
		switch backend {
		case floor.BackendVictoriaLogs:
			streamSel := "{" + strings.Join(selectors, ",") + "}"
			vc := floor.NewVictoriaLogsClient(logsHTTP)
			es, qerr := vc.QueryStream(ctx, streamSel, "", sinceArg, "", limit)
			if qerr != nil {
				return nil, fmt.Errorf("victorialogs query: %w", qerr)
			}
			return es, nil
		default:
			logql := "{" + strings.Join(selectors, ",") + "}"
			lc := floor.NewLokiClient(logsHTTP)
			es, qerr := lc.QueryRange(ctx, logql, sinceArg, "", limit)
			if qerr != nil {
				return nil, fmt.Errorf("loki query: %w", qerr)
			}
			return es, nil
		}
	}

	printEntry := func(e floor.LokiEntry) {
		fmt.Fprintf(out, "[%s %s] %s\n", e.Timestamp.Format("15:04:05"), e.Labels["stream"], e.Line)
	}

	// Initial backlog.
	entries, err := queryLogs(since, tail)
	if err != nil {
		return err
	}
	if len(entries) == 0 && !follow {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"  No logs found for app %q in the last %s.\n", r.Name, since)
		return nil
	}
	var last time.Time
	for _, e := range entries {
		printEntry(e)
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	if !follow {
		return nil
	}

	// Follow: poll for lines newer than the last one printed. A logs-archive
	// hiccup is reported but does not abort the stream. Ctrl-C (ctx cancel) ends.
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(logsFollowInterval):
		}
		sinceArg := since
		if !last.IsZero() {
			sinceArg = last.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano)
		}
		fresh, qerr := queryLogs(sinceArg, 1000)
		if qerr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  (log follow: %v; retrying)\n", qerr)
			continue
		}
		for _, e := range fresh {
			if !e.Timestamp.After(last) {
				continue
			}
			printEntry(e)
			last = e.Timestamp
		}
	}
}
