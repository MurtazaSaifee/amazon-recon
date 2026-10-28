package store

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/mapping"
	"amazonrecon/internal/textfile"
)

// ConfigFiles names the three CSVs that seed the mapping tables.
type ConfigFiles struct {
	Payments   string // payments mapping config
	Settlement string // settlement mapping config
	SummaryMap string // Summary field to Summary line map (committed in configs/)
}

// The exact headers of the config CSVs. A file with any other header is refused.
var (
	paymentConfigHeader    = []string{"transaction_type", "description", "amount_field", "record_ref", "to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"}
	settlementConfigHeader = []string{"transaction_type", "amount_type", "amount_description", "record_ref", "to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"}
	summaryMapHeader       = []string{"summary_field", "summary_group", "summary_line", "display_order"}
)

// csvRecord is one data row with the CSV line it came from. The header is line 1.
type csvRecord struct {
	line   int
	fields []string
}

// SeedConfig replaces the three mapping tables with the contents of the CSVs, in one
// transaction. Rule cells are stored verbatim; normalisation happens at match time.
func SeedConfig(ctx context.Context, pool *pgxpool.Pool, files ConfigFiles) error {
	payments, err := readConfigCSV(files.Payments, paymentConfigHeader)
	if err != nil {
		return err
	}
	settlement, err := readConfigCSV(files.Settlement, settlementConfigHeader)
	if err != nil {
		return err
	}
	summary, err := readConfigCSV(files.SummaryMap, summaryMapHeader)
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

	paymentRows := make([][]any, 0, len(payments))
	for _, r := range payments {
		paymentRows = append(paymentRows, []any{ruleID("P", r.line), r.line, r.fields[0], r.fields[1], r.fields[2], r.fields[3], r.fields[4], r.fields[5]})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"payment_mapping_rules"},
		[]string{"rule_id", "csv_line", "transaction_type", "description", "amount_field", "record_ref",
			"to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"},
		pgx.CopyFromRows(paymentRows)); err != nil {
		return fmt.Errorf("load payments config: %w", err)
	}

	settlementRows := make([][]any, 0, len(settlement))
	for _, r := range settlement {
		settlementRows = append(settlementRows, []any{ruleID("S", r.line), r.line, r.fields[0], r.fields[1], r.fields[2], r.fields[3], r.fields[4], r.fields[5]})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"settlement_mapping_rules"},
		[]string{"rule_id", "csv_line", "transaction_type", "amount_type", "amount_description", "record_ref",
			"to_summary_field_when_positive_amount", "to_summary_field_when_negative_amount"},
		pgx.CopyFromRows(settlementRows)); err != nil {
		return fmt.Errorf("load settlement config: %w", err)
	}

	summaryRows := make([][]any, 0, len(summary))
	for _, r := range summary {
		order, err := strconv.Atoi(r.fields[3])
		if err != nil {
			return fmt.Errorf("%s line %d: display_order %q is not a whole number", files.SummaryMap, r.line, r.fields[3])
		}
		summaryRows = append(summaryRows, []any{nullable(r.fields[0]), r.fields[1], r.fields[2], order})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"summary_line_map"},
		[]string{"summary_field", "summary_group", "summary_line", "display_order"},
		pgx.CopyFromRows(summaryRows)); err != nil {
		return fmt.Errorf("load summary line map: %w", err)
	}
	return tx.Commit(ctx)
}

// LoadRules reads both mapping configs in CSV line order. It fails when either is empty,
// because ingesting against no rules would route nothing and say nothing about it.
func LoadRules(ctx context.Context, tx pgx.Tx) ([]mapping.PaymentRule, []mapping.SettlementRule, error) {
	var payments []mapping.PaymentRule
	rows, err := tx.Query(ctx, `SELECT rule_id, transaction_type, description, amount_field, record_ref,
			to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
		FROM payment_mapping_rules ORDER BY csv_line`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var r mapping.PaymentRule
		if err := rows.Scan(&r.ID, &r.TransactionType, &r.Description, &r.AmountField, &r.RecordRef, &r.PositiveSummary, &r.NegativeSummary); err != nil {
			rows.Close()
			return nil, nil, err
		}
		payments = append(payments, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var settlement []mapping.SettlementRule
	rows, err = tx.Query(ctx, `SELECT rule_id, transaction_type, amount_type, amount_description, record_ref,
			to_summary_field_when_positive_amount, to_summary_field_when_negative_amount
		FROM settlement_mapping_rules ORDER BY csv_line`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var r mapping.SettlementRule
		if err := rows.Scan(&r.ID, &r.TransactionType, &r.AmountType, &r.AmountDescription, &r.RecordRef, &r.PositiveSummary, &r.NegativeSummary); err != nil {
			rows.Close()
			return nil, nil, err
		}
		settlement = append(settlement, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if len(payments) == 0 || len(settlement) == 0 {
		return nil, nil, errors.New("the mapping configs are not loaded: run seed-config first")
	}
	return payments, settlement, nil
}

func ruleID(prefix string, csvLine int) string {
	return fmt.Sprintf("%s-%03d", prefix, csvLine)
}

// readConfigCSV reads a config CSV. The header must match exactly and every row must have
// the same number of fields. Each row keeps the CSV line number it came from.
func readConfigCSV(path string, header []string) ([]csvRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	if err := textfile.SkipBOM(br); err != nil {
		return nil, err
	}

	cr := csv.NewReader(br)
	cr.FieldsPerRecord = len(header)
	cr.TrimLeadingSpace = false

	got, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: header: %w", path, err)
	}
	if !slices.Equal(got, header) {
		return nil, fmt.Errorf("%s: header does not match the expected config shape", path)
	}

	var out []csvRecord
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		line, _ := cr.FieldPos(0)
		out = append(out, csvRecord{line: line, fields: rec})
	}
	return out, nil
}
