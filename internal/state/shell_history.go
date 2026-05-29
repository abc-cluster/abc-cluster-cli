package state

import (
	"database/sql"
	"time"
)

// ShellCommand is one row from shell_history.
type ShellCommand struct {
	ID         int64
	AtuinID    string    // empty for bash_history imports
	Command    string
	Cwd        string
	ExitCode   *int      // nil when unknown
	DurationMS *int64    // nil when unknown
	SessionID  string    // atuin $ATUIN_SESSION
	Hostname   string
	RanAt      time.Time
	Source     string // "atuin" | "bash_history"
	Context    string // workbench slot or "" for local
	ImportedAt time.Time
}

// ImportedAtuinIDs returns the set of atuin UUIDs already in shell_history,
// optionally scoped to a workbench context. Used to skip rows already imported.
func ImportedAtuinIDs(db *sql.DB, context string) (map[string]struct{}, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if context != "" {
		rows, err = db.Query(
			`SELECT atuin_id FROM shell_history WHERE atuin_id IS NOT NULL AND context = ?`,
			context,
		)
	} else {
		rows, err = db.Query(
			`SELECT atuin_id FROM shell_history WHERE atuin_id IS NOT NULL`,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// InsertShellCommands bulk-inserts shell history rows. Rows whose atuin_id
// already exists in the table are silently skipped (INSERT OR IGNORE).
// Returns the count of newly inserted rows.
func InsertShellCommands(db *sql.DB, cmds []ShellCommand) (int, error) {
	if len(cmds) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO shell_history
		  (atuin_id, command, cwd, exit_code, duration_ms,
		   session_id, hostname, ran_at, source, context, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	inserted := 0
	for _, c := range cmds {
		atuinID := sql.NullString{String: c.AtuinID, Valid: c.AtuinID != ""}
		res, err := stmt.Exec(
			atuinID, c.Command, c.Cwd, c.ExitCode, c.DurationMS,
			c.SessionID, c.Hostname, c.RanAt.Unix(),
			c.Source, c.Context, now,
		)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		n, _ := res.RowsAffected()
		inserted += int(n)
	}
	return inserted, tx.Commit()
}

// ListShellHistory returns commands ordered newest-first, optionally filtered
// by context. limit <= 0 means unlimited.
func ListShellHistory(db *sql.DB, context string, limit int) ([]ShellCommand, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const cols = `id, COALESCE(atuin_id,''), command, cwd,
	              exit_code, duration_ms, session_id, hostname,
	              ran_at, source, context, imported_at`
	q := `SELECT ` + cols + ` FROM shell_history`
	if context != "" {
		q += ` WHERE context = ?`
		if limit > 0 {
			q += ` ORDER BY ran_at DESC LIMIT ?`
			rows, err = db.Query(q, context, limit)
		} else {
			q += ` ORDER BY ran_at DESC`
			rows, err = db.Query(q, context)
		}
	} else {
		if limit > 0 {
			q += ` ORDER BY ran_at DESC LIMIT ?`
			rows, err = db.Query(q, limit)
		} else {
			q += ` ORDER BY ran_at DESC`
			rows, err = db.Query(q)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShellCommand
	for rows.Next() {
		c, e := scanShellCommand(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanShellCommand(rows *sql.Rows) (ShellCommand, error) {
	var c ShellCommand
	var ranAt, importedAt int64
	if err := rows.Scan(
		&c.ID, &c.AtuinID, &c.Command, &c.Cwd,
		&c.ExitCode, &c.DurationMS, &c.SessionID, &c.Hostname,
		&ranAt, &c.Source, &c.Context, &importedAt,
	); err != nil {
		return ShellCommand{}, err
	}
	c.RanAt = time.Unix(ranAt, 0)
	c.ImportedAt = time.Unix(importedAt, 0)
	return c, nil
}
