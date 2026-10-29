package recon

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/store"
)

// AuditRow is one Contribution, with what a reviewer needs to trace it to its input line.
type AuditRow struct {
	Source       string // payments or settlement
	FileName     string
	LineNumber   int
	RuleID       string
	AmountField  string
	Amount       decimal.Decimal
	SummaryField string
	SummaryGroup string // empty when the Summary field has no Summary line
	SummaryLine  string
	RecordRef    string
}

// readAuditTrail reads every Contribution of the run, with its file, its Summary line and its record_ref.
func readAuditTrail(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	rows, err := pool.Query(ctx, `SELECT c.source, r.file_name, c.line_number, c.rule_id, c.amount_field,
			c.amount, c.summary_field, COALESCE(m.summary_group, ''), COALESCE(m.summary_line, ''),
			COALESCE(r.record_ref, '')
		FROM summary_contributions c
		JOIN source_rows r ON r.run_id = c.run_id AND r.source = c.source AND r.line_number = c.line_number
		LEFT JOIN summary_line_map m ON m.summary_field = c.summary_field
		WHERE c.run_id = $1
		ORDER BY c.source, c.line_number, c.contribution_id`, runID)
	if err != nil {
		return fmt.Errorf("read audit trail: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AuditRow
		var amount store.Amount
		if err := rows.Scan(&a.Source, &a.FileName, &a.LineNumber, &a.RuleID, &a.AmountField, &amount,
			&a.SummaryField, &a.SummaryGroup, &a.SummaryLine, &a.RecordRef); err != nil {
			return fmt.Errorf("scan audit row: %w", err)
		}
		a.Amount = amount.Decimal
		rep.Audit = append(rep.Audit, a)
	}
	return rows.Err()
}
