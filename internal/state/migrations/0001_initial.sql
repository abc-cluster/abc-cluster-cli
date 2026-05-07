-- 0001_initial.sql — initial schema for abc CLI local state
-- All CREATE statements are idempotent. See specs/active/abc-investigation.md §B.

CREATE TABLE IF NOT EXISTS projects (
  project_id     TEXT PRIMARY KEY,
  slug           TEXT NOT NULL,
  context_name   TEXT NOT NULL,
  title          TEXT NOT NULL,
  description    TEXT,
  status         TEXT NOT NULL DEFAULT 'active',
  tags_json      TEXT,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  UNIQUE (context_name, slug)
);
CREATE INDEX IF NOT EXISTS idx_projects_context_status ON projects(context_name, status);

CREATE TABLE IF NOT EXISTS investigations (
  investigation_id  TEXT PRIMARY KEY,
  slug              TEXT NOT NULL,
  context_name      TEXT NOT NULL,
  project_id        TEXT REFERENCES projects(project_id),
  parent_id         TEXT REFERENCES investigations(investigation_id),
  title             TEXT NOT NULL,
  status            TEXT NOT NULL DEFAULT 'active',
  merged_into       TEXT REFERENCES investigations(investigation_id),
  dead_end_reason   TEXT,
  tags_json         TEXT,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  UNIQUE (context_name, slug)
);
CREATE INDEX IF NOT EXISTS idx_investigations_project ON investigations(project_id);
CREATE INDEX IF NOT EXISTS idx_investigations_parent ON investigations(parent_id);
CREATE INDEX IF NOT EXISTS idx_investigations_context_status ON investigations(context_name, status);

CREATE TABLE IF NOT EXISTS annotations (
  annotation_id     TEXT PRIMARY KEY,
  investigation_id  TEXT NOT NULL REFERENCES investigations(investigation_id),
  tag               TEXT,
  body              TEXT NOT NULL,
  carried_from      TEXT REFERENCES annotations(annotation_id),
  created_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_annotations_investigation ON annotations(investigation_id, created_at);

CREATE TABLE IF NOT EXISTS freezes (
  freeze_id          TEXT PRIMARY KEY,
  context_name       TEXT NOT NULL,
  pipeline           TEXT NOT NULL,
  version            TEXT NOT NULL,
  commit_sha         TEXT,
  workflow_path      TEXT,
  status             TEXT NOT NULL DEFAULT 'resolving',
  delete_at          INTEGER,
  pinned             INTEGER NOT NULL DEFAULT 0,
  pin_reason         TEXT,
  owner_pid          INTEGER,
  frozen_at          INTEGER NOT NULL,
  UNIQUE (context_name, pipeline, version)
);

CREATE TABLE IF NOT EXISTS runs (
  run_id              TEXT PRIMARY KEY,
  context_name        TEXT NOT NULL,
  project_id          TEXT REFERENCES projects(project_id),
  investigation_id    TEXT REFERENCES investigations(investigation_id),
  verb                TEXT NOT NULL,
  workload_ref        TEXT NOT NULL,
  workload_version    TEXT,
  params_json         TEXT,
  namespace           TEXT,
  workspace           TEXT,
  submitted_at        INTEGER NOT NULL,
  completed_at        INTEGER,
  status              TEXT NOT NULL DEFAULT 'running',
  exit_reason         TEXT,
  cpu_hours           REAL,
  memory_gb_hours     REAL,
  walltime_seconds    INTEGER,
  freeze_id           TEXT REFERENCES freezes(freeze_id),
  created_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_runs_context_submitted ON runs(context_name, submitted_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_project ON runs(project_id);
CREATE INDEX IF NOT EXISTS idx_runs_investigation ON runs(investigation_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);

CREATE TABLE IF NOT EXISTS active_pointers (
  context_name    TEXT NOT NULL,
  pointer_kind    TEXT NOT NULL,
  pointer_value   TEXT,
  updated_at      INTEGER NOT NULL,
  PRIMARY KEY (context_name, pointer_kind)
);

CREATE TABLE IF NOT EXISTS cli_audit (
  audit_id        INTEGER PRIMARY KEY AUTOINCREMENT,
  invoked_at      INTEGER NOT NULL,
  context_name    TEXT,
  user_ulid       TEXT,
  verb            TEXT NOT NULL,
  args_json       TEXT,
  exit_code       INTEGER,
  duration_ms     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_cli_audit_invoked ON cli_audit(invoked_at DESC);

CREATE TABLE IF NOT EXISTS citations (
  citation_id          INTEGER PRIMARY KEY AUTOINCREMENT,
  source_annotation_id TEXT NOT NULL REFERENCES annotations(annotation_id),
  target_investigation TEXT NOT NULL REFERENCES investigations(investigation_id),
  target_annotation_id TEXT REFERENCES annotations(annotation_id),
  created_at           INTEGER NOT NULL,
  UNIQUE (source_annotation_id, target_investigation, target_annotation_id)
);
CREATE INDEX IF NOT EXISTS idx_citations_source ON citations(source_annotation_id);
CREATE INDEX IF NOT EXISTS idx_citations_target ON citations(target_investigation);

CREATE TABLE IF NOT EXISTS container_digests (
  freeze_id    TEXT NOT NULL REFERENCES freezes(freeze_id) ON DELETE CASCADE,
  image        TEXT NOT NULL,
  tag          TEXT NOT NULL,
  digest       TEXT NOT NULL,
  resolved_at  INTEGER NOT NULL,
  PRIMARY KEY (freeze_id, image, tag)
);

CREATE TABLE IF NOT EXISTS pipeline_metadata (
  context_name      TEXT NOT NULL,
  pipeline          TEXT NOT NULL,
  version           TEXT NOT NULL,
  schema_json       TEXT,
  params_json       TEXT,
  samplesheet_json  TEXT,
  fetched_at        INTEGER NOT NULL,
  PRIMARY KEY (context_name, pipeline, version)
);

CREATE TABLE IF NOT EXISTS telemetry_queue (
  event_id            TEXT PRIMARY KEY,
  context_name        TEXT NOT NULL,
  verb                TEXT NOT NULL,
  payload_json        TEXT NOT NULL,
  enqueued_at         INTEGER NOT NULL,
  shipped_at          INTEGER,
  shipping_started_at INTEGER,
  cloud_account       TEXT,
  delete_at           INTEGER
);
CREATE INDEX IF NOT EXISTS idx_telemetry_unshipped
  ON telemetry_queue(verb, enqueued_at) WHERE shipped_at IS NULL;
