package mapping

import "testing"

func sampleKeys() Keys {
	return Keys{
		TxnRef:          "111-2233445-6677889",
		SKU:             "SKU-A",
		Date:            "2026-07-17",
		SettlementID:    "90000000001",
		Description:     "Service fee",
		ShipmentID:      "SHP-9",
		MerchantOrderID: "MO-7",
	}
}

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		keys         Keys
		want         string
		wantWarnings []WarningKind
	}{
		{
			name:     "field tokens resolve to the source's own columns",
			template: "txn_ref+sku+date",
			keys:     sampleKeys(),
			want:     "111-2233445-6677889|SKU-A|2026-07-17",
		},
		{
			name:     "every known field name resolves",
			template: "settlement_id+description+shipment_id+merchant_order_id",
			keys:     sampleKeys(),
			want:     "90000000001|Service fee|SHP-9|MO-7",
		},
		{
			name:     "empty field keeps an empty part",
			template: "txn_ref+sku+date",
			keys:     Keys{TxnRef: "T1", Date: "2026-07-17"},
			want:     "T1||2026-07-17",
		},
		{
			name:     "literal text keeps its exact spelling and spaces",
			template: "GENERAL ADJUSTMENT+sku+settlement_id+date",
			keys:     sampleKeys(),
			want:     "GENERAL ADJUSTMENT|SKU-A|90000000001|2026-07-17",
		},
		{
			name:     "literal with colon, hyphen and upper case is kept as is",
			template: "LOST:WAREHOUSE+FBA_AMAZON-PARTNERED",
			keys:     sampleKeys(),
			want:     "LOST:WAREHOUSE|FBA_AMAZON-PARTNERED",
		},
		{
			name:         "unknown field-looking token is literal and warns",
			template:     "ADJUSTMENT_OTHER+settlement_id+date+record_type+settlement_id",
			keys:         sampleKeys(),
			want:         "ADJUSTMENT_OTHER|90000000001|2026-07-17|record_type|90000000001",
			wantWarnings: []WarningKind{WarnUnknownToken},
		},
		{
			name:     "upper-case token is literal and does not warn",
			template: "TXN_REF+date",
			keys:     sampleKeys(),
			want:     "TXN_REF|2026-07-17",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := resolveTemplate(tc.template, tc.keys)
			if got != tc.want {
				t.Fatalf("resolveTemplate(%q) = %q, want %q", tc.template, got, tc.want)
			}
			if len(warnings) != len(tc.wantWarnings) {
				t.Fatalf("warnings = %v, want kinds %v", warnings, tc.wantWarnings)
			}
			for i, w := range warnings {
				if w.Kind != tc.wantWarnings[i] {
					t.Fatalf("warning %d kind = %q, want %q", i, w.Kind, tc.wantWarnings[i])
				}
			}
		})
	}
}
