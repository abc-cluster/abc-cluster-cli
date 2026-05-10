-- 0009_runs_resource_request.sql — record what the user requested.
-- See specs/active/abc-report.md §A.
--
-- Populated at submit time from the resolved Nomad job spec — the values
-- the user requested (not what the alloc actually consumed; that's in
-- cpu_hours / memory_gb_hours). NULL handled gracefully by the
-- `resource_fit` metric.

ALTER TABLE runs ADD COLUMN cpu_request    REAL;
ALTER TABLE runs ADD COLUMN mem_request_gb REAL;
