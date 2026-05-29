-- 0015_shell_history.sql — shell_history table.
-- Stores shell commands imported from atuin (primary) or ~/.bash_history (fallback).
-- One row per command. atuin_id is the atuin UUID; set on import to prevent
-- duplicate rows on re-import. Source 'bash_history' leaves atuin_id empty.
--
-- Design reference: brainstorms/abc-local-shell/2026-05-23-atuin-client-model.md
-- Workbench integration: specs/active/abc-workbench.md §Atuin integration

CREATE TABLE IF NOT EXISTS shell_history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    atuin_id     TEXT UNIQUE,            -- atuin row UUID; NULL for bash_history imports
    command      TEXT    NOT NULL,
    cwd          TEXT    NOT NULL DEFAULT '',
    exit_code    INTEGER,                -- NULL when unknown (bash_history fallback)
    duration_ms  INTEGER,               -- ms; NULL when unknown
    session_id   TEXT    NOT NULL DEFAULT '',  -- atuin $ATUIN_SESSION for context-binding
    hostname     TEXT    NOT NULL DEFAULT '',
    ran_at       INTEGER NOT NULL,      -- unix epoch seconds (nanoseconds → seconds on import)
    source       TEXT    NOT NULL DEFAULT 'atuin',  -- 'atuin' | 'bash_history'
    context      TEXT    NOT NULL DEFAULT '',       -- workbench slot name, or '' for local
    imported_at  INTEGER NOT NULL       -- unix epoch seconds
);

CREATE INDEX IF NOT EXISTS idx_sh_ran_at
    ON shell_history(ran_at DESC);

CREATE INDEX IF NOT EXISTS idx_sh_context_at
    ON shell_history(context, ran_at DESC);

CREATE INDEX IF NOT EXISTS idx_sh_session
    ON shell_history(session_id);
