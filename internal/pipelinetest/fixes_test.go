package pipelinetest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/store"
)

// A mapping-fix file runs in one transaction: all of its statements apply, or none do.
func TestApplyFixesIsOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)

	const rule = "P-002"
	before := ruleSummaryField(t, ctx, pool, rule)

	t.Run("a file with a failing statement changes nothing", func(t *testing.T) {
		broken := `UPDATE payment_mapping_rules SET to_summary_field_when_positive_amount = 'expenses_other' WHERE rule_id = 'P-002';
			UPDATE no_such_table SET x = 1;`
		if err := store.ApplyFixes(ctx, pool, broken); err == nil {
			t.Fatal("ApplyFixes with a failing statement returned no error")
		}
		if got := ruleSummaryField(t, ctx, pool, rule); got != before {
			t.Errorf("%s routes to %q after a failed fix file, want %q: the first statement was not rolled back", rule, got, before)
		}
	})

	t.Run("a comment-only file is a no-op", func(t *testing.T) {
		if err := store.ApplyFixes(ctx, pool, "-- no fixes yet\n"); err != nil {
			t.Fatalf("ApplyFixes with only comments: %v", err)
		}
	})

	t.Run("a guarded fix applies once and a second application changes nothing", func(t *testing.T) {
		fix := `UPDATE payment_mapping_rules
			SET to_summary_field_when_positive_amount = 'expenses_other'
			WHERE rule_id = 'P-002' AND to_summary_field_when_positive_amount = '` + before + `';`
		for i := 0; i < 2; i++ {
			if err := store.ApplyFixes(ctx, pool, fix); err != nil {
				t.Fatalf("ApplyFixes pass %d: %v", i+1, err)
			}
		}
		if got := ruleSummaryField(t, ctx, pool, rule); got != "expenses_other" {
			t.Errorf("%s routes to %q, want expenses_other", rule, got)
		}
		var changes int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM mapping_rule_history
			WHERE table_name = 'payment_mapping_rules' AND row_key = $1 AND operation = 'UPDATE'`, rule).Scan(&changes); err != nil {
			t.Fatalf("count history: %v", err)
		}
		if changes != 1 {
			t.Errorf("history has %d updates of %s, want 1: the second application must not change the row", changes, rule)
		}
	})
}

// ruleSummaryField reads the positive Summary field a payments rule routes to.
func ruleSummaryField(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rule string) string {
	t.Helper()
	var field string
	if err := pool.QueryRow(ctx, "SELECT to_summary_field_when_positive_amount FROM payment_mapping_rules WHERE rule_id = $1", rule).Scan(&field); err != nil {
		t.Fatalf("read %s: %v", rule, err)
	}
	return field
}
