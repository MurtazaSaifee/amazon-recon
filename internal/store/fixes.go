package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplyFixes runs a mapping-fix file in one transaction. Every statement applies, or none does.
// The file is run as given through the simple protocol, so it may hold several statements; a
// file of comments only does nothing.
func ApplyFixes(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("apply fixes: %w (no fix was applied)", err)
	}
	return tx.Commit(ctx)
}
