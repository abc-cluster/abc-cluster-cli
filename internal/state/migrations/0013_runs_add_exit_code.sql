-- 0013_runs_add_exit_code.sql — close the long-standing missing-column
-- in runner.watcher.go.
--
-- The watcher tries to write the alloc's process exit_code into a
-- runs.exit_code column that was never added by an earlier migration
-- (the column exists on the freezes table; the watcher's UPDATE was
-- written assuming a runs column would land alongside it). Every
-- run-watcher cycle has been logging
--   "[abc] run-watcher: exit_code update: SQL logic error:
--    no such column: exit_code (1)"
-- which is harmless but noisy and obscures real reconcile failures.
--
-- INTEGER (nullable). Populated by runner.watcher.go after CompleteRun
-- sets the row's status/exit_reason — semantically a separate concern
-- (status is the abc-cluster-level rollup, exit_code is the raw alloc
-- process exit). NULL for rows where the watcher never reached the
-- exit_code path (e.g. pre-this-migration rows, or runs that exited
-- without a Nomad alloc-task event details payload).

ALTER TABLE runs ADD COLUMN exit_code INTEGER;
