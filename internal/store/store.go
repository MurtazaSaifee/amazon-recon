// Package store owns the PostgreSQL schema: migrations, the run register,
// bulk writes of source lines, and reads of stored lines.
package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/source"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Connect opens a connection pool and checks that the database answers.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	return pool, nil
}

// Migrate applies the embedded migrations in filename order. Each migration
// runs once, in its own transaction, and is recorded in schema_migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		var applied bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", e.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		if err := applyMigration(ctx, pool, e.Name(), string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, name, sql string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reset drops and recreates the public schema, so the next migrate starts from an empty database.
func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
	return err
}

// FileRecord describes one ingested report file.
type FileRecord struct {
	Source         string
	FileName       string
	SHA256         string
	PhysicalLines  int
	StoredLines    int
	MalformedLines int
	Preamble       []string // payments only
}

// BeginRun records a new run as running and returns its ID.
func BeginRun(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, "INSERT INTO ingest_runs (status) VALUES ('running') RETURNING run_id").Scan(&id)
	return id, err
}

// FinishRun sets a run's final status. errText is empty on success.
func FinishRun(ctx context.Context, pool *pgxpool.Pool, runID int64, status, errText string) error {
	_, err := pool.Exec(ctx,
		"UPDATE ingest_runs SET status = $2, finished_at = now(), error = NULLIF($3, '') WHERE run_id = $1",
		runID, status, errText)
	return err
}

// RecordErrors stores the malformed lines of a run.
func RecordErrors(ctx context.Context, pool *pgxpool.Pool, runID int64, errs []source.ParseError) error {
	rows := make([][]any, 0, len(errs))
	for _, e := range errs {
		rows = append(rows, []any{runID, e.Source, e.LineNumber, e.Raw, e.Reason})
	}
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"ingest_errors"},
		[]string{"run_id", "source", "line_number", "raw_text", "reason"}, pgx.CopyFromRows(rows))
	return err
}

// RecordFile stores a file's ingest record inside the run's transaction.
func RecordFile(ctx context.Context, tx pgx.Tx, runID int64, f FileRecord) error {
	var preamble []byte
	if f.Preamble != nil {
		b, err := json.Marshal(f.Preamble)
		if err != nil {
			return err
		}
		preamble = b
	}
	_, err := tx.Exec(ctx, `INSERT INTO ingest_files
		(run_id, source, file_name, sha256, physical_lines, stored_lines, malformed_lines, preamble)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		runID, f.Source, f.FileName, f.SHA256, f.PhysicalLines, f.StoredLines, f.MalformedLines, preamble)
	return err
}

// SourceRow is one line ready to store, with its scope decided.
type SourceRow struct {
	FileName    string
	Line        source.Line
	InScope     bool
	ScopeReason string
}

// ReplaceSourceLines replaces every stored line of one report with rows, inside tx.
// Re-ingesting a report therefore never double-counts.
func ReplaceSourceLines(ctx context.Context, tx pgx.Tx, runID int64, reportSource string, rows []SourceRow) error {
	if _, err := tx.Exec(ctx, "DELETE FROM source_rows WHERE source = $1", reportSource); err != nil {
		return err
	}
	copyRows := make([][]any, 0, len(rows))
	for _, r := range rows {
		v, err := sourceRowValues(runID, r)
		if err != nil {
			return err
		}
		copyRows = append(copyRows, v)
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"source_rows"}, sourceRowColumns, pgx.CopyFromRows(copyRows))
	return err
}

var sourceRowColumns = []string{
	"run_id", "source", "line_kind", "file_name", "line_number", "raw_payload",
	"settlement_id", "txn_type", "amount_type", "description", "txn_ref", "sku", "status",
	"amount", "posted_date", "posted_date_basis", "currency", "period_start", "period_end", "deposit_date",
	"in_scope", "scope_reason",
}

func sourceRowValues(runID int64, r SourceRow) ([]any, error) {
	l := r.Line
	raw, err := json.Marshal(l.Raw)
	if err != nil {
		return nil, err
	}
	amount, err := numeric(l.Amount)
	if err != nil {
		return nil, err
	}
	return []any{
		runID, l.Source, l.Kind, r.FileName, l.LineNumber, raw,
		nullable(l.SettlementID), nullable(l.TxnType), nullable(l.AmountType), nullable(l.Description),
		nullable(l.TxnRef), nullable(l.SKU), nullable(l.Status),
		amount, optTime(l.PostedDate), nullable(l.PostedDateBasis),
		nullable(l.Currency), optTime(l.PeriodStart), optTime(l.PeriodEnd), optTime(l.DepositDate),
		r.InScope, r.ScopeReason,
	}, nil
}

// numeric converts a decimal to the NUMERIC(14,2) wire value without going through float64.
func numeric(d decimal.Decimal) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(d.StringFixed(2)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("amount %s: %w", d, err)
	}
	return n, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optTime maps a zero time to SQL NULL.
func optTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// LatestCompleteRun returns the most recent run that finished without errors.
func LatestCompleteRun(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var id *int64
	if err := pool.QueryRow(ctx, "SELECT max(run_id) FROM ingest_runs WHERE status = 'complete'").Scan(&id); err != nil {
		return 0, err
	}
	if id == nil {
		return 0, errors.New("no complete ingest run: run ingest first")
	}
	return *id, nil
}

// Line is one stored source line, as read back for reporting and tests.
type Line struct {
	Source          string
	LineKind        string
	FileName        string
	LineNumber      int
	Amount          decimal.Decimal
	PostedDate      time.Time // zero when the line has no posted date
	PostedDateBasis string
	InScope         bool
	ScopeReason     string
}

// LoadLine reads one stored line of a run.
func LoadLine(ctx context.Context, pool *pgxpool.Pool, runID int64, reportSource string, lineNumber int) (Line, error) {
	var (
		l      Line
		amount *string
		posted *time.Time
		basis  *string
	)
	err := pool.QueryRow(ctx, `SELECT source, line_kind, file_name, line_number, amount::text,
			posted_date, posted_date_basis, in_scope, scope_reason
		FROM source_rows WHERE run_id = $1 AND source = $2 AND line_number = $3`,
		runID, reportSource, lineNumber).Scan(
		&l.Source, &l.LineKind, &l.FileName, &l.LineNumber, &amount,
		&posted, &basis, &l.InScope, &l.ScopeReason)
	if err != nil {
		return Line{}, fmt.Errorf("load %s line %d: %w", reportSource, lineNumber, err)
	}
	if amount != nil {
		l.Amount, err = decimal.NewFromString(*amount)
		if err != nil {
			return Line{}, err
		}
	}
	if posted != nil {
		l.PostedDate = *posted
	}
	if basis != nil {
		l.PostedDateBasis = *basis
	}
	return l, nil
}
