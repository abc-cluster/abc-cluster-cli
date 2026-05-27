-- 0012_runs_workdir_root_for_report_runs.sql
--
-- Forward-compatibility schema slot for the `abc report runs` subverb
-- (brainstorm: brainstorms/abc-report-use-cases/2026-05-27-…) and the
-- future Nextflow CloudCache aggregator that will join per-task cost
-- data across resume-chains.
--
-- The CLI doesn't read this column yet — it's populated by
-- `cmd/pipeline/run.go` when a pipeline is submitted so that a later
-- aggregator (Khan v1) can group resumes by their shared workdir root
-- WITHOUT another schema migration. Workdir root is "the part of the
-- s3:// workdir path that's stable across resumes":
--
--   s3://su-mbhg-hostgen/user/abhi/workdir/abhi-1779…086/
--   └─────────── workdir_root ────────────┘
--
-- For job rows (verb='job') the column stays NULL — single-alloc jobs
-- don't have a workdir-keyed resume model.
--
-- New composite index `idx_runs_context_verb_submitted` powers the
-- `report runs` query:
--   SELECT … FROM runs
--    WHERE context_name = ?
--      AND submitted_at >= ?
--    ORDER BY verb ASC, submitted_at DESC
--    LIMIT ?
--
-- Sort by verb ASC so 'job' (alphabetically first) leads, then pipeline,
-- as researchers asked for at thousands-of-rows scale.

ALTER TABLE runs ADD COLUMN workdir_root TEXT;

CREATE INDEX IF NOT EXISTS idx_runs_context_verb_submitted
  ON runs(context_name, verb, submitted_at DESC);
