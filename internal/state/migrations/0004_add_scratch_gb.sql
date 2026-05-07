-- 0004_add_scratch_gb.sql — capture per-run scratch storage reservation.
--
-- Required by the storage extension to `abc accounting` and `abc emissions`
-- reporting verbs. Stored as the reservation size in GB (not GB·hour) so
-- that report-time multiplication by walltime_hours yields the GB·hour
-- attribution. Pair with walltime_seconds to compute scratch_gb_hours
-- on demand (mirroring how gpu_hours is derived from gpu_count and
-- walltime).
--
-- See brainstorms/emissions-accounting/2026-05-07-storage-accounting.md
-- for the full design (transient vs persistent attribution, SA defaults,
-- erasure coding amplification).

ALTER TABLE runs ADD COLUMN scratch_gb REAL;
