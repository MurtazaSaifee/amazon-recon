package mapping

import (
	"testing"

	"github.com/shopspring/decimal"
)

// wantContribution is an expected Contribution. Amount is compared as its
// canonical decimal string, so the test never touches float64.
type wantContribution struct {
	RuleID       string
	AmountField  string
	Amount       string
	SummaryField string
}

type wantResult struct {
	RecordRef     string
	Tier          Tier
	Matched       []string
	Contributions []wantContribution
	Warnings      []WarningKind
}

func assertResult(t *testing.T, got Result, want wantResult) {
	t.Helper()
	if got.RecordRef != want.RecordRef {
		t.Errorf("RecordRef = %q, want %q", got.RecordRef, want.RecordRef)
	}
	if got.Tier != want.Tier {
		t.Errorf("Tier = %v, want %v", got.Tier, want.Tier)
	}
	if !equalStrings(got.MatchedRuleIDs, want.Matched) {
		t.Errorf("MatchedRuleIDs = %v, want %v", got.MatchedRuleIDs, want.Matched)
	}
	if len(got.Contributions) != len(want.Contributions) {
		t.Fatalf("Contributions = %+v, want %+v", got.Contributions, want.Contributions)
	}
	for i, c := range got.Contributions {
		w := want.Contributions[i]
		if c.RuleID != w.RuleID || c.AmountField != w.AmountField || c.Amount.String() != w.Amount || c.SummaryField != w.SummaryField {
			t.Errorf("Contributions[%d] = {%s %s %s %s}, want {%s %s %s %s}",
				i, c.RuleID, c.AmountField, c.Amount, c.SummaryField,
				w.RuleID, w.AmountField, w.Amount, w.SummaryField)
		}
	}
	gotKinds := make([]WarningKind, 0, len(got.Warnings))
	for _, w := range got.Warnings {
		gotKinds = append(gotKinds, w.Kind)
	}
	if !equalKinds(gotKinds, want.Warnings) {
		t.Errorf("Warning kinds = %v, want %v (warnings: %v)", gotKinds, want.Warnings, got.Warnings)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalKinds(a, b []WarningKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestMapTransactionTiersAndRouting(t *testing.T) {
	const (
		txnRef = "T-100"
		sku    = "SKU-1"
		date   = "2026-07-17"
		settle = "S-1"
	)
	orderKeys := Keys{TxnRef: txnRef, SKU: sku, Date: date, SettlementID: settle}

	// Rules shared by the table below. Each case picks the subset it needs.
	ruleExactTransfer := PaymentRule{ID: "P-001", TransactionType: "TRANSFER", Description: "TO_ACCOUNT_ENDING", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date", PositiveSummary: "", NegativeSummary: ""}
	ruleWildTransfer := PaymentRule{ID: "P-002", TransactionType: "TRANSFER", Description: "any", AmountField: "total", RecordRef: "TRANSFER_WILD+settlement_id+date", PositiveSummary: "sales_other", NegativeSummary: "sales_other"}
	ruleCatchTotal := PaymentRule{ID: "P-110", TransactionType: "", Description: "any", AmountField: "total", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "expenses_amazon_fees", NegativeSummary: "expenses_amazon_fees"}
	ruleCatchOther := PaymentRule{ID: "P-111", TransactionType: "", Description: "any", AmountField: "other", RecordRef: "txn_ref+settlement_id+date"}
	ruleCatchSpecial := PaymentRule{ID: "P-120", TransactionType: "", Description: "SPECIAL", AmountField: "total", RecordRef: "SPECIAL+txn_ref", PositiveSummary: "sales_other", NegativeSummary: "sales_other"}
	ruleSellingFees := PaymentRule{ID: "P-021", TransactionType: "ORDER", Description: "any", AmountField: "selling_fees", RecordRef: "txn_ref+sku+date", PositiveSummary: "sales_amazon_fees", NegativeSummary: "expenses_amazon_fees"}
	ruleLowValueShipping := PaymentRule{ID: "P-005", TransactionType: "ORDER", Description: "any", AmountField: "low_value_goods", RecordRef: "txn_ref+sku+date", PositiveSummary: "sales_shipping", NegativeSummary: "sales_shipping"}
	ruleLowValueProduct := PaymentRule{ID: "P-006", TransactionType: "ORDER", Description: "any", AmountField: "low_value_goods", RecordRef: "txn_ref+sku+date", PositiveSummary: "sales_product_charges", NegativeSummary: "sales_product_charges"}
	ruleSalesNoTarget := PaymentRule{ID: "P-030", TransactionType: "ORDER", Description: "any", AmountField: "gift_wrap_credits", RecordRef: "txn_ref+sku+date", PositiveSummary: "", NegativeSummary: "expenses_other"}
	ruleFBA := PaymentRule{ID: "P-040", TransactionType: "ORDER", Description: "any", AmountField: "fba_fees", RecordRef: "txn_ref+sku+date", PositiveSummary: "expenses_fba_fees", NegativeSummary: "expenses_fba_fees"}
	ruleTotalOrder := PaymentRule{ID: "P-041", TransactionType: "ORDER", Description: "any", AmountField: "total", RecordRef: "txn_ref+sku+date"}

	tests := []struct {
		name  string
		rules []PaymentRule
		line  TransactionLine
		want  wantResult
	}{
		{
			name:  "exact description beats wildcard in the same type",
			rules: []PaymentRule{ruleWildTransfer, ruleExactTransfer},
			line:  TransactionLine{Keys: Keys{TxnRef: txnRef, SettlementID: settle, Date: date, Description: "To account ending"}, TransactionType: "TRANSFER", Total: d("-5.00")},
			want: wantResult{
				RecordRef: "TRANSFER|To account ending|S-1|2026-07-17",
				Tier:      TierTypeDescription,
				Matched:   []string{"P-001"},
				// P-001 has blank Summary fields: it matched but routes nowhere.
			},
		},
		{
			name:  "source description is normalised before matching",
			rules: []PaymentRule{ruleWildTransfer, ruleExactTransfer},
			line:  TransactionLine{Keys: Keys{TxnRef: txnRef, SettlementID: settle, Date: date, Description: "  to  account ending "}, TransactionType: "Transfer", Total: d("-5.00")},
			want: wantResult{
				// The record_ref uses the raw source column, not the normalised form.
				RecordRef: "TRANSFER|  to  account ending |S-1|2026-07-17",
				Tier:      TierTypeDescription,
				Matched:   []string{"P-001"},
			},
		},
		{
			name:  "wildcard is used when no exact description matches",
			rules: []PaymentRule{ruleWildTransfer, ruleExactTransfer},
			line:  TransactionLine{Keys: Keys{TxnRef: txnRef, SettlementID: settle, Date: date, Description: "something else"}, TransactionType: "TRANSFER", Total: d("-5.00")},
			want: wantResult{
				RecordRef: "TRANSFER_WILD|S-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-002"},
				Contributions: []wantContribution{
					{RuleID: "P-002", AmountField: "total", Amount: "-5", SummaryField: "sales_other"},
				},
			},
		},
		{
			name:  "unknown type falls to the catch-all wildcard",
			rules: []PaymentRule{ruleWildTransfer, ruleCatchTotal, ruleCatchOther},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "NEW_THING", Total: d("12.34")},
			want: wantResult{
				RecordRef: "T-100|S-1|2026-07-17",
				Tier:      TierCatchAllWildcard,
				Matched:   []string{"P-110", "P-111"},
				Contributions: []wantContribution{
					{RuleID: "P-110", AmountField: "total", Amount: "12.34", SummaryField: "expenses_amazon_fees"},
				},
			},
		},
		{
			name:  "catch-all with exact description beats catch-all wildcard",
			rules: []PaymentRule{ruleCatchTotal, ruleCatchSpecial},
			line:  TransactionLine{Keys: Keys{TxnRef: txnRef, SettlementID: settle, Date: date, Description: "special"}, TransactionType: "NEW_THING", Total: d("1.00")},
			want: wantResult{
				RecordRef: "SPECIAL|T-100",
				Tier:      TierCatchAllDescription,
				Matched:   []string{"P-120"},
				Contributions: []wantContribution{
					{RuleID: "P-120", AmountField: "total", Amount: "1", SummaryField: "sales_other"},
				},
			},
		},
		{
			name:  "type-level match wins over catch-all even when the catch-all is listed first",
			rules: []PaymentRule{ruleCatchTotal, ruleSellingFees},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"selling_fees": d("2.00")}, Total: d("2.00")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-021"},
				Contributions: []wantContribution{
					{RuleID: "P-021", AmountField: "selling_fees", Amount: "2", SummaryField: "sales_amazon_fees"},
				},
			},
		},
		{
			name:  "positive and negative amounts route to their own Summary field",
			rules: []PaymentRule{ruleSellingFees},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"selling_fees": d("-7.25")}, Total: d("-7.25")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-021"},
				Contributions: []wantContribution{
					{RuleID: "P-021", AmountField: "selling_fees", Amount: "-7.25", SummaryField: "expenses_amazon_fees"},
				},
			},
		},
		{
			name:  "zero amount produces no Contribution but the rule still counts as matched",
			rules: []PaymentRule{ruleSellingFees},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"selling_fees": d("0.00")}, Total: d("0")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-021"},
			},
		},
		{
			name:  "empty Summary field produces no Contribution but the rule still counts as matched",
			rules: []PaymentRule{ruleSalesNoTarget},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"gift_wrap_credits": d("3.00")}, Total: d("3.00")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-030"},
			},
		},
		{
			name:  "every rule in the winning tier applies and a Multi-rule hit is raised",
			rules: []PaymentRule{ruleLowValueShipping, ruleLowValueProduct},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"low_value_goods": d("3.50")}, Total: d("3.50")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-005", "P-006"},
				Contributions: []wantContribution{
					{RuleID: "P-005", AmountField: "low_value_goods", Amount: "3.5", SummaryField: "sales_shipping"},
					{RuleID: "P-006", AmountField: "low_value_goods", Amount: "3.5", SummaryField: "sales_product_charges"},
				},
				Warnings: []WarningKind{WarnMultiRuleHit},
			},
		},
		{
			name:  "Multi-rule hit is raised even when the amount is zero",
			rules: []PaymentRule{ruleLowValueShipping, ruleLowValueProduct},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"low_value_goods": d("0")}, Total: d("0")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-005", "P-006"},
				Warnings:  []WarningKind{WarnMultiRuleHit},
			},
		},
		{
			name:  "rules on different Amount fields do not raise a Multi-rule hit",
			rules: []PaymentRule{ruleSellingFees, ruleFBA},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"selling_fees": d("1.00"), "fba_fees": d("-2.00")}, Total: d("-1.00")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-021", "P-040"},
				Contributions: []wantContribution{
					{RuleID: "P-021", AmountField: "selling_fees", Amount: "1", SummaryField: "sales_amazon_fees"},
					{RuleID: "P-040", AmountField: "fba_fees", Amount: "-2", SummaryField: "expenses_fba_fees"},
				},
			},
		},
		{
			name:  "rule with amount_field total reads the line total",
			rules: []PaymentRule{ruleTotalOrder},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Total: d("9.99")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-041"},
			},
		},
		{
			name:  "amount field name is matched after normalisation",
			rules: []PaymentRule{ruleFBA},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "ORDER", Amounts: map[string]decimal.Decimal{"FBA_FEES": d("4.00")}, Total: d("4.00")},
			want: wantResult{
				RecordRef: "T-100|SKU-1|2026-07-17",
				Tier:      TierTypeWildcard,
				Matched:   []string{"P-040"},
				Contributions: []wantContribution{
					{RuleID: "P-040", AmountField: "fba_fees", Amount: "4", SummaryField: "expenses_fba_fees"},
				},
			},
		},
		{
			name:  "line with no matching type or catch-all is Unmapped",
			rules: []PaymentRule{ruleSellingFees},
			line:  TransactionLine{Keys: orderKeys, TransactionType: "NEW_THING", Total: d("1.00")},
			want: wantResult{
				RecordRef: "",
				Tier:      TierUnmapped,
				Warnings:  []WarningKind{WarnUnmapped},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMapper(tc.rules, nil, SwitchSet{})
			got := m.MapTransaction(tc.line)
			assertResult(t, got, tc.want)
		})
	}
}
