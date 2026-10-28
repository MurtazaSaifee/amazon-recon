package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
)

// The reconciliation fixtures (testdata/fixtures/reconciliation_*) hold one line of each outcome.
// Expected values are worked by hand from the fixture files:
//
//	Reconciled                  701-0000001-0000001|SYN-A-001|2026-07-02: payments 1234.56 (payments line 11),
//	                            settlement 1000.00 + 234.56 (settlement lines 3, 4)
//	Reconciled with difference  701-0000002-0000002|SYN-B-002|2026-07-03: payments 25.50 (line 12), settlement 25.00 (line 5)
//	Unreconciled payment        the refund on 2026-07-04 (line 14), order C on 2026-07-04 (line 13), the Transfer (line 15)
//	Unreconciled settlement     order F and order G (settlement lines 7 and 8)
//	Unmapped                    the Refund component (settlement line 6) and the Service fee (settlement line 9)
//	Out of scope                Deferred lines 16 and 17, Other settlement line 18
const (
	reconRefundRef   = "701-0000002-0000002|SYN-B-002|2026-07-04"
	reconOrderARef   = "701-0000001-0000001|SYN-A-001|2026-07-02"
	reconOrderBRef   = "701-0000002-0000002|SYN-B-002|2026-07-03"
	reconOrderCRef   = "701-0000003-0000003|SYN-C-003|2026-07-04"
	reconTransferRef = "TRANSFER|12000000003|2026-07-05"
	reconOrderFRef   = "701-0000006-0000006|SYN-F-006|2026-07-05"
	reconOrderGRef   = "701-0000007-0000007|SYN-G-007|2026-07-06"
)

func TestReconciliationOutcomes(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestReconFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r := rep.Reconciliation

	t.Run("each status is counted once per record_ref", func(t *testing.T) {
		if r.Counts.Reconciled != 1 {
			t.Errorf("reconciled = %d, want 1", r.Counts.Reconciled)
		}
		if r.Counts.ReconciledWithDifference != 1 {
			t.Errorf("reconciled with difference = %d, want 1", r.Counts.ReconciledWithDifference)
		}
		if r.Counts.UnreconciledPayment != 3 {
			t.Errorf("unreconciled payments = %d, want 3", r.Counts.UnreconciledPayment)
		}
		if r.Counts.UnreconciledSettlement != 2 {
			t.Errorf("unreconciled settlements = %d, want 2", r.Counts.UnreconciledSettlement)
		}
		if r.Counts.UnmappedPayments != 0 || r.Counts.UnmappedSettlements != 2 {
			t.Errorf("unmapped lines payments/settlements = %d/%d, want 0/2",
				r.Counts.UnmappedPayments, r.Counts.UnmappedSettlements)
		}
	})

	t.Run("a reconciled record has equal payments and settlement amounts", func(t *testing.T) {
		rec := findRecord(t, r, reconOrderARef)
		if rec.Status != recon.Reconciled {
			t.Errorf("status = %q, want %q", rec.Status, recon.Reconciled)
		}
		assertDec(t, "payments total", rec.Payments.Total, "1234.56")
		assertDec(t, "settlement total", rec.Settlements.Total, "1234.56")
		assertDec(t, "difference", rec.Difference, "0.00")
		assertInts(t, "payments source lines", rec.Payments.Lines, []int{11})
		assertInts(t, "settlement source lines", rec.Settlements.Lines, []int{3, 4})
	})

	t.Run("a record with unequal amounts is reconciled with difference", func(t *testing.T) {
		rec := findRecord(t, r, reconOrderBRef)
		if rec.Status != recon.ReconciledWithDifference {
			t.Errorf("status = %q, want %q", rec.Status, recon.ReconciledWithDifference)
		}
		assertDec(t, "difference (payments minus settlement)", rec.Difference, "0.50")
	})

	t.Run("records on one side only are unreconciled, and the Transfer is an unreconciled payment", func(t *testing.T) {
		for ref, want := range map[string]recon.Status{
			reconRefundRef:   recon.UnreconciledPayment,
			reconOrderCRef:   recon.UnreconciledPayment,
			reconTransferRef: recon.UnreconciledPayment,
			reconOrderFRef:   recon.UnreconciledSettlement,
			reconOrderGRef:   recon.UnreconciledSettlement,
		} {
			if got := findRecord(t, r, ref).Status; got != want {
				t.Errorf("%s status = %q, want %q", ref, got, want)
			}
		}
	})

	t.Run("consolidated rows are ordered reconciled first, then payments, then settlements", func(t *testing.T) {
		want := []string{reconOrderARef, reconOrderBRef, reconRefundRef, reconOrderCRef, reconTransferRef,
			reconOrderFRef, reconOrderGRef}
		var got []string
		for _, rec := range r.Records {
			got = append(got, rec.RecordRef)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("record order\n got %v\nwant %v", got, want)
		}
	})

	t.Run("unmapped lines are listed separately with their line numbers", func(t *testing.T) {
		var lines []int
		for _, u := range r.Unmapped {
			if u.Source != "settlement" {
				t.Errorf("unmapped line source = %q, want settlement", u.Source)
			}
			lines = append(lines, u.LineNumber)
		}
		assertInts(t, "unmapped settlement lines", lines, []int{6, 9})
	})

	t.Run("out-of-scope lines are listed with their reason", func(t *testing.T) {
		want := []struct {
			line   int
			reason string
			amount string
		}{{16, "Deferred", "100.00"}, {17, "Deferred", "25.50"}, {18, "Other settlement", "60.00"}}
		if len(r.OutOfScope) != len(want) {
			t.Fatalf("out-of-scope lines = %d, want %d", len(r.OutOfScope), len(want))
		}
		for i, w := range want {
			got := r.OutOfScope[i]
			if got.Source != "payments" || got.LineNumber != w.line || got.Reason != w.reason {
				t.Errorf("out-of-scope %d = %s line %d %q, want payments line %d %q",
					i, got.Source, got.LineNumber, got.Reason, w.line, w.reason)
			}
			assertDec(t, fmt.Sprintf("out-of-scope line %d amount", w.line), got.Amount, w.amount)
		}
	})
}

func ingestReconFixtures(t *testing.T, pool *pgxpool.Pool) ingest.Result {
	t.Helper()
	res, err := ingest.Run(context.Background(), pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "reconciliation_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "reconciliation_settlement.txt"),
	})
	if err != nil {
		t.Fatalf("ingest reconciliation fixtures: %v", err)
	}
	return res
}

func findRecord(t *testing.T, r recon.Reconciliation, ref string) recon.RecordRow {
	t.Helper()
	for _, rec := range r.Records {
		if rec.RecordRef == ref {
			return rec
		}
	}
	t.Fatalf("no record_ref %q", ref)
	return recon.RecordRow{}
}

func assertInts(t *testing.T, what string, got, want []int) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
