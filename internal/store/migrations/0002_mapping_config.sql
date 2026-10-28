-- Mapping configs as data, the Summary-line map, and the Summary totals written during ingestion.

-- The payments mapping config, verbatim. rule_id is P-<CSV line number>, e.g. P-024.
CREATE TABLE payment_mapping_rules (
    rule_id                               TEXT PRIMARY KEY,
    csv_line                              INTEGER NOT NULL UNIQUE CHECK (csv_line > 1),
    transaction_type                      TEXT NOT NULL,
    description                           TEXT NOT NULL,
    amount_field                          TEXT NOT NULL,
    record_ref                            TEXT NOT NULL,
    to_summary_field_when_positive_amount TEXT NOT NULL,
    to_summary_field_when_negative_amount TEXT NOT NULL
);

-- The settlement mapping config, verbatim. rule_id is S-<CSV line number>, e.g. S-071.
CREATE TABLE settlement_mapping_rules (
    rule_id                               TEXT PRIMARY KEY,
    csv_line                              INTEGER NOT NULL UNIQUE CHECK (csv_line > 1),
    transaction_type                      TEXT NOT NULL,
    amount_type                           TEXT NOT NULL,
    amount_description                    TEXT NOT NULL,
    record_ref                            TEXT NOT NULL,
    to_summary_field_when_positive_amount TEXT NOT NULL,
    to_summary_field_when_negative_amount TEXT NOT NULL
);

-- One row per Summary line, in sample order. summary_field is blank for a line the configs never
-- route to (it still shows on the sheet). A Summary field with no row here is unmapped on purpose.
CREATE TABLE summary_line_map (
    summary_field  TEXT UNIQUE,
    summary_group  TEXT NOT NULL,
    summary_line   TEXT NOT NULL,
    display_order  INTEGER NOT NULL UNIQUE,
    PRIMARY KEY (summary_group, summary_line)
);

-- One Contribution per amount a rule routed to a Summary field, from an in-scope line.
CREATE TABLE summary_contributions (
    contribution_id BIGSERIAL PRIMARY KEY,
    run_id          BIGINT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('payments', 'settlement')),
    line_number     INTEGER NOT NULL,
    rule_id         TEXT NOT NULL,
    amount_field    TEXT NOT NULL,
    amount          NUMERIC(14, 2) NOT NULL,
    summary_field   TEXT NOT NULL,
    FOREIGN KEY (run_id, source, line_number)
        REFERENCES source_rows (run_id, source, line_number) ON DELETE CASCADE
);

CREATE INDEX summary_contributions_summary_idx ON summary_contributions (run_id, source, summary_field);
CREATE INDEX summary_contributions_line_idx ON summary_contributions (run_id, source, line_number);

-- Running Summary totals per run, source and Summary field. Updated per batch in the ingest transaction.
CREATE TABLE summary_running (
    run_id        BIGINT NOT NULL REFERENCES ingest_runs (run_id) ON DELETE CASCADE,
    source        TEXT NOT NULL CHECK (source IN ('payments', 'settlement')),
    summary_field TEXT NOT NULL,
    amount        NUMERIC(14, 2) NOT NULL,
    PRIMARY KEY (run_id, source, summary_field)
);
