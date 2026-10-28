// Package report writes the reconciliation workbook. It has no database access:
// it formats the numbers that the recon package computed.
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// FormatAmount renders an amount for display with thousands separators and two decimals.
// It is for printing only; the workbook stores the decimal text.
func FormatAmount(d decimal.Decimal) string {
	s := d.StringFixed(2)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

// WriteWorkbook writes the reconciliation workbook to path, creating its directory. The sheets are
// Summary (with the run header and the tie-out), Consolidated Data, Audit Trail and Out of Scope.
// Amounts are written as decimal text, so no value passes through float64.
func WriteWorkbook(path string, r recon.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", SummarySheet); err != nil {
		return err
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}
	money, err := f.NewStyle(&excelize.Style{NumFmt: 4}) // #,##0.00
	if err != nil {
		return err
	}
	if err := writeSummary(f, r, bold, money); err != nil {
		return err
	}
	if err := writeConsolidated(f, r, bold, money); err != nil {
		return err
	}
	if _, err := f.NewSheet(AuditTrailSheet); err != nil {
		return err
	}
	if err := writeAuditTrail(f, r.Audit, bold, money); err != nil {
		return err
	}
	if err := writeOutOfScope(f, r, bold, money); err != nil {
		return err
	}
	// Excel recalculates the formulas when it opens the file; the stored values serve every other reader.
	fullCalc := true
	if err := f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &fullCalc}); err != nil {
		return err
	}
	if err := f.SaveAs(path); err != nil {
		return err
	}
	return numericFormulaCache(path)
}

// sheetWriter writes cells and keeps the first error.
type sheetWriter struct {
	f     *excelize.File
	sheet string
	err   error
}

func (w *sheetWriter) cell(row int, col string) string {
	return fmt.Sprintf("%s%d", col, row)
}

func (w *sheetWriter) text(row int, col, value string, style int) {
	if w.err != nil {
		return
	}
	if err := w.f.SetCellStr(w.sheet, w.cell(row, col), value); err != nil {
		w.err = err
		return
	}
	if style != 0 {
		w.err = w.f.SetCellStyle(w.sheet, w.cell(row, col), w.cell(row, col), style)
	}
}

// number writes a stored decimal value as a number cell, keeping its exact text.
func (w *sheetWriter) number(row int, col string, d decimal.Decimal, style int) {
	if w.err != nil {
		return
	}
	w.err = w.f.SetCellDefault(w.sheet, w.cell(row, col), d.StringFixed(2))
	if w.err == nil && style != 0 {
		w.err = w.f.SetCellStyle(w.sheet, w.cell(row, col), w.cell(row, col), style)
	}
}

// formula writes a formula and keeps its stored value, the number the formula gives. Excel
// recalculates the formula; previewers and readers that do not recalculate still see the value.
func (w *sheetWriter) formula(row int, col, formula string, value decimal.Decimal, style int) {
	if w.err != nil {
		return
	}
	// The value is set first: SetCellFormula keeps the stored value, while SetCellDefault would drop the formula.
	cell := w.cell(row, col)
	if w.err = w.f.SetCellDefault(w.sheet, cell, value.StringFixed(2)); w.err != nil {
		return
	}
	if w.err = w.f.SetCellFormula(w.sheet, cell, formula); w.err != nil {
		return
	}
	if style != 0 {
		w.err = w.f.SetCellStyle(w.sheet, cell, cell, style)
	}
}
