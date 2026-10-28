-- The interpretation switches a run was produced with, by name (sorted). Empty for a baseline run.
ALTER TABLE ingest_runs ADD COLUMN switches TEXT[] NOT NULL DEFAULT '{}';
