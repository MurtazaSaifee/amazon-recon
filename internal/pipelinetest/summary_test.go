package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
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
//	line 15 Transfer -1000.00 (P-009, the type+any rule, routes it to bank_account_transfer_round_off)
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
		assertMatched(t, ctx, pool, res.RunID, "payments", pmtTransfer, []string{"P-009"})
		assertMatched(t, ctx, pool, res.RunID, "settlement", setPrincipalA, []string{"S-002"})
	})

	t.Run("in-scope lines route into the Summary, out-of-scope lines do not", func(t *testing.T) {
		assertSummary(t, rep, "Sales", "Product Charges", "1025.00", "1025.00")
		assertSummary(t, rep, "Sales", "Tax", "469.12", "234.56")
		assertSummary(t, rep, "Refunds", "Refunded sales", "-10.00", "0.00")
		assertSummary(t, rep, "Expenses", "FBA fees", "0.00", "0.00")
		assertUnmapped(t, rep, "bank_account_transfer_round_off", "-1000.00", "0.00")
	})

	t.Run("a duplicated rule shows as a negative routing gap", func(t *testing.T) {
		assertGap(t, rep.TieOut.Payments, "P-002,P-003,P-004,P-005", "-234.56")
	})

	t.Run("a settlement component with an unknown type is an Unmapped routing gap", func(t *testing.T) {
		assertGap(t, rep.TieOut.Settlements, "Unmapped", "-10.00")
		assertLoaded(t, ctx, pool, res.RunID, "settlement", setRefundUnmap, func(l store.Line) {
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
		assertDec(t, "payments summary net", tie.SummaryNet, "484.12")
		assertDec(t, "payments routing gaps", tie.GapsTotal, "-234.56")
		assertDec(t, "payments in-scope total", tie.InScope.Amount, "249.56")
		if tie.InScope.Count != 5 {
			t.Errorf("payments in-scope lines = %d, want 5", tie.InScope.Count)
		}
		assertDec(t, "payments remaining difference to the Settlement total", tie.Remaining, "-1000.00")
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
	})

	t.Run("out-of-scope and in-scope lines keep their record_ref and matched rules", func(t *testing.T) {
		assertLoaded(t, ctx, pool, res.RunID, "payments", pmtTransfer, func(l store.Line) {
			if l.RecordRef != "TRANSFER|12000000001|2026-07-05" {
				t.Errorf("transfer record_ref = %q, want TRANSFER|12000000001|2026-07-05", l.RecordRef)
			}
		})
		assertLoaded(t, ctx, pool, res.RunID, "payments", pmtRefund, func(l store.Line) {
			if l.RecordRef != "701-0000002-0000002|SYN-B-002|2026-07-04" {
				t.Errorf("refund record_ref = %q", l.RecordRef)
			}
		})
		assertLoaded(t, ctx, pool, res.RunID, "payments", pmtDeferred, func(l store.Line) {
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
	for row := 1; row <= 80; row++ {
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
	checkValue(t, f, sheet, "bank_account_transfer_round_off", "C", "-1000.00")
	checkValue(t, f, sheet, "Summary net (all Summary fields)", "C", "484.12")
	checkValue(t, f, sheet, "Summary net (all Summary fields)", "D", "1259.56")
	checkValue(t, f, sheet, "Routing gap, rule group P-002,P-003,P-004,P-005", "C", "-234.56")
	checkFormulaCell(t, f, sheet, "Remaining difference (in-scope lines minus Settlement total)", "C", "-1000.00")
	checkValue(t, f, sheet, "Remaining difference (in-scope lines minus Settlement total)", "D", "0.00")
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

func assertUnmapped(t *testing.T, rep recon.Report, field, wantP, wantS string) {
	t.Helper()
	for _, u := range rep.Summary.Unmapped {
		if u.Field == field {
			assertDec(t, field+" payments", u.Payments, wantP)
			assertDec(t, field+" settlements", u.Settlements, wantS)
			return
		}
	}
	t.Errorf("unmapped block has no field %s", field)
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
	assertLoaded(t, ctx, pool, runID, source, lineNumber, func(l store.Line) {
		if fmt.Sprint(l.MatchedRuleIDs) != fmt.Sprint(want) {
			t.Errorf("%s line %d matched rules = %v, want %v", source, lineNumber, l.MatchedRuleIDs, want)
		}
	})
}

func assertLoaded(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, source string, lineNumber int, check func(store.Line)) {
	t.Helper()
	l, err := store.LoadLine(ctx, pool, runID, source, lineNumber)
	if err != nil {
		t.Fatalf("load %s line %d: %v", source, lineNumber, err)
	}
	check(l)
}

func hasWarning(ws []string, kind mapping.WarningKind) bool {
	for _, w := range ws {
		if strings.HasPrefix(w, string(kind)+":") {
			return true
		}
	}
	return false
}

// checkValue finds the row labelled label in column B and compares the raw value of col.
func checkValue(t *testing.T, f *excelize.File, sheet, label, col, want string) {
	t.Helper()
	row := findLabel(t, f, sheet, label)
	got, err := f.GetCellValue(sheet, fmt.Sprintf("%s%d", col, row), excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read %s%d: %v", col, row, err)
	}
	if got != want {
		t.Errorf("%q column %s = %q, want %q", label, col, got, want)
	}
}

// checkFormulaCell checks that the cell holds a formula and that its stored value is want.
func checkFormulaCell(t *testing.T, f *excelize.File, sheet, label, col, want string) {
	t.Helper()
	row := findLabel(t, f, sheet, label)
	cell := fmt.Sprintf("%s%d", col, row)
	formula, err := f.GetCellFormula(sheet, cell)
	if err != nil {
		t.Fatalf("formula %s: %v", cell, err)
	}
	if formula == "" {
		t.Errorf("%q %s has no formula", label, cell)
	}
	value, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("value %s: %v", cell, err)
	}
	if value != want {
		t.Errorf("%q %s stored value = %q, want %q (formula %q)", label, cell, value, want, formula)
	}
}

func findLabel(t *testing.T, f *excelize.File, sheet, label string) int {
	t.Helper()
	for row := 1; row <= 80; row++ {
		v, err := f.GetCellValue(sheet, fmt.Sprintf("B%d", row))
		if err != nil {
			t.Fatalf("read B%d: %v", row, err)
		}
		if strings.TrimSpace(v) == label {
			return row
		}
	}
	t.Fatalf("sheet %s has no row labelled %q", sheet, label)
	return 0
}
