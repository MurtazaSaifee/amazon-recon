package report

import (
	"fmt"

	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// AuditTrailSheet is the name of the sheet with one row per Contribution.
const AuditTrailSheet = "Audit Trail"

// auditHeader is the first row of the Audit Trail: where each Contribution came from, and where it went.
var auditHeader = []string{
	"Source", "File", "Line number", "Rule ID", "Amount field", "Amount", "Summary field",
	"Summary group", "Summary line", "record_ref",
}

// writeAuditTrail writes one row per Contribution, below a header row, with a filter and frozen header.
// Amounts are stored as decimal text, as the other sheets do.
func writeAuditTrail(f *excelize.File, rows []recon.AuditRow, bold, money int) error {
	w := &sheetWriter{f: f, sheet: AuditTrailSheet}
	for i, h := range auditHeader {
		w.text(1, columnName(i), h, bold)
	}
	for i, a := range rows {
		row := i + 2
		w.text(row, "A", a.Source, 0)
		w.text(row, "B", a.FileName, 0)
		if w.err == nil {
			w.err = f.SetCellInt(AuditTrailSheet, w.cell(row, "C"), int64(a.LineNumber))
		}
		w.text(row, "D", a.RuleID, 0)
		w.text(row, "E", a.AmountField, 0)
		w.number(row, "F", a.Amount, money)
		w.text(row, "G", a.SummaryField, 0)
		if a.SummaryGroup != "" {
			w.text(row, "H", a.SummaryGroup, 0)
		}
		if a.SummaryLine != "" {
			w.text(row, "I", a.SummaryLine, 0)
		}
		w.text(row, "J", a.RecordRef, 0)
	}
	if w.err != nil {
		return w.err
	}
	if err := f.SetPanes(AuditTrailSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return f.AutoFilter(AuditTrailSheet, fmt.Sprintf("A1:J%d", len(rows)+1), nil)
}

// columnName returns the column letter for a zero-based index, up to Z.
func columnName(i int) string {
	return string(rune('A' + i))
}
