-- 0010_runs_submission_source.sql — record how the run was authored.
-- See specs/active/abc-report.md §A.
--
-- Allowed values:
--   template:<id>   e.g. "template:nf-core/rnaseq" (set when invoked
--                   with --template=<id> or equivalent)
--   handwritten     default for interactive submits with no template
--   rerun           set by `abc job rerun` / `abc pipeline rerun`
--   automation      set when ABC_AUTOMATION=1 in env (or future
--                   non-interactive automation hook)
--
-- NULL for historical rows; the reporter buckets NULL as "unknown".

ALTER TABLE runs ADD COLUMN submission_source TEXT;
