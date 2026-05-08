package capability

import (
	"github.com/abc-cluster/abc-cluster-cli/internal/state"
	"github.com/abc-cluster/abc-cluster-cli/internal/state/migrations"
)

// localStateSatisfies evaluates a Need against the local-state
// pseudo-service per OQ-CAP-8. Local-state's version is the highest
// applied migration name; features are derived from the
// migration→features table.
//
// Best-effort: if we can't open the local DB to query schema_migrations,
// fall back to the embedded-migrations list as the version (a fresh
// install has at least the 0001 floor when any verb has run once).
func localStateSatisfies(n Need) bool {
	have := highestAppliedMigration()
	if have == "" {
		// No DB ever opened; assume the embedded floor.
		have = embeddedFloor()
	}
	if !versionAtLeast(have, n.MinVersion) {
		return false
	}
	if len(n.Features) == 0 {
		return true
	}
	feats := localStateFeatures(have)
	for _, f := range n.Features {
		if !contains(feats, f) {
			return false
		}
	}
	return true
}

// highestAppliedMigration opens the local DB and returns the highest
// applied migration name (e.g. "0007_annotations_tags_json_and_withdraw").
// Returns "" if the DB is unreachable or empty — callers fall back.
func highestAppliedMigration() string {
	db, err := state.Open()
	if err != nil {
		return ""
	}
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var v string
	if err := rows.Scan(&v); err != nil {
		return ""
	}
	return v
}

// embeddedFloor returns the lowest embedded migration name. Used as a
// floor for fresh installs.
func embeddedFloor() string {
	versions, err := migrations.List()
	if err != nil || len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// localStateFeatures returns the union of features introduced by every
// migration up to and including the given version. Mirrors the table
// in internal/state/migrations/features.go (which is the canonical
// home; we just call into it).
func localStateFeatures(highestApplied string) []string {
	versions, err := migrations.List()
	if err != nil {
		return nil
	}
	var feats []string
	seen := map[string]bool{}
	for _, v := range versions {
		if v > highestApplied {
			break
		}
		for _, f := range migrations.FeaturesIntroducedBy(v) {
			if !seen[f] {
				feats = append(feats, f)
				seen[f] = true
			}
		}
	}
	return feats
}
