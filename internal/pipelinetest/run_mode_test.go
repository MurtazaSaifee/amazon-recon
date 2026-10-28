package pipelinetest

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

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
		checkSheetCell(t, f, "Tie-out", "Interpretation switches", "B", "none")
	})

	if _, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           "sometimes",
	}); err == nil {
		t.Error("an unknown mode was accepted, want an error")
	}
}
