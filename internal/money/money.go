// Package money formats amounts for people to read. It is for printing only: stored and computed
// amounts stay exact decimals, and the workbook writes their decimal text.
package money

import (
	"strings"

	"github.com/shopspring/decimal"
)

// Format renders an amount with thousands separators and two decimals, for example -133,756.51.
func Format(d decimal.Decimal) string {
	s := d.StringFixed(2)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}
