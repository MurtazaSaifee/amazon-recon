package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MalformedLine is a line that could not be read, stored with its file and the scope decided for it.
type MalformedLine struct {
	FileName    string
	Source      string
	LineNumber  int
	RawText     string
	Reason      string
	InScope     bool
	ScopeReason string
}

// RecordErrors stores the malformed lines of a run.
func RecordErrors(ctx context.Context, pool *pgxpool.Pool, runID int64, lines []MalformedLine) error {
	if len(lines) == 0 {
		return nil
	}
	rows := make([][]any, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, []any{runID, l.Source, l.LineNumber, l.RawText, l.Reason, l.FileName, l.InScope, l.ScopeReason})
	}
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"ingest_errors"},
		[]string{"run_id", "source", "line_number", "raw_text", "reason", "file_name", "in_scope", "scope_reason"},
		pgx.CopyFromRows(rows))
	return err
}
