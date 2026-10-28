package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FixResult is one statement of a mapping-fix file and the rows it changed.
type FixResult struct {
	Number       int    // 1-based, in file order
	Head         string // the start of the statement, for messages
	RowsAffected int64
}

// ApplyFixes runs a mapping-fix file in one transaction. The file is split into statements, and each one
// runs in order. Every statement must change at least one row: a guard that matches no row means the fix
// no longer applies to the config, so the apply fails and nothing is committed. It returns one result
// per statement; a file of comments only has none.
func ApplyFixes(ctx context.Context, pool *pgxpool.Pool, sql string) ([]FixResult, error) {
	stmts := SplitStatements(sql)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply fixes: %w", err)
	}
	defer tx.Rollback(ctx)

	results := make([]FixResult, 0, len(stmts))
	for i, s := range stmts {
		head := statementHead(s)
		tag, err := tx.Exec(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("fix statement %d (%s): %w (no fix was applied)", i+1, head, err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("fix statement %d (%s) matched no rows: its guard no longer matches the config, "+
				"so the fix was not applied and nothing was committed", i+1, head)
		}
		results = append(results, FixResult{Number: i + 1, Head: head, RowsAffected: tag.RowsAffected()})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("apply fixes: commit: %w", err)
	}
	return results, nil
}

// SplitStatements splits SQL into its statements at semicolons. Line comments ("--" to the end of the
// line) and single-quoted strings, including doubled quotes, are read as text, so a comment or a value
// that contains a semicolon does not end a statement. Blank statements are dropped.
func SplitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	inString := false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case inString:
			cur.WriteByte(c)
			if c == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					cur.WriteByte('\'')
					i++
				} else {
					inString = false
				}
			}
		case c == '\'':
			inString = true
			cur.WriteByte(c)
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			cur.WriteByte('\n')
		case c == ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// statementHead returns the first words of a statement, on one line, for messages.
func statementHead(s string) string {
	h := strings.Join(strings.Fields(s), " ")
	if len(h) > 60 {
		h = h[:60] + "..."
	}
	return h
}
