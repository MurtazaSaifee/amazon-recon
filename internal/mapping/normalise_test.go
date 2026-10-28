package mapping

import "testing"

func TestNormalise(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"upper-cases", "Service fee", "SERVICE_FEE"},
		{"spaces become underscore", "FBA Inventory Reimbursement - Damaged:Warehouse", "FBA_INVENTORY_REIMBURSEMENT_-_DAMAGED:WAREHOUSE"},
		{"hyphen-separated type is not folded", "other-transaction", "OTHER-TRANSACTION"},
		{"colon and digits are kept", "To account ending with: 999", "TO_ACCOUNT_ENDING_WITH:_999"},
		{"surrounding whitespace is trimmed", "  refund  ", "REFUND"},
		{"whitespace run becomes one underscore", "a \t  b", "A_B"},
		{"empty stays empty", "", ""},
		{"wildcard any is not special at this layer", "any", "ANY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalise(tc.in); got != tc.want {
				t.Fatalf("Normalise(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
