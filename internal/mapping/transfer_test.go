package mapping

import "testing"

// A Transfer line whose description does not exactly match the TRANSFER rules
// falls through to the Catch-all wildcard rules, so its total is routed to the
// Amazon fees Summary field. The rules below mirror the shape of the real
// config rows (hand-written, not loaded from the CSV); the amounts and
// identifiers are invented.
func TestTransferDescriptionFallsToCatchAll(t *testing.T) {
	rules := []PaymentRule{
		{ID: "P-062", TransactionType: "TRANSFER", Description: "TO_YOUR_ACCOUNT_ENDING", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		{ID: "P-063", TransactionType: "TRANSFER", Description: "TO_ACCOUNT_ENDING", AmountField: "total", RecordRef: "TRANSFER+description+settlement_id+date"},
		{ID: "P-110", TransactionType: "", Description: "any", AmountField: "total", RecordRef: "txn_ref+settlement_id+date", PositiveSummary: "expenses_amazon_fees", NegativeSummary: "expenses_amazon_fees"},
		{ID: "P-111", TransactionType: "", Description: "any", AmountField: "other", RecordRef: "txn_ref+settlement_id+date"},
		{ID: "P-112", TransactionType: "", Description: "any", AmountField: "other_transaction_fees", RecordRef: "txn_ref+settlement_id+date"},
	}
	m := NewMapper(rules, nil, SwitchSet{})

	t.Run("description with a colon and digits does not match TO_ACCOUNT_ENDING", func(t *testing.T) {
		line := TransactionLine{
			Keys: Keys{
				TxnRef:       "T-900",
				SettlementID: "S-900",
				Date:         "2026-07-17",
				Description:  "To account ending with: 555",
			},
			TransactionType: "Transfer",
			Total:           d("-123.45"),
		}
		got := m.MapTransaction(line)
		assertResult(t, got, wantResult{
			RecordRef: "T-900|S-900|2026-07-17",
			Tier:      TierCatchAllWildcard,
			Matched:   []string{"P-110", "P-111", "P-112"},
			Contributions: []wantContribution{
				{RuleID: "P-110", AmountField: "total", Amount: "-123.45", SummaryField: "expenses_amazon_fees"},
			},
		})
	})

	t.Run("the exact description still matches the TRANSFER rule and routes nothing", func(t *testing.T) {
		line := TransactionLine{
			Keys: Keys{
				TxnRef:       "T-901",
				SettlementID: "S-900",
				Date:         "2026-07-17",
				Description:  "TO_ACCOUNT_ENDING",
			},
			TransactionType: "TRANSFER",
			Total:           d("-50.00"),
		}
		got := m.MapTransaction(line)
		assertResult(t, got, wantResult{
			RecordRef: "TRANSFER|TO_ACCOUNT_ENDING|S-900|2026-07-17",
			Tier:      TierTypeDescription,
			Matched:   []string{"P-063"},
		})
	})
}
