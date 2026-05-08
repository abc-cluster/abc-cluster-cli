-- 0006_annotation_revisions.sql — audit trail for annotation mutations.
-- See specs/active/abc-investigation.md §B.3.1.
--
-- Every body edit, tag change, move between investigations, withdraw, and
-- restore writes one row here capturing the PREVIOUS state. The
-- `annotations` row always carries the current value; reconstructing
-- history requires walking revisions in `edited_at DESC` order from the
-- current annotation backward.

CREATE TABLE IF NOT EXISTS annotation_revisions (
  revision_id            INTEGER PRIMARY KEY AUTOINCREMENT,
  annotation_id          TEXT NOT NULL REFERENCES annotations(annotation_id),
  prev_body              TEXT,
  prev_tags_json         TEXT,
  prev_investigation_id  TEXT,
  edited_at              INTEGER NOT NULL,
  edit_kind              TEXT NOT NULL,
  edit_reason            TEXT
);
CREATE INDEX IF NOT EXISTS idx_annotation_revisions_ann
  ON annotation_revisions(annotation_id, edited_at DESC);
