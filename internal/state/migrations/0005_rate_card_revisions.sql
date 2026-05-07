-- 0005_rate_card_revisions.sql — bitemporal rate-card revision substrate.
--
-- Lays the schema for server-side authoritative rate cards even though
-- only Layer 3 (~/.abc/config.yaml) is writable in this release. Once
-- abc-grove ships, namespace and cluster admins write into this table
-- via Khan; runs resolve to the rate row active at run-completion time.
--
-- See brainstorms/emissions-accounting/2026-05-07-permissions-model.md
-- for the full design (5-layer rate card, advisory vs authoritative
-- sources, signed-report verification semantics).
--
-- Forward-compatible: no consumers in this release. The columns are
-- shaped so that `abc accounting report --signed` (when implemented)
-- can join runs.completed_at against (active_from, active_to) to pick
-- the rate row that was authoritative when the run finished.

CREATE TABLE IF NOT EXISTS rate_card_revisions (
  revision_id    TEXT PRIMARY KEY,
  -- Scope of this revision. "cluster" rows apply when the run's namespace
  -- has no namespace-scoped row; otherwise the namespace row wins.
  scope          TEXT NOT NULL CHECK (scope IN ('cluster', 'namespace')),
  -- Namespace name when scope = 'namespace'; NULL when scope = 'cluster'.
  namespace_name TEXT,
  context_name   TEXT NOT NULL,
  -- Rate-card field key, e.g. "cost.cpu_hour", "emissions.grid_factor_gco2_per_kwh".
  -- Free-form so new fields land without a migration.
  rate_key       TEXT NOT NULL,
  -- Stored as text so cost rates ("0.50"), grid factors ("900"), and
  -- string-valued rates (currency: "ZAR") share one schema.
  rate_value     TEXT NOT NULL,
  -- Optional citation, e.g. an Eskom publication reference or an
  -- internal procurement quote ID.
  citation       TEXT,
  -- Bitemporal validity window. active_to IS NULL means "current".
  -- A new row for the same (scope, namespace_name, context_name, rate_key)
  -- must set the previous row's active_to = its own active_from.
  active_from    INTEGER NOT NULL,
  active_to      INTEGER,
  -- Audit: identity that set this revision and when. set_at differs from
  -- active_from when an admin back-dates a correction.
  set_by         TEXT NOT NULL,
  set_at         INTEGER NOT NULL,
  -- Forward link from a superseding revision back to the row it replaces.
  -- NULL for the first revision of any (scope, namespace, context, key).
  supersedes_id  TEXT REFERENCES rate_card_revisions(revision_id)
);

CREATE INDEX IF NOT EXISTS idx_rate_revisions_lookup
  ON rate_card_revisions (context_name, scope, namespace_name, rate_key, active_from);

CREATE INDEX IF NOT EXISTS idx_rate_revisions_current
  ON rate_card_revisions (context_name, scope, namespace_name, rate_key)
  WHERE active_to IS NULL;
