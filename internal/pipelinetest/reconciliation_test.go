package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
	"amazonrecon/internal/store"
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
	reconSettlementID = "12000000003"
	reconRefundRef    = "701-0000002-0000002|SYN-B-002|2026-07-04"
	reconOrderARef    = "701-0000001-0000001|SYN-A-001|2026-07-02"
	reconOrderBRef    = "701-0000002-0000002|SYN-B-002|2026-07-03"
	reconOrderCRef    = "701-0000003-0000003|SYN-C-003|2026-07-04"
	reconTransferRef  = "TRANSFER|To account ending with: 999|12000000003|2026-07-05"
	reconOrderFRef    = "701-0000006-0000006|SYN-F-006|2026-07-05"
	reconOrderGRef    = "701-0000007-0000007|SYN-G-007|2026-07-06"
)

// The Consolidated Data header row, as the sample lays it out (the payments and settlement
// columns, the status, the total difference and the per-line differences), keyed by column letter.
var consolidatedHeaders = map[string]string{
	"A": "Date", "B": "date/time", "C": "Transaction Status", "D": "Transaction Release Date",
	"E": "settlement id", "F": "type", "G": "order id", "H": "sku", "I": "description", "J": "Quantity",
	"K": "marketplace", "L": "account type", "M": "fulfillment", "N": "order city", "O": "order state",
	"P": "order postal", "Q": "tax collection model", "R": "Total",
	"S": "Product Charges", "T": "Tax", "V": "Amazon Fees", "AB": "Refund Expenses", "AD": "Promo Rebates",
	"AE": "FBA Fees", "AH": "Amazon Fees", "AM": "Paid To Amazon",
	// The one routed Summary field with no Summary line gets a column on each side, after the Summary lines.
	"AN": "bank_account_transfer_round_off",
	"AO": "settlement-id", "AP": "settlement-start-date", "BI": "promotion-id", "BJ": "Total",
	"BK": "Product Charges", "BN": "Amazon Fees", "BT": "Refund Expenses", "CE": "Paid To Amazon",
	"CF": "bank_account_transfer_round_off",
	"CG": "Reconciliation Status", "CH": "total (DS1-DS2)", "CI": "Product Charges (DS1-DS2)",
	"DD": "bank_account_transfer_round_off (DS1-DS2)",
	"DE": "record_ref", "DF": "Payments source lines", "DG": "Settlement source lines",
}

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

func TestConsolidatedAndOutOfScopeSheets(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	routeTransferToUnmappedField(t, pool)
	res := ingestReconFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	path := filepath.Join(t.TempDir(), "recon.xlsx")
	if err := report.WriteWorkbook(path, rep); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer f.Close()

	t.Run("the Summary sheet shows the counts per status", func(t *testing.T) {
		checkValue(t, f, "Summary", "Reconciled", "C", "1")
		checkValue(t, f, "Summary", "Reconciled with difference", "C", "1")
		checkValue(t, f, "Summary", "Unreconciled payment", "C", "3")
		checkValue(t, f, "Summary", "Unreconciled settlement", "C", "2")
		checkValue(t, f, "Summary", "Unmapped (lines)", "C", "2")
	})

	t.Run("Consolidated Data follows the sample's column order", func(t *testing.T) {
		rows := sheetRows(t, f, "Consolidated Data")
		header := rows[consolidatedHeaderRow-1]
		for col, want := range consolidatedHeaders {
			if got := cellAt(header, col); got != want {
				t.Errorf("header %s = %q, want %q", col, got, want)
			}
		}
	})

	t.Run("each consolidated row carries its totals, status and source line numbers", func(t *testing.T) {
		rows := sheetRows(t, f, "Consolidated Data")
		row := findConsolidatedRow(t, rows, reconOrderARef)
		want := map[string]string{
			"CG": "Reconciled", "R": "1234.56", "BJ": "1234.56", "CH": "0.00",
			"S": "1000.00", "BK": "1000.00", "T": "469.12", "BL": "234.56",
			"DF": "11", "DG": "3,4",
		}
		for col, v := range want {
			if got := cellAt(row, col); got != v {
				t.Errorf("record A column %s = %q, want %q", col, got, v)
			}
		}
	})

	t.Run("a routed Summary field with no Summary line is kept in its own columns", func(t *testing.T) {
		// The Transfer routes -500.00 to bank_account_transfer_round_off, which has no Summary line.
		rows := sheetRows(t, f, "Consolidated Data")
		row := findConsolidatedRow(t, rows, reconTransferRef)
		want := map[string]string{
			"AN": "-500.00", // payments side
			"CF": "",        // settlement side has no lines for this record
			"DD": "-500.00", // difference: payments minus settlements
		}
		for col, v := range want {
			if got := cellAt(row, col); got != v {
				t.Errorf("transfer column %s = %q, want %q", col, got, v)
			}
		}
	})

	t.Run("the Out of Scope sheet lists each out-of-scope line", func(t *testing.T) {
		rows := sheetRows(t, f, "Out of Scope")
		var found []string
		for _, r := range rows {
			if len(r) > 0 && (r[0] == "Deferred" || r[0] == "Other settlement") {
				found = append(found, fmt.Sprintf("%s|%s|%s", r[0], cellAt(r, "D"), cellAt(r, "L")))
			}
		}
		want := []string{"Deferred|16|100.00", "Deferred|17|25.50", "Other settlement|18|60.00"}
		if fmt.Sprint(found) != fmt.Sprint(want) {
			t.Errorf("out-of-scope rows\n got %v\nwant %v", found, want)
		}
	})

	t.Run("out-of-scope line numbers are numeric cells", func(t *testing.T) {
		rows := sheetRows(t, f, "Out of Scope")
		checked := 0
		for i, r := range rows {
			if len(r) == 0 || (r[0] != "Deferred" && r[0] != "Other settlement") {
				continue
			}
			cell := fmt.Sprintf("D%d", i+1)
			typ, err := f.GetCellType("Out of Scope", cell)
			if err != nil {
				t.Fatalf("cell type of %s: %v", cell, err)
			}
			// A numeric cell carries no text type: excelize reports it as unset or as a number. A text cell would be
			// a shared or inline string.
			if typ != excelize.CellTypeNumber && typ != excelize.CellTypeUnset {
				t.Errorf("line number in %s is text (type %v), want a number", cell, typ)
			}
			checked++
		}
		if checked != 3 {
			t.Errorf("checked %d out-of-scope line numbers, want 3", checked)
		}
	})
}

// consolidatedHeaderRow is the 1-based row of the Consolidated Data column headers.
const consolidatedHeaderRow = 10

// routeTransferToUnmappedField routes the Transfer's rule (P-008) to a Summary field with no Summary
// line, the way a mapping fix would. The Consolidated sheet then has a column for that field.
// It must run after seeding and before ingesting.
func routeTransferToUnmappedField(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := store.ApplyFixes(context.Background(), pool, `UPDATE payment_mapping_rules
		SET to_summary_field_when_positive_amount = 'bank_account_transfer_round_off',
		    to_summary_field_when_negative_amount = 'bank_account_transfer_round_off'
		WHERE rule_id = 'P-008';`); err != nil {
		t.Fatalf("route the Transfer to an unmapped field: %v", err)
	}
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

// sheetRows reads every row of a sheet as raw values.
func sheetRows(t *testing.T, f *excelize.File, sheet string) [][]string {
	t.Helper()
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read sheet %s: %v", sheet, err)
	}
	return rows
}

// cellAt returns the value of a column letter in a row read by sheetRows.
func cellAt(row []string, col string) string {
	idx := 0
	for _, c := range col {
		idx = idx*26 + int(c-'A') + 1
	}
	idx--
	if idx < len(row) {
		return row[idx]
	}
	return ""
}

// findConsolidatedRow returns the Consolidated Data row whose record_ref column is ref.
func findConsolidatedRow(t *testing.T, rows [][]string, ref string) []string {
	t.Helper()
	for _, r := range rows {
		if cellAt(r, "DE") == ref {
			return r
		}
	}
	t.Fatalf("Consolidated Data has no row for record_ref %q", ref)
	return nil
}
