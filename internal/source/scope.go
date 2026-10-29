package source

// Payments transaction statuses, as the report writes them. Scope turns on them.
const (
	StatusReleased = "Released"
	StatusDeferred = "Deferred"
)

// Scope reasons: why a line is in or out of scope. They are stored in source_rows.scope_reason and
// shown on the Out of Scope sheet, so every place that decides or reads a scope uses these.
const (
	ReasonSettlementHeader    = "Settlement header (reference total)"
	ReasonOtherSettlement     = "Other settlement"
	ReasonSettlementComponent = "Settlement component"
	ReasonReleased            = StatusReleased
	ReasonDeferred            = StatusDeferred
	ReasonUnknownSettlementID = "scope unknown: settlement ID unreadable"
	ReasonUnknownStatus       = "scope unknown: status unreadable"
	// ReasonUnmapped labels a line that no mapping rule matched. It is a reason on the Unmapped list, not a scope.
	ReasonUnmapped     = "Unmapped"
	reasonStatusPrefix = "Status "
)

// ScopeCells are the cells a scope decision reads. A malformed line may lack some of them.
type ScopeCells struct {
	Source       string // Payments or Settlement
	Kind         string // a line kind; empty for a malformed line, which is read as a component
	SettlementID string
	Status       string // payments only
}

// Scope decides whether a line is in scope, and why. The Settlement is the one named by the Settlement
// report's header. unknown reports a cell the decision needed but could not read; such a line is
// treated as in scope, so the run fails rather than dropping it.
func Scope(headerID string, c ScopeCells) (inScope bool, reason string, unknown bool) {
	switch {
	case c.Kind == KindSettlementHeader:
		return false, ReasonSettlementHeader, false
	case c.SettlementID == "":
		return true, ReasonUnknownSettlementID, true
	case c.SettlementID != headerID:
		return false, ReasonOtherSettlement, false
	case c.Source == Settlement:
		return true, ReasonSettlementComponent, false
	case c.Status == "":
		return true, ReasonUnknownStatus, true
	case c.Status == StatusReleased:
		return true, ReasonReleased, false
	case c.Status == StatusDeferred:
		return false, ReasonDeferred, false
	default:
		return false, reasonStatusPrefix + c.Status, false
	}
}
