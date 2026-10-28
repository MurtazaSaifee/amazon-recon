package report

import (
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

// sheetWriter writes cells to one sheet and keeps the first error, so a sheet is written as a
// sequence of calls and checked once at the end.
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

// count writes a whole number cell.
func (w *sheetWriter) count(row int, col string, n int) {
	if w.err != nil {
		return
	}
	w.err = w.f.SetCellInt(w.sheet, w.cell(row, col), int64(n))
}
