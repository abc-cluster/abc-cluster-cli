// Package workbench owns the data model and HCL generator for interactive
// workbench sessions (abc workbench start/stop/status/url/logs).
package workbench

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoSession is returned when no session row matches the query.
var ErrNoSession = errors.New("no workbench session found")

// Session mirrors one row in workbench_sessions.
type Session struct {
	SessionID       string
	User            string
	JobID           string
	Namespace       string
	IDE             string
	Host            string
	Port            int
	Token           string
	Cores           int
	MemoryMB        int
	GPUs            int
	Telemetry       bool
	IdleTimeoutSecs int
	Status          string // starting | running | stopped | failed
	StartedAt       int64
	StoppedAt       int64
	LastSeenAt      int64
}

// InsertSession writes a new session row with status=starting.
func InsertSession(ctx context.Context, db *sql.DB, s *Session) error {
	now := time.Now().Unix()
	tel := 1
	if !s.Telemetry {
		tel = 0
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO workbench_sessions
		  (session_id, user, job_id, namespace, ide, host, port, token,
		   cores, memory_mb, gpus, telemetry, idle_timeout_secs,
		   status, started_at, last_seen_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.SessionID, s.User, s.JobID, s.Namespace, s.IDE,
		s.Host, s.Port, s.Token,
		s.Cores, s.MemoryMB, s.GPUs, tel, s.IdleTimeoutSecs,
		"starting", now, now,
	)
	return err
}

// UpdateRunning marks a session as running and stores the resolved host:port.
func UpdateRunning(ctx context.Context, db *sql.DB, sessionID, host string, port int) error {
	_, err := db.ExecContext(ctx, `
		UPDATE workbench_sessions
		   SET status='running', host=?, port=?, started_at=?, last_seen_at=?
		 WHERE session_id=?`,
		host, port, time.Now().Unix(), time.Now().Unix(), sessionID,
	)
	return err
}

// UpdateStopped marks a session as stopped.
func UpdateStopped(ctx context.Context, db *sql.DB, sessionID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE workbench_sessions
		   SET status='stopped', stopped_at=?
		 WHERE session_id=?`,
		time.Now().Unix(), sessionID,
	)
	return err
}

// ActiveSession returns the running or starting session for a user.
// Returns ErrNoSession if none.
func ActiveSession(ctx context.Context, db *sql.DB, user string) (*Session, error) {
	row := db.QueryRowContext(ctx, `
		SELECT session_id, user, job_id, namespace, ide,
		       COALESCE(host,''), COALESCE(port,0), COALESCE(token,''),
		       cores, memory_mb, gpus, telemetry, idle_timeout_secs,
		       status, COALESCE(started_at,0), COALESCE(stopped_at,0),
		       COALESCE(last_seen_at,0)
		  FROM workbench_sessions
		 WHERE user=? AND status IN ('running','starting')
		 ORDER BY started_at DESC
		 LIMIT 1`, user)
	return scanSession(row)
}

// LatestSession returns the most recent session for a user regardless of status.
// Returns ErrNoSession if none.
func LatestSession(ctx context.Context, db *sql.DB, user string) (*Session, error) {
	row := db.QueryRowContext(ctx, `
		SELECT session_id, user, job_id, namespace, ide,
		       COALESCE(host,''), COALESCE(port,0), COALESCE(token,''),
		       cores, memory_mb, gpus, telemetry, idle_timeout_secs,
		       status, COALESCE(started_at,0), COALESCE(stopped_at,0),
		       COALESCE(last_seen_at,0)
		  FROM workbench_sessions
		 WHERE user=?
		 ORDER BY started_at DESC
		 LIMIT 1`, user)
	return scanSession(row)
}

func scanSession(row *sql.Row) (*Session, error) {
	var s Session
	var tel int
	err := row.Scan(
		&s.SessionID, &s.User, &s.JobID, &s.Namespace, &s.IDE,
		&s.Host, &s.Port, &s.Token,
		&s.Cores, &s.MemoryMB, &s.GPUs, &tel, &s.IdleTimeoutSecs,
		&s.Status, &s.StartedAt, &s.StoppedAt, &s.LastSeenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	s.Telemetry = tel == 1
	return &s, nil
}

