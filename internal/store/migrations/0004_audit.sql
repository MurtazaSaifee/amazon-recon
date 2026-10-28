-- Audit trail: the config hash and mode of each run, malformed-line detail, and a history of every mapping-config change.

-- The config hash (SHA-256 of the mapping tables) and mode each run was produced with.
ALTER TABLE ingest_runs ADD COLUMN config_hash TEXT;
ALTER TABLE ingest_runs ADD COLUMN mode TEXT NOT NULL DEFAULT 'fixed' CHECK (mode IN ('baseline', 'fixed'));

-- A malformed line also names its file, and whether its scope made it block the run.
ALTER TABLE ingest_errors ADD COLUMN file_name TEXT NOT NULL DEFAULT '';
ALTER TABLE ingest_errors ADD COLUMN in_scope BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE ingest_errors ADD COLUMN scope_reason TEXT NOT NULL DEFAULT '';

-- Every INSERT, UPDATE and DELETE on a mapping table, with the old and new row and a timestamp.
CREATE TABLE mapping_rule_history (
    history_id BIGSERIAL PRIMARY KEY,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    table_name TEXT NOT NULL,
    operation  TEXT NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
    row_key    TEXT NOT NULL,
    old_row    JSONB,
    new_row    JSONB
);

-- Records one change. The trigger arguments name the key columns of the table; their values, joined
-- with '|', identify the row in row_key (for example P-002, or Sales|Product Charges).
CREATE FUNCTION record_mapping_change() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    old_row JSONB;
    new_row JSONB;
    row_key TEXT := '';
    i       INTEGER;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_row := to_jsonb(OLD);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_row := to_jsonb(NEW);
    END IF;
    FOR i IN 0 .. TG_NARGS - 1 LOOP
        IF i > 0 THEN
            row_key := row_key || '|';
        END IF;
        row_key := row_key || COALESCE(COALESCE(new_row, old_row) ->> TG_ARGV[i], '');
    END LOOP;
    INSERT INTO mapping_rule_history (table_name, operation, row_key, old_row, new_row)
    VALUES (TG_TABLE_NAME, TG_OP, row_key, old_row, new_row);
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER payment_mapping_rules_history
    AFTER INSERT OR UPDATE OR DELETE ON payment_mapping_rules
    FOR EACH ROW EXECUTE FUNCTION record_mapping_change('rule_id');

CREATE TRIGGER settlement_mapping_rules_history
    AFTER INSERT OR UPDATE OR DELETE ON settlement_mapping_rules
    FOR EACH ROW EXECUTE FUNCTION record_mapping_change('rule_id');

CREATE TRIGGER summary_line_map_history
    AFTER INSERT OR UPDATE OR DELETE ON summary_line_map
    FOR EACH ROW EXECUTE FUNCTION record_mapping_change('summary_group', 'summary_line');
