-- 0011_runs_add_nomad_job_id.sql — link a run row to its Nomad job ID.
--
-- Populated synchronously by runner.Watch() before the background poll
-- goroutine is spawned. Enables ReconcileStuckRuns(): if the CLI exits
-- before the goroutine can write completion data, a future invocation
-- (e.g. `abc accounting report`) can re-probe Nomad using this stored ID
-- and update the run row without user intervention.
--
-- NULL for historical rows and runs submitted without a valid Nomad addr
-- (e.g. --no-submit, --dry-run). ReconcileStuckRuns() skips rows where
-- this column is NULL.

ALTER TABLE runs ADD COLUMN nomad_job_id TEXT;
