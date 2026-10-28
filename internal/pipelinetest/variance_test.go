package pipelinetest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The expected variance rows below are worked by hand from the synthetic fixtures and configs
// (see summary_test.go). Payments line 11 is Order A: product 1000.00, tax 234.56, routed to
// Product Charges by P-002 and to Tax by P-003 and P-004 (the duplicate). Settlement line 4 is its
// Tax component, routed by S-003. Payments line 13 is the refund of B, routed to Refunded sales by
// P-006. Payments line 15 is the Transfer, routed by the type-and-any rule P-009.

// varianceRow is one row of v_line_variance: a rule, its source, its amount and its line count.
type varianceRow struct {
	rule, source, label, amount string
	lines                       int
}

func TestLineVarianceView(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)

	t.Run("a duplicated rule appears once per rule, by source", func(t *testing.T) {
		got := lineVariance(t, ctx, pool, res.RunID, "summary_line = 'Tax'")
		want := []varianceRow{
			{"P-003", "payments", "ORDER / any", "234.56", 1},
			{"P-004", "payments", "ORDER / any", "234.56", 1},
			{"S-003", "settlement", "Order / ItemPrice / Tax", "234.56", 1},
		}
		assertVariance(t, got, want)
	})

	t.Run("a Summary line counts the lines behind each rule's amount", func(t *testing.T) {
		got := lineVariance(t, ctx, pool, res.RunID, "summary_line = 'Product Charges'")
		want := []varianceRow{
			{"P-002", "payments", "ORDER / any", "1025.00", 2},
			{"S-002", "settlement", "Order / ItemPrice / Principal", "1025.00", 2},
		}
		assertVariance(t, got, want)
	})

	t.Run("a refund routes to its Summary line on the payments side only", func(t *testing.T) {
		got := lineVariance(t, ctx, pool, res.RunID, "summary_line = 'Refunded sales'")
		want := []varianceRow{{"P-006", "payments", "REFUND / any", "-10.00", 1}}
		assertVariance(t, got, want)
	})

	t.Run("a routed field with no Summary line is blank on group and line", func(t *testing.T) {
		got := lineVariance(t, ctx, pool, res.RunID, "summary_field = 'bank_account_transfer_round_off'")
		want := []varianceRow{{"P-009", "payments", "TRANSFER / any", "-1000.00", 1}}
		assertVariance(t, got, want)
	})
}

// recordVarianceRow is one row of v_record_variance.
type recordVarianceRow struct {
	recordRef, payments, settlements, difference string
}

func TestRecordVarianceView(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)

	t.Run("a record that routed twice on one side shows a difference", func(t *testing.T) {
		got := recordVariance(t, ctx, pool, res.RunID, "summary_line = 'Tax'")
		want := []recordVarianceRow{
			{"701-0000001-0000001|SYN-A-001|2026-07-02", "469.12", "234.56", "234.56"},
		}
		assertRecordVariance(t, got, want)
	})

	t.Run("records that agree on both sides have no difference", func(t *testing.T) {
		got := recordVariance(t, ctx, pool, res.RunID, "summary_line = 'Product Charges'")
		want := []recordVarianceRow{
			{"701-0000001-0000001|SYN-A-001|2026-07-02", "1000.00", "1000.00", "0.00"},
			{"701-0000002-0000002|SYN-B-002|2026-07-03", "25.00", "25.00", "0.00"},
		}
		assertRecordVariance(t, got, want)
	})

	t.Run("a payments-only record shows its whole amount as the difference", func(t *testing.T) {
		got := recordVariance(t, ctx, pool, res.RunID, "summary_line = 'Refunded sales'")
		want := []recordVarianceRow{
			{"701-0000002-0000002|SYN-B-002|2026-07-04", "-10.00", "0.00", "-10.00"},
		}
		assertRecordVariance(t, got, want)
	})
}

// lineVariance runs v_line_variance for one run, filtered by where, and returns its rows in rule order.
func lineVariance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, where string) []varianceRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT rule_id, source, rule_label, amount::text, line_count
		FROM v_line_variance WHERE run_id = $1 AND `+where+` ORDER BY source, rule_id`, runID)
	if err != nil {
		t.Fatalf("query v_line_variance: %v", err)
	}
	defer rows.Close()
	var out []varianceRow
	for rows.Next() {
		var r varianceRow
		if err := rows.Scan(&r.rule, &r.source, &r.label, &r.amount, &r.lines); err != nil {
			t.Fatalf("scan v_line_variance: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read v_line_variance: %v", err)
	}
	return out
}

func assertVariance(t *testing.T, got, want []varianceRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("variance rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("variance row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// recordVariance runs v_record_variance for one run, filtered by where, ordered by record_ref.
func recordVariance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, where string) []recordVarianceRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT record_ref, payments::text, settlements::text, difference::text
		FROM v_record_variance WHERE run_id = $1 AND `+where+` ORDER BY record_ref`, runID)
	if err != nil {
		t.Fatalf("query v_record_variance: %v", err)
	}
	defer rows.Close()
	var out []recordVarianceRow
	for rows.Next() {
		var r recordVarianceRow
		if err := rows.Scan(&r.recordRef, &r.payments, &r.settlements, &r.difference); err != nil {
			t.Fatalf("scan v_record_variance: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read v_record_variance: %v", err)
	}
	return out
}

func assertRecordVariance(t *testing.T, got, want []recordVarianceRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("record variance rows = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record variance row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
