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

// TieOutSheet is the name of the sheet that holds the tie-out.
const TieOutSheet = "Tie-out"

// WriteWorkbook writes the tie-out workbook to path, creating its directory.
// Amounts are written as decimal text, so no value passes through float64.
func WriteWorkbook(path string, r recon.Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", TieOutSheet); err != nil {
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

	w := &sheetWriter{f: f, sheet: TieOutSheet}
	w.text(1, "A", "Amazon Payments vs Settlement: tie-out", bold)

	w.text(3, "A", "Settlement ID", 0)
	w.text(3, "B", r.Settlement.ID, 0)
	w.text(4, "A", "Settlement period (UTC)", 0)
	w.text(4, "B", r.Settlement.PeriodStart.Format(stamp)+" to "+r.Settlement.PeriodEnd.Format(stamp), 0)
	w.text(5, "A", "Deposit date (UTC)", 0)
	w.text(5, "B", r.Settlement.DepositDate.Format(stamp), 0)
	w.text(6, "A", "Run ID", 0)
	w.text(6, "B", fmt.Sprint(r.RunID), 0)
	w.text(7, "A", "Run started / finished", 0)
	w.text(7, "B", r.StartedAt.Format(stamp)+" / "+r.FinishedAt.Format(stamp), 0)
	for i, file := range r.Files {
		row := 8 + i
		w.text(row, "A", file.Source+" file", 0)
		w.text(row, "B", file.FileName, 0)
		w.text(row, "C", fmt.Sprintf("sha256 %s; %d physical lines, %d stored",
			file.SHA256, file.PhysicalLines, file.StoredLines), 0)
	}

	const top = 12
	w.text(top-1, "A", "Line", bold)
	w.text(top-1, "B", "Count", bold)
	w.text(top-1, "C", "Amount ("+r.Settlement.Currency+")", bold)

	w.amount(top, "Settlement total (Settlement report header)", nil, r.Settlement.Total, money)
	w.amount(top+1, "In-scope Components", &r.Components.Count, r.Components.Amount, money)
	w.amount(top+2, "Components minus Settlement total", nil, r.ComponentsMinusTotal, money)
	w.amount(top+3, "In-scope Transaction lines (Released, incl. Transfer)", &r.TransactionsInScope.Count, r.TransactionsInScope.Amount, money)
	w.amount(top+4, "Transfer (in scope)", &r.Transfers.Count, r.Transfers.Amount, money)
	w.amount(top+5, "In-scope Released excl. Transfer", &r.ReleasedExcludingTransfer.Count, r.ReleasedExcludingTransfer.Amount, money)
	w.amount(top+6, "Released excl. Transfer minus Settlement total", nil, r.ExcludingTransferMinusTotal, money)
	w.amount(top+7, "Deferred memo (out of scope)", &r.Deferred.Count, r.Deferred.Amount, money)

	w.text(top+9, "A", "Deferred lines have no Transaction Release Date. They are out of scope and shown here as a memo only; they are not part of the tie-out.", 0)

	if w.err != nil {
		return w.err
	}
	return f.SaveAs(path)
}

const stamp = "2006-01-02 15:04:05 MST"

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

// amount writes one tie-out line: a label, an optional count and an amount.
func (w *sheetWriter) amount(row int, label string, count *int, amount decimal.Decimal, style int) {
	w.text(row, "A", label, 0)
	if count != nil {
		if w.err == nil {
			w.err = w.f.SetCellInt(w.sheet, w.cell(row, "B"), int64(*count))
		}
	}
	if w.err == nil {
		// SetCellDefault stores the decimal text as a number cell, so the exact value is kept.
		w.err = w.f.SetCellDefault(w.sheet, w.cell(row, "C"), amount.StringFixed(2))
	}
	if w.err == nil {
		w.err = w.f.SetCellStyle(w.sheet, w.cell(row, "C"), w.cell(row, "C"), style)
	}
}
