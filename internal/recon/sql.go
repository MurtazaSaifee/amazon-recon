package recon

import "amazonrecon/internal/source"

// The lines that count in reconciliation are defined here once. Every reconciliation query builds its
// predicate from these fragments, so the scope rule and the line kinds cannot drift apart. The values
// are package constants, never input, so they are spliced into the SQL as literals.
var (
	// sqlLines selects Transaction lines and Components: the lines that reconcile. The Settlement header
	// carries the reference total and is not a line that reconciles.
	sqlLines = "line_kind IN ('" + source.KindTransaction + "', '" + source.KindComponent + "')"

	// sqlInScope selects the in-scope lines that reconcile.
	sqlInScope = "in_scope AND " + sqlLines

	// sqlInScopeMapped selects the in-scope lines that a rule mapped. Unmapped lines have precedence_tier 0,
	// no record_ref, and are listed separately.
	sqlInScopeMapped = sqlInScope + " AND precedence_tier > 0"
)
