package mapping

import "testing"

func TestAmountFieldForColumn(t *testing.T) {
	tests := []struct {
		column string
		want   string
	}{
		{"fulfilment by amazon fees", "fba_fees"},
		{"product sales", "product_sales"},
		{"shipping credits", "shipping_credits"},
		{"sales tax collected", "sales_tax_collected"},
		{"other", "other"},
		{"total", "total"},
		{"low value goods", "low_value_goods"},
	}
	for _, tc := range tests {
		t.Run(tc.column, func(t *testing.T) {
			if got := AmountFieldForColumn(tc.column); got != tc.want {
				t.Fatalf("AmountFieldForColumn(%q) = %q, want %q", tc.column, got, tc.want)
			}
		})
	}
}
