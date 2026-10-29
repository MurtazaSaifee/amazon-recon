package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/mapping"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
	"amazonrecon/internal/store"
)

// The expected numbers below are worked by hand from the synthetic fixtures and
// their configs. The code under test must reproduce them; nothing here is
// computed from the code.
//
// Payments, in-scope lines (rules in synthetic_payment_configs.csv):
//
//	line 11 Order  product 1000.00, tax 234.56 (rules P-002, P-003, P-004 route it;
//	               P-004 duplicates P-003, so tax is routed twice)
//	line 12 Order  product 25.00
//	line 13 Refund product -10.00 (P-006 routes it to refunded_sales)
//	line 14 Order  all zero
//	line 15 Transfer -1000.00 (P-008, the type+description rule: a prefix match in fixed mode, not summarised)
//
// Settlement, in-scope components (synthetic_settlement_configs.csv):
//
//	line 3 Principal 1000.00 (S-002), line 4 Tax 234.56 (S-003),
//	line 5 Principal 25.00 (S-002), line 6 Refund Principal -10.00 (no rule: Unmapped)
const (
	pmtOrderA      = 11
	pmtRefund      = 13
	setPrincipalA  = 3
	setRefundUnmap = 6
)

func TestSummaryDuringIngestion(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	t.Run("lines are identified by the CSV line number of the rule that routed them", func(t *testing.T) {
		assertMatched(t, ctx, pool, res.RunID, "payments", pmtOrderA, []string{"P-002", "P-003", "P-004", "P-005"})
		assertMatched(t, ctx, pool, res.RunID, "payments", pmtTransfer, []string{"P-008"})
		assertMatched(t, ctx, pool, res.RunID, "settlement", setPrincipalA, []string{"S-002"})
	})

	t.Run("in-scope lines route into the Summary, out-of-scope lines do not", func(t *testing.T) {
		assertSummary(t, rep, "Sales", "Product Charges", "1025.00", "1025.00")
		assertSummary(t, rep, "Sales", "Tax", "469.12", "234.56")
		assertSummary(t, rep, "Refunds", "Refunded sales", "-10.00", "0.00")
		assertSummary(t, rep, "Expenses", "FBA fees", "0.00", "0.00")
		if len(rep.Summary.Unmapped) != 0 {
			t.Errorf("unmapped Summary fields = %+v, want none: the Transfer is not summarised", rep.Summary.Unmapped)
		}
	})

	t.Run("a duplicated rule shows as a negative routing gap", func(t *testing.T) {
		assertGap(t, rep.TieOut.Payments, "P-002,P-003,P-004,P-005", "-234.56")
	})

	t.Run("a line its prefix rule matches, and that is not summarised, is a routing gap", func(t *testing.T) {
		assertGap(t, rep.TieOut.Payments, "P-008", "-1000.00")
	})

	t.Run("a settlement component with an unknown type is an Unmapped routing gap", func(t *testing.T) {
		assertGap(t, rep.TieOut.Settlements, "Unmapped", "-10.00")
		assertLine(t, ctx, pool, res.RunID, "settlement", setRefundUnmap, func(l store.Line) {
			if l.RecordRef != "" || len(l.MatchedRuleIDs) != 0 {
				t.Errorf("unmapped component record_ref %q rules %v, want none", l.RecordRef, l.MatchedRuleIDs)
			}
			if !hasWarning(l.Warnings, mapping.WarnUnmapped) {
				t.Errorf("unmapped component warnings %v, want an unmapped warning", l.Warnings)
			}
		})
	})

	t.Run("payments tie-out: Summary net plus routing gaps is the in-scope total", func(t *testing.T) {
		tie := rep.TieOut.Payments
		assertDec(t, "payments summary net", tie.SummaryNet, "1484.12")
		assertDec(t, "payments routing gaps", tie.GapsTotal, "-1234.56")
		assertDec(t, "payments in-scope total", tie.InScope.Amount, "249.56")
		if tie.InScope.Count != 5 {
			t.Errorf("payments in-scope lines = %d, want 5", tie.InScope.Count)
		}
		assertDec(t, "payments remaining difference to the Settlement total", tie.Remaining, "-1000.00")
		// The in-scope lines of Unreconciled payment records are the Transfer (-1000.00), the refund on
		// line 13 (-10.00, whose settlement component is unmapped) and the zero-amount line 14.
		assertDec(t, "payments unreconciled payment amounts", tie.Unreconciled.Amount, "-1010.00")
		if tie.Unreconciled.Count != 3 {
			t.Errorf("payments unreconciled payment lines = %d, want 3", tie.Unreconciled.Count)
		}
		// The unexplained 10.00 is the unmapped refund component (settlement line 6): it is in the
		// Settlement total but on no record_ref, so it is not an Unreconciled settlement amount.
		assertDec(t, "payments unexplained difference", tie.Unexplained, "10.00")
		if rep.Deferred.Count != 2 {
			t.Errorf("deferred memo count = %d, want 2", rep.Deferred.Count)
		}
	})

	t.Run("settlement tie-out reaches the Settlement total with no remaining difference", func(t *testing.T) {
		tie := rep.TieOut.Settlements
		assertDec(t, "settlement summary net", tie.SummaryNet, "1259.56")
		assertDec(t, "settlement routing gaps", tie.GapsTotal, "-10.00")
		assertDec(t, "settlement in-scope total", tie.InScope.Amount, "1249.56")
		assertDec(t, "settlement remaining difference", tie.Remaining, "0.00")
		assertDec(t, "settlement unreconciled settlement amounts", tie.Unreconciled.Amount, "0.00")
		assertDec(t, "settlement unexplained difference", tie.Unexplained, "0.00")
	})

	t.Run("out-of-scope and in-scope lines keep their record_ref and matched rules", func(t *testing.T) {
		assertLine(t, ctx, pool, res.RunID, "payments", pmtTransfer, func(l store.Line) {
			if want := "TRANSFER|To account ending with: 999|12000000001|2026-07-05"; l.RecordRef != want {
				t.Errorf("transfer record_ref = %q, want %q", l.RecordRef, want)
			}
		})
		assertLine(t, ctx, pool, res.RunID, "payments", pmtRefund, func(l store.Line) {
			if l.RecordRef != "701-0000002-0000002|SYN-B-002|2026-07-04" {
				t.Errorf("refund record_ref = %q", l.RecordRef)
			}
		})
		assertLine(t, ctx, pool, res.RunID, "payments", pmtDeferred, func(l store.Line) {
			if l.RecordRef != "701-0000005-0000005|SYN-D-004|2026-07-09" {
				t.Errorf("deferred record_ref = %q", l.RecordRef)
			}
			if l.InScope {
				t.Errorf("deferred line is in scope")
			}
		})
	})

	t.Run("re-ingesting leaves the Summary and tie-out unchanged", func(t *testing.T) {
		again := ingestFixtures(t, pool)
		rep2, err := recon.Build(ctx, pool, again.RunID)
		if err != nil {
			t.Fatalf("reconcile second run: %v", err)
		}
		if len(rep2.Summary.Lines) != len(rep.Summary.Lines) {
			t.Fatalf("summary lines = %d, want %d", len(rep2.Summary.Lines), len(rep.Summary.Lines))
		}
		for i := range rep.Summary.Lines {
			a, b := rep.Summary.Lines[i], rep2.Summary.Lines[i]
			if a.Line != b.Line || !a.Payments.Equal(b.Payments) || !a.Settlements.Equal(b.Settlements) {
				t.Errorf("summary line %q changed after re-ingest", a.Line)
			}
		}
		if !rep2.TieOut.Payments.SummaryNet.Equal(rep.TieOut.Payments.SummaryNet) {
			t.Errorf("payments net changed after re-ingest")
		}
	})
}

// A summary running total that disagrees with the Contributions makes the report fail, with a message that names the field.
func TestRecomputeMismatchFailsReport(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)

	if _, err := pool.Exec(ctx,
		"UPDATE summary_running SET amount = amount + 0.01 WHERE run_id = $1 AND source = 'payments' AND summary_field = 'sales_tax'",
		res.RunID); err != nil {
		t.Fatalf("inject mismatch: %v", err)
	}
	_, err := recon.Build(ctx, pool, res.RunID)
	if err == nil {
		t.Fatal("report built despite running totals that differ from the contributions")
	}
	if !strings.Contains(err.Error(), "sales_tax") {
		t.Errorf("error %q does not name the mismatched field", err)
	}
}

// The workbook has four sheets, in this order. The tie-out is part of the Summary sheet, not a sheet of its own.
func TestWorkbookSheetsAreSummaryConsolidatedAuditAndOutOfScope(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	f := writeReport(t, rep)
	want := []string{"Summary", "Consolidated Data", "Audit Trail", "Out of Scope"}
	if got := f.GetSheetList(); !slices.Equal(got, want) {
		t.Errorf("sheets = %v, want %v", got, want)
	}
}

// sampleSummaryLabels is column B of the Summary sheet in the sample workbook, rows 1 to 32. An empty
// string is a blank row. Row 1 holds the column headers in C:E, so its B cell is blank too. The labels
// are written out from the sample, not read from configs/summary_line_map.csv, so the test checks the
// layout itself.
var sampleSummaryLabels = []string{
	"", "", "", // rows 1 to 3: the headers, then two blank rows
	"Sales", "Product Charges", "Tax", "Shipping", "Amazon fees", "Inventory Reimbursements",
	"Cross-account Debt Adjustment", "Other", "FBA Fees", "Micro Deposit (Failed)",
	"",
	"Refunds", "Refund expenses", "Refunded sales",
	"",
	"Expenses", "Promo rebates", "FBA fees", "Cost of Advertising", "Shipping Charges",
	"Amazon fees", "Reversed Reimbursements", "Cross-account Debt Adjustment", "Other", "Micro Deposit",
	"", "", "", // three blank rows before a single-line group named as its line
	"Paid To Amazon",
}

// The Summary sheet's first 32 rows match the sample workbook: the column headers in row 1, the
// groups from row 4, and the rest of the sheet from row 35, below two blank rows.
func TestSummarySheetLayoutMatchesSample(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	f := writeReport(t, rep)
	const sheet = report.SummarySheet
	cellText := func(cell string) string {
		t.Helper()
		v, err := f.GetCellValue(sheet, cell)
		if err != nil {
			t.Fatalf("read %s: %v", cell, err)
		}
		return v
	}

	for cell, want := range map[string]string{"B1": "", "C1": "Payments", "D1": "Settlements", "E1": "Payments - Settlements"} {
		if got := cellText(cell); got != want {
			t.Errorf("%s = %q, want %q", cell, got, want)
		}
	}
	for i, want := range sampleSummaryLabels {
		row := i + 1
		if got := cellText(fmt.Sprintf("B%d", row)); got != want {
			t.Errorf("B%d = %q, want %q", row, got, want)
		}
		if want == "" && row > 1 {
			for _, col := range []string{"C", "D", "E"} {
				if got := cellText(fmt.Sprintf("%s%d", col, row)); got != "" {
					t.Errorf("%s%d = %q, want a blank row", col, row, got)
				}
			}
		}
	}
	for _, cell := range []string{"B33", "B34"} {
		if got := cellText(cell); got != "" {
			t.Errorf("%s = %q, want a blank row before the rest of the sheet", cell, got)
		}
	}
	if got := cellText("B35"); got != "Amazon Payments vs Settlement: Summary" {
		t.Errorf("B35 = %q, want the sheet title", got)
	}
}

// The Summary sheet: groups with formula subtotals that also keep their values,
// a variance column, the unmapped block, the tie-out, and the sign note.
func TestSummarySheetFormulasKeepValues(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	path := filepath.Join(t.TempDir(), "summary.xlsx")
	if err := report.WriteWorkbook(path, rep); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer f.Close()

	const sheet = "Summary"
	signNote := false
	for row := 1; row <= 200; row++ {
		if v, _ := f.GetCellValue(sheet, fmt.Sprintf("B%d", row)); strings.Contains(v, "signs are kept") {
			signNote = true
		}
	}
	if !signNote {
		t.Error("the Summary sheet has no note that source signs are kept")
	}
	checkFormulaCell(t, f, sheet, "Sales", "C", "1494.12")
	checkFormulaCell(t, f, sheet, "Sales", "D", "1259.56")
	checkFormulaCell(t, f, sheet, "Sales", "E", "234.56")
	checkFormulaCell(t, f, sheet, "Product Charges", "E", "0.00")
	checkValue(t, f, sheet, "Product Charges", "C", "1025.00")
	checkFormulaCell(t, f, sheet, "Tax", "E", "234.56")
	checkValue(t, f, sheet, "Refunded sales", "C", "-10.00")
	checkValue(t, f, sheet, "Summary net (all Summary fields)", "C", "1484.12")
	checkValue(t, f, sheet, "Summary net (all Summary fields)", "D", "1259.56")
	checkValue(t, f, sheet, "Payments routing gap, rule group P-002,P-003,P-004,P-005", "C", "-234.56")
	checkValue(t, f, sheet, "Payments routing gap, rule group P-008", "C", "-1000.00")
	checkValue(t, f, sheet, "Settlements routing gap, rule group Unmapped", "D", "-10.00")
	// Each source lists only its own rule groups, one row per group with a non-zero gap.
	wantGaps := []string{
		"Payments routing gap, rule group P-002,P-003,P-004,P-005|-234.56|",
		"Payments routing gap, rule group P-008|-1000.00|",
		"Settlements routing gap, rule group Unmapped||-10.00",
	}
	if got := routingGapRows(t, f, sheet); !slices.Equal(got, wantGaps) {
		t.Errorf("routing gap rows\n got %q\nwant %q", got, wantGaps)
	}
	checkFormulaCell(t, f, sheet, "Remaining difference (in-scope lines minus Settlement total)", "C", "-1000.00")
	checkValue(t, f, sheet, "Remaining difference (in-scope lines minus Settlement total)", "D", "0.00")
	checkValue(t, f, sheet, "Unreconciled payment amounts", "C", "-1010.00")
	checkValue(t, f, sheet, "Unreconciled settlement amounts", "D", "0.00")
	checkFormulaCell(t, f, sheet, "Unexplained difference (must be 0.00)", "C", "10.00")
	checkFormulaCell(t, f, sheet, "Unexplained difference (must be 0.00)", "D", "0.00")
	checkValue(t, f, sheet, "Deferred memo, out of scope (2 lines)", "C", "125.50")
}

func newPipelineDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.SeedConfig(ctx, pool, fixtureConfigFiles()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return pool
}

func fixtureConfigFiles() store.ConfigFiles {
	return store.ConfigFiles{
		Payments:   filepath.Join(fixtures, "synthetic_payment_configs.csv"),
		Settlement: filepath.Join(fixtures, "synthetic_settlement_configs.csv"),
		SummaryMap: filepath.Join(fixtures, "..", "..", "configs", "summary_line_map.csv"),
	}
}

func assertSummary(t *testing.T, rep recon.Report, group, line, wantP, wantS string) {
	t.Helper()
	for _, l := range rep.Summary.Lines {
		if l.Group == group && l.Line == line {
			assertDec(t, group+" > "+line+" payments", l.Payments, wantP)
			assertDec(t, group+" > "+line+" settlements", l.Settlements, wantS)
			return
		}
	}
	t.Errorf("summary has no line %s > %s", group, line)
}

func assertGap(t *testing.T, side recon.TieOutSide, rule, want string) {
	t.Helper()
	for _, g := range side.Gaps {
		if g.Rule == rule {
			assertDec(t, "routing gap "+rule, g.Amount, want)
			return
		}
	}
	t.Errorf("no routing gap group %q", rule)
}

func assertMatched(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, source string, lineNumber int, want []string) {
	t.Helper()
	assertLine(t, ctx, pool, runID, source, lineNumber, func(l store.Line) {
		if fmt.Sprint(l.MatchedRuleIDs) != fmt.Sprint(want) {
			t.Errorf("%s line %d matched rules = %v, want %v", source, lineNumber, l.MatchedRuleIDs, want)
		}
	})
}

func hasWarning(ws []string, kind mapping.WarningKind) bool {
	for _, w := range ws {
		if strings.HasPrefix(w, string(kind)+":") {
			return true
		}
	}
	return false
}

// routingGapRows returns the routing-gap rows of a sheet as "label|payments|settlements", raw values.
func routingGapRows(t *testing.T, f *excelize.File, sheet string) []string {
	t.Helper()
	var out []string
	for row := 1; row <= 200; row++ {
		label, err := f.GetCellValue(sheet, fmt.Sprintf("B%d", row))
		if err != nil {
			t.Fatalf("read B%d: %v", row, err)
		}
		if !strings.Contains(label, "routing gap, rule group") {
			continue
		}
		p, _ := f.GetCellValue(sheet, fmt.Sprintf("C%d", row), excelize.Options{RawCellValue: true})
		s, _ := f.GetCellValue(sheet, fmt.Sprintf("D%d", row), excelize.Options{RawCellValue: true})
		out = append(out, label+"|"+p+"|"+s)
	}
	return out
}
