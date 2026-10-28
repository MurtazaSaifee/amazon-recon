package report

import (
	"fmt"
	"strings"

	"amazonrecon/internal/recon"
)

// headerField is one labelled line of the run header that every sheet shows.
type headerField struct {
	label string
	value string
}

// runFields is the part of the header that says which config, mode, interpretation switches and
// malformed-line count produced the numbers.
func runFields(r recon.Report) []headerField {
	return []headerField{
		{"Config hash (first 12 characters)", shortHash(r.ConfigHash)},
		{"Mode", r.Mode},
		{"Interpretation switches", switchList(r.Switches)},
		{"Malformed lines (both reports)", fmt.Sprint(r.MalformedLines)},
	}
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
