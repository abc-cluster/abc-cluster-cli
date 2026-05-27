-- 0013_workbench.sql — workbench_sessions table.
-- Tracks interactive workbench sessions started by `abc workbench start`.
-- All CREATE/INDEX statements are idempotent.

CREATE TABLE IF NOT EXISTS workbench_sessions (
    session_id         TEXT PRIMARY KEY,
    user               TEXT    NOT NULL,
    job_id             TEXT    NOT NULL,
    namespace          TEXT    NOT NULL DEFAULT 'abc-sessions',
    ide                TEXT    NOT NULL DEFAULT 'quarto',
    host               TEXT,
    port               INTEGER,
    token              TEXT,
    cores              INTEGER NOT NULL DEFAULT 2,
    memory_mb          INTEGER NOT NULL DEFAULT 4096,
    gpus               INTEGER NOT NULL DEFAULT 0,
    telemetry          INTEGER NOT NULL DEFAULT 1,
    idle_timeout_secs  INTEGER NOT NULL DEFAULT 14400,
    status             TEXT    NOT NULL DEFAULT 'starting',
    started_at         INTEGER,
    stopped_at         INTEGER,
    last_seen_at       INTEGER
);

CREATE INDEX IF NOT EXISTS idx_wb_user_status
    ON workbench_sessions(user, status);
