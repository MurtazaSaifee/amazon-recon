-- Variance views: plain SQL over the Contributions, so every investigation can be repeated and quoted.
-- The Variance of a Summary line is its Payments amount minus its Settlements amount.

-- One row per run, Summary field, source and rule: the amount that rule routed to the field, and the number
-- of distinct source lines behind it. rule_label is the rule as written in its config: transaction type and
-- description (payments), or transaction type, amount type and amount description (settlement). A blank
-- payments transaction type is the Catch-all, shown as "(catch-all)".
-- summary_group and summary_line are blank for a field with no Summary line.
CREATE VIEW v_line_variance AS
WITH rule_labels AS (
    SELECT 'payments'::TEXT AS source, rule_id,
           COALESCE(NULLIF(transaction_type, ''), '(catch-all)') || ' / ' || description AS rule_label
    FROM payment_mapping_rules
    UNION ALL
    SELECT 'settlement'::TEXT, rule_id, transaction_type || ' / ' || amount_type || ' / ' || amount_description
    FROM settlement_mapping_rules
)
SELECT
    c.run_id,
    COALESCE(m.summary_group, '')  AS summary_group,
    COALESCE(m.summary_line, '')   AS summary_line,
    c.summary_field,
    c.source,
    c.rule_id,
    l.rule_label,
    c.amount_field,
    count(DISTINCT c.line_number)::INTEGER AS line_count,
    SUM(c.amount)                  AS amount
FROM summary_contributions c
JOIN rule_labels l ON l.source = c.source AND l.rule_id = c.rule_id
LEFT JOIN summary_line_map m ON m.summary_field = c.summary_field
GROUP BY c.run_id, m.summary_group, m.summary_line, c.summary_field, c.source, c.rule_id, l.rule_label, c.amount_field;

-- One row per run, record_ref and Summary field: the routed amount on each side, and the difference
-- (payments minus settlements). A record that routed nothing on a side shows 0 for that side. The
-- Summary line is blank for a field with no Summary line.
CREATE VIEW v_record_variance AS
WITH routed AS (
    SELECT c.run_id, r.record_ref, c.summary_field, c.source, SUM(c.amount) AS amount
    FROM summary_contributions c
    JOIN source_rows r ON r.run_id = c.run_id AND r.source = c.source AND r.line_number = c.line_number
    WHERE r.record_ref IS NOT NULL
    GROUP BY c.run_id, r.record_ref, c.summary_field, c.source
)
SELECT
    t.run_id,
    t.record_ref,
    COALESCE(m.summary_group, '') AS summary_group,
    COALESCE(m.summary_line, '')  AS summary_line,
    t.summary_field,
    COALESCE(SUM(t.amount) FILTER (WHERE t.source = 'payments'), 0.00)   AS payments,
    COALESCE(SUM(t.amount) FILTER (WHERE t.source = 'settlement'), 0.00) AS settlements,
    COALESCE(SUM(t.amount) FILTER (WHERE t.source = 'payments'), 0.00)
        - COALESCE(SUM(t.amount) FILTER (WHERE t.source = 'settlement'), 0.00) AS difference
FROM routed t
LEFT JOIN summary_line_map m ON m.summary_field = t.summary_field
GROUP BY t.run_id, t.record_ref, t.summary_field, m.summary_group, m.summary_line;
