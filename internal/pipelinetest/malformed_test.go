package pipelinetest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/store"
)

// Ticket 05: the audit trail, safe re-runs, malformed lines, the mapping-config
// history and the run header. Everything here runs through the pipeline on
// synthetic fixtures.

// The payments fixture has 19 physical lines. The malformed-line tests append
// lines after them, so these are the line numbers those lines get.

const (
	appendedFirstLine = 20
)

// --- helpers -----------------------------------------------------------------

// paymentsRow returns one synthetic payments line in the fixture's shape: 25 quoted cells.
// Amounts are plain (no thousands separators), so the caller controls exactly what is malformed.

func paymentsRow(settlementID, txnType, status, total, release string) string {
	cells := []string{
		"28 June 2026 5:39:32 pm GMT+9", settlementID, txnType, "701-0000009-0000009", "SYN-I-009",
		"Synthetic extra", "1", "amazon.com.au", "Amazon", "Testville", "TS", "0000",
		"0", "0", "0", "0", "0", "0", "0", "0", "0", "0",
		total, status, release,
	}
	quoted := make([]string, len(cells))
	for i, c := range cells {
		quoted[i] = `"` + c + `"`
	}
	return strings.Join(quoted, ",")
}

// fixtureWithAppendedPayments copies the synthetic payments fixture and appends lines after its last line.

func fixtureWithAppendedPayments(t *testing.T, lines ...string) string {
	t.Helper()
	original, err := os.ReadFile(filepath.Join(fixtures, "synthetic_payments.csv"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(original) > 0 && original[len(original)-1] != '\n' {
		original = append(original, '\n')
	}
	for _, l := range lines {
		original = append(original, []byte(l+"\n")...)
	}
	path := filepath.Join(t.TempDir(), "synthetic_payments.csv")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	return path
}

type malformedRow struct {
	file        string
	line        int
	raw         string
	reason      string
	inScope     bool
	scopeReason string
}

// malformedRows reads the ingest_errors rows of a run in line order.

func malformedRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64) []malformedRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT file_name, line_number, raw_text, reason, in_scope, scope_reason
		FROM ingest_errors WHERE run_id = $1 ORDER BY line_number`, runID)
	if err != nil {
		t.Fatalf("read ingest_errors: %v", err)
	}
	defer rows.Close()
	var out []malformedRow
	for rows.Next() {
		var r malformedRow
		if err := rows.Scan(&r.file, &r.line, &r.raw, &r.reason, &r.inScope, &r.scopeReason); err != nil {
			t.Fatalf("scan ingest_errors: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read ingest_errors: %v", err)
	}
	return out
}

func latestRunID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, "SELECT max(run_id) FROM ingest_runs").Scan(&id); err != nil {
		t.Fatalf("latest run: %v", err)
	}
	return id
}

// A malformed line that is out of scope is stored with its file, line, raw text and reason,
// and ingestion carries on: the run completes and the in-scope totals are unchanged.

func TestMalformedLineOutOfScopeIsStoredAndIngestionContinues(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	clean := ingestFixtures(t, pool)
	before, err := recon.Build(ctx, pool, clean.RunID)
	if err != nil {
		t.Fatalf("reconcile clean run: %v", err)
	}

	otherSettlementBroken := paymentsRow("12000000002", "Order", "Released", "abc", "3 July 2026 9:00:00 am GMT+9")
	deferredBroken := paymentsRow(settlementID, "Order", "Deferred", "10.00", "not a date")
	payments := fixtureWithAppendedPayments(t, otherSettlementBroken, deferredBroken)

	res, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   payments,
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
	})
	if err != nil {
		t.Fatalf("ingest with out-of-scope malformed lines failed: %v", err)
	}
	if res.Malformed != 2 {
		t.Errorf("malformed lines = %d, want 2", res.Malformed)
	}
	if latest, err := store.LatestCompleteRun(ctx, pool); err != nil || latest != res.RunID {
		t.Errorf("latest complete run = %d (%v), want the new run %d", latest, err, res.RunID)
	}

	rows := malformedRows(t, ctx, pool, res.RunID)
	if len(rows) != 2 {
		t.Fatalf("ingest_errors rows = %d, want 2", len(rows))
	}
	first := rows[0]
	if first.file != "synthetic_payments.csv" || first.line != appendedFirstLine || first.raw != otherSettlementBroken || first.inScope {
		t.Errorf("first malformed line = %+v, want out of scope, line %d, raw text kept", first, appendedFirstLine)
	}
	if !strings.Contains(first.reason, "amount") || first.scopeReason != "Other settlement" {
		t.Errorf("first malformed line reason %q scope %q, want an amount reason and scope Other settlement", first.reason, first.scopeReason)
	}
	second := rows[1]
	if second.line != appendedFirstLine+1 || second.inScope || second.scopeReason != "Deferred" || second.raw != deferredBroken {
		t.Errorf("second malformed line = %+v, want out of scope Deferred on line %d", second, appendedFirstLine+1)
	}

	after, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile run with malformed lines: %v", err)
	}
	assertDec(t, "components after malformed lines", after.Components.Amount, before.Components.Amount.String())
	assertDec(t, "settlement remaining difference", after.TieOut.Settlements.Remaining, "0.00")
	if after.TransactionsInScope.Count != before.TransactionsInScope.Count {
		t.Errorf("in-scope transactions = %d, want %d", after.TransactionsInScope.Count, before.TransactionsInScope.Count)
	}
}

// A malformed line that is in scope, or whose scope cannot be read, fails the run and replaces nothing.

func TestMalformedLineInScopeFailsTheRun(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantReason string
	}{
		{
			name:       "released line with an unreadable release date",
			line:       paymentsRow(settlementID, "Order", "Released", "10.00", "not a date"),
			wantReason: "date",
		},
		{
			name:       "released line with an unreadable amount",
			line:       paymentsRow(settlementID, "Order", "Released", "abc", "3 July 2026 9:00:00 am GMT+9"),
			wantReason: "amount",
		},
		{
			name:       "wrong column count, so the scope cannot be read",
			line:       `"only one column"`,
			wantReason: "treated as in scope",
		},
		{
			name:       "settlement ID cell empty, so the scope cannot be read",
			line:       paymentsRow("", "Order", "Released", "abc", "3 July 2026 9:00:00 am GMT+9"),
			wantReason: "treated as in scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newPipelineDB(t)
			good := ingestFixtures(t, pool)

			_, err := ingest.Run(ctx, pool, ingest.Inputs{
				PaymentsPath:   fixtureWithAppendedPayments(t, tc.line),
				SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
			})
			if err == nil {
				t.Fatal("ingest with an in-scope malformed line succeeded, want it to fail")
			}
			if latest, err := store.LatestCompleteRun(ctx, pool); err != nil || latest != good.RunID {
				t.Errorf("latest complete run = %d (%v), want the earlier run %d", latest, err, good.RunID)
			}

			rows := malformedRows(t, ctx, pool, latestRunID(t, ctx, pool))
			if len(rows) != 1 {
				t.Fatalf("ingest_errors rows = %d, want 1", len(rows))
			}
			if !rows[0].inScope {
				t.Errorf("malformed line recorded as out of scope: %+v", rows[0])
			}
			if !strings.Contains(rows[0].reason, tc.wantReason) {
				t.Errorf("reason %q does not contain %q", rows[0].reason, tc.wantReason)
			}
		})
	}
}
