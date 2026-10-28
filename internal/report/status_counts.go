package report

import "amazonrecon/internal/recon"

// writeStatusCounts writes the count of record_refs per reconciliation status, and the count of
// unmapped lines. It returns the next free row.
func writeStatusCounts(w *sheetWriter, row int, c recon.Counts, bold int) int {
	w.text(row, "B", "Reconciliation by record_ref", bold)
	w.text(row, "C", "Count", bold)
	row++
	for _, s := range []struct {
		label string
		n     int
	}{
		{"Reconciled", c.Reconciled},
		{"Reconciled with difference", c.ReconciledWithDifference},
		{"Unreconciled payment", c.UnreconciledPayment},
		{"Unreconciled settlement", c.UnreconciledSettlement},
		{"Unmapped (lines)", c.UnmappedPayments + c.UnmappedSettlements},
	} {
		w.text(row, "B", s.label, 0)
		w.count(row, "C", s.n)
		row++
	}
	w.text(row, "B", "Unmapped lines have no record_ref. They are listed on Consolidated Data, one row each.", 0)
	return row + 2
}

// count writes a whole number cell.
func (w *sheetWriter) count(row int, col string, n int) {
	if w.err != nil {
		return
	}
	w.err = w.f.SetCellInt(w.sheet, w.cell(row, col), int64(n))
}
