package report

import (
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
	"amazonrecon/internal/source"
)

// ConsolidatedSheet is the name of the sheet with one row per record_ref.
const ConsolidatedSheet = "Consolidated Data"

// ConsolidatedHeaderRow is the 1-based row of the Consolidated Data column headers, as in the sample.
const ConsolidatedHeaderRow = 10

const consolidatedFirstRow = 11

// dateKey marks the Date column, which holds the posted date (UTC) of the record rather than a raw cell.
const dateKey = "@posted"

// column is one descriptive column: its sample header and the raw CSV header it reads.
// An empty key leaves the cell blank, because the source has no value for it.
type column struct{ header, key string }

// paymentsColumns are the payments-side descriptive columns, in the sample's order.
var paymentsColumns = []column{
	{"Date", dateKey},
	{"date/time", "date/time"},
	{"Transaction Status", "Transaction status"},
	{"Transaction Release Date", "Transaction Release Date"},
	{"settlement id", "settlement ID"},
	{"type", "type"},
	{"order id", "order ID"},
	{"sku", "sku"},
	{"description", "description"},
	{"Quantity", "quantity"},
	{"marketplace", "marketplace"},
	{"account type", ""},
	{"fulfillment", "fulfilment"},
	{"order city", "order city"},
	{"order state", "order state"},
	{"order postal", "order postal"},
	{"tax collection model", ""},
}

// settlementColumns are the settlement-side descriptive columns, in the sample's order.
var settlementColumns = []column{
	{"settlement-id", "settlement-id"},
	{"settlement-start-date", "settlement-start-date"},
	{"settlement-end-date", "settlement-end-date"},
	{"deposit-date", "deposit-date"},
	{"total-amount", "total-amount"},
	{"currency", "currency"},
	{"transaction-type", "transaction-type"},
	{"order-id", "order-id"},
	{"merchant-order-id", "merchant-order-id"},
	{"adjustment-id", "adjustment-id"},
	{"shipment-id", "shipment-id"},
	{"marketplace-name", "marketplace-name"},
	{"fulfillment-id", "fulfillment-id"},
	{"posted-date", "posted-date"},
	{"posted-date-time", "posted-date-time"},
	{"order-item-code", "order-item-code"},
	{"merchant-order-item-id", "merchant-order-item-id"},
	{"merchant-adjustment-item-id", "merchant-adjustment-item-id"},
	{"sku", "sku"},
	{"quantity", "quantity-purchased"},
	{"promotion-id", "promotion-id"},
}

// slot is one Contribution column: a Summary line, or a routed Summary field with no Summary line.
// Field is empty for a Summary line the configs never route to; that column stays blank.
type slot struct{ label, group, field string }

// consolidatedCols holds the 1-based column number of each block, following the sample. Each side
// has its Summary-line columns, then one column per routed Summary field with no Summary line.
type consolidatedCols struct {
	payDesc, payTotal, paySum, payUnm int
	setDesc, setTotal, setSum, setUnm int
	status, totalDiff, diff           int
	ref, payLines, setLines           int
}

func layoutConsolidated(summaryLines, unmappedFields int) consolidatedCols {
	var c consolidatedCols
	c.payDesc = 1
	c.payTotal = c.payDesc + len(paymentsColumns)
	c.paySum = c.payTotal + 1
	c.payUnm = c.paySum + summaryLines
	c.setDesc = c.payUnm + unmappedFields
	c.setTotal = c.setDesc + len(settlementColumns)
	c.setSum = c.setTotal + 1
	c.setUnm = c.setSum + summaryLines
	c.status = c.setUnm + unmappedFields
	c.totalDiff = c.status + 1
	c.diff = c.totalDiff + 1
	c.ref = c.diff + summaryLines + unmappedFields
	c.payLines = c.ref + 1
	c.setLines = c.payLines + 1
	return c
}

// writeConsolidated writes the Consolidated Data sheet: the sample's columns, one row per record_ref
// in Consolidated order, then one row per unmapped line. Amounts are written as decimal text.
func writeConsolidated(f *excelize.File, r recon.Report, bold, money int) error {
	if _, err := f.NewSheet(ConsolidatedSheet); err != nil {
		return err
	}
	w := &sheetWriter{f: f, sheet: ConsolidatedSheet}
	summary := summarySlots(r.Summary.Lines)
	unmapped := unmappedSlots(r.Summary.Unmapped)
	c := layoutConsolidated(len(summary), len(unmapped))

	w.text(1, "A", "Consolidated Data: one row per record_ref, then unmapped lines", bold)
	w.text(3, "A", "DS1 =", 0)
	w.text(3, "B", "Payment Report", 0)
	w.text(4, "A", "DS2 =", 0)
	w.text(4, "B", "Settlement Report", 0)
	w.band(7, c.payDesc, c.setDesc-1, "Payment Report", bold)
	w.band(7, c.setDesc, c.setUnm+len(unmapped)-1, "Settlement Report", bold)
	w.band(8, c.paySum, c.payUnm+len(unmapped)-1, "Summary", bold)
	w.band(8, c.setSum, c.setUnm+len(unmapped)-1, "Summary", bold)
	w.band(8, c.diff, c.ref-1, "Summary Mismatch", bold)
	diffSlots := append(append([]slot{}, summary...), unmapped...)
	for i, s := range diffSlots {
		w.text(9, colName(c.diff+i), s.group, 0)
	}
	for i, s := range summary {
		w.text(9, colName(c.paySum+i), s.group, 0)
		w.text(9, colName(c.setSum+i), s.group, 0)
	}
	for i, s := range unmapped {
		w.text(9, colName(c.payUnm+i), s.group, 0)
		w.text(9, colName(c.setUnm+i), s.group, 0)
	}

	w.header(c, summary, unmapped, bold)

	row := consolidatedFirstRow
	for _, rec := range r.Reconciliation.Records {
		writeRecordRow(w, c, row, rec, summary, unmapped, money)
		row++
	}
	for _, u := range r.Reconciliation.Unmapped {
		writeUnmappedRow(w, c, row, u, money)
		row++
	}
	if w.err != nil {
		return w.err
	}
	last := row - 1
	if err := f.AutoFilter(ConsolidatedSheet, "A"+strconv.Itoa(ConsolidatedHeaderRow)+":"+
		colName(c.setLines)+strconv.Itoa(max(last, ConsolidatedHeaderRow)), nil); err != nil {
		return err
	}
	return f.SetPanes(ConsolidatedSheet, &excelize.Panes{
		Freeze: true, XSplit: 1, YSplit: ConsolidatedHeaderRow, TopLeftCell: "B" + strconv.Itoa(consolidatedFirstRow),
		ActivePane: "bottomRight",
	})
}

// sampleHeaders spells the Summary-line headers of the Consolidated Data sheet as the sample workbook
// does. The Summary sheet keeps the spelling of the Summary-line map; only these headers differ. Keyed by
// group and line.
var sampleHeaders = map[string]string{
	"Sales/Amazon fees":                      "Amazon Fees",
	"Sales/Cross-account Debt Adjustment":    "Cross-Account Debt Adjustment",
	"Refunds/Refund expenses":                "Refund Expenses",
	"Refunds/Refunded sales":                 "Refunded Sales",
	"Expenses/Promo rebates":                 "Promo Rebates",
	"Expenses/FBA fees":                      "FBA Fees",
	"Expenses/Amazon fees":                   "Amazon Fees",
	"Expenses/Cross-account Debt Adjustment": "Cross-Account Debt Adjustment",
}

// headerText is the Consolidated header of a slot: the sample's spelling where the sample has one,
// otherwise the slot's own label.
func headerText(s slot) string {
	if h, ok := sampleHeaders[s.group+"/"+s.label]; ok {
		return h
	}
	return s.label
}

// summarySlots makes one slot per Summary line, in display order.
func summarySlots(lines []recon.SummaryLine) []slot {
	out := make([]slot, len(lines))
	for i, l := range lines {
		out[i] = slot{label: l.Line, group: l.Group, field: l.Field}
	}
	return out
}

// unmappedSlots makes one slot per routed Summary field that has no Summary line.
func unmappedSlots(fields []recon.FieldTotal) []slot {
	out := make([]slot, len(fields))
	for i, f := range fields {
		out[i] = slot{label: f.Field, group: "No Summary line", field: f.Field}
	}
	return out
}

// header writes the column headers of every block on the header row.
func (w *sheetWriter) header(c consolidatedCols, summary, unmapped []slot, bold int) {
	for i, col := range paymentsColumns {
		w.text(ConsolidatedHeaderRow, colName(c.payDesc+i), col.header, bold)
	}
	w.text(ConsolidatedHeaderRow, colName(c.payTotal), "Total", bold)
	for i, col := range settlementColumns {
		w.text(ConsolidatedHeaderRow, colName(c.setDesc+i), col.header, bold)
	}
	w.text(ConsolidatedHeaderRow, colName(c.setTotal), "Total", bold)
	w.text(ConsolidatedHeaderRow, colName(c.status), "Reconciliation Status", bold)
	w.text(ConsolidatedHeaderRow, colName(c.totalDiff), "total (DS1-DS2)", bold)
	w.text(ConsolidatedHeaderRow, colName(c.ref), "record_ref", bold)
	w.text(ConsolidatedHeaderRow, colName(c.payLines), "Payments source lines", bold)
	w.text(ConsolidatedHeaderRow, colName(c.setLines), "Settlement source lines", bold)
	for i, s := range summary {
		w.text(ConsolidatedHeaderRow, colName(c.paySum+i), headerText(s), bold)
		w.text(ConsolidatedHeaderRow, colName(c.setSum+i), headerText(s), bold)
	}
	for i, s := range unmapped {
		w.text(ConsolidatedHeaderRow, colName(c.payUnm+i), headerText(s), bold)
		w.text(ConsolidatedHeaderRow, colName(c.setUnm+i), headerText(s), bold)
	}
	for i, s := range append(append([]slot{}, summary...), unmapped...) {
		w.text(ConsolidatedHeaderRow, colName(c.diff+i), headerText(s)+" (DS1-DS2)", bold)
	}
}

// writeRecordRow writes one record_ref: both sides' descriptive cells, totals, Contributions per Summary
// line and per unmapped Summary field, the status, the total difference and the differences per column.
func writeRecordRow(w *sheetWriter, c consolidatedCols, row int, rec recon.RecordRow, summary, unmapped []slot, money int) {
	date := rec.Payments.PostedDate
	if date == "" {
		date = rec.Settlements.PostedDate
	}
	writeDescriptive(w, c.payDesc, row, paymentsColumns, rec.Payments.Earliest, date, len(rec.Payments.Lines) > 0)
	w.number(row, colName(c.payTotal), rec.Payments.Total, money)
	writeRouted(w, row, c.paySum, summary, rec.Payments, money)
	writeRouted(w, row, c.payUnm, unmapped, rec.Payments, money)

	writeDescriptive(w, c.setDesc, row, settlementColumns, rec.Settlements.Earliest, "", len(rec.Settlements.Lines) > 0)
	w.number(row, colName(c.setTotal), rec.Settlements.Total, money)
	writeRouted(w, row, c.setSum, summary, rec.Settlements, money)
	writeRouted(w, row, c.setUnm, unmapped, rec.Settlements, money)

	w.text(row, colName(c.status), rec.Status.Label(), 0)
	w.number(row, colName(c.totalDiff), rec.Difference, money)
	for i, s := range append(append([]slot{}, summary...), unmapped...) {
		if s.field == "" {
			continue // a Summary line with no field has no Contributions to compare
		}
		diff := rec.Payments.Routed[s.field].Sub(rec.Settlements.Routed[s.field])
		w.number(row, colName(c.diff+i), diff, money)
	}
	w.text(row, colName(c.ref), rec.RecordRef, 0)
	w.text(row, colName(c.payLines), joinLines(rec.Payments.Lines), 0)
	w.text(row, colName(c.setLines), joinLines(rec.Settlements.Lines), 0)
}

// writeUnmappedRow writes one unmapped line on its own row, on its own side's columns.
func writeUnmappedRow(w *sheetWriter, c consolidatedCols, row int, u recon.ListedLine, money int) {
	w.text(row, colName(c.status), source.ReasonUnmapped, 0)
	if u.Source == source.Payments {
		writeDescriptive(w, c.payDesc, row, paymentsColumns, u.Raw, u.PostedDate, true)
		w.number(row, colName(c.payTotal), u.Amount, money)
		w.text(row, colName(c.payLines), strconv.Itoa(u.LineNumber), 0)
		return
	}
	writeDescriptive(w, c.setDesc, row, settlementColumns, u.Raw, u.PostedDate, true)
	w.number(row, colName(c.setTotal), u.Amount, money)
	w.text(row, colName(c.setLines), strconv.Itoa(u.LineNumber), 0)
}

// writeDescriptive writes a side's descriptive cells from its earliest line's raw cells.
func writeDescriptive(w *sheetWriter, first, row int, cols []column, raw map[string]string, posted string, present bool) {
	if !present {
		return
	}
	for i, col := range cols {
		value := ""
		switch col.key {
		case "":
			continue
		case dateKey:
			value = posted
		default:
			value = raw[col.key]
		}
		if value != "" {
			w.text(row, colName(first+i), value, 0)
		}
	}
}

// writeRouted writes a side's Contributions for a block of slots. A slot with no field stays blank,
// and so does every slot of a side that has no lines.
func writeRouted(w *sheetWriter, row, first int, slots []slot, side recon.Side, money int) {
	if len(side.Lines) == 0 {
		return
	}
	for i, s := range slots {
		if s.field == "" {
			continue
		}
		w.number(row, colName(first+i), side.Routed[s.field], money)
	}
}

// joinLines lists source line numbers as "3,4".
func joinLines(lines []int) string {
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// colName returns the column letters of a 1-based column number.
func colName(n int) string {
	name, err := excelize.ColumnNumberToName(n)
	if err != nil {
		panic(err) // n is computed from the layout; an invalid column is a bug
	}
	return name
}

// band writes a label across columns first..last of a row.
func (w *sheetWriter) band(row, first, last int, label string, style int) {
	for col := first; col <= last; col++ {
		w.text(row, colName(col), label, style)
	}
}
