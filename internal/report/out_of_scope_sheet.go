package report

import (
	"strconv"

	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// OutOfScopeSheet is the name of the sheet that lists every out-of-scope line with its reason.
const OutOfScopeSheet = "Out of Scope"

// outOfScopeColumns are the columns of the Out of Scope sheet. keys holds the raw header each report
// reads for the column: [payments, settlement]. An empty key leaves the cell blank.
var outOfScopeColumns = []struct {
	header string
	keys   [2]string // payments, settlement; empty when that report has no such field
}{
	{"Reason", [2]string{}},
	{"Source", [2]string{}},
	{"File", [2]string{}},
	{"Line number", [2]string{}},
	{"Settlement ID", [2]string{"settlement ID", "settlement-id"}},
	{"Status", [2]string{"Transaction status", ""}},
	{"Type", [2]string{"type", "transaction-type"}},
	{"Order ID", [2]string{"order ID", "order-id"}},
	{"SKU", [2]string{"sku", "sku"}},
	{"Description", [2]string{"description", "amount-description"}},
	{"Transaction Release Date", [2]string{"Transaction Release Date", ""}},
	{"Amount (AUD)", [2]string{}},
}

// writeOutOfScope writes the Out of Scope sheet: one row per out-of-scope line, with its reason,
// source, file, line number, key fields and line amount. The Settlement header is not listed.
func writeOutOfScope(f *excelize.File, r recon.Report, bold, money int) error {
	if _, err := f.NewSheet(OutOfScopeSheet); err != nil {
		return err
	}
	w := &sheetWriter{f: f, sheet: OutOfScopeSheet}
	w.text(1, "A", "Out of Scope: lines that do not feed the Summary or the reconciliation", bold)
	w.text(2, "A", "Deferred lines have no Transaction Release Date and belong to a later disbursement. "+
		"Other Settlement lines belong to another Settlement. The Settlement header is the reference total, not a line, and is not listed.", 0)

	const headerRow = 4
	for i, col := range outOfScopeColumns {
		w.text(headerRow, colName(i+1), col.header, bold)
	}
	row := headerRow + 1
	for _, l := range r.Reconciliation.OutOfScope {
		w.text(row, "A", l.Reason, 0)
		w.text(row, "B", l.Source, 0)
		w.text(row, "C", l.FileName, 0)
		w.text(row, "D", strconv.Itoa(l.LineNumber), 0)
		source := 0
		if l.Source != "payments" {
			source = 1
		}
		// Key fields run from column E (Settlement ID) to column K (Transaction Release Date).
		for i, col := range outOfScopeColumns[4:11] {
			if key := col.keys[source]; key != "" {
				if v := l.Raw[key]; v != "" {
					w.text(row, colName(i+5), v, 0)
				}
			}
		}
		w.number(row, "L", l.Amount, money)
		row++
	}
	return w.err
}
