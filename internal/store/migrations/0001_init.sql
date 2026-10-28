-- Ingestion register and line-grain source table.

-- One row per ingest command. A run is complete only after its rows are stored.
CREATE TABLE ingest_runs (
    run_id      BIGSERIAL PRIMARY KEY,
    status      TEXT NOT NULL CHECK (status IN ('running', 'complete', 'failed')),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    error       TEXT
);

-- One row per report file in a run: name, SHA-256, line counts, and the payments preamble.
CREATE TABLE ingest_files (
    run_id          BIGINT NOT NULL REFERENCES ingest_runs (run_id) ON DELETE CASCADE,
    source          TEXT NOT NULL CHECK (source IN ('payments', 'settlement')),
    file_name       TEXT NOT NULL,
    sha256          TEXT NOT NULL,
    physical_lines  INTEGER NOT NULL,
    stored_lines    INTEGER NOT NULL,
    malformed_lines INTEGER NOT NULL,
    preamble        JSONB,
    PRIMARY KEY (run_id, source)
);

-- Lines that could not be read. A run with any of these fails before it replaces source rows.
CREATE TABLE ingest_errors (
    error_id    BIGSERIAL PRIMARY KEY,
    run_id      BIGINT NOT NULL REFERENCES ingest_runs (run_id) ON DELETE CASCADE,
    source      TEXT NOT NULL,
    line_number INTEGER NOT NULL,
    raw_text    TEXT NOT NULL,
    reason      TEXT NOT NULL
);

-- Both reports at line grain. Each row keeps its file, line number and raw cells.
CREATE TABLE source_rows (
    row_id            BIGSERIAL PRIMARY KEY,
    run_id            BIGINT NOT NULL REFERENCES ingest_runs (run_id) ON DELETE CASCADE,
    source            TEXT NOT NULL CHECK (source IN ('payments', 'settlement')),
    line_kind         TEXT NOT NULL CHECK (line_kind IN ('transaction', 'component', 'settlement_header')),
    file_name         TEXT NOT NULL,
    line_number       INTEGER NOT NULL CHECK (line_number > 0),
    raw_payload       JSONB NOT NULL,
    settlement_id     TEXT,
    txn_type          TEXT,
    amount_type       TEXT,
    description       TEXT,
    txn_ref           TEXT,
    sku               TEXT,
    status            TEXT,
    amount            NUMERIC(14, 2),
    posted_date       DATE,
    posted_date_basis TEXT CHECK (posted_date_basis IN ('release_date_utc', 'date_time_utc', 'posted_date')),
    currency          TEXT,
    period_start      TIMESTAMPTZ,
    period_end        TIMESTAMPTZ,
    deposit_date      TIMESTAMPTZ,
    in_scope          BOOLEAN NOT NULL,
    scope_reason      TEXT NOT NULL,
    -- Filled by the mapping and reconciliation tickets; nullable until then.
    record_ref        TEXT,
    precedence_tier   SMALLINT,
    matched_rule_ids  TEXT[],
    warnings          TEXT[],
    UNIQUE (run_id, source, line_number)
);

CREATE INDEX source_rows_scope_idx ON source_rows (run_id, source, in_scope);
