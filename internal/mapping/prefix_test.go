package mapping

import "testing"

// prefix_before_catch_all lets a rule of the same transaction type match a line whose description
// starts with the rule's description, followed by "_" or ":", when no rule in tiers 1 and 2 matches.
// The rules below mirror the shape of the real config rows (hand-written, not loaded from the CSV);
// the descriptions, amounts and identifiers are invented.

// transferRules are the Transfer rules of the real config: one exact description that does not
// match the real Transfer text, and a sibling with a different prefix. The Catch-all wildcard
// rules follow, as in the real config.
func transferRules() []PaymentRule {
	return []PaymentRule{
		{ID: "P-062", TransactionType: "TRANSFER", Description: "TO_YOUR_ACCOUNT_ENDING", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		{ID: "P-063", TransactionType: "TRANSFER", Description: "TO_ACCOUNT_ENDING", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		{ID: "P-110", TransactionType: "", Description: "any", AmountField: "total", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "expenses_amazon_fees", NegativeSummary: "expenses_amazon_fees"},
		{ID: "P-111", TransactionType: "", Description: "any", AmountField: "other", RecordRef: "txn_ref+settlement_id+date"},
	}
}

// prefixSwitchOn is the switch set of a fixed run. It fails when the switch is not registered.
func prefixSwitchOn(t *testing.T) SwitchSet {
	t.Helper()
	set, err := NewSwitchSet("prefix_before_catch_all")
	if err != nil {
		t.Fatalf("NewSwitchSet: %v", err)
	}
	return set
}

func TestPrefixBeforeCatchAllOnAndOff(t *testing.T) {
	transfer := func(desc string) TransactionLine {
		return TransactionLine{
			Keys: Keys{
				TxnRef:       "",
				SettlementID: "S-900",
				Date:         "2026-07-18",
				Description:  desc,
			},
			TransactionType: "Transfer",
			Total:           d("-1234.56"),
		}
	}

	t.Run("on: a Transfer whose description extends the prefix matches the prefix rule, not the catch-all", func(t *testing.T) {
		m := NewMapper(transferRules(), nil, prefixSwitchOn(t))
		got := m.MapTransaction(transfer("To account ending with: 905"))
		assertResult(t, got, wantResult{
			RecordRef: "TRANSFER|To account ending with: 905|S-900|2026-07-18",
			Tier:      TierTypePrefix,
			Matched:   []string{"P-063"},
		})
	})

	t.Run("off (baseline): the same line falls to the catch-all wildcard rules", func(t *testing.T) {
		m := NewMapper(transferRules(), nil, SwitchSet{})
		got := m.MapTransaction(transfer("To account ending with: 905"))
		assertResult(t, got, wantResult{
			RecordRef: "|S-900|2026-07-18",
			Tier:      TierCatchAllWildcard,
			Matched:   []string{"P-110", "P-111"},
			Contributions: []wantContribution{
				{RuleID: "P-110", AmountField: "total", Amount: "-1234.56", SummaryField: "expenses_amazon_fees"},
			},
		})
	})

	t.Run("on: a colon right after the prefix also matches", func(t *testing.T) {
		m := NewMapper(transferRules(), nil, prefixSwitchOn(t))
		got := m.MapTransaction(transfer("TO_ACCOUNT_ENDING:905"))
		assertResult(t, got, wantResult{
			RecordRef: "TRANSFER|TO_ACCOUNT_ENDING:905|S-900|2026-07-18",
			Tier:      TierTypePrefix,
			Matched:   []string{"P-063"},
		})
	})

	t.Run("on: a prefix with no separator after it does not match", func(t *testing.T) {
		m := NewMapper(transferRules(), nil, prefixSwitchOn(t))
		got := m.MapTransaction(transfer("TO_ACCOUNT_ENDINGS"))
		if got.Tier != TierCatchAllWildcard {
			t.Fatalf("Tier = %v, want %v (the prefix must end at a _ or : boundary)", got.Tier, TierCatchAllWildcard)
		}
	})

	t.Run("on: the prefix rule is not used when the type differs", func(t *testing.T) {
		m := NewMapper(transferRules(), nil, prefixSwitchOn(t))
		line := transfer("To account ending with: 905")
		line.TransactionType = "Order"
		got := m.MapTransaction(line)
		if got.Tier != TierCatchAllWildcard {
			t.Fatalf("Tier = %v, want %v (a prefix rule of TRANSFER must not match an ORDER line)", got.Tier, TierCatchAllWildcard)
		}
	})

	t.Run("on: the longest matching prefix wins over a shorter one", func(t *testing.T) {
		rules := append([]PaymentRule{
			{ID: "P-060", TransactionType: "TRANSFER", Description: "TO", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		}, transferRules()...)
		m := NewMapper(rules, nil, prefixSwitchOn(t))
		got := m.MapTransaction(transfer("To account ending with: 905"))
		assertResult(t, got, wantResult{
			RecordRef: "TRANSFER|To account ending with: 905|S-900|2026-07-18",
			Tier:      TierTypePrefix,
			Matched:   []string{"P-063"},
		})
	})

	t.Run("on: a type+any rule still wins over the prefix rule", func(t *testing.T) {
		rules := append([]PaymentRule{
			{ID: "P-061", TransactionType: "TRANSFER", Description: "any", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		}, transferRules()...)
		m := NewMapper(rules, nil, prefixSwitchOn(t))
		got := m.MapTransaction(transfer("To account ending with: 905"))
		assertResult(t, got, wantResult{
			RecordRef: "TRANSFER|To account ending with: 905|S-900|2026-07-18",
			Tier:      TierTypeWildcard,
			Matched:   []string{"P-061"},
		})
	})

	t.Run("on and off: an exact description keeps tier 1 and never reaches the prefix tier", func(t *testing.T) {
		for _, set := range []SwitchSet{prefixSwitchOn(t), {}} {
			m := NewMapper(transferRules(), nil, set)
			got := m.MapTransaction(transfer("TO_ACCOUNT_ENDING"))
			if got.Tier != TierTypeDescription {
				t.Fatalf("switches %v: Tier = %v, want %v", set.Names(), got.Tier, TierTypeDescription)
			}
			if !equalStrings(got.MatchedRuleIDs, []string{"P-063"}) {
				t.Fatalf("switches %v: Matched = %v, want [P-063]", set.Names(), got.MatchedRuleIDs)
			}
		}
	})
}

func TestPrefixBeforeCatchAllSettlement(t *testing.T) {
	rules := []SettlementRule{
		{ID: "S-100", TransactionType: "OTHER-TRANSACTION", AmountType: "OTHER-TRANSACTION", AmountDescription: "SUBSCRIPTION_FEE", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "expenses_other", NegativeSummary: "expenses_other"},
		{ID: "S-101", TransactionType: "", AmountType: "ITEMPRICE", AmountDescription: "any", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "sales_other", NegativeSummary: "sales_other"},
	}
	keys := Keys{TxnRef: "T-300", SettlementID: "S-7", Date: "2026-07-18"}

	t.Run("on: a component whose description extends the prefix, with the same amount type, matches the prefix rule", func(t *testing.T) {
		m := NewMapper(nil, rules, prefixSwitchOn(t))
		got := m.MapComponent(Component{Keys: keys, TransactionType: "OTHER-TRANSACTION", AmountType: "OTHER-TRANSACTION", AmountDescription: "SUBSCRIPTION_FEE_RENEWAL", Amount: d("-16.97")})
		assertResult(t, got, wantResult{
			RecordRef: "T-300|S-7|2026-07-18",
			Tier:      TierTypePrefix,
			Matched:   []string{"S-100"},
			Contributions: []wantContribution{
				{RuleID: "S-100", AmountField: "amount", Amount: "-16.97", SummaryField: "expenses_other"},
			},
		})
	})

	t.Run("on: a different amount type is not matched by the prefix rule", func(t *testing.T) {
		m := NewMapper(nil, rules, prefixSwitchOn(t))
		got := m.MapComponent(Component{Keys: keys, TransactionType: "OTHER-TRANSACTION", AmountType: "ITEMFEES", AmountDescription: "SUBSCRIPTION_FEE_RENEWAL", Amount: d("-16.97")})
		if got.Tier != TierUnmapped {
			t.Fatalf("Tier = %v, want %v (the amount type must still match, and the catch-all here is ITEMPRICE)", got.Tier, TierUnmapped)
		}
	})

	t.Run("off (baseline): the same component falls to the catch-all", func(t *testing.T) {
		m := NewMapper(nil, rules, SwitchSet{})
		got := m.MapComponent(Component{Keys: keys, TransactionType: "OTHER-TRANSACTION", AmountType: "OTHER-TRANSACTION", AmountDescription: "SUBSCRIPTION_FEE_RENEWAL", Amount: d("-16.97")})
		if got.Tier != TierUnmapped {
			t.Fatalf("Tier = %v with the switch off, want %v (no exact or wildcard rule, and the catch-all is ITEMPRICE)", got.Tier, TierUnmapped)
		}
	})
}
