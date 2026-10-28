package mapping

import "github.com/shopspring/decimal"

// TransactionLine is one ingested Payments line.
type TransactionLine struct {
	Keys
	TransactionType string
	// Amounts holds the typed amount columns keyed by Amount field name
	// (e.g. "product_sales", "fba_fees"). The key "total" is not read from
	// here; the Amount field "total" always means Total below.
	Amounts map[string]decimal.Decimal
	// Total is the line amount that reconciliation compares against.
	Total decimal.Decimal
}

// Component is one ingested Settlement component. Its single amount is the
// Amount field every settlement rule applies to.
type Component struct {
	Keys
	TransactionType   string
	AmountType        string
	AmountDescription string
	Amount            decimal.Decimal
}
