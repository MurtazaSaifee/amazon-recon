package report

import (
	"fmt"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// SummarySheet is the name of the sheet that holds the run header, the Summary, the unmapped block,
// the tie-out to the Settlement total and the reconciliation counts.
const SummarySheet = "Summary"

// summaryNote is printed on the sheet. Source signs are kept, so the reader is told how to read them.
const summaryNote = "Source signs are kept: expenses and refunds are negative, as the reports give them. " +
	"Payments are Transaction lines only and Settlements are Components only. " +
	"The last column is Payments minus Settlements."

// The row layout of the Summary block, taken from the sample workbook. The column headers are in row 1.
// The first group starts two blank rows below them. Each later group starts one blank row below the
// previous group's last row, or three blank rows below it when the group is a single line named as the
// group (Paid To Amazon). The rest of the sheet starts two blank rows below the block.
const (
	summaryHeaderRow           = 1
	blankRowsAfterHeader       = 2
	blankRowsBetweenGroups     = 1
	blankRowsBeforeSingleGroup = 3
	blankRowsBeforeRest        = 2
)

// writeSummary writes the Summary sheet: the column headers and the Summary block from row 1, then the
// run header and the rest of the sheet below the block. Subtotal and difference cells are formulas whose
// stored values are the same numbers the formulas produce.
func writeSummary(f *excelize.File, r recon.Report, st styles) error {
	w := &sheetWriter{f: f, sheet: SummarySheet}
	w.text(summaryHeaderRow, "C", "Payments", st.bold)
	w.text(summaryHeaderRow, "D", "Settlements", st.bold)
	w.text(summaryHeaderRow, "E", "Payments - Settlements", st.bold)
	row := writeSummaryBlock(w, r.Summary.Lines, st) + 1 + blankRowsBeforeRest

	w.text(row, "B", "Amazon Payments vs Settlement: Summary", st.bold)
	row = writeRunHeader(w, r, row+2) + 1
	w.text(row, "B", summaryNote, 0)
	row = writeUnmapped(w, row+2, r.Summary.Unmapped, st.money, st.bold)
	row = writeTieOut(w, row+1, r, st.money, st.bold)
	writeStatusCounts(w, row+1, r.Reconciliation, st.bold)
	return w.err
}

// writeSummaryBlock writes the Summary groups in display order, starting from the first group row, and
// returns the last row it uses. It spaces the groups by the rule above.
func writeSummaryBlock(w *sheetWriter, lines []recon.SummaryLine, st styles) int {
	last := summaryHeaderRow
	for i, g := range groupLines(lines) {
		blank := blankRowsBetweenGroups
		switch {
		case i == 0:
			blank = blankRowsAfterHeader
		case g.isSingleLine():
			blank = blankRowsBeforeSingleGroup
		}
		last = writeGroup(w, last+1+blank, g, st)
	}
	return last
}

// writeGroup writes one group whose first row is row, and returns its last row. A single line named as
// its group is one labelled row. Any other group has its subtotal on its first row, with its lines below.
func writeGroup(w *sheetWriter, row int, g lineGroup, st styles) int {
	if g.isSingleLine() {
		writeLineRow(w, row, g.name, g.lines[0].Payments, g.lines[0].Settlements, st.money, st.indent)
		return row
	}
	first := row + 1
	payments, settlements := decimal.Zero, decimal.Zero
	for i, l := range g.lines {
		writeLineRow(w, first+i, l.Line, l.Payments, l.Settlements, st.money, st.indent)
		payments = payments.Add(l.Payments)
		settlements = settlements.Add(l.Settlements)
	}
	last := first + len(g.lines) - 1
	w.text(row, "B", g.name, st.bold)
	w.formula(row, "C", fmt.Sprintf("SUM(C%d:C%d)", first, last), payments, st.money)
	w.formula(row, "D", fmt.Sprintf("SUM(D%d:D%d)", first, last), settlements, st.money)
	w.formula(row, "E", fmt.Sprintf("C%d-D%d", row, row), payments.Sub(settlements), st.money)
	return last
}

// lineGroup is one Summary group and its lines, in display order.
type lineGroup struct {
	name  string
	lines []recon.SummaryLine
}

// isSingleLine reports whether the group is one line named as the group, such as Paid To Amazon.
func (g lineGroup) isSingleLine() bool {
	return len(g.lines) == 1 && g.lines[0].Line == g.name
}

// groupLines keeps the groups in the order their first line appears.
func groupLines(lines []recon.SummaryLine) []lineGroup {
	var out []lineGroup
	index := map[string]int{}
	for _, l := range lines {
		i, ok := index[l.Group]
		if !ok {
			i = len(out)
			index[l.Group] = i
			out = append(out, lineGroup{name: l.Group})
		}
		out[i].lines = append(out[i].lines, l)
	}
	return out
}

// writeLineRow writes one labelled Payments and Settlements pair with its variance formula.
func writeLineRow(w *sheetWriter, row int, label string, payments, settlements decimal.Decimal, money, style int) {
	w.text(row, "B", label, style)
	w.number(row, "C", payments, money)
	w.number(row, "D", settlements, money)
	w.formula(row, "E", fmt.Sprintf("C%d-D%d", row, row), payments.Sub(settlements), money)
}

// writeUnmapped writes the block for routed Summary fields that have no Summary line.
func writeUnmapped(w *sheetWriter, row int, fields []recon.FieldTotal, money, bold int) int {
	w.text(row, "B", "Summary fields with no Summary line", bold)
	row++
	if len(fields) == 0 {
		w.text(row, "B", "None: every routed Summary field has a Summary line.", 0)
		return row + 2
	}
	first := row
	payments, settlements := decimal.Zero, decimal.Zero
	for _, f := range fields {
		writeLineRow(w, row, f.Field, f.Payments, f.Settlements, money, 0)
		payments = payments.Add(f.Payments)
		settlements = settlements.Add(f.Settlements)
		row++
	}
	last := row - 1
	w.text(row, "B", "Total of unmapped fields", bold)
	w.formula(row, "C", fmt.Sprintf("SUM(C%d:C%d)", first, last), payments, money)
	w.formula(row, "D", fmt.Sprintf("SUM(D%d:D%d)", first, last), settlements, money)
	w.formula(row, "E", fmt.Sprintf("C%d-D%d", row, row), payments.Sub(settlements), money)
	return row + 2
}

// writeTieOut writes the tie-out, one column per source, from the in-scope lines to the Settlement
// total. The residual is explained by the Unreconciled amounts of each side; what is left is the
// unexplained difference, which must be zero.
func writeTieOut(w *sheetWriter, row int, r recon.Report, money, bold int) int {
	p, s := r.TieOut.Payments, r.TieOut.Settlements
	w.text(row, "B", "Tie-out to the Settlement total", bold)
	w.text(row, "C", "Payments", bold)
	w.text(row, "D", "Settlements", bold)
	row++

	w.text(row, "B", "In-scope lines (count)", 0)
	w.count(row, "C", p.InScope.Count)
	w.count(row, "D", s.InScope.Count)
	row++

	netRow := row
	w.text(row, "B", "Summary net (all Summary fields)", 0)
	w.number(row, "C", p.SummaryNet, money)
	w.number(row, "D", s.SummaryNet, money)
	row++

	// Each source lists only its own rule groups, and only the groups with a non-zero routing gap.
	firstGap := row
	listed := 0
	for _, g := range p.Gaps {
		if g.Amount.IsZero() {
			continue
		}
		w.text(row, "B", "Payments routing gap, rule group "+g.Rule, 0)
		w.number(row, "C", g.Amount, money)
		row++
		listed++
	}
	for _, g := range s.Gaps {
		if g.Amount.IsZero() {
			continue
		}
		w.text(row, "B", "Settlements routing gap, rule group "+g.Rule, 0)
		w.number(row, "D", g.Amount, money)
		row++
		listed++
	}
	lastGap := row - 1
	gapsRow := row
	w.text(row, "B", "Routing gaps total", bold)
	if listed == 0 {
		w.number(row, "C", decimal.Zero, money)
		w.number(row, "D", decimal.Zero, money)
	} else {
		w.formula(row, "C", fmt.Sprintf("SUM(C%d:C%d)", firstGap, lastGap), p.GapsTotal, money)
		w.formula(row, "D", fmt.Sprintf("SUM(D%d:D%d)", firstGap, lastGap), s.GapsTotal, money)
	}
	row++

	inScopeRow := row
	w.text(row, "B", "In-scope line total (Summary net plus routing gaps)", bold)
	w.formula(row, "C", fmt.Sprintf("C%d+C%d", netRow, gapsRow), p.SummaryNet.Add(p.GapsTotal), money)
	w.formula(row, "D", fmt.Sprintf("D%d+D%d", netRow, gapsRow), s.SummaryNet.Add(s.GapsTotal), money)
	row++

	totalRow := row
	w.text(row, "B", "Settlement total (Settlement report header)", 0)
	w.number(row, "C", r.Settlement.Total, money)
	w.number(row, "D", r.Settlement.Total, money)
	row++

	remainingRow := row
	w.text(row, "B", "Remaining difference (in-scope lines minus Settlement total)", bold)
	w.formula(row, "C", fmt.Sprintf("C%d-C%d", inScopeRow, totalRow), p.Remaining, money)
	w.formula(row, "D", fmt.Sprintf("D%d-D%d", inScopeRow, totalRow), s.Remaining, money)
	row++

	unreconPaymentRow := row
	w.text(row, "B", "Unreconciled payment amounts", 0)
	w.number(row, "C", p.Unreconciled.Amount, money)
	row++
	unreconSettlementRow := row
	w.text(row, "B", "Unreconciled settlement amounts", 0)
	w.number(row, "D", s.Unreconciled.Amount, money)
	row++
	w.text(row, "B", "Unexplained difference (must be 0.00)", bold)
	w.formula(row, "C", fmt.Sprintf("C%d-C%d", remainingRow, unreconPaymentRow), p.Unexplained, money)
	w.formula(row, "D", fmt.Sprintf("D%d-D%d", remainingRow, unreconSettlementRow), s.Unexplained, money)
	row++

	w.text(row, "B", "In-scope Released excl. Unreconciled payments", 0)
	w.number(row, "C", r.ReleasedExcludingUnreconciled, money)
	row++
	w.text(row, "B", "Released excl. Unreconciled payments minus Settlement total", 0)
	w.number(row, "C", r.ReleasedExcludingUnreconciledMinusTotal, money)
	row++

	w.text(row, "B", fmt.Sprintf("Deferred memo, out of scope (%d lines)", r.Deferred.Count), 0)
	w.number(row, "C", r.Deferred.Amount, money)
	w.text(row+1, "B", "Deferred lines have no Transaction Release Date. They are not in the tie-out; the memo shows the gap to the Settlement report.", 0)
	return row + 3
}
