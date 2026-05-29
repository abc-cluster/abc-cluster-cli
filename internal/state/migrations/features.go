package migrations

// migrationFeatures maps each migration to the features it introduces.
// Used by the capability layer (internal/capability/local_state.go) to
// reason about what the local-state pseudo-service can do based on the
// highest applied migration.
//
// Per OQ-CAP-8: local-state's `Version` field is the highest applied
// migration name; its `Features[]` is the union of every entry's
// contribution up to and including that version.
//
// When adding a new migration, add a new entry here listing the features
// the migration enables. Features are user-facing, stable identifiers
// (e.g. "scratch-storage-attribution"); pick names verbs will check
// against in their `Need.Features` declarations.
//
// Note: 0003 is intentionally absent — it was renumbered to 0005
// historically (see internal/state/migrations/0005_rate_card_revisions.sql).
var migrationFeatures = map[string][]string{
	"0001_initial": {
		"projects",
		"investigations",
		"annotations",
		"runs",
		"active_pointers",
		"cli_audit",
		"citations",
	},
	"0002_add_gpu_count": {
		"gpu-attribution",
	},
	"0004_add_scratch_gb": {
		"scratch-storage-attribution",
	},
	"0005_rate_card_revisions": {
		"bitemporal-rate-cards",
	},
	"0006_annotation_revisions": {
		"annotation-history",
		"annotation-revisions",
	},
	"0007_annotations_tags_json_and_withdraw": {
		"annotation-multi-tags",
		"annotation-soft-delete",
	},
	"0008_runs_queue_wait": {
		"queue-wait-fraction",
	},
	"0009_runs_resource_request": {
		"resource-fit",
	},
	"0010_runs_submission_source": {
		"template-vs-handwritten",
	},
	"0014_uploads": {
		"upload-registry",
	},
	"0015_shell_history": {
		"shell-history",
	},
}

// FeaturesIntroducedBy returns the features a single migration adds.
// Returns an empty slice for unknown / unmapped migrations (additive —
// callers union across all applied migrations, so missing entries are
// harmless).
func FeaturesIntroducedBy(version string) []string {
	return append([]string(nil), migrationFeatures[version]...)
}
