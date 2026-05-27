// Package runner spawns a background goroutine that polls Nomad allocation
// status for a submitted job and writes the completion fields back to the
// runs row in ~/.abc/local.db. Resolves spec abc-investigation §E + OQ-6.
//
// Lifecycle: started at the END of `abc pipeline run` / `abc job run` /
// `abc module run` AFTER auto-attach has inserted the runs row. Polls every
// PollInterval (default 5s) up to MaxDuration (default 24h). Holds its own
// DB handle so it does not block the caller's `db.Close()`. Best-effort —
// if the CLI process exits before completion, the run row remains in
// `status='running'` (visible to a future `abc accounting report
// --include-incomplete`).
package runner

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/cmd/utils"
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

// Defaults per spec §E + OQ-6.
const (
	DefaultPollInterval = 5 * time.Second
	DefaultMaxDuration  = 24 * time.Hour
)

// Config holds the watcher's tuning knobs.
type Config struct {
	PollInterval time.Duration
	MaxDuration  time.Duration
}

// WatchTarget describes one run to watch.
type WatchTarget struct {
	RunID     string
	JobID     string // Nomad job ID
	Namespace string
}

// Watch spawns a background goroutine that polls Nomad for the given target
// and writes back to runs on completion. It returns immediately. The
// goroutine opens its own DB handle and Nomad client; the caller may
// continue (and exit) without affecting correctness — early CLI exit just
// leaves the run row at status='running' (best-effort, per spec OQ-6).
//
// Before spawning the goroutine, Watch stores the Nomad job ID in the runs
// table synchronously (migration 0011). This allows ReconcileStuckRuns() to
// re-probe Nomad on a future invocation even when the goroutine died early.
//
// addr/token come from the caller's resolved Nomad config (typically via
// `abc pipeline run` flags or ~/.abc/config.yaml). If addr is empty, no
// poll runs (warning emitted to stderr if logTo is non-nil).
func Watch(addr, token, region string, target WatchTarget, cfg Config, logTo io.Writer) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = DefaultMaxDuration
	}
	if addr == "" || target.RunID == "" || target.JobID == "" {
		if logTo != nil {
			fmt.Fprintln(logTo, "[abc] run-watcher: insufficient config (addr/runID/jobID); skipping write-back")
		}
		return
	}

	// Store the Nomad job ID synchronously so ReconcileStuckRuns() can find it
	// even if the goroutine below dies before writing back the completion row.
	if db, err := state.Open(); err == nil {
		if err := state.UpdateRunJobID(context.Background(), db, target.RunID, target.JobID); err != nil && logTo != nil {
			fmt.Fprintf(logTo, "[abc] run-watcher: UpdateRunJobID: %v\n", err)
		}
		db.Close()
	}

	go run(addr, token, region, target, cfg, logTo)
}

// ReconcileStuckRuns queries the local state DB for runs that are still at
// status='running' with a nomad_job_id set and submitted more than minAge ago,
// polls Nomad for each, and writes back completion data. It is called at the
// start of accounting/emissions reports to catch runs whose background watcher
// goroutine died before completion (e.g. CLI was killed mid-run).
//
// Returns the number of runs successfully reconciled. Errors are logged to
// logTo (non-fatal: the report continues with whatever data is available).
//
// Pass `fallbackNamespace` to handle runs whose namespace column was never
// populated (`abc job run` historically didn't always write it). When the
// row's namespace is "", reconcile uses the fallback — typically the
// active context's Nomad namespace — instead of letting the Nomad
// client default to "default" and 404.
func ReconcileStuckRuns(ctx context.Context, nomadAddr, token, region string, minAge time.Duration, logTo io.Writer, fallbackNamespace ...string) int {
	if nomadAddr == "" {
		return 0
	}
	if minAge <= 0 {
		minAge = 30 * time.Second
	}
	// state.Open() returns a process-lifetime cached connection. Do NOT close
	// it here — the caller (e.g. accounting report) may hold the same handle
	// and will fail with "database is closed" if we defer db.Close().
	db, err := state.Open()
	if err != nil {
		if logTo != nil {
			fmt.Fprintf(logTo, "[abc] reconcile: state.Open: %v\n", err)
		}
		return 0
	}

	cutoff := time.Now().Add(-minAge).Unix()
	rows, err := db.QueryContext(ctx, `
		SELECT run_id, nomad_job_id, namespace
		FROM runs
		WHERE status = 'running'
		  AND completed_at IS NULL
		  AND nomad_job_id IS NOT NULL
		  AND submitted_at < ?`, cutoff)
	if err != nil {
		if logTo != nil {
			fmt.Fprintf(logTo, "[abc] reconcile: query stuck runs: %v\n", err)
		}
		return 0
	}
	defer rows.Close()

	type stuckRun struct {
		runID    string
		jobID    string
		ns       string
	}
	var stuck []stuckRun
	for rows.Next() {
		var r stuckRun
		var nsNull sql.NullString
		if err := rows.Scan(&r.runID, &r.jobID, &nsNull); err != nil {
			continue
		}
		if nsNull.Valid {
			r.ns = nsNull.String
		}
		stuck = append(stuck, r)
	}
	rows.Close()

	if len(stuck) == 0 {
		return 0
	}

	fallbackNS := ""
	if len(fallbackNamespace) > 0 {
		fallbackNS = fallbackNamespace[0]
	}

	nc := utils.NewNomadClient(nomadAddr, token, region)
	reconciled := 0
	for _, r := range stuck {
		ns := r.ns
		if ns == "" {
			ns = fallbackNS
		}
		target := WatchTarget{RunID: r.runID, JobID: r.jobID, Namespace: ns}
		if final, ok := pollOnce(ctx, db, nc, target, logTo); ok {
			if logTo != nil {
				fmt.Fprintf(logTo, "[abc] reconcile: run %s completed → %s\n", r.runID, final)
			}
			reconciled++
		}
	}
	return reconciled
}

func run(addr, token, region string, t WatchTarget, cfg Config, logTo io.Writer) {
	// Independent DB handle so the caller can Close their own.
	db, err := state.Open()
	if err != nil {
		if logTo != nil {
			fmt.Fprintf(logTo, "[abc] run-watcher: state.Open: %v\n", err)
		}
		return
	}
	defer db.Close()

	nc := utils.NewNomadClient(addr, token, region)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.MaxDuration)
	defer cancel()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// First poll immediately rather than waiting for the first tick.
	for {
		if final, ok := pollOnce(ctx, db, nc, t, logTo); ok {
			if logTo != nil {
				fmt.Fprintf(logTo, "[abc] run-watcher: run %s reached terminal status %q; wrote completion to local.db\n",
					t.RunID, final)
			}
			return
		}
		select {
		case <-ctx.Done():
			if logTo != nil {
				fmt.Fprintf(logTo, "[abc] run-watcher: timed out after %s for run %s; row stays at status='running'\n",
					cfg.MaxDuration, t.RunID)
			}
			return
		case <-ticker.C:
		}
	}
}

// pollOnce checks Nomad for the job's allocations. Returns (status, true) if
// the run reached a terminal state and was written back; (_, false) if still
// running or transient error.
func pollOnce(ctx context.Context, db *sql.DB, nc *utils.NomadClient, t WatchTarget, logTo io.Writer) (string, bool) {
	allocs, err := nc.GetJobAllocs(ctx, t.JobID, t.Namespace, false)
	if err != nil {
		// Transient — keep polling.
		return "", false
	}
	if len(allocs) == 0 {
		return "", false
	}
	// Pick the most recently-modified alloc as the canonical one for this job.
	chosen := allocs[0]
	for _, a := range allocs[1:] {
		if a.ModifyTime > chosen.ModifyTime {
			chosen = a
		}
	}
	if !utils.AllocClientTerminalStatus(chosen.ClientStatus) {
		return "", false
	}

	// Map Nomad client status to our runs.status.
	status := "completed"
	switch strings.ToLower(strings.TrimSpace(chosen.ClientStatus)) {
	case "failed", "lost":
		status = "failed"
	}

	// Walltime: prefer task's StartedAt → FinishedAt (one task is typical for
	// our pipeline / job / module runs). Fall back to alloc CreateTime → now.
	var (
		startedAt, finishedAt time.Time
	)
	exitCode := int64(0)
	exitReason := ""
	for _, ts := range chosen.TaskStates {
		if ts.StartedAt != "" {
			if tt, err := time.Parse(time.RFC3339Nano, ts.StartedAt); err == nil {
				if startedAt.IsZero() || tt.Before(startedAt) {
					startedAt = tt
				}
			}
		}
		if ts.FinishedAt != "" {
			if tt, err := time.Parse(time.RFC3339Nano, ts.FinishedAt); err == nil {
				if tt.After(finishedAt) {
					finishedAt = tt
				}
			}
		}
		if ts.Failed && exitReason == "" {
			// Best-effort: pluck the most useful event message.
			for _, ev := range ts.Events {
				if ev.DisplayMessage != "" {
					exitReason = ev.DisplayMessage
					break
				}
			}
		}
		// Try to read exit_code from the most recent "Terminated" event details.
		for _, ev := range ts.Events {
			if ev.Type == "Terminated" {
				if ec, ok := ev.Details["exit_code"]; ok {
					if n, err := strconv.ParseInt(ec, 10, 64); err == nil {
						exitCode = n
					}
				}
			}
		}
	}
	if startedAt.IsZero() && chosen.CreateTime > 0 {
		startedAt = time.Unix(0, chosen.CreateTime)
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}

	walltimeSec := int64(0)
	if !startedAt.IsZero() {
		walltimeSec = int64(finishedAt.Sub(startedAt).Seconds())
		if walltimeSec < 0 {
			walltimeSec = 0
		}
	}

	// Compute resource hours from the run's stored cpu_request / mem_request_gb
	// and the observed walltime. Both fields may be NULL (zero-value) for runs
	// submitted before these columns existed or before the flag was set.
	walltimeHours := float64(walltimeSec) / 3600.0
	cpuHours := 0.0
	memGBHours := 0.0
	{
		var cpuReq, memReq sql.NullFloat64
		_ = db.QueryRowContext(ctx,
			`SELECT cpu_request, mem_request_gb FROM runs WHERE run_id = ?`, t.RunID,
		).Scan(&cpuReq, &memReq)
		if cpuReq.Valid && cpuReq.Float64 > 0 {
			cpuHours = cpuReq.Float64 * walltimeHours
		}
		if memReq.Valid && memReq.Float64 > 0 {
			memGBHours = memReq.Float64 * walltimeHours
		}
	}

	// Write back. CompleteRun handles status/exit_reason/cpu_hours/mem/wall;
	// we additionally update exit_code separately (no helper exists).
	if err := state.CompleteRun(ctx, db, t.RunID, status, exitReason,
		finishedAt.Unix(), cpuHours, memGBHours, walltimeSec); err != nil {
		if logTo != nil {
			fmt.Fprintf(logTo, "[abc] run-watcher: CompleteRun: %v\n", err)
		}
	}
	// pending_seconds = first-task StartedAt - runs.submitted_at.
	// Best-effort, per spec abc-report.md §B: if either timestamp is
	// missing or the difference is negative, leave the column NULL.
	if !startedAt.IsZero() {
		var submittedAt sql.NullInt64
		if err := db.QueryRowContext(ctx,
			`SELECT submitted_at FROM runs WHERE run_id = ?`, t.RunID).Scan(&submittedAt); err == nil && submittedAt.Valid {
			pending := startedAt.Unix() - submittedAt.Int64
			if pending >= 0 {
				if _, err := db.ExecContext(ctx,
					`UPDATE runs SET pending_seconds = ? WHERE run_id = ?`,
					pending, t.RunID); err != nil && logTo != nil {
					fmt.Fprintf(logTo, "[abc] run-watcher: pending_seconds update: %v\n", err)
				}
			}
		}
	}
	if exitCode != 0 || status == "failed" {
		if _, err := db.ExecContext(ctx,
			`UPDATE runs SET exit_code = ? WHERE run_id = ?`, exitCode, t.RunID); err != nil && logTo != nil {
			fmt.Fprintf(logTo, "[abc] run-watcher: exit_code update: %v\n", err)
		}
	}
	return status, true
}
