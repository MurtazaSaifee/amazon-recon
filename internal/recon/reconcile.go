package recon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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

// inScopeMapped selects the lines that reconcile: in-scope Transaction lines and Components that
// a rule mapped. Unmapped lines have precedence_tier 0 and are listed separately.
const inScopeMapped = "in_scope AND line_kind IN ('transaction', 'component') AND precedence_tier > 0"

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

// Counts holds the number of record_refs per status, and of unmapped lines per source.
type Counts struct {
	Reconciled               int
	ReconciledWithDifference int
	UnreconciledPayment      int
	UnreconciledSettlement   int
	UnmappedPayments         int // unmapped lines, not records: they have no record_ref
	UnmappedSettlements      int
}

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
	Counts     Counts
	Records    []RecordRow // Reconciled (with difference) first, then unreconciled payments, then settlements
	Unmapped   []ListedLine
	OutOfScope []ListedLine
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
	for _, rec := range rep.Reconciliation.Records {
		switch rec.Status {
		case Reconciled:
			rep.Reconciliation.Counts.Reconciled++
		case ReconciledWithDifference:
			rep.Reconciliation.Counts.ReconciledWithDifference++
		case UnreconciledPayment:
			rep.Reconciliation.Counts.UnreconciledPayment++
		case UnreconciledSettlement:
			rep.Reconciliation.Counts.UnreconciledSettlement++
		}
	}
	for _, u := range rep.Reconciliation.Unmapped {
		if u.Source == sourcePayments {
			rep.Reconciliation.Counts.UnmappedPayments++
		} else {
			rep.Reconciliation.Counts.UnmappedSettlements++
		}
	}
	return nil
}

type sideKey struct{ ref, source string }

// readSides reads, per record_ref and source, the line numbers, the earliest line's raw cells and
// posted date. Totals come from the reconciliation view.
func readSides(ctx context.Context, pool *pgxpool.Pool, runID int64) (map[sideKey]*Side, error) {
	sides := map[sideKey]*Side{}
	rows, err := pool.Query(ctx, `SELECT record_ref, source, array_agg(line_number ORDER BY line_number)::int[]
		FROM source_rows WHERE run_id = $1 AND `+inScopeMapped+`
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
		FROM source_rows WHERE run_id = $1 AND `+inScopeMapped+`
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
	rows, err := pool.Query(ctx, `SELECT record_ref, status, payments_total::text, settlement_total::text, difference::text
		FROM reconciliation_by_record_ref WHERE run_id = $1
		ORDER BY CASE WHEN status IN ('Reconciled', 'Reconciled with difference') THEN 1
		              WHEN status = 'Unreconciled payment' THEN 2 ELSE 3 END, record_ref`, runID)
	if err != nil {
		return fmt.Errorf("reconciliation of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var rec RecordRow
		var status, pay, set, diff string
		if err := rows.Scan(&rec.RecordRef, &status, &pay, &set, &diff); err != nil {
			return err
		}
		rec.Status = Status(status)
		if rec.Difference, err = decimal.NewFromString(diff); err != nil {
			return err
		}
		if rec.Payments, err = sideWithTotal(sides, rec.RecordRef, sourcePayments, pay); err != nil {
			return err
		}
		if rec.Settlements, err = sideWithTotal(sides, rec.RecordRef, sourceSettlement, set); err != nil {
			return err
		}
		out.Records = append(out.Records, rec)
	}
	return rows.Err()
}

// sideWithTotal returns a record's side with the total from the view.
func sideWithTotal(sides map[sideKey]*Side, ref, src, total string) (Side, error) {
	s := Side{Routed: map[string]decimal.Decimal{}}
	if p, ok := sides[sideKey{ref, src}]; ok {
		s = *p
	}
	d, err := decimal.NewFromString(total)
	if err != nil {
		return Side{}, err
	}
	s.Total = d
	return s, nil
}

// readRouted attaches each record's Contributions, by Summary field, to its sides.
func readRouted(ctx context.Context, pool *pgxpool.Pool, runID int64, sides map[sideKey]*Side) error {
	rows, err := pool.Query(ctx, `SELECT r.record_ref, c.source, c.summary_field, SUM(c.amount)::text
		FROM summary_contributions c
		JOIN source_rows r ON r.run_id = c.run_id AND r.source = c.source AND r.line_number = c.line_number
		WHERE c.run_id = $1 AND r.`+inScopeMapped+`
		GROUP BY r.record_ref, c.source, c.summary_field`, runID)
	if err != nil {
		return fmt.Errorf("contributions of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref, src, field, amount string
		if err := rows.Scan(&ref, &src, &field, &amount); err != nil {
			return err
		}
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return err
		}
		sideOf(sides, ref, src).Routed[field] = d
	}
	return rows.Err()
}

// readListed reads the unmapped lines and the out-of-scope lines of a run, in file order.
func readListed(ctx context.Context, pool *pgxpool.Pool, runID int64, out *Reconciliation) error {
	rows, err := pool.Query(ctx, `SELECT source, file_name, line_number, 'Unmapped', amount::text, posted_date, raw_payload
		FROM source_rows
		WHERE run_id = $1 AND in_scope AND line_kind IN ('transaction', 'component') AND precedence_tier = 0
		ORDER BY source, line_number`, runID)
	if err != nil {
		return fmt.Errorf("unmapped lines of run %d: %w", runID, err)
	}
	out.Unmapped, err = scanListed(rows)
	if err != nil {
		return err
	}

	rows, err = pool.Query(ctx, `SELECT source, file_name, line_number, scope_reason, amount::text, posted_date, raw_payload
		FROM source_rows
		WHERE run_id = $1 AND NOT in_scope AND line_kind <> 'settlement_header'
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
		var amount string
		var posted *time.Time
		var raw []byte
		if err := rows.Scan(&l.Source, &l.FileName, &l.LineNumber, &l.Reason, &amount, &posted, &raw); err != nil {
			return nil, err
		}
		d, err := decimal.NewFromString(amount)
		if err != nil {
			return nil, err
		}
		l.Amount = d
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
