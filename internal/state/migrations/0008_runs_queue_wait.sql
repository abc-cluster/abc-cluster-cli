-- 0008_runs_queue_wait.sql — add pending_seconds column to runs.
-- See specs/active/abc-report.md §A.
--
-- Set by the run-watcher on alloc transition `pending → running`:
-- pending_seconds = (running_at - submitted_at). NULL for historical
-- rows; the `queue_wait_fraction` metric reports "n/a" when this column
-- is NULL on a given row.

ALTER TABLE runs ADD COLUMN pending_seconds INTEGER;
