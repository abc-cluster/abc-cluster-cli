package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestConcurrentWriters exercises spec §A acceptance: concurrent CLI processes
// hitting the same state.db succeed without errors. Two goroutines share a
// single *sql.DB (same as a single CLI process), but the modernc.org/sqlite
// driver routes writes through SQLite's WAL serialisation, and busy_timeout
// covers contention. This is a softer version of the §H multi-process test.
func TestConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; ctx.Err() == nil && i < 25; i++ {
				p := Project{
					ProjectID:   NewProjectID(),
					Slug:        slugFor(id, i),
					ContextName: "default",
					Title:       "concurrent",
				}
				if _, err := CreateProject(ctx, db, p); err != nil && ctx.Err() == nil {
					t.Errorf("worker %d iter %d: %v", id, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Errorf("expected projects to exist")
	}
}

func slugFor(worker, iter int) string {
	return concatLowerHex(worker, iter)
}

func concatLowerHex(a, b int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	return string([]byte{alphabet[(a*7+1)%26], alphabet[(b*3+5)%26]}) + "-w-" + itoa(worker(a, b))
}

func worker(a, b int) int { return a*1000 + b }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// Silence unused import warning when running individual tests.
var _ sql.NullString
