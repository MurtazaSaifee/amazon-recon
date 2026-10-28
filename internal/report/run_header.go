package report

import (
	"fmt"
	"strings"

	"amazonrecon/internal/recon"
	"amazonrecon/internal/timefmt"
)

// headerField is one labelled line of the run header.
type headerField struct {
	label string
	value string
}

// writeRunHeader writes the run header: the Settlement and period, the run, the config, mode and
// interpretation switches it was produced with, the malformed-line count, and the two report files.
// It is the only place these fields are written. It starts at row and returns the next free row.
func writeRunHeader(w *sheetWriter, r recon.Report, row int) int {
	fields := []headerField{
		{"Settlement ID", r.Settlement.ID},
		{"Settlement period (UTC)", timefmt.Date(r.Settlement.PeriodStart) + " to " + timefmt.Date(r.Settlement.PeriodEnd)},
		{"Deposit date (UTC)", timefmt.Timestamp(r.Settlement.DepositDate)},
		{"Run ID", fmt.Sprint(r.RunID)},
		{"Run started / finished", timefmt.Timestamp(r.StartedAt) + " / " + timefmt.Timestamp(r.FinishedAt)},
		{"Config hash (first 12 characters)", shortHash(r.ConfigHash)},
		{"Mode", r.Mode},
		{"Interpretation switches", switchList(r.Switches)},
		{"Malformed lines (both reports)", fmt.Sprint(r.MalformedLines)},
	}
	for _, field := range fields {
		w.text(row, "B", field.label, 0)
		w.text(row, "C", field.value, 0)
		row++
	}
	for _, file := range r.Files {
		w.text(row, "B", file.Source+" file", 0)
		w.text(row, "C", fmt.Sprintf("%s; sha256 %s; %d physical lines, %d stored",
			file.FileName, file.SHA256, file.PhysicalLines, file.StoredLines), 0)
		row++
	}
	return row
}

// switchList names the enabled switches, or says none when the run had none on.
func switchList(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// shortHash is the first 12 characters of a config hash, enough to tell two configs apart on a sheet.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
