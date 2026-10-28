// Package recon builds the reconciliation report data for one ingest run.
// Totals are computed in SQL over the stored lines; the report writer only formats them.
package recon

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Total is a count of lines and the sum of their amounts.
type Total struct {
	Count  int
	Amount decimal.Decimal
}

// SettlementHeader is the Settlement report's header line: the Settlement under reconciliation.
type SettlementHeader struct {
	ID          string
	Total       decimal.Decimal
	Currency    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	DepositDate time.Time
	FileName    string
	LineNumber  int
}

// FileRecord is the ingest record of one report file.
type FileRecord struct {
	Source         string
	FileName       string
	SHA256         string
	PhysicalLines  int
	StoredLines    int
	MalformedLines int
}

// Report is the data behind the tie-out. It holds numbers only; the workbook writer formats them.
type Report struct {
	RunID          int64
	StartedAt      time.Time
	FinishedAt     time.Time
	ConfigHash     string   // SHA-256 (hex) of the mapping configs the run used
	Mode           string   // "fixed" or "baseline"
	Switches       []string // enabled interpretation switches, sorted; empty for a baseline run
	MalformedLines int      // malformed lines of both reports, all stored in ingest_errors
	Settlement     SettlementHeader
	Files          []FileRecord

	Components          Total // in-scope Settlement components
	TransactionsInScope Total // in-scope payments lines (Released), Unreconciled ones included
	Deferred            Total // memo: Deferred lines of this Settlement, out of scope

	// ReleasedExcludingUnreconciled is TransactionsInScope minus the payments lines of Unreconciled
	// payment records, so it is the part of the payments total that a record accounts for.
	ReleasedExcludingUnreconciled decimal.Decimal

	ComponentsMinusTotal                    decimal.Decimal // should be zero
	ReleasedExcludingUnreconciledMinusTotal decimal.Decimal // should be zero

	Summary        Summary
	TieOut         TieOut
	Reconciliation Reconciliation
	Audit          []AuditRow // one row per Contribution, in source and line order
}

// Build reads the report data for one completed run.
func Build(ctx context.Context, pool *pgxpool.Pool, runID int64) (Report, error) {
	rep := Report{RunID: runID}

	var status string
	if err := pool.QueryRow(ctx,
		"SELECT status, started_at, COALESCE(finished_at, started_at) FROM ingest_runs WHERE run_id = $1",
		runID).Scan(&status, &rep.StartedAt, &rep.FinishedAt); err != nil {
		return Report{}, fmt.Errorf("run %d: %w", runID, err)
	}
	if status != "complete" {
		return Report{}, fmt.Errorf("run %d is %s, not complete", runID, status)
	}
	if err := pool.QueryRow(ctx, "SELECT COALESCE(config_hash, ''), mode, switches FROM ingest_runs WHERE run_id = $1",
		runID).Scan(&rep.ConfigHash, &rep.Mode, &rep.Switches); err != nil {
		return Report{}, fmt.Errorf("config of run %d: %w", runID, err)
	}

	var totalText string
	if err := pool.QueryRow(ctx, `SELECT settlement_id, amount::text, currency, period_start, period_end,
			deposit_date, file_name, line_number
		FROM source_rows WHERE run_id = $1 AND line_kind = 'settlement_header'`, runID).Scan(
		&rep.Settlement.ID, &totalText, &rep.Settlement.Currency, &rep.Settlement.PeriodStart,
		&rep.Settlement.PeriodEnd, &rep.Settlement.DepositDate, &rep.Settlement.FileName, &rep.Settlement.LineNumber,
	); err != nil {
		return Report{}, fmt.Errorf("settlement header of run %d: %w", runID, err)
	}
	total, err := decimal.NewFromString(totalText)
	if err != nil {
		return Report{}, err
	}
	rep.Settlement.Total = total

	rows, err := pool.Query(ctx, `SELECT source, file_name, sha256, physical_lines, stored_lines, malformed_lines
		FROM ingest_files WHERE run_id = $1 ORDER BY source`, runID)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.Source, &f.FileName, &f.SHA256, &f.PhysicalLines, &f.StoredLines, &f.MalformedLines); err != nil {
			rows.Close()
			return Report{}, err
		}
		rep.Files = append(rep.Files, f)
		rep.MalformedLines += f.MalformedLines
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Report{}, err
	}

	var comp, pay, def Total
	var compAmt, payAmt, defAmt string
	if err := pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE source = 'settlement' AND line_kind = 'component' AND in_scope),
			COALESCE(sum(amount) FILTER (WHERE source = 'settlement' AND line_kind = 'component' AND in_scope), 0)::text,
			count(*) FILTER (WHERE source = 'payments' AND in_scope),
			COALESCE(sum(amount) FILTER (WHERE source = 'payments' AND in_scope), 0)::text,
			count(*) FILTER (WHERE source = 'payments' AND scope_reason = 'Deferred'),
			COALESCE(sum(amount) FILTER (WHERE source = 'payments' AND scope_reason = 'Deferred'), 0)::text
		FROM source_rows WHERE run_id = $1`, runID).Scan(
		&comp.Count, &compAmt, &pay.Count, &payAmt, &def.Count, &defAmt,
	); err != nil {
		return Report{}, fmt.Errorf("totals of run %d: %w", runID, err)
	}
	for _, p := range []struct {
		t *Total
		s string
	}{{&comp, compAmt}, {&pay, payAmt}, {&def, defAmt}} {
		if p.t.Amount, err = decimal.NewFromString(p.s); err != nil {
			return Report{}, err
		}
	}

	rep.Components = comp
	rep.TransactionsInScope = pay
	rep.Deferred = def
	rep.ComponentsMinusTotal = comp.Amount.Sub(total)

	if err := buildSummary(ctx, pool, runID, &rep); err != nil {
		return Report{}, err
	}
	rep.ReleasedExcludingUnreconciled = pay.Amount.Sub(rep.TieOut.Payments.Unreconciled.Amount)
	rep.ReleasedExcludingUnreconciledMinusTotal = rep.ReleasedExcludingUnreconciled.Sub(total)
	if err := buildReconciliation(ctx, pool, runID, &rep); err != nil {
		return Report{}, err
	}
	if err := readAuditTrail(ctx, pool, runID, &rep); err != nil {
		return Report{}, err
	}
	return rep, nil
}
