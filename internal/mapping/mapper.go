package mapping

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// amountKeyTotal is the normalised Amount field name that reads the line total.
const amountKeyTotal = "TOTAL"

// amountKeySettlement is the normalised Amount field name for a Component.
const amountKeySettlement = "AMOUNT"

// compiled is a rule with its matching keys normalised once.
type compiled struct {
	id          string
	txnType     string // normalised; "" is the Catch-all
	qualifier   string // normalised amount type for settlement rules; "" for payments
	desc        string // normalised description
	wildcard    bool
	amountKey   string // normalised Amount field
	amountField string // Amount field as written in the config
	recordRef   string
	posSummary  string
	negSummary  string
}

// index groups compiled rules by normalised transaction type, keeping config order.
type index map[string][]compiled

// Mapper maps lines against a fixed pair of rule sets. Build it once and
// reuse it; it is safe for concurrent reads.
type Mapper struct {
	payments   index
	settlement index
	switches   SwitchSet
}

// NewMapper loads both rule sets, in config (rule ID) order, and the enabled
// interpretation switches. A nil or empty SwitchSet turns every switch off.
func NewMapper(payments []PaymentRule, settlement []SettlementRule, switches SwitchSet) *Mapper {
	m := &Mapper{payments: index{}, settlement: index{}, switches: switches}
	for _, r := range payments {
		c := compiled{
			id:          r.ID,
			txnType:     Normalise(r.TransactionType),
			desc:        Normalise(r.Description),
			amountKey:   Normalise(r.AmountField),
			amountField: r.AmountField,
			recordRef:   r.RecordRef,
			posSummary:  r.PositiveSummary,
			negSummary:  r.NegativeSummary,
		}
		c.wildcard = c.desc == Normalise(Wildcard)
		m.payments[c.txnType] = append(m.payments[c.txnType], c)
	}
	for _, r := range settlement {
		c := compiled{
			id:          r.ID,
			txnType:     Normalise(r.TransactionType),
			qualifier:   Normalise(r.AmountType),
			desc:        Normalise(r.AmountDescription),
			amountKey:   amountKeySettlement,
			amountField: "amount",
			recordRef:   r.RecordRef,
			posSummary:  r.PositiveSummary,
			negSummary:  r.NegativeSummary,
		}
		c.wildcard = c.desc == Normalise(Wildcard)
		m.settlement[c.txnType] = append(m.settlement[c.txnType], c)
	}
	return m
}

// MapTransaction maps one Payments line.
func (m *Mapper) MapTransaction(line TransactionLine) Result {
	amounts := make(map[string]decimal.Decimal, len(line.Amounts))
	for field, amount := range line.Amounts {
		amounts[Normalise(field)] = amount
	}
	value := func(c compiled) decimal.Decimal {
		if c.amountKey == amountKeyTotal {
			return line.Total
		}
		return amounts[c.amountKey]
	}
	return m.mapLine(m.payments, "payments", line.Keys, Normalise(line.TransactionType), "", Normalise(line.Description), value)
}

// MapComponent maps one Settlement component.
func (m *Mapper) MapComponent(c Component) Result {
	value := func(compiled) decimal.Decimal { return c.Amount }
	return m.mapLine(m.settlement, "settlement", c.Keys, Normalise(c.TransactionType), Normalise(c.AmountType), Normalise(c.AmountDescription), value)
}

// mapLine applies the precedence tiers, builds the record_ref, routes every
// rule in the winning tier, and collects warnings.
func (m *Mapper) mapLine(ix index, side string, keys Keys, txnType, qualifier, desc string, value func(compiled) decimal.Decimal) Result {
	tier, rules := ix.match(txnType, qualifier, desc, m.switches.Enabled(PrefixBeforeCatchAll))
	if tier == TierUnmapped {
		return Result{
			Tier: TierUnmapped,
			Warnings: []Warning{{
				Kind:   WarnUnmapped,
				Detail: fmt.Sprintf("no %s rule matches type %q, qualifier %q, description %q", side, txnType, qualifier, desc),
			}},
		}
	}

	res := Result{Tier: tier}
	for _, r := range rules {
		res.MatchedRuleIDs = append(res.MatchedRuleIDs, r.id)
	}

	recordRef, warnings := resolveTemplate(rules[0].recordRef, keys)
	res.RecordRef = recordRef
	res.Warnings = append(res.Warnings, warnings...)
	res.Warnings = append(res.Warnings, multiRuleWarnings(rules)...)

	for _, r := range rules {
		amount := value(r)
		if amount.IsZero() {
			continue
		}
		summary := r.posSummary
		if amount.IsNegative() {
			summary = r.negSummary
		}
		if summary == "" {
			continue
		}
		res.Contributions = append(res.Contributions, Contribution{
			RuleID:       r.id,
			AmountField:  r.amountField,
			Amount:       amount,
			SummaryField: summary,
		})
	}
	return res
}

// match returns the highest tier with any matching rule and its rules, in
// config order. Tiers 1 and 2 need a non-empty transaction type. The prefix
// tier (only when prefix is on) comes after them and before the Catch-all.
// The last two tiers use the Catch-all (empty type).
func (ix index) match(txnType, qualifier, desc string, prefix bool) (Tier, []compiled) {
	if txnType != "" {
		exact, wild := pick(ix[txnType], qualifier, desc)
		if len(exact) > 0 {
			return TierTypeDescription, exact
		}
		if len(wild) > 0 {
			return TierTypeWildcard, wild
		}
		if prefix {
			if pre := prefixPick(ix[txnType], qualifier, desc); len(pre) > 0 {
				return TierTypePrefix, pre
			}
		}
	}
	exact, wild := pick(ix[""], qualifier, desc)
	if len(exact) > 0 {
		return TierCatchAllDescription, exact
	}
	if len(wild) > 0 {
		return TierCatchAllWildcard, wild
	}
	return TierUnmapped, nil
}

// pick splits the rules of one type into exact-description and wildcard
// matches, keeping only rules whose qualifier equals the line's.
func pick(rules []compiled, qualifier, desc string) (exact, wild []compiled) {
	for _, r := range rules {
		if r.qualifier != qualifier {
			continue
		}
		switch {
		case r.wildcard:
			wild = append(wild, r)
		case r.desc == desc:
			exact = append(exact, r)
		}
	}
	return exact, wild
}

// prefixPick returns the non-wildcard rules of one type whose description is the longest prefix of
// desc that ends at a "_" or ":" boundary, keeping only rules whose qualifier equals the line's.
// Rules that share the longest prefix all match, as exact rules do in tier 1.
func prefixPick(rules []compiled, qualifier, desc string) []compiled {
	var best []compiled
	bestLen := 0
	for _, r := range rules {
		if r.qualifier != qualifier || r.wildcard || !atPrefixBoundary(desc, r.desc) {
			continue
		}
		switch {
		case len(r.desc) > bestLen:
			best, bestLen = []compiled{r}, len(r.desc)
		case len(r.desc) == bestLen:
			best = append(best, r)
		}
	}
	return best
}

// atPrefixBoundary reports whether prefix is a leading part of desc that ends
// where desc continues with "_" or ":". An empty prefix never matches.
func atPrefixBoundary(desc, prefix string) bool {
	if prefix == "" || !strings.HasPrefix(desc, prefix) {
		return false
	}
	if len(desc) == len(prefix) {
		return true
	}
	c := desc[len(prefix)]
	return c == '_' || c == ':'
}

// multiRuleWarnings warns once per Amount field that two or more matched
// rules share. Every such rule still applies.
func multiRuleWarnings(rules []compiled) []Warning {
	type group struct {
		field string
		ids   []string
	}
	var order []string
	groups := map[string]*group{}
	for _, r := range rules {
		g, ok := groups[r.amountKey]
		if !ok {
			g = &group{field: r.amountField}
			groups[r.amountKey] = g
			order = append(order, r.amountKey)
		}
		g.ids = append(g.ids, r.id)
	}
	var warnings []Warning
	for _, key := range order {
		g := groups[key]
		if len(g.ids) > 1 {
			warnings = append(warnings, Warning{
				Kind:   WarnMultiRuleHit,
				Detail: fmt.Sprintf("Amount field %s is matched by rules %s", g.field, strings.Join(g.ids, ", ")),
			})
		}
	}
	return warnings
}
