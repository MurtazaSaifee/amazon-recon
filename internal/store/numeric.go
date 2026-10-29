package store

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Amount is a NUMERIC money value that scans straight into an exact decimal. pgx hands a numeric to a
// scanner as text, so the query needs no ::text cast and no float64 appears on the way. A NULL scans as zero.
type Amount struct {
	decimal.Decimal
}

// Scan implements sql.Scanner.
func (a *Amount) Scan(src any) error {
	var text string
	switch v := src.(type) {
	case nil:
		a.Decimal = decimal.Zero
		return nil
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("amount: cannot scan a %T", src)
	}
	d, err := decimal.NewFromString(text)
	if err != nil {
		return fmt.Errorf("amount %q: %w", text, err)
	}
	a.Decimal = d
	return nil
}
