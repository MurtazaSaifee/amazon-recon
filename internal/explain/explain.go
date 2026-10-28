// Package explain traces the Variance of one Summary line to the rules, record_refs and source
// lines behind it. Load reads the variance views; Format writes the explanation as text.
package explain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/report"
	"amazonrecon/internal/source"
)

// TopRecords is how many record_refs an explanation lists, by absolute difference.
const TopRecords = 10

// Line names one Summary line by its group and line, for example Expenses and Amazon fees.
type Line struct {
	Group string
	Name  string
}

// ParseLine reads "<group>/<line>", splitting at the first slash.
func ParseLine(s string) (Line, error) {
	group, name, ok := strings.Cut(s, "/")
	group, name = strings.TrimSpace(group), strings.TrimSpace(name)
	if !ok || group == "" || name == "" {
		return Line{}, fmt.Errorf("summary line %q: want \"<group>/<line>\", for example \"Expenses/Amazon fees\"", s)
	}
	return Line{Group: group, Name: name}, nil
}

// RuleAmount is what one rule routed to a Summary line on one side.
type RuleAmount struct {
	RuleID      string
	Label       string // the rule's transaction type and description, as written in its config
	AmountField string
	Amount      decimal.Decimal
	Lines       int // distinct source lines the rule routed from
}

// SourceLine is one line of a report file.
type SourceLine struct {
	FileName   string
	LineNumber int
}

// RecordVariance is one record_ref with a difference on the Summary line.
type RecordVariance struct {
	RecordRef       string
	Payments        decimal.Decimal
	Settlements     decimal.Decimal
	Difference      decimal.Decimal // payments minus settlements
	PaymentsLines   []SourceLine    // the payments lines that routed to this line for this record
	SettlementLines []SourceLine    // the settlement lines that routed to this line for this record
}

// Explanation is the trace of one Summary line in one run.
type Explanation struct {
	RunID           int64
	Line            Line
	Field           string // the Summary field the line reads; empty when no rule routes to it
	Payments        decimal.Decimal
	Settlements     decimal.Decimal
	Variance        decimal.Decimal // payments minus settlements
	PaymentRules    []RuleAmount
	SettlementRules []RuleAmount
	Records         []RecordVariance // up to TopRecords, largest absolute difference first
}

// Load reads the explanation of one Summary line in one run from the variance views.
// It fails when the Summary-line map has no such line.
func Load(ctx context.Context, pool *pgxpool.Pool, runID int64, line Line) (Explanation, error) {
	e := Explanation{RunID: runID, Line: line}
	err := pool.QueryRow(ctx, `SELECT COALESCE(summary_field, '') FROM summary_line_map
		WHERE summary_group = $1 AND summary_line = $2`, line.Group, line.Name).Scan(&e.Field)
	if errors.Is(err, pgx.ErrNoRows) {
		return Explanation{}, fmt.Errorf("no Summary line %s/%s", line.Group, line.Name)
	}
	if err != nil {
		return Explanation{}, fmt.Errorf("find Summary line: %w", err)
	}

	rows, err := pool.Query(ctx, `SELECT source, rule_id, rule_label, amount_field, line_count, amount::text
		FROM v_line_variance WHERE run_id = $1 AND summary_group = $2 AND summary_line = $3
		ORDER BY source, rule_id`, runID, line.Group, line.Name)
	if err != nil {
		return Explanation{}, fmt.Errorf("rules of %s/%s: %w", line.Group, line.Name, err)
	}
	defer rows.Close()
	payments, settlements := decimal.Zero, decimal.Zero
	for rows.Next() {
		var side string
		var r RuleAmount
		var amount string
		if err := rows.Scan(&side, &r.RuleID, &r.Label, &r.AmountField, &r.Lines, &amount); err != nil {
			return Explanation{}, err
		}
		if r.Amount, err = decimal.NewFromString(amount); err != nil {
			return Explanation{}, err
		}
		if side == source.Payments {
			e.PaymentRules = append(e.PaymentRules, r)
			payments = payments.Add(r.Amount)
		} else {
			e.SettlementRules = append(e.SettlementRules, r)
			settlements = settlements.Add(r.Amount)
		}
	}
	if err := rows.Err(); err != nil {
		return Explanation{}, err
	}
	e.Payments, e.Settlements = payments, settlements
	e.Variance = payments.Sub(settlements)

	if err := loadRecords(ctx, pool, &e); err != nil {
		return Explanation{}, err
	}
	return e, nil
}

// loadRecords reads the record_refs with a difference on the line, largest first, and the source lines behind each.
func loadRecords(ctx context.Context, pool *pgxpool.Pool, e *Explanation) error {
	rows, err := pool.Query(ctx, `SELECT record_ref, payments::text, settlements::text, difference::text
		FROM v_record_variance
		WHERE run_id = $1 AND summary_group = $2 AND summary_line = $3 AND difference <> 0
		ORDER BY abs(difference) DESC, record_ref
		LIMIT $4`, e.RunID, e.Line.Group, e.Line.Name, TopRecords)
	if err != nil {
		return fmt.Errorf("records of %s/%s: %w", e.Line.Group, e.Line.Name, err)
	}
	for rows.Next() {
		var rec RecordVariance
		var p, s, d string
		if err := rows.Scan(&rec.RecordRef, &p, &s, &d); err != nil {
			rows.Close()
			return err
		}
		for _, v := range []struct {
			text string
			dst  *decimal.Decimal
		}{{p, &rec.Payments}, {s, &rec.Settlements}, {d, &rec.Difference}} {
			if *v.dst, err = decimal.NewFromString(v.text); err != nil {
				rows.Close()
				return err
			}
		}
		e.Records = append(e.Records, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range e.Records {
		lines, err := sourceLines(ctx, pool, e.RunID, e.Field, e.Records[i].RecordRef)
		if err != nil {
			return err
		}
		e.Records[i].PaymentsLines = lines[source.Payments]
		e.Records[i].SettlementLines = lines[source.Settlement]
	}
	return nil
}

// sourceLines returns, per source, the distinct lines of one record that routed to a Summary field.
func sourceLines(ctx context.Context, pool *pgxpool.Pool, runID int64, field, recordRef string) (map[string][]SourceLine, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT c.source, r.file_name, c.line_number
		FROM summary_contributions c
		JOIN source_rows r ON r.run_id = c.run_id AND r.source = c.source AND r.line_number = c.line_number
		WHERE c.run_id = $1 AND c.summary_field = $2 AND r.record_ref = $3
		ORDER BY c.source, c.line_number`, runID, field, recordRef)
	if err != nil {
		return nil, fmt.Errorf("source lines of %s: %w", recordRef, err)
	}
	defer rows.Close()
	out := map[string][]SourceLine{}
	for rows.Next() {
		var side string
		var l SourceLine
		if err := rows.Scan(&side, &l.FileName, &l.LineNumber); err != nil {
			return nil, err
		}
		out[side] = append(out[side], l)
	}
	return out, rows.Err()
}

// Format writes the explanation as text: the Variance, the Contributions by rule on each side,
// and the top record_refs with their source lines.
func Format(e Explanation) string {
	var b strings.Builder
	field := e.Field
	if field == "" {
		field = "none"
	}
	fmt.Fprintf(&b, "%s / %s  (Summary field %s, run %d)\n\n", e.Line.Group, e.Line.Name, field, e.RunID)

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Payments\t%s\n", report.FormatAmount(e.Payments))
	fmt.Fprintf(tw, "Settlements\t%s\n", report.FormatAmount(e.Settlements))
	fmt.Fprintf(tw, "Variance\t%s\n", report.FormatAmount(e.Variance))
	tw.Flush()

	b.WriteString("\nContributions by rule\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Side\tRule\tTransaction type / description\tAmount field\tAmount\tLines\n")
	writeRules(tw, "Payments", e.PaymentRules)
	writeRules(tw, "Settlements", e.SettlementRules)
	tw.Flush()

	fmt.Fprintf(&b, "\nTop record_refs by absolute difference (%d shown)\n", len(e.Records))
	if len(e.Records) == 0 {
		b.WriteString("  none: every record_ref agrees on this line\n")
	}
	for i, r := range e.Records {
		fmt.Fprintf(&b, "%d. %s\n", i+1, r.RecordRef)
		fmt.Fprintf(&b, "   difference %s (payments %s, settlements %s)\n",
			report.FormatAmount(r.Difference), report.FormatAmount(r.Payments), report.FormatAmount(r.Settlements))
		fmt.Fprintf(&b, "   payments lines:    %s\n", formatLines(r.PaymentsLines))
		fmt.Fprintf(&b, "   settlement lines:  %s\n", formatLines(r.SettlementLines))
	}
	return b.String()
}

func writeRules(tw *tabwriter.Writer, side string, rules []RuleAmount) {
	if len(rules) == 0 {
		fmt.Fprintf(tw, "%s\t(none)\t\t\t\t\n", side)
		return
	}
	for _, r := range rules {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", side, r.RuleID, r.Label, r.AmountField,
			report.FormatAmount(r.Amount), r.Lines)
		side = ""
	}
}

// formatLines lists source lines as "file line n", joined with commas.
func formatLines(lines []SourceLine) string {
	if len(lines) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		parts = append(parts, fmt.Sprintf("%s line %d", l.FileName, l.LineNumber))
	}
	return strings.Join(parts, ", ")
}
