package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

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

// SetRunConfig records the config hash, mode and enabled interpretation switches a run was produced with.
func SetRunConfig(ctx context.Context, tx pgx.Tx, runID int64, configHash, mode string, switches []string) error {
	if switches == nil {
		switches = []string{}
	}
	_, err := tx.Exec(ctx, "UPDATE ingest_runs SET config_hash = $2, mode = $3, switches = $4 WHERE run_id = $1",
		runID, configHash, mode, switches)
	return err
}

// ConfigHash is the SHA-256 (hex) of the mapping rules and the Summary-line map, read inside tx.
// Each table is read in rule order (csv_line for the rules, display_order for the Summary lines)
// and each row is hashed as its JSON text, so any change to a rule, its order or the Summary-line
// map changes the hash.
func ConfigHash(ctx context.Context, tx pgx.Tx) (string, error) {
	sections := []struct{ name, query string }{
		{"payment_mapping_rules", "SELECT to_jsonb(t)::text FROM payment_mapping_rules t ORDER BY csv_line"},
		{"settlement_mapping_rules", "SELECT to_jsonb(t)::text FROM settlement_mapping_rules t ORDER BY csv_line"},
		{"summary_line_map", "SELECT to_jsonb(t)::text FROM summary_line_map t ORDER BY display_order"},
	}
	h := sha256.New()
	for _, s := range sections {
		fmt.Fprintf(h, "[%s]\n", s.name)
		rows, err := tx.Query(ctx, s.query)
		if err != nil {
			return "", fmt.Errorf("hash %s: %w", s.name, err)
		}
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				rows.Close()
				return "", err
			}
			fmt.Fprintln(h, text)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("hash %s: %w", s.name, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
