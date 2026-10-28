package pipelinetest

import (
	"context"
	"testing"

	"amazonrecon/internal/recon"
)

// Ingesting the same files twice replaces the earlier lines, Contributions and totals, and leaves the same counts.
func TestReingestReplacesEarlierRowsAndKeepsCountsAndTotals(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	first := ingestFixtures(t, pool)
	rep1, err := recon.Build(ctx, pool, first.RunID)
	if err != nil {
		t.Fatalf("reconcile first run: %v", err)
	}
	var firstContributions int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM summary_contributions WHERE run_id = $1", first.RunID).Scan(&firstContributions); err != nil {
		t.Fatalf("count first-run contributions: %v", err)
	}

	second := ingestFixtures(t, pool)
	rep2, err := recon.Build(ctx, pool, second.RunID)
	if err != nil {
		t.Fatalf("reconcile second run: %v", err)
	}

	var storedLines, storedContributions int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM source_rows), (SELECT count(*) FROM summary_contributions)`).
		Scan(&storedLines, &storedContributions); err != nil {
		t.Fatalf("count stored rows: %v", err)
	}
	wantLines := 0
	for _, f := range rep2.Files {
		wantLines += f.StoredLines
	}
	if storedLines != wantLines {
		t.Errorf("stored lines after two ingests = %d, want %d: the earlier lines should have been replaced", storedLines, wantLines)
	}
	if storedContributions != firstContributions {
		t.Errorf("stored contributions after two ingests = %d, want %d: the earlier contributions should have been replaced", storedContributions, firstContributions)
	}

	assertDec(t, "components", rep2.Components.Amount, rep1.Components.Amount.String())
	assertDec(t, "transactions in scope", rep2.TransactionsInScope.Amount, rep1.TransactionsInScope.Amount.String())
	assertDec(t, "payments summary net", rep2.TieOut.Payments.SummaryNet, rep1.TieOut.Payments.SummaryNet.String())
	assertDec(t, "settlement summary net", rep2.TieOut.Settlements.SummaryNet, rep1.TieOut.Settlements.SummaryNet.String())
}
