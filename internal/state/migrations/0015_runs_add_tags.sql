-- Run-level tags (MLflow-style key=value), enabling compare-by-tag of runs
-- within one investigation (e.g. model=knn|rf|svm over the same dataset).
-- Stored as a JSON array of "k=v" strings, mirroring investigations.tags_json.
-- NULL on rows predating this migration.
ALTER TABLE runs ADD COLUMN tags_json TEXT;
