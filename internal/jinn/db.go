package jinn

import (
	"context"
	"database/sql"
	"fmt"
)

// db.go holds shared low-level database helpers used across the memory and idempotency stores.

// transact runs fn inside a DB transaction, rolling back on error.
func transact(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
