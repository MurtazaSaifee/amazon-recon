-- Reconciliation by record_ref. Each source is grouped by record_ref over its in-scope, mapped lines,
-- and the payments line total is compared with the sum of Component amounts. Unmapped lines have no
-- record_ref and are listed separately by the recon package.
CREATE VIEW reconciliation_by_record_ref AS
SELECT
    run_id,
    record_ref,
    COALESCE(SUM(amount) FILTER (WHERE source = 'payments'), 0)   AS payments_total,
    COALESCE(SUM(amount) FILTER (WHERE source = 'settlement'), 0) AS settlement_total,
    COALESCE(SUM(amount) FILTER (WHERE source = 'payments'), 0)
        - COALESCE(SUM(amount) FILTER (WHERE source = 'settlement'), 0) AS difference,
    count(*) FILTER (WHERE source = 'payments')                    AS payments_lines,
    count(*) FILTER (WHERE source = 'settlement')                  AS settlement_lines,
    CASE
        WHEN count(*) FILTER (WHERE source = 'payments') > 0 AND count(*) FILTER (WHERE source = 'settlement') > 0 THEN
            CASE WHEN SUM(amount) FILTER (WHERE source = 'payments') = SUM(amount) FILTER (WHERE source = 'settlement')
                THEN 'Reconciled'
                ELSE 'Reconciled with difference'
            END
        WHEN count(*) FILTER (WHERE source = 'payments') > 0 THEN 'Unreconciled payment'
        ELSE 'Unreconciled settlement'
    END AS status
FROM source_rows
WHERE in_scope
  AND line_kind IN ('transaction', 'component')
  AND precedence_tier > 0
GROUP BY run_id, record_ref;
