-- 0007_annotations_tags_json_and_withdraw.sql — evolve annotations table.
-- See specs/active/abc-investigation.md §B.3.
--
-- Adds:
--   - tags_json TEXT (JSON array; supersedes singular `tag` column for new writes)
--   - withdrawn_at INTEGER, withdrawn_reason TEXT (soft-delete)
--   - updated_at INTEGER (per-row mutation timestamp)
--
-- Migrates existing `tag` values into `tags_json` as a single-element JSON
-- array if non-NULL. Keeps the legacy `tag` column for backward compat
-- (additive only — read both, write tags_json).

ALTER TABLE annotations ADD COLUMN tags_json        TEXT;
ALTER TABLE annotations ADD COLUMN withdrawn_at     INTEGER;
ALTER TABLE annotations ADD COLUMN withdrawn_reason TEXT;
ALTER TABLE annotations ADD COLUMN updated_at       INTEGER;

-- Backfill tags_json from legacy tag column (single-element JSON array).
UPDATE annotations
   SET tags_json = json_array(tag)
 WHERE tag IS NOT NULL AND tag <> ''
   AND (tags_json IS NULL OR tags_json = '');

-- Backfill updated_at to created_at where not yet set.
UPDATE annotations SET updated_at = created_at WHERE updated_at IS NULL;

-- Partial index over active (non-withdrawn) annotations, scoped by investigation.
CREATE INDEX IF NOT EXISTS idx_annotations_active
  ON annotations(investigation_id) WHERE withdrawn_at IS NULL;
