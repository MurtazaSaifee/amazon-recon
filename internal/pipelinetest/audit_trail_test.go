package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
)

func findAudit(t *testing.T, rep recon.Report, source string, lineNumber int, rule string) recon.AuditRow {
	t.Helper()
	got := findAuditAll(rep, source, lineNumber, rule)
	if len(got) != 1 {
		t.Fatalf("audit rows for %s line %d rule %s = %d, want 1", source, lineNumber, rule, len(got))
	}
	return got[0]
}

func findAuditAll(rep recon.Report, source string, lineNumber int, rule string) []recon.AuditRow {
	var out []recon.AuditRow
	for _, a := range rep.Audit {
		if a.Source == source && a.LineNumber == lineNumber && a.RuleID == rule {
			out = append(out, a)
		}
	}
	return out
}

func compareAudit(t *testing.T, got, want recon.AuditRow) {
	t.Helper()
	if got.Source != want.Source || got.FileName != want.FileName || got.LineNumber != want.LineNumber ||
		got.RuleID != want.RuleID || got.AmountField != want.AmountField || !got.Amount.Equal(want.Amount) ||
		got.SummaryField != want.SummaryField || got.SummaryGroup != want.SummaryGroup ||
		got.SummaryLine != want.SummaryLine || got.RecordRef != want.RecordRef {
		t.Errorf("audit row = %+v\nwant          %+v", got, want)
	}
}

// writeReport writes the workbook for rep to a temporary file and opens it.

func writeReport(t *testing.T, rep recon.Report) *excelize.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.xlsx")
	if err := report.WriteWorkbook(path, rep); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestAuditTrailHasOneRowPerContribution(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM summary_contributions WHERE run_id = $1", res.RunID).Scan(&stored); err != nil {
		t.Fatalf("count contributions: %v", err)
	}
	if stored == 0 || len(rep.Audit) != stored {
		t.Fatalf("audit rows = %d, want one per contribution (%d)", len(rep.Audit), stored)
	}

	t.Run("a payments contribution names its file, line, rule, amount field, Summary line and record_ref", func(t *testing.T) {
		compareAudit(t, findAudit(t, rep, "payments", pmtOrderA, "P-002"), recon.AuditRow{
			Source: "payments", FileName: "synthetic_payments.csv", LineNumber: pmtOrderA, RuleID: "P-002",
			AmountField: "product_sales", Amount: decimal.RequireFromString("1000.00"),
			SummaryField: "sales_product_charges", SummaryGroup: "Sales", SummaryLine: "Product Charges",
			RecordRef: "701-0000001-0000001|SYN-A-001|2026-07-02",
		})
	})

	t.Run("each rule that routes an amount has its own row, and a rule routing nothing has none", func(t *testing.T) {
		if n := len(findAuditAll(rep, "payments", pmtOrderA, "P-003")) + len(findAuditAll(rep, "payments", pmtOrderA, "P-004")); n != 2 {
			t.Errorf("tax rows for line %d = %d, want 2 (the duplicated rule applies twice)", pmtOrderA, n)
		}
		if n := len(findAuditAll(rep, "payments", pmtOrderA, "P-005")); n != 0 {
			t.Errorf("rule P-005 routes no Summary field but has %d audit rows", n)
		}
	})

	t.Run("a settlement component is audited with its own file and line", func(t *testing.T) {
		a := findAudit(t, rep, "settlement", setPrincipalA, "S-002")
		if a.FileName != "synthetic_settlement.txt" || !a.Amount.Equal(decimal.RequireFromString("1000.00")) || a.SummaryField != "sales_product_charges" {
			t.Errorf("settlement audit row = %+v", a)
		}
	})
}

// The Audit Trail sheet has one row per Contribution, with the columns a reviewer traces by.

func TestAuditTrailSheetHasOneRowPerContribution(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	f := writeReport(t, rep)
	rows, err := f.GetRows(report.AuditTrailSheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read Audit Trail sheet: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Audit Trail sheet is empty")
	}
	wantHeader := []string{"Source", "File", "Line number", "Rule ID", "Amount field", "Amount", "Summary field",
		"Summary group", "Summary line", "record_ref"}
	if fmt.Sprint(rows[0][:len(wantHeader)]) != fmt.Sprint(wantHeader) {
		t.Errorf("Audit Trail header = %v, want %v", rows[0], wantHeader)
	}
	if got := len(rows) - 1; got != len(rep.Audit) {
		t.Errorf("Audit Trail data rows = %d, want %d", got, len(rep.Audit))
	}
	for _, r := range rows[1:] {
		if len(r) >= 6 && r[0] == "payments" && r[2] == fmt.Sprint(pmtOrderA) && r[3] == "P-002" {
			if r[5] != "1000.00" || r[8] != "Product Charges" || r[9] != "701-0000001-0000001|SYN-A-001|2026-07-02" {
				t.Errorf("Audit Trail row for P-002 = %v", r)
			}
			return
		}
	}
	t.Error("Audit Trail has no row for payments line 11, rule P-002")
}

// Each run records its config hash and mode. The workbook header shows them with the run's identity.

func TestRunHeaderShowsRunConfigAndMode(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Mode != "fixed" {
		t.Errorf("mode = %q, want fixed by default", rep.Mode)
	}
	if len(rep.ConfigHash) != 64 {
		t.Errorf("config hash = %q, want a 64-character SHA-256", rep.ConfigHash)
	}

	f := writeReport(t, rep)
	checkValue(t, f, report.SummarySheet, "Settlement ID", "C", settlementID)
	checkValue(t, f, report.SummarySheet, "Run ID", "C", fmt.Sprint(res.RunID))
	checkValue(t, f, report.SummarySheet, "Config hash (first 12 characters)", "C", rep.ConfigHash[:12])
	checkValue(t, f, report.SummarySheet, "Mode", "C", "fixed")
	checkValue(t, f, report.SummarySheet, "Malformed lines (both reports)", "C", "0")
}
