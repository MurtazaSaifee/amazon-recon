package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/configcsv"
	"amazonrecon/internal/mapping"
)

// ConfigFiles names the three CSVs that seed the mapping tables.
type ConfigFiles struct {
	Payments   string // payments mapping config
	Settlement string // settlement mapping config
	SummaryMap string // Summary field to Summary line map (committed in configs/)
}

// The exact headers of the config CSVs. A file with any other header is refused. Each header is also
// the column list of its rule table, after rule_id and csv_line.
var (
	paymentConfigHeader    = []string{"transaction_type", "description", "amount_field", "record_ref", "to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"}
	settlementConfigHeader = []string{"transaction_type", "amount_type", "amount_description", "record_ref", "to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"}
	summaryMapHeader       = []string{"summary_field", "summary_group", "summary_line", "display_order"}
)

// SeedConfig replaces the three mapping tables with the contents of the CSVs, in one
// transaction. Rule cells are stored verbatim; normalisation happens at match time.
func SeedConfig(ctx context.Context, pool *pgxpool.Pool, files ConfigFiles) error {
	payments, err := configcsv.Read(files.Payments, paymentConfigHeader)
	if err != nil {
		return err
	}
	settlement, err := configcsv.Read(files.Settlement, settlementConfigHeader)
	if err != nil {
		return err
	}
	summary, err := configcsv.Read(files.SummaryMap, summaryMapHeader)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, table := range []string{"payment_mapping_rules", "settlement_mapping_rules", "summary_line_map"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if err := copyRules(ctx, tx, "payment_mapping_rules", "P", paymentConfigHeader, payments); err != nil {
		return err
	}
	if err := copyRules(ctx, tx, "settlement_mapping_rules", "S", settlementConfigHeader, settlement); err != nil {
		return err
	}

	summaryRows := make([][]any, 0, len(summary))
	for _, r := range summary {
		order, err := strconv.Atoi(r.Fields[3])
		if err != nil {
			return fmt.Errorf("%s line %d: display_order %q is not a whole number", files.SummaryMap, r.Line, r.Fields[3])
		}
		summaryRows = append(summaryRows, []any{nullable(r.Fields[0]), r.Fields[1], r.Fields[2], order})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"summary_line_map"},
		[]string{"summary_field", "summary_group", "summary_line", "display_order"},
		pgx.CopyFromRows(summaryRows)); err != nil {
		return fmt.Errorf("load summary line map: %w", err)
	}
	return tx.Commit(ctx)
}

// copyRules writes one rule table. A rule's ID is its prefix and its CSV line number (P-024), and the
// CSV's own cells follow it, in the header's column order.
func copyRules(ctx context.Context, tx pgx.Tx, table, prefix string, header []string, rows []configcsv.Record) error {
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		row := make([]any, 0, len(r.Fields)+2)
		row = append(row, ruleID(prefix, r.Line), r.Line)
		for _, f := range r.Fields {
			row = append(row, f)
		}
		data = append(data, row)
	}
	columns := append([]string{"rule_id", "csv_line"}, header...)
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(data)); err != nil {
		return fmt.Errorf("load %s: %w", table, err)
	}
	return nil
}

// LoadRules reads both mapping configs in CSV line order. It fails when either is empty,
// because ingesting against no rules would route nothing and say nothing about it.
func LoadRules(ctx context.Context, tx pgx.Tx) ([]mapping.PaymentRule, []mapping.SettlementRule, error) {
	payments, err := queryAll(ctx, tx, `SELECT rule_id, transaction_type, description, amount_field, record_ref,
			to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
		FROM payment_mapping_rules ORDER BY csv_line`, scanPaymentRule)
	if err != nil {
		return nil, nil, fmt.Errorf("read payments rules: %w", err)
	}
	settlement, err := queryAll(ctx, tx, `SELECT rule_id, transaction_type, amount_type, amount_description, record_ref,
			to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
		FROM settlement_mapping_rules ORDER BY csv_line`, scanSettlementRule)
	if err != nil {
		return nil, nil, fmt.Errorf("read settlement rules: %w", err)
	}
	if len(payments) == 0 || len(settlement) == 0 {
		return nil, nil, errors.New("the mapping configs are not loaded: run seed-config first")
	}
	return payments, settlement, nil
}

// queryAll runs a query and scans every row with scan, in query order.
func queryAll[T any](ctx context.Context, tx pgx.Tx, query string, scan func(pgx.Rows, *T) error) ([]T, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanPaymentRule(rows pgx.Rows, r *mapping.PaymentRule) error {
	return rows.Scan(&r.ID, &r.TransactionType, &r.Description, &r.AmountField, &r.RecordRef, &r.PositiveSummary, &r.NegativeSummary)
}

func scanSettlementRule(rows pgx.Rows, r *mapping.SettlementRule) error {
	return rows.Scan(&r.ID, &r.TransactionType, &r.AmountType, &r.AmountDescription, &r.RecordRef, &r.PositiveSummary, &r.NegativeSummary)
}

func ruleID(prefix string, csvLine int) string {
	return fmt.Sprintf("%s-%03d", prefix, csvLine)
}
