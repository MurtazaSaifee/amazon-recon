package pipelinetest

import (
	"context"
	"testing"

	"amazonrecon/internal/store"
)

// Every INSERT, UPDATE and DELETE on the mapping tables is recorded with the old and new row and a timestamp.
func TestConfigChangesAreRecordedInHistory(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	ingestFixtures(t, pool)

	t.Run("seeding records an insert for every rule", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM mapping_rule_history
			WHERE operation = 'INSERT' AND table_name = 'payment_mapping_rules' AND row_key = 'P-002' AND new_row IS NOT NULL`).Scan(&n); err != nil {
			t.Fatalf("query history: %v", err)
		}
		if n != 1 {
			t.Errorf("INSERT rows for P-002 = %d, want 1", n)
		}
	})

	if _, err := pool.Exec(ctx, `UPDATE payment_mapping_rules SET to_summary_field_when_positive_amount = 'sales_shipping'
		WHERE rule_id = 'P-002'`); err != nil {
		t.Fatalf("update config: %v", err)
	}

	t.Run("an UPDATE appears in the history with the old and new row", func(t *testing.T) {
		var oldField, newField string
		var changedAt any
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*), max(old_row->>'to_summary_field_when_positive_amount'),
				max(new_row->>'to_summary_field_when_positive_amount'), max(changed_at)
			FROM mapping_rule_history WHERE operation = 'UPDATE' AND table_name = 'payment_mapping_rules' AND row_key = 'P-002'`).
			Scan(&n, &oldField, &newField, &changedAt)
		if err != nil {
			t.Fatalf("query history: %v", err)
		}
		if n != 1 || oldField != "sales_product_charges" || newField != "sales_shipping" || changedAt == nil {
			t.Errorf("UPDATE history rows=%d old=%q new=%q changed_at=%v, want 1 row sales_product_charges to sales_shipping with a timestamp",
				n, oldField, newField, changedAt)
		}
	})

	t.Run("a DELETE from a re-seed appears in the history too", func(t *testing.T) {
		if err := store.SeedConfig(ctx, pool, fixtureConfigFiles()); err != nil {
			t.Fatalf("re-seed config: %v", err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM mapping_rule_history
			WHERE operation = 'DELETE' AND table_name = 'payment_mapping_rules' AND row_key = 'P-002'
			  AND old_row IS NOT NULL AND new_row IS NULL`).Scan(&n); err != nil {
			t.Fatalf("query history: %v", err)
		}
		if n != 1 {
			t.Errorf("DELETE rows for P-002 = %d, want 1", n)
		}
	})
}
