package mapping

import "github.com/shopspring/decimal"

// Tier is the precedence tier a line was mapped under. Only the highest tier
// with any matching rule is used for a line.
type Tier int

const (
	// TierUnmapped means no rule matched the line.
	TierUnmapped Tier = iota
	// TierTypeDescription is (transaction type, exact description).
	TierTypeDescription
	// TierTypeWildcard is (transaction type, any).
	TierTypeWildcard
	// TierCatchAllDescription is (empty type, exact description).
	TierCatchAllDescription
	// TierCatchAllWildcard is (empty type, any).
	TierCatchAllWildcard
)

func (t Tier) String() string {
	switch t {
	case TierTypeDescription:
		return "type+description"
	case TierTypeWildcard:
		return "type+any"
	case TierCatchAllDescription:
		return "catch-all+description"
	case TierCatchAllWildcard:
		return "catch-all+any"
	default:
		return "unmapped"
	}
}

// Contribution is one amount routed to one Summary field by one rule.
type Contribution struct {
	RuleID       string
	AmountField  string
	Amount       decimal.Decimal
	SummaryField string
}

// Result is the mapping of one line.
type Result struct {
	// RecordRef is empty when the line is Unmapped.
	RecordRef string
	Tier      Tier
	// MatchedRuleIDs lists every rule in the winning tier, including those
	// that routed nothing because of a zero amount or an empty Summary field.
	MatchedRuleIDs []string
	Contributions  []Contribution
	Warnings       []Warning
}
