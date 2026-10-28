package report

import "amazonrecon/internal/recon"

// writeStatusCounts writes the count of record_refs per reconciliation status, and the count of
// unmapped lines. It returns the next free row.
func writeStatusCounts(w *sheetWriter, row int, r recon.Reconciliation, bold int) int {
	w.text(row, "B", "Reconciliation by record_ref", bold)
	w.text(row, "C", "Count", bold)
	row++
	for _, s := range recon.Statuses {
		w.text(row, "B", s.Label(), 0)
		w.count(row, "C", r.Counts[s])
		row++
	}
	w.text(row, "B", "Unmapped (lines)", 0)
	w.count(row, "C", r.UnmappedTotal())
	row++
	w.text(row, "B", "Unmapped lines have no record_ref. They are listed on Consolidated Data, one row each.", 0)
	return row + 2
}
