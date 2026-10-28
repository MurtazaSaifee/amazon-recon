package report

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/recon"
)

// SummarySheet is the name of the sheet that holds the Summary, the unmapped block and the tie-out.
const SummarySheet = "Summary"

// summaryNote is printed on the sheet. Source signs are kept, so the reader is told how to read them.
const summaryNote = "Source signs are kept: expenses and refunds are negative, as the reports give them. " +
	"Payments are Transaction lines only and Settlements are Components only. " +
	"The last column is Payments minus Settlements."

// writeSummary writes the Summary sheet. Subtotal and difference cells are formulas whose
// stored values are the same numbers the formulas produce.
func writeSummary(f *excelize.File, r recon.Report, bold, money int) error {
	w := &sheetWriter{f: f, sheet: SummarySheet}
	w.text(1, "B", "Amazon Payments vs Settlement: Summary", bold)
	w.text(3, "B", "Settlement ID", 0)
	w.text(3, "C", r.Settlement.ID, 0)
	w.text(4, "B", "Settlement period (UTC)", 0)
	w.text(4, "C", r.Settlement.PeriodStart.Format("2006-01-02")+" to "+r.Settlement.PeriodEnd.Format("2006-01-02"), 0)
	w.text(5, "B", "Run ID", 0)
	w.text(5, "C", fmt.Sprint(r.RunID), 0)
	for i, field := range runFields(r) {
		w.text(6+i, "B", field.label, 0)
		w.text(6+i, "C", field.value, 0)
	}
	w.text(10, "B", summaryNote, 0)

	const header = 11
	w.text(header, "B", "Summary", bold)
	w.text(header, "C", "Payments", bold)
	w.text(header, "D", "Settlements", bold)
	w.text(header, "E", "Payments minus Settlements", bold)

	row := header + 1
	for _, g := range groupLines(r.Summary.Lines) {
		if len(g.lines) == 1 && g.lines[0].Line == g.name {
			// A group with one line of the same name (Paid To Amazon) is that line.
			writeLineRow(w, row, g.name, g.lines[0].Payments, g.lines[0].Settlements, money, bold)
			row += 2
			continue
		}
		groupRow := row
		first := row + 1
		payments, settlements := decimal.Zero, decimal.Zero
		for i, l := range g.lines {
			writeLineRow(w, first+i, "  "+l.Line, l.Payments, l.Settlements, money, 0)
			payments = payments.Add(l.Payments)
			settlements = settlements.Add(l.Settlements)
		}
		last := first + len(g.lines) - 1
		w.text(groupRow, "B", g.name, bold)
		w.formula(groupRow, "C", fmt.Sprintf("SUM(C%d:C%d)", first, last), payments, money)
		w.formula(groupRow, "D", fmt.Sprintf("SUM(D%d:D%d)", first, last), settlements, money)
		w.formula(groupRow, "E", fmt.Sprintf("C%d-D%d", groupRow, groupRow), payments.Sub(settlements), money)
		row = last + 2
	}

	row = writeUnmapped(w, row, r.Summary.Unmapped, money, bold)
	row = writeTieOut(w, row+1, r, money, bold)
	writeStatusCounts(w, row+1, r.Reconciliation.Counts, bold)
	return w.err
}

// lineGroup is one Summary group and its lines, in display order.
type lineGroup struct {
	name  string
	lines []recon.SummaryLine
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

// writeTieOut writes the tie-out, one column per source. Each source's in-scope total is its
// Summary net plus its routing gaps. The remaining difference is that total minus the Settlement total.
func writeTieOut(w *sheetWriter, row int, r recon.Report, money, bold int) int {
	p, s := r.TieOut.Payments, r.TieOut.Settlements
	w.text(row, "B", "Tie-out to the Settlement total", bold)
	w.text(row, "C", "Payments", bold)
	w.text(row, "D", "Settlements", bold)
	row++

	netRow := row
	w.text(row, "B", "Summary net (all Summary fields)", 0)
	w.number(row, "C", p.SummaryNet, money)
	w.number(row, "D", s.SummaryNet, money)
	row++

	gapRules := map[string]bool{}
	for _, g := range p.Gaps {
		gapRules[g.Rule] = true
	}
	for _, g := range s.Gaps {
		gapRules[g.Rule] = true
	}
	rules := make([]string, 0, len(gapRules))
	for rule := range gapRules {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	firstGap := row
	for _, rule := range rules {
		w.text(row, "B", "Routing gap, rule group "+rule, 0)
		w.number(row, "C", gapAmount(p.Gaps, rule), money)
		w.number(row, "D", gapAmount(s.Gaps, rule), money)
		row++
	}
	lastGap := row - 1
	gapsRow := row
	w.text(row, "B", "Routing gaps total", bold)
	if len(rules) == 0 {
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

	// The residual is explained by the in-scope lines of records that are Unreconciled on each side.
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

	w.text(row, "B", fmt.Sprintf("Deferred memo, out of scope (%d lines)", r.Deferred.Count), 0)
	w.number(row, "C", r.Deferred.Amount, money)
	w.text(row+1, "B", "Deferred lines have no Transaction Release Date. They are not in the tie-out; the memo shows the gap to the Settlement report.", 0)
	return row + 3
}

// gapAmount returns a rule group's routing gap, or zero when the side has no such group.
func gapAmount(gaps []recon.RoutingGap, rule string) decimal.Decimal {
	for _, g := range gaps {
		if g.Rule == rule {
			return g.Amount
		}
	}
	return decimal.Zero
}
