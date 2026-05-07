-- 0002_add_gpu_count.sql — add gpu_count column to runs table.
-- Required by `abc accounting` and `abc emissions` reporting verbs.
-- See specs/active/abc-emissions-accounting.md §A.

ALTER TABLE runs ADD COLUMN gpu_count INTEGER;
