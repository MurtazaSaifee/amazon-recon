package recon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/source"
	"amazonrecon/internal/store"
	"amazonrecon/internal/timefmt"
)

// Status is the reconciliation outcome of one record_ref.
type Status string

const (
	Reconciled               Status = "Reconciled"
	ReconciledWithDifference Status = "Reconciled with difference"
	UnreconciledPayment      Status = "Unreconciled payment"
	UnreconciledSettlement   Status = "Unreconciled settlement"
)

// Statuses lists the record_ref statuses in the order every sheet and the CLI show them.
var Statuses = []Status{Reconciled, ReconciledWithDifference, UnreconciledPayment, UnreconciledSettlement}

// Label is the status as the sheets and the CLI print it. It is the only place a status is named for
// a reader, so the Summary, the Consolidated Data and the CLI cannot disagree on a word.
func (s Status) Label() string {
	return string(s)
}

// Side is one source's lines for one record_ref.
type Side struct {
	Lines      []int                      // source line numbers, ascending
	Total      decimal.Decimal            // payments line totals, or Component amounts
	PostedDate string                     // posted date (UTC) of the earliest line, YYYY-MM-DD
	Earliest   map[string]string          // raw cells of the earliest line
	Routed     map[string]decimal.Decimal // Contributions by Summary field
}

// RecordRow is one record_ref of the Consolidated Data sheet.
type RecordRow struct {
	RecordRef   string
	Status      Status
	Payments    Side
	Settlements Side
	Difference  decimal.Decimal // payments total minus settlement total
}

// Counts holds the number of record_refs per status. Every status is present, zero when it has none.
type Counts map[Status]int

// ListedLine is one line listed by itself: an unmapped line or an out-of-scope line.
type ListedLine struct {
	Source     string
	FileName   string
	LineNumber int
	Reason     string // the scope reason, or "Unmapped"
	Amount     decimal.Decimal
	PostedDate string
	Raw        map[string]string
}

// Reconciliation is the reconciliation of one run: its records in Consolidated order, the
// unmapped lines and the out-of-scope lines.
type Reconciliation struct {
	Counts Counts
	// UnmappedLines counts the unmapped lines per source. They are lines, not records: they have no record_ref.
	UnmappedLines map[string]int
	Records       []RecordRow // Reconciled (with difference) first, then unreconciled payments, then settlements
	Unmapped      []ListedLine
	OutOfScope    []ListedLine
}

// UnmappedTotal is the number of unmapped lines of both sources.
func (r Reconciliation) UnmappedTotal() int {
	n := 0
	for _, c := range r.UnmappedLines {
		n += c
	}
	return n
}

// buildReconciliation fills rep.Reconciliation for one run.
func buildReconciliation(ctx context.Context, pool *pgxpool.Pool, runID int64, rep *Report) error {
	sides, err := readSides(ctx, pool, runID)
	if err != nil {
		return err
	}
	if err := readRecords(ctx, pool, runID, sides, &rep.Reconciliation); err != nil {
		return err
	}
	if err := readRouted(ctx, pool, runID, sides); err != nil {
		return err
	}
	if err := readListed(ctx, pool, runID, &rep.Reconciliation); err != nil {
		return err
	}
	counts := Counts{}
	for _, s := range Statuses {
		counts[s] = 0
	}
	for _, rec := range rep.Reconciliation.Records {
		counts[rec.Status]++
	}
	rep.Reconciliation.Counts = counts
	rep.Reconciliation.UnmappedLines = map[string]int{source.Payments: 0, source.Settlement: 0}
	for _, u := range rep.Reconciliation.Unmapped {
		rep.Reconciliation.UnmappedLines[u.Source]++
	}
	return nil
}

type sideKey struct{ ref, source string }

// readSides reads, per record_ref and source, the line numbers, the earliest line's raw cells and
// posted date. Totals come from the reconciliation view.
func readSides(ctx context.Context, pool *pgxpool.Pool, runID int64) (map[sideKey]*Side, error) {
	sides := map[sideKey]*Side{}
	rows, err := pool.Query(ctx, `SELECT record_ref, source, array_agg(line_number ORDER BY line_number)::int[]
		FROM source_rows WHERE run_id = $1 AND `+sqlInScopeMapped+`
		GROUP BY record_ref, source`, runID)
	if err != nil {
		return nil, fmt.Errorf("source lines of run %d: %w", runID, err)
	}
	for rows.Next() {
		var ref, src string
		var lines []int32
		if err := rows.Scan(&ref, &src, &lines); err != nil {
			rows.Close()
			return nil, err
		}
		s := sideOf(sides, ref, src)
		for _, n := range lines {
			s.Lines = append(s.Lines, int(n))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `SELECT DISTINCT ON (record_ref, source) record_ref, source, raw_payload, posted_date
		FROM source_rows WHERE run_id = $1 AND `+sqlInScopeMapped+`
		ORDER BY record_ref, source, line_number`, runID)
	if err != nil {
		return nil, fmt.Errorf("earliest lines of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref, src string
		var raw []byte
		var posted *time.Time
		if err := rows.Scan(&ref, &src, &raw, &posted); err != nil {
			return nil, err
		}
		s := sideOf(sides, ref, src)
		if err := json.Unmarshal(raw, &s.Earliest); err != nil {
			return nil, fmt.Errorf("raw payload of %s record %s: %w", src, ref, err)
		}
		if posted != nil {
			s.PostedDate = timefmt.Date(*posted)
		}
	}
	return sides, rows.Err()
}

func sideOf(sides map[sideKey]*Side, ref, src string) *Side {
	k := sideKey{ref, src}
	s, ok := sides[k]
	if !ok {
		s = &Side{Routed: map[string]decimal.Decimal{}}
		sides[k] = s
	}
	return s
}

// readRecords reads the reconciliation view in Consolidated order and attaches each record's sides.
func readRecords(ctx context.Context, pool *pgxpool.Pool, runID int64, sides map[sideKey]*Side, out *Reconciliation) error {
	rows, err := pool.Query(ctx, `SELECT record_ref, status, payments_total, settlement_total, difference
		FROM reconciliation_by_record_ref WHERE run_id = $1
		ORDER BY CASE WHEN status IN ('Reconciled', 'Reconciled with difference') THEN 1
		              WHEN status = 'Unreconciled payment' THEN 2 ELSE 3 END, record_ref`, runID)
	if err != nil {
		return fmt.Errorf("reconciliation of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var rec RecordRow
		var status string
		var pay, set, diff store.Amount
		if err := rows.Scan(&rec.RecordRef, &status, &pay, &set, &diff); err != nil {
			return fmt.Errorf("scan record of run %d: %w", runID, err)
		}
		rec.Status = Status(status)
		rec.Difference = diff.Decimal
		rec.Payments = sideWithTotal(sides, rec.RecordRef, source.Payments, pay.Decimal)
		rec.Settlements = sideWithTotal(sides, rec.RecordRef, source.Settlement, set.Decimal)
		out.Records = append(out.Records, rec)
	}
	return rows.Err()
}

// sideWithTotal returns a record's side with the total from the view.
func sideWithTotal(sides map[sideKey]*Side, ref, src string, total decimal.Decimal) Side {
	s := Side{Routed: map[string]decimal.Decimal{}}
	if p, ok := sides[sideKey{ref, src}]; ok {
		s = *p
	}
	s.Total = total
	return s
}

// readRouted attaches each record's Contributions, by Summary field, to its sides.
func readRouted(ctx context.Context, pool *pgxpool.Pool, runID int64, sides map[sideKey]*Side) error {
	rows, err := pool.Query(ctx, `SELECT r.record_ref, c.source, c.summary_field, SUM(c.amount)
		FROM summary_contributions c
		JOIN source_rows r ON r.run_id = c.run_id AND r.source = c.source AND r.line_number = c.line_number
		WHERE c.run_id = $1 AND r.`+sqlInScopeMapped+`
		GROUP BY r.record_ref, c.source, c.summary_field`, runID)
	if err != nil {
		return fmt.Errorf("contributions of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref, src, field string
		var amount store.Amount
		if err := rows.Scan(&ref, &src, &field, &amount); err != nil {
			return fmt.Errorf("scan contribution of run %d: %w", runID, err)
		}
		sideOf(sides, ref, src).Routed[field] = amount.Decimal
	}
	return rows.Err()
}

// readListed reads the unmapped lines and the out-of-scope lines of a run, in file order.
func readListed(ctx context.Context, pool *pgxpool.Pool, runID int64, out *Reconciliation) error {
	rows, err := pool.Query(ctx, `SELECT source, file_name, line_number, '`+source.ReasonUnmapped+`', amount, posted_date, raw_payload
		FROM source_rows
		WHERE run_id = $1 AND `+sqlInScope+` AND precedence_tier = 0
		ORDER BY source, line_number`, runID)
	if err != nil {
		return fmt.Errorf("unmapped lines of run %d: %w", runID, err)
	}
	out.Unmapped, err = scanListed(rows)
	if err != nil {
		return err
	}

	rows, err = pool.Query(ctx, `SELECT source, file_name, line_number, scope_reason, amount, posted_date, raw_payload
		FROM source_rows
		WHERE run_id = $1 AND NOT in_scope AND line_kind <> '`+source.KindSettlementHeader+`'
		ORDER BY source, line_number`, runID)
	if err != nil {
		return fmt.Errorf("out-of-scope lines of run %d: %w", runID, err)
	}
	out.OutOfScope, err = scanListed(rows)
	return err
}

func scanListed(rows pgx.Rows) ([]ListedLine, error) {
	defer rows.Close()
	var out []ListedLine
	for rows.Next() {
		var l ListedLine
		var amount store.Amount
		var posted *time.Time
		var raw []byte
		if err := rows.Scan(&l.Source, &l.FileName, &l.LineNumber, &l.Reason, &amount, &posted, &raw); err != nil {
			return nil, fmt.Errorf("scan listed line: %w", err)
		}
		l.Amount = amount.Decimal
		if posted != nil {
			l.PostedDate = timefmt.Date(*posted)
		}
		if err := json.Unmarshal(raw, &l.Raw); err != nil {
			return nil, fmt.Errorf("raw payload of %s line %d: %w", l.Source, l.LineNumber, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
