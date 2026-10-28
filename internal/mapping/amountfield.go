package mapping

import "strings"

// columnAliases holds Payments columns whose config name is not their
// snake_case spelling.
var columnAliases = map[string]string{
	Normalise("fulfilment by amazon fees"): "fba_fees",
}

// AmountFieldForColumn returns the Amount field name for a Payments column
// header. The fulfilment-by-Amazon fees column is aliased to fba_fees; every
// other column is its lower-case snake_case spelling.
func AmountFieldForColumn(column string) string {
	if alias, ok := columnAliases[Normalise(column)]; ok {
		return alias
	}
	return strings.ToLower(Normalise(column))
}
