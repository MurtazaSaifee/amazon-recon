package recon

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Source names as stored in the summary tables.
const (
	sourcePayments   = "payments"
	sourceSettlement = "settlement"
)

// SummaryLine is one line of the Summary sheet, with its amount from each source.
type SummaryLine struct {
	Group       string
	Line        string
	Field       string // empty for a line the configs never route to
	Payments    decimal.Decimal
	Settlements decimal.Decimal
}

// FieldTotal is a routed Summary field with no Summary line.
type FieldTotal struct {
	Field       string
	Payments    decimal.Decimal
	Settlements decimal.Decimal
}

// Summary is the Summary sheet data.
type Summary struct {
	Lines    []SummaryLine // in display order
	Unmapped []FieldTotal  // routed fields with no Summary line, sorted by field
}

// RoutingGap is the part of one rule group's lines that no Contribution routed.
type RoutingGap struct {
	Rule   string // the matched rule IDs joined by commas, or "Unmapped"
	Lines  int
	Amount decimal.Decimal
}

// TieOutSide is the tie-out for one source.
type TieOutSide struct {
	SummaryNet      decimal.Decimal // every routed Contribution of the source, in scope
	Gaps            []RoutingGap    // grouped by rule
	GapsTotal       decimal.Decimal
	InScope         Total // in-scope lines of the source
	SettlementTotal decimal.Decimal
	Remaining       decimal.Decimal // in-scope total minus the Settlement total

	// Unreconciled is the in-scope lines of this side's Unreconciled records: payment lines for the
	// payments side, settlement components for the settlements side. Unreconciled is the reason the
	// residual exists, so it is read from the reconciliation status, not from any rule or line type.
	Unreconciled Total
	// Unexplained is Remaining minus Unreconciled. It is the part of the residual no record accounts for.
	Unexplained decimal.Decimal
}

// TieOut holds the tie-out of both sources.
type TieOut struct {
	Payments    TieOutSide
	Settlements TieOutSide
}

// buildSummary reads the Summary lines, the unmapped block and the tie-out for one run.
// It first checks that the running totals equal the Contributions recomputed in SQL.
func buildSummary(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	if err := checkRunningTotals(ctx, pool, runID); err != nil {
		return err
	}
	if err := readSummaryLines(ctx, pool, runID, rep); err != nil {
		return err
	}
	if err := readUnmapped(ctx, pool, runID, rep); err != nil {
		return err
	}
	if err := readTieOut(ctx, pool, runID, rep); err != nil {
		return err
	}
	return nil
}

// checkRunningTotals recomputes each (source, Summary field) total from the Contributions and
// fails with the first field that differs from the running total written during ingestion.
func checkRunningTotals(ctx context.Context, pool *pgxpool.Pool, runID int64) error {
	recomputed, err := sumsByField(ctx, pool, `SELECT source, summary_field, SUM(amount)::text
		FROM summary_contributions WHERE run_id = $1 GROUP BY source, summary_field`, runID)
	if err != nil {
		return fmt.Errorf("recompute summary: %w", err)
	}
	running, err := sumsByField(ctx, pool, `SELECT source, summary_field, amount::text
		FROM summary_running WHERE run_id = $1`, runID)
	if err != nil {
		return fmt.Errorf("read running summary totals: %w", err)
	}
	keys := map[string]bool{}
	for k := range recomputed {
		keys[k] = true
	}
	for k := range running {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		want, got := recomputed[k], running[k]
		if !want.Equal(got) {
			return fmt.Errorf("summary running total for %s is %s, but the contributions sum to %s: "+
				"the running totals and the contributions disagree; re-run ingest", k, got, want)
		}
	}
	return nil
}

// sumsByField runs a query of (source, summary_field, amount) and keys the amounts by "source field".
func sumsByField(ctx context.Context, pool *pgxpool.Pool, query string, runID int64) (map[string]decimal.Decimal, error) {
	rows, err := pool.Query(ctx, query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var source, field, amount string
		if err := rows.Scan(&source, &field, &amount); err != nil {
			return nil, err
		}
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return nil, err
		}
		out[source+" "+field] = d
	}
	return out, rows.Err()
}

// readSummaryLines reads every Summary line in display order with its amounts from each source.
func readSummaryLines(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	rows, err := pool.Query(ctx, `SELECT m.summary_group, m.summary_line, COALESCE(m.summary_field, ''),
			COALESCE(SUM(r.amount) FILTER (WHERE r.source = 'payments'), 0)::text,
			COALESCE(SUM(r.amount) FILTER (WHERE r.source = 'settlement'), 0)::text
		FROM summary_line_map m
		LEFT JOIN summary_running r ON r.run_id = $1 AND r.summary_field = m.summary_field
		GROUP BY m.display_order, m.summary_group, m.summary_line, m.summary_field
		ORDER BY m.display_order`, runID)
	if err != nil {
		return fmt.Errorf("read summary lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l SummaryLine
		var p, s string
		if err := rows.Scan(&l.Group, &l.Line, &l.Field, &p, &s); err != nil {
			return err
		}
		if l.Payments, err = decimal.NewFromString(p); err != nil {
			return err
		}
		if l.Settlements, err = decimal.NewFromString(s); err != nil {
			return err
		}
		rep.Summary.Lines = append(rep.Summary.Lines, l)
	}
	return rows.Err()
}

// readUnmapped reads the routed Summary fields that have no Summary line.
func readUnmapped(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	rows, err := pool.Query(ctx, `SELECT r.summary_field,
			COALESCE(SUM(r.amount) FILTER (WHERE r.source = 'payments'), 0)::text,
			COALESCE(SUM(r.amount) FILTER (WHERE r.source = 'settlement'), 0)::text
		FROM summary_running r
		WHERE r.run_id = $1
		  AND NOT EXISTS (SELECT 1 FROM summary_line_map m WHERE m.summary_field = r.summary_field)
		GROUP BY r.summary_field
		ORDER BY r.summary_field`, runID)
	if err != nil {
		return fmt.Errorf("read unmapped summary fields: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f FieldTotal
		var p, s string
		if err := rows.Scan(&f.Field, &p, &s); err != nil {
			return err
		}
		if f.Payments, err = decimal.NewFromString(p); err != nil {
			return err
		}
		if f.Settlements, err = decimal.NewFromString(s); err != nil {
			return err
		}
		rep.Summary.Unmapped = append(rep.Summary.Unmapped, f)
	}
	return rows.Err()
}

// readTieOut reads, per source, the Summary net, the routing gaps grouped by matched rule, and the
// in-scope total. The remaining difference is that total minus the Settlement total.
func readTieOut(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	for _, side := range []struct {
		source string
		out    *TieOutSide
	}{
		{sourcePayments, &rep.TieOut.Payments},
		{sourceSettlement, &rep.TieOut.Settlements},
	} {
		if err := readSideTieOut(ctx, pool, runID, side.source, side.out, rep.Settlement.Total); err != nil {
			return err
		}
	}
	return nil
}

func readSideTieOut(ctx context.Context, pool *pgxpool.Pool, runID int64, source string, out *TieOutSide, settlementTotal decimal.Decimal) error {
	var net string
	if err := pool.QueryRow(ctx,
		"SELECT COALESCE(SUM(amount), 0)::text FROM summary_running WHERE run_id = $1 AND source = $2",
		runID, source).Scan(&net); err != nil {
		return fmt.Errorf("summary net of %s: %w", source, err)
	}
	var err error
	if out.SummaryNet, err = decimal.NewFromString(net); err != nil {
		return err
	}

	var count int
	var inScope string
	if err := pool.QueryRow(ctx, `SELECT count(*)::int, COALESCE(SUM(amount), 0)::text
		FROM source_rows
		WHERE run_id = $1 AND source = $2 AND in_scope AND line_kind IN ('transaction', 'component')`,
		runID, source).Scan(&count, &inScope); err != nil {
		return fmt.Errorf("in-scope total of %s: %w", source, err)
	}
	out.InScope.Count = count
	if out.InScope.Amount, err = decimal.NewFromString(inScope); err != nil {
		return err
	}

	// Routing gap = line amount minus the sum of that line's Contributions, grouped by the matched rules.
	rows, err := pool.Query(ctx, `SELECT COALESCE(array_to_string(matched_rule_ids, ','), 'Unmapped') AS rule_group,
			count(*)::int, COALESCE(SUM(amount - routed), 0)::text
		FROM (
			SELECT r.matched_rule_ids, r.amount,
				COALESCE((SELECT SUM(c.amount) FROM summary_contributions c
					WHERE c.run_id = r.run_id AND c.source = r.source AND c.line_number = r.line_number), 0) AS routed
			FROM source_rows r
			WHERE r.run_id = $1 AND r.source = $2 AND r.in_scope AND r.line_kind IN ('transaction', 'component')
		) lines
		GROUP BY rule_group
		ORDER BY rule_group`, runID, source)
	if err != nil {
		return fmt.Errorf("routing gaps of %s: %w", source, err)
	}
	defer rows.Close()
	for rows.Next() {
		var g RoutingGap
		var gap string
		if err := rows.Scan(&g.Rule, &g.Lines, &gap); err != nil {
			return err
		}
		if g.Amount, err = decimal.NewFromString(gap); err != nil {
			return err
		}
		out.Gaps = append(out.Gaps, g)
		out.GapsTotal = out.GapsTotal.Add(g.Amount)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out.SettlementTotal = settlementTotal
	out.Remaining = out.InScope.Amount.Sub(settlementTotal)

	unreconciled := UnreconciledPayment
	if source == sourceSettlement {
		unreconciled = UnreconciledSettlement
	}
	var unreconCount int
	var unreconAmount string
	if err := pool.QueryRow(ctx, `SELECT count(*)::int, COALESCE(SUM(r.amount), 0)
		FROM source_rows r
		JOIN reconciliation_by_record_ref v ON v.run_id = r.run_id AND v.record_ref = r.record_ref
		WHERE r.run_id = $1 AND r.source = $2 AND r.in_scope AND v.status = $3`,
		runID, source, string(unreconciled)).Scan(&unreconCount, &unreconAmount); err != nil {
		return fmt.Errorf("unreconciled amounts of %s: %w", source, err)
	}
	out.Unreconciled.Count = unreconCount
	if out.Unreconciled.Amount, err = decimal.NewFromString(unreconAmount); err != nil {
		return err
	}
	out.Unexplained = out.Remaining.Sub(out.Unreconciled.Amount)
	return nil
}
