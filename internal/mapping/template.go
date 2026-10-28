package mapping

import (
	"regexp"
	"strings"
)

var fieldNameShaped = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// resolveTemplate builds a record_ref from a `+`-separated template. Known
// field names resolve to the line's own key columns, anything else is
// literal text, and the resolved parts are joined with `|`. An empty field
// keeps an empty part.
func resolveTemplate(template string, keys Keys) (string, []Warning) {
	fields := map[string]string{
		"txn_ref":           keys.TxnRef,
		"sku":               keys.SKU,
		"date":              keys.Date,
		"settlement_id":     keys.SettlementID,
		"description":       keys.Description,
		"shipment_id":       keys.ShipmentID,
		"merchant_order_id": keys.MerchantOrderID,
	}
	var parts []string
	var warnings []Warning
	for _, token := range strings.Split(template, "+") {
		if value, ok := fields[token]; ok {
			parts = append(parts, value)
			continue
		}
		if fieldNameShaped.MatchString(token) {
			warnings = append(warnings, Warning{
				Kind:   WarnUnknownToken,
				Detail: "template token " + token + " looks like a field name but is not one; used as literal text",
			})
		}
		parts = append(parts, token)
	}
	return strings.Join(parts, "|"), warnings
}
