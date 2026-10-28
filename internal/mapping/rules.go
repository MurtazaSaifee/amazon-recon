package mapping

// Wildcard is the description value that matches any description.
const Wildcard = "any"

// PaymentRule is one row of the payments mapping config, read verbatim.
// ID is the CSV line number as a rule ID, e.g. "P-024".
type PaymentRule struct {
	ID              string
	TransactionType string // empty means the Catch-all
	Description     string // "any" is the Wildcard
	AmountField     string // e.g. "total", "fba_fees", "product_sales"
	RecordRef       string // `+`-separated template
	PositiveSummary string // empty means not summarised
	NegativeSummary string // empty means not summarised
}

// SettlementRule is one row of the settlement mapping config, read verbatim.
// ID is the CSV line number as a rule ID, e.g. "S-071".
type SettlementRule struct {
	ID                string
	TransactionType   string // empty means the Catch-all
	AmountType        string
	AmountDescription string // "any" is the Wildcard
	RecordRef         string
	PositiveSummary   string
	NegativeSummary   string
}
