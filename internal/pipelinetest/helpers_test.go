package pipelinetest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/store"
)

// The synthetic fixture pair and the reconciliation fixture pair, named relative to the fixtures directory.
const (
	synthPaymentsFile   = "synthetic_payments.csv"
	synthSettlementFile = "synthetic_settlement.txt"
	reconPaymentsFile   = "reconciliation_payments.csv"
	reconSettlementFile = "reconciliation_settlement.txt"
)

// ingestFixtures ingests the synthetic fixture pair in fixed mode.
func ingestFixtures(t *testing.T, pool *pgxpool.Pool) ingest.Result {
	t.Helper()
	return ingestFiles(t, pool, synthPaymentsFile, synthSettlementFile)
}

// ingestFiles ingests one pair of fixture files, named relative to the fixtures directory, in fixed mode.
func ingestFiles(t *testing.T, pool *pgxpool.Pool, paymentsFile, settlementFile string) ingest.Result {
	t.Helper()
	res, err := ingest.Run(context.Background(), pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, paymentsFile),
		SettlementPath: filepath.Join(fixtures, settlementFile),
	})
	if err != nil {
		t.Fatalf("ingest %s and %s: %v", paymentsFile, settlementFile, err)
	}
	return res
}

// assertDec checks an exact decimal against the decimal text want.
func assertDec(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	w := decimal.RequireFromString(want)
	if !got.Equal(w) {
		t.Errorf("%s = %s, want %s", what, got.StringFixed(2), want)
	}
}

// assertLine reads one stored line of a run and hands it to check.
func assertLine(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, source string, lineNumber int, check func(store.Line)) {
	t.Helper()
	l, err := store.LoadLine(ctx, pool, runID, source, lineNumber)
	if err != nil {
		t.Fatalf("load %s line %d: %v", source, lineNumber, err)
	}
	check(l)
}

// checkValue finds the row labelled label in column B of a sheet and compares the raw value of col.
func checkValue(t *testing.T, f *excelize.File, sheet, label, col, want string) {
	t.Helper()
	row := findLabel(t, f, sheet, label)
	cell := fmt.Sprintf("%s%d", col, row)
	got, err := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read %s: %v", cell, err)
	}
	if got != want {
		t.Errorf("%q column %s = %q, want %q", label, col, got, want)
	}
}

// checkFormulaCell checks that the cell in col of the labelled row holds a formula and that its stored value is want.
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

// findLabel returns the row whose column B holds label exactly, and fails the test if no row does.
func findLabel(t *testing.T, f *excelize.File, sheet, label string) int {
	t.Helper()
	for row := 1; row <= 200; row++ {
		v, err := f.GetCellValue(sheet, fmt.Sprintf("B%d", row))
		if err != nil {
			t.Fatalf("read B%d: %v", row, err)
		}
		if v == label {
			return row
		}
	}
	t.Fatalf("sheet %s has no row labelled %q", sheet, label)
	return 0
}
