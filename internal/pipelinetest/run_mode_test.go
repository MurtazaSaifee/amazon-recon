package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/mapping"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
)

// checkSheetCell finds the row labelled label on the named sheet and checks the cell in col of
// that row. Labels sit in column B on the Summary sheet and column A on the Tie-out sheet.
func checkSheetCell(t *testing.T, f *excelize.File, sheet, label, col, want string) {
	t.Helper()
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read %s: %v", sheet, err)
	}
	labelCol := 0 // Tie-out: column A
	if sheet == "Summary" {
		labelCol = 1 // column B
	}
	colIdx := int(col[0] - 'A')
	for _, r := range rows {
		if len(r) > labelCol && r[labelCol] == label {
			got := ""
			if colIdx < len(r) {
				got = r[colIdx]
			}
			if got != want {
				t.Errorf("%s %q column %s = %q, want %q", sheet, label, col, got, want)
			}
			return
		}
	}
	t.Errorf("%s has no row labelled %q", sheet, label)
}

// The baseline and fixed runs differ only where a switch applies. In the synthetic fixture the
// Transfer is the one line a switch touches: fixed mode matches it with its prefix rule P-008, which
// summarises nothing, while baseline mode falls to the Catch-all P-009 and routes it to Expenses > Amazon fees.
func TestBaselineAndFixedDifferOnlyWhereASwitchApplies(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)

	// The baseline run is read before the fixed run replaces the stored lines of both reports.
	baseRes, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           ingest.ModeBaseline,
	})
	if err != nil {
		t.Fatalf("ingest in baseline mode: %v", err)
	}
	assertMatched(t, ctx, pool, baseRes.RunID, "payments", pmtTransfer, []string{"P-009"})
	base, err := recon.Build(ctx, pool, baseRes.RunID)
	if err != nil {
		t.Fatalf("reconcile baseline run: %v", err)
	}

	fixedRes := ingestFixtures(t, pool)
	assertMatched(t, ctx, pool, fixedRes.RunID, "payments", pmtTransfer, []string{"P-008"})
	fixed, err := recon.Build(ctx, pool, fixedRes.RunID)
	if err != nil {
		t.Fatalf("reconcile fixed run: %v", err)
	}

	amazonFees := func(rep recon.Report) recon.SummaryLine {
		for _, l := range rep.Summary.Lines {
			if l.Group == "Expenses" && l.Line == "Amazon fees" {
				return l
			}
		}
		t.Fatal("no Expenses > Amazon fees line")
		return recon.SummaryLine{}
	}
	assertDec(t, "baseline Amazon fees minus fixed, payments", amazonFees(base).Payments.Sub(amazonFees(fixed).Payments), "-1000.00")

	if len(base.Summary.Lines) != len(fixed.Summary.Lines) {
		t.Fatalf("summary lines differ: baseline %d, fixed %d", len(base.Summary.Lines), len(fixed.Summary.Lines))
	}
	for i, b := range base.Summary.Lines {
		f := fixed.Summary.Lines[i]
		if b.Group == "Expenses" && b.Line == "Amazon fees" {
			if !b.Settlements.Equal(f.Settlements) {
				t.Errorf("Amazon fees settlements differ: baseline %s, fixed %s", b.Settlements, f.Settlements)
			}
			continue
		}
		if b.Line != f.Line || !b.Payments.Equal(f.Payments) || !b.Settlements.Equal(f.Settlements) {
			t.Errorf("Summary line %s > %s differs between modes: baseline %s/%s, fixed %s/%s", b.Group, b.Line,
				b.Payments, b.Settlements, f.Payments, f.Settlements)
		}
	}
	if fmt.Sprint(base.Summary.Unmapped) != fmt.Sprint(fixed.Summary.Unmapped) {
		t.Errorf("unmapped Summary fields differ: baseline %+v, fixed %+v", base.Summary.Unmapped, fixed.Summary.Unmapped)
	}
	if !base.TieOut.Settlements.SummaryNet.Equal(fixed.TieOut.Settlements.SummaryNet) {
		t.Errorf("settlement Summary net differs between modes")
	}
	if !base.Components.Amount.Equal(fixed.Components.Amount) || base.Reconciliation.Counts != fixed.Reconciliation.Counts {
		t.Errorf("reconciliation of the settlement side differs between modes")
	}
}

// Timestamps labelled UTC are written in UTC, whatever zone the value was read in. The times are
// moved here into a zone 7 hours behind UTC, where the date of 1 July 00:00 UTC is still 30 June.
func TestTimestampsLabelledUTCAreWrittenInUTC(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	behind := time.FixedZone("UTC-7", -7*60*60)
	rep.Settlement.PeriodStart = rep.Settlement.PeriodStart.In(behind)
	rep.Settlement.PeriodEnd = rep.Settlement.PeriodEnd.In(behind)
	rep.Settlement.DepositDate = rep.Settlement.DepositDate.In(behind)
	rep.StartedAt = rep.StartedAt.In(behind)
	rep.FinishedAt = rep.FinishedAt.In(behind)

	f := writeReport(t, rep)
	checkValue(t, f, report.SummarySheet, "Settlement period (UTC)", "C", "2026-07-01 to 2026-07-15")
	checkValue(t, f, report.SummarySheet, "Deposit date (UTC)", "C", "2026-07-17 00:00:00 UTC")
	started, err := f.GetCellValue(report.SummarySheet, "C"+fmt.Sprint(findLabel(t, f, report.SummarySheet, "Run started / finished")), excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read run started: %v", err)
	}
	if strings.Count(started, " UTC") != 2 {
		t.Errorf("run started / finished = %q, want both times in UTC", started)
	}
}

func TestRunModeIsRecorded(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           "baseline",
	})
	if err != nil {
		t.Fatalf("ingest in baseline mode: %v", err)
	}
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Mode != "baseline" {
		t.Errorf("mode = %q, want baseline", rep.Mode)
	}
	if len(rep.Switches) != 0 {
		t.Errorf("baseline run recorded switches %v, want none", rep.Switches)
	}

	fixed, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
	})
	if err != nil {
		t.Fatalf("ingest in fixed mode: %v", err)
	}
	fixedRep, err := recon.Build(ctx, pool, fixed.RunID)
	if err != nil {
		t.Fatalf("reconcile fixed run: %v", err)
	}
	if fixedRep.Mode != "fixed" {
		t.Errorf("default mode = %q, want fixed", fixedRep.Mode)
	}
	if want := mapping.SwitchesFor(false).Names(); !slices.Equal(fixedRep.Switches, want) {
		t.Errorf("fixed run recorded switches %v, want the registry's fixed set %v", fixedRep.Switches, want)
	}

	t.Run("the report header shows the mode and the enabled switches", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "header.xlsx")
		if err := report.WriteWorkbook(path, rep); err != nil {
			t.Fatalf("write baseline workbook: %v", err)
		}
		f, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatalf("open workbook: %v", err)
		}
		defer f.Close()
		checkSheetCell(t, f, "Summary", "Mode", "C", "baseline")
		checkSheetCell(t, f, "Summary", "Interpretation switches", "C", "none")
	})

	if _, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           "sometimes",
	}); err == nil {
		t.Error("an unknown mode was accepted, want an error")
	}
}
