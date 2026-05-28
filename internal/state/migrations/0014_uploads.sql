-- 0014_uploads.sql — uploads table.
-- Tracks files successfully uploaded via `abc data upload`. One row per
-- completed upload; directory uploads record one row per file.
--
-- s3_path is the canonical destination constructed at upload time from the
-- active context's Nomad namespace (= MinIO bucket) and auth.whoami path
-- segment:  s3://<namespace>/user/<whoami-slug>/data/<filename>
-- Empty when the active context lacked bucket or identity info at upload time.

CREATE TABLE IF NOT EXISTS uploads (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    filename      TEXT    NOT NULL,
    s3_path       TEXT    NOT NULL DEFAULT '',
    original_path TEXT    NOT NULL DEFAULT '',
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    checksum      TEXT    NOT NULL DEFAULT '',
    context_name  TEXT    NOT NULL DEFAULT '',
    uploaded_at   TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_uploads_filename
    ON uploads(filename);

CREATE INDEX IF NOT EXISTS idx_uploads_context_at
    ON uploads(context_name, uploaded_at DESC);
