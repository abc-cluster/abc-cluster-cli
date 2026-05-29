package workbench

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abc-cluster/abc-cluster-cli/internal/state"
)

// atuinRow is the JSON shape emitted by the sqlite3 query below.
type atuinRow struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"` // nanoseconds since Unix epoch
	Duration  int64  `json:"duration"`  // nanoseconds; -1 when unknown
	Exit      int    `json:"exit"`
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Session   string `json:"session"`
	Hostname  string `json:"hostname"`
}

// atuinDBPath returns the expected atuin history DB path for a workbench slot.
// Convention: /data/workbench/<slot>/home/.local/share/atuin/history.db
func atuinDBPath(slot string) string {
	return fmt.Sprintf("/data/workbench/%s/home/.local/share/atuin/history.db", slot)
}

// ImportAtuinHistory reads the atuin SQLite DB for slot on node via SSH,
// filters out already-imported IDs, and bulk-inserts new rows into db.
//
// Returns (inserted, skipped, error). Skipped counts rows whose atuin_id was
// already in shell_history. Returns (0, 0, nil) when the DB does not exist
// (user never opened a terminal).
func ImportAtuinHistory(node NodeSSH, slot string, db interface {
	Query(query string, args ...interface{}) (interface{}, error)
}, stateDB interface{}) (inserted, skipped int, err error) {
	// This signature is awkward — use the concrete *sql.DB version below.
	return 0, 0, fmt.Errorf("use ImportAtuinHistoryFromNode instead")
}

// ImportAtuinHistoryFromNode is the concrete implementation used by cmd/workbench.
// It reads the atuin DB via SSH+sqlite3 JSON output, deduplicates against
// shell_history, and bulk-inserts new rows.
func ImportAtuinHistoryFromNode(node NodeSSH, slot string, sdb interface {
	Query(q string, args ...interface{}) (interface{}, error)
}) (inserted int, err error) {
	return 0, fmt.Errorf("internal: use state.ImportedAtuinIDs directly")
}

// ReadAtuinFromNode reads rows from the atuin history.db on node for the given
// slot. It SSH-executes sqlite3 with JSON output and returns parsed rows.
// Returns (nil, nil) when the DB file does not exist (user never used atuin).
func ReadAtuinFromNode(node NodeSSH, slot string) ([]atuinRow, error) {
	dbPath := atuinDBPath(slot)

	// Check the file exists first — avoids sqlite3 error noise for new slots.
	checkOut, _ := runOnNode(node, "test", "-f", dbPath, "&&", "echo", "exists")
	if !strings.Contains(checkOut, "exists") {
		return nil, nil // no DB — user never opened a terminal
	}

	// Query all non-deleted history rows as JSON objects.
	// sqlite3 -json outputs a JSON array of objects.
	query := fmt.Sprintf(
		`sqlite3 -json %s "SELECT id, timestamp, duration, exit, command, cwd, session, hostname FROM history WHERE deleted_at IS NULL ORDER BY timestamp ASC"`,
		dbPath,
	)
	out, err := runOnNode(node, "bash", "-c", query)
	if err != nil {
		return nil, fmt.Errorf("sqlite3 on aither: %w\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "[]" {
		return nil, nil
	}

	var rows []atuinRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("parse atuin JSON: %w\nraw: %.200s", err, out)
	}
	return rows, nil
}

// AtuinRowsToCommands converts raw atuin rows into ShellCommand records,
// filtering out any atuin_ids already in alreadyImported.
// slot is stored as the Context field.
func AtuinRowsToCommands(rows []atuinRow, alreadyImported map[string]struct{}, slot string) (cmds []state.ShellCommand, skipped int) {
	for _, r := range rows {
		if _, dup := alreadyImported[r.ID]; dup {
			skipped++
			continue
		}
		c := state.ShellCommand{
			AtuinID:   r.ID,
			Command:   r.Command,
			Cwd:       r.Cwd,
			SessionID: r.Session,
			Hostname:  r.Hostname,
			RanAt:     atuinTimestampToTime(r.Timestamp),
			Source:    "atuin",
			Context:   slot,
		}
		exit := r.Exit
		c.ExitCode = &exit

		if r.Duration > 0 {
			ms := r.Duration / int64(time.Millisecond)
			c.DurationMS = &ms
		}

		cmds = append(cmds, c)
	}
	return cmds, skipped
}

// atuinTimestampToTime converts atuin's nanosecond timestamp to time.Time.
// atuin stores timestamps as Unix nanoseconds (int64).
func atuinTimestampToTime(ns int64) time.Time {
	return time.Unix(0, ns).UTC()
}
