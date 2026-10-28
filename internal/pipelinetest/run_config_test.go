package pipelinetest

import (
	"context"
	"testing"

	"amazonrecon/internal/recon"
)

// A mapping-config change changes the config hash that the next run records.
func TestConfigChangeChangesRunConfigHash(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	first := ingestFixtures(t, pool)
	before, err := recon.Build(ctx, pool, first.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(before.ConfigHash) != 64 {
		t.Fatalf("config hash = %q, want a 64-character SHA-256", before.ConfigHash)
	}

	if _, err := pool.Exec(ctx, `UPDATE payment_mapping_rules SET to_summary_field_when_positive_amount = 'sales_shipping'
		WHERE rule_id = 'P-002'`); err != nil {
		t.Fatalf("update config: %v", err)
	}
	again := ingestFixtures(t, pool)
	after, err := recon.Build(ctx, pool, again.RunID)
	if err != nil {
		t.Fatalf("reconcile after config change: %v", err)
	}
	if after.ConfigHash == before.ConfigHash || len(after.ConfigHash) != 64 {
		t.Errorf("config hash after a config change = %s, before %s: want a different 64-character hash", after.ConfigHash, before.ConfigHash)
	}
}
