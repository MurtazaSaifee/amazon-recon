package mapping

import "testing"

func TestMapComponentTiersAndRouting(t *testing.T) {
	keys := Keys{TxnRef: "T-200", SKU: "SKU-9", Date: "2026-07-18", SettlementID: "S-7"}

	ruleTaxSigned := SettlementRule{ID: "S-010", TransactionType: "REFUND", AmountType: "ITEMPRICE", AmountDescription: "TAX", RecordRef: "txn_ref+sku+date", PositiveSummary: "total_refund_expense_or_sales_amt", NegativeSummary: "refunded_expenses"}
	ruleItemPriceWild := SettlementRule{ID: "S-011", TransactionType: "REFUND", AmountType: "ITEMPRICE", AmountDescription: "any", RecordRef: "txn_ref+sku+date", PositiveSummary: "sales_other", NegativeSummary: "sales_other"}
	ruleCommission := SettlementRule{ID: "S-012", TransactionType: "REFUND", AmountType: "ITEMFEES", AmountDescription: "COMMISSION", RecordRef: "txn_ref+sku+date", PositiveSummary: "refunded_expenses", NegativeSummary: "refunded_expenses"}
	ruleCommissionDup := SettlementRule{ID: "S-013", TransactionType: "REFUND", AmountType: "ITEMFEES", AmountDescription: "COMMISSION", RecordRef: "txn_ref+sku+date", PositiveSummary: "refunded_expenses", NegativeSummary: "refunded_expenses"}
	ruleCatchAll := SettlementRule{ID: "S-020", TransactionType: "", AmountType: "ITEMPRICE", AmountDescription: "any", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "sales_other", NegativeSummary: "sales_other"}
	ruleBadTemplate := SettlementRule{ID: "S-138", TransactionType: "OTHER-TRANSACTION", AmountType: "PROMOTION_ADJUSTMENT", AmountDescription: "any", RecordRef: "ADJUSTMENT_OTHER+settlement_id+date+record_type+settlement_id", PositiveSummary: "total_adjustment_other_buyer_recharge_amt", NegativeSummary: "total_adjustment_other_buyer_recharge_amt"}

	all := []SettlementRule{ruleTaxSigned, ruleItemPriceWild, ruleCommission, ruleCommissionDup, ruleCatchAll, ruleBadTemplate}

	tests := []struct {
		name  string
		rules []SettlementRule
		c     Component
		want  wantResult
	}{
		{
			name:  "positive amount with exact description routes to the positive Summary field",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ItemPrice", AmountDescription: "tax", Amount: d("4.00")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeDescription,
				Matched:   []string{"S-010"},
				Contributions: []wantContribution{
					{RuleID: "S-010", AmountField: "amount", Amount: "4", SummaryField: "total_refund_expense_or_sales_amt"},
				},
			},
		},
		{
			name:  "negative amount with exact description routes to the negative Summary field",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ITEMPRICE", AmountDescription: "TAX", Amount: d("-4.00")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeDescription,
				Matched:   []string{"S-010"},
				Contributions: []wantContribution{
					{RuleID: "S-010", AmountField: "amount", Amount: "-4", SummaryField: "refunded_expenses"},
				},
			},
		},
		{
			name:  "wildcard amount description is used when no exact match",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ITEMPRICE", AmountDescription: "SHIPPING", Amount: d("1.50")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeWildcard,
				Matched:   []string{"S-011"},
				Contributions: []wantContribution{
					{RuleID: "S-011", AmountField: "amount", Amount: "1.5", SummaryField: "sales_other"},
				},
			},
		},
		{
			name:  "amount type must match: same description under another amount type is Unmapped",
			rules: []SettlementRule{ruleTaxSigned},
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ITEMFEES", AmountDescription: "TAX", Amount: d("1.00")},
			want: wantResult{
				Tier:     TierUnmapped,
				Warnings: []WarningKind{WarnUnmapped},
			},
		},
		{
			name:  "two rules on the same component both apply and raise a Multi-rule hit",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ITEMFEES", AmountDescription: "COMMISSION", Amount: d("-1.00")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeDescription,
				Matched:   []string{"S-012", "S-013"},
				Contributions: []wantContribution{
					{RuleID: "S-012", AmountField: "amount", Amount: "-1", SummaryField: "refunded_expenses"},
					{RuleID: "S-013", AmountField: "amount", Amount: "-1", SummaryField: "refunded_expenses"},
				},
				Warnings: []WarningKind{WarnMultiRuleHit},
			},
		},
		{
			name:  "catch-all applies to an unrecognised transaction type with the same amount type",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "NEW-TYPE", AmountType: "ITEMPRICE", AmountDescription: "GOODWILL", Amount: d("2.00")},
			want: wantResult{
				RecordRef: "T-200|S-7|2026-07-18",
				Tier:      TierCatchAllWildcard,
				Matched:   []string{"S-020"},
				Contributions: []wantContribution{
					{RuleID: "S-020", AmountField: "amount", Amount: "2", SummaryField: "sales_other"},
				},
			},
		},
		{
			name:  "no catch-all and no type match is Unmapped",
			rules: []SettlementRule{ruleTaxSigned},
			c:     Component{Keys: keys, TransactionType: "PROMOTION", AmountType: "ITEMPRICE", AmountDescription: "TAX", Amount: d("1.00")},
			want: wantResult{
				Tier:     TierUnmapped,
				Warnings: []WarningKind{WarnUnmapped},
			},
		},
		{
			name:  "zero amount produces no Contribution but the rule still counts as matched",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "REFUND", AmountType: "ITEMPRICE", AmountDescription: "TAX", Amount: d("0")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeDescription,
				Matched:   []string{"S-010"},
			},
		},
		{
			name:  "unknown field-looking token in a settlement template warns and stays literal",
			rules: all,
			c:     Component{Keys: keys, TransactionType: "OTHER-TRANSACTION", AmountType: "PROMOTION_ADJUSTMENT", AmountDescription: "PROMO", Amount: d("3.00")},
			want: wantResult{
				RecordRef: "ADJUSTMENT_OTHER|S-7|2026-07-18|record_type|S-7",
				Tier:      TierTypeWildcard,
				Matched:   []string{"S-138"},
				Contributions: []wantContribution{
					{RuleID: "S-138", AmountField: "amount", Amount: "3", SummaryField: "total_adjustment_other_buyer_recharge_amt"},
				},
				Warnings: []WarningKind{WarnUnknownToken},
			},
		},
		{
			name:  "source type and amount type are normalised before matching",
			rules: all,
			c:     Component{Keys: keys, TransactionType: " refund ", AmountType: "ItemPrice", AmountDescription: "Tax", Amount: d("4.00")},
			want: wantResult{
				RecordRef: "T-200|SKU-9|2026-07-18",
				Tier:      TierTypeDescription,
				Matched:   []string{"S-010"},
				Contributions: []wantContribution{
					{RuleID: "S-010", AmountField: "amount", Amount: "4", SummaryField: "total_refund_expense_or_sales_amt"},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMapper(nil, tc.rules, SwitchSet{})
			got := m.MapComponent(tc.c)
			assertResult(t, got, tc.want)
		})
	}
}
