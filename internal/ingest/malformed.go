package ingest

import (
	"amazonrecon/internal/source"
	"amazonrecon/internal/store"
)

// malformedLines turns the parse errors of one report into stored malformed lines. Each line's
// scope is decided from the cells that could still be read. When the scope cannot be read, the
// line is treated as in scope, so it fails the run, and the reason says so.
func malformedLines(fileName string, errs []source.ParseError, headerID string) []store.MalformedLine {
	out := make([]store.MalformedLine, 0, len(errs))
	for _, e := range errs {
		inScope, scope, unknown := scopeOfMalformed(e, headerID)
		reason := e.Reason
		if unknown {
			reason += " (scope cannot be determined, treated as in scope)"
		}
		out = append(out, store.MalformedLine{
			FileName:    fileName,
			Source:      e.Source,
			LineNumber:  e.LineNumber,
			RawText:     e.Raw,
			Reason:      reason,
			InScope:     inScope,
			ScopeReason: scope,
		})
	}
	return out
}

// scopeOfMalformed applies the same scope rules as scopedRow, to the cells a malformed line still has.
// It returns the scope reason, and unknown when a cell needed for the decision is unreadable.
func scopeOfMalformed(e source.ParseError, headerID string) (inScope bool, reason string, unknown bool) {
	switch {
	case e.SettlementID == "":
		return true, "scope unknown: settlement ID unreadable", true
	case e.SettlementID != headerID:
		return false, "Other settlement", false
	case e.Source == source.Settlement:
		return true, "Settlement component", false
	case e.Status == "":
		return true, "scope unknown: status unreadable", true
	case e.Status == "Released":
		return true, "Released", false
	case e.Status == "Deferred":
		return false, "Deferred", false
	default:
		return false, "Status " + e.Status, false
	}
}

// blockingCount counts the malformed lines in scope, which are the ones that fail a run.
func blockingCount(lines []store.MalformedLine) int {
	n := 0
	for _, l := range lines {
		if l.InScope {
			n++
		}
	}
	return n
}
