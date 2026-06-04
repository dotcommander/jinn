package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// runIdempotent executes fn inside a transaction with optional deduplication.
//
// requestID == "" → plain transact; no idempotency row written.
// requestID != "" → one transaction: INSERT idempotency row, run fn, UPDATE
// result_json, commit. A duplicate (agent, requestID) replays the cached
// result without re-running fn. A row with empty result_json (crash mid-flight)
// returns ErrCodeConflict so the caller can retry later.
//
// Replay returns the exact JSON string from the first call, byte-identical.
//
// idempotentRequest carries the data fields for runIdempotent. ctx and the
// *sql.DB handle stay positional; everything else (including the operation fn)
// lives here so the signature stays under the argument limit.
type idempotentRequest struct {
	agent     string
	requestID string
	command   string
	fn        func(tx *sql.Tx) (any, error)
}

func runIdempotent(ctx context.Context, db *sql.DB, req idempotentRequest) (any, error) {
	if req.requestID == "" {
		return runPlainTransact(ctx, db, req.fn)
	}

	// Single transaction: begin + work + complete.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	// Attempt to claim the slot.
	_, insertErr := tx.ExecContext(ctx,
		`INSERT INTO idempotency(agent_name, request_id, command, result_json) VALUES(?,?,?,'')`,
		req.agent, req.requestID, req.command,
	)
	if insertErr != nil {
		_ = tx.Rollback()
		return replayIdempotent(ctx, db, req.agent, req.requestID, insertErr)
	}

	// Slot claimed — run the work.
	result, fnErr := req.fn(tx)
	if fnErr != nil {
		_ = tx.Rollback()
		return nil, fnErr
	}

	return completeIdempotent(ctx, tx, req.agent, req.requestID, result)
}

// runPlainTransact runs fn in a transaction without writing an idempotency row.
// Returns the core's result as-is when it is already a string (already
// serialized); otherwise marshals to JSON.
func runPlainTransact(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) (any, error)) (any, error) {
	var result any
	err := transact(ctx, db, func(tx *sql.Tx) error {
		r, e := fn(tx)
		result = r
		return e
	})
	if err != nil {
		return nil, err
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	b, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal result: %w", marshalErr)
	}
	return string(b), nil
}

// replayIdempotent handles a failed slot-claim INSERT. A non-unique error is
// surfaced; a unique violation means the row exists, so the cached result is
// loaded and replayed (or ErrCodeConflict if a prior attempt crashed mid-flight).
func replayIdempotent(ctx context.Context, db *sql.DB, agent, requestID string, insertErr error) (any, error) {
	if !isUniqueConstraintErr(insertErr) {
		return nil, fmt.Errorf("insert idempotency row: %w", insertErr)
	}
	// Row exists — load cached result.
	var resultJSON string
	if selErr := db.QueryRowContext(ctx,
		`SELECT result_json FROM idempotency WHERE agent_name=? AND request_id=?`,
		agent, requestID,
	).Scan(&resultJSON); selErr != nil {
		return nil, fmt.Errorf("load idempotency row: %w", selErr)
	}
	if strings.TrimSpace(resultJSON) == "" {
		// Prior attempt crashed before completing.
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("request %q is still in progress or crashed; retry later", requestID),
			Suggestion: "wait and retry with the same request_id",
			Code:       ErrCodeConflict,
		}
	}
	// Replay: return raw JSON string — byte-identical to first call.
	return resultJSON, nil
}

// completeIdempotent serializes result, writes it to the claimed row, and commits.
// String results are already serialized; non-string results are marshaled to
// JSON. Rolls back tx on any failure before commit. Returns the raw JSON string
// so replay and first call are byte-identical.
func completeIdempotent(ctx context.Context, tx *sql.Tx, agent, requestID string, result any) (any, error) {
	var resultJSON string
	if s, ok := result.(string); ok {
		resultJSON = s
	} else {
		b, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("marshal idempotency result: %w", marshalErr)
		}
		resultJSON = string(b)
	}

	if _, updErr := tx.ExecContext(ctx,
		`UPDATE idempotency SET result_json=? WHERE agent_name=? AND request_id=?`,
		resultJSON, agent, requestID,
	); updErr != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("complete idempotency row: %w", updErr)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("commit idempotent tx: %w", commitErr)
	}

	return resultJSON, nil
}

// isUniqueConstraintErr returns true when err is a SQLite UNIQUE or PRIMARY KEY
// constraint violation. Checks typed error first, falls back to string matching.
//
//	SQLITE_CONSTRAINT_UNIQUE      = 2067  (19 | (11 << 8))
//	SQLITE_CONSTRAINT_PRIMARYKEY  = 1555  (19 | (6  << 8))
func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		c := sqliteErr.Code()
		return c == 2067 || c == 1555
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "PRIMARY KEY constraint failed")
}
