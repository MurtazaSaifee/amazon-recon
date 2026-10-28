package mapping

import "fmt"

// Keys are the common key fields shared by a Transaction line and a
// Component. A record_ref template token that names one of these fields
// resolves to that field on whichever side is being mapped.
type Keys struct {
	TxnRef          string // payments order ID / settlement order-id
	SKU             string
	Date            string // posted date, YYYY-MM-DD (UTC)
	SettlementID    string
	Description     string // payments description; for a Component, its amount description
	ShipmentID      string
	MerchantOrderID string
}

// WarningKind classifies a warning raised while mapping a line.
type WarningKind string

const (
	// WarnMultiRuleHit means two or more rules in the winning tier apply to
	// the same Amount field. Every such rule still applies.
	WarnMultiRuleHit WarningKind = "multi_rule_hit"
	// WarnUnknownToken means a record_ref token looks like a field name but
	// is not one. It is used as literal text.
	WarnUnknownToken WarningKind = "unknown_template_token"
	// WarnUnmapped means no rule matched the line.
	WarnUnmapped WarningKind = "unmapped"
)

// Warning is one non-fatal finding about a mapped line.
type Warning struct {
	Kind   WarningKind
	Detail string
}

func (w Warning) String() string {
	return fmt.Sprintf("%s: %s", w.Kind, w.Detail)
}
