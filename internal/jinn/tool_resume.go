package jinn

import (
	"context"
	"database/sql"
	"time"
)

// resumeTool builds a brief packet via the deterministic 5-rule focus selection.
// peek=true performs ZERO writes (no row create, no cursor advance, no focus
// persist); resume=false advances atomically in one tx.
func (e *Engine) resumeTool(ctx context.Context, args map[string]interface{}) (string, error) {
	agent := resolveAgent(args)
	projectID := e.resolveProjectID(args)
	peek := boolArg(args, "peek")
	limit := intArg(args, "limit", 20) // intArg caps nothing; clamp below
	if limit > 100 {
		limit = 100
	}
	asOf := time.Now()

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	if peek {
		return e.resumePeek(ctx, db, agent, projectID, limit, asOf)
	}
	return e.resumeAdvance(ctx, db, agent, projectID, limit, asOf)
}

// resumePeek: read-only. No agent_state row creation. Empty-state cursor when
// the agent has no row yet.
func (e *Engine) resumePeek(ctx context.Context, db *sql.DB, agent, projectID string, limit int, asOf time.Time) (string, error) {
	var packet *BriefPacket
	err := transact(ctx, db, func(tx *sql.Tx) error {
		state, lErr := loadAgentStateTx(ctx, tx, agent)
		if lErr != nil {
			return lErr
		}
		// Focus project: persisted focus wins, else the requested project arg.
		focusProj := state.ProjectID
		if focusProj == "" {
			focusProj = projectID
		}
		deltas, newCursor, dErr := fetchDeltasTx(ctx, tx, state.Cursor, focusProj, limit)
		if dErr != nil {
			return dErr
		}
		focus, fErr := determineFocusTx(ctx, tx, state.TaskID, deltas, focusProj)
		if fErr != nil {
			return fErr
		}
		b, bErr := buildBriefTx(ctx, tx, focus.TaskID, focusProj, asOf)
		if bErr != nil {
			return bErr
		}
		b.FocusRule = focus.Rule
		b.Cursor = CursorWindow{Old: state.Cursor, New: state.Cursor} // peek never advances
		b.Deltas = deltas
		_ = newCursor
		packet = b
		return nil
	})
	if err != nil {
		return "", err
	}
	return finalizePacket(packet)
}

// resumeAdvance: create row if absent, determine focus, persist atomically.
func (e *Engine) resumeAdvance(ctx context.Context, db *sql.DB, agent, projectID string, limit int, asOf time.Time) (string, error) {
	var packet *BriefPacket
	err := transact(ctx, db, func(tx *sql.Tx) error {
		state, lErr := loadOrCreateAgentStateTx(ctx, tx, agent)
		if lErr != nil {
			return lErr
		}
		focusProj := state.ProjectID
		if focusProj == "" {
			focusProj = projectID
		}
		deltas, newCursor, dErr := fetchDeltasTx(ctx, tx, state.Cursor, focusProj, limit)
		if dErr != nil {
			return dErr
		}
		focus, fErr := determineFocusTx(ctx, tx, state.TaskID, deltas, focusProj)
		if fErr != nil {
			return fErr
		}
		b, bErr := buildBriefTx(ctx, tx, focus.TaskID, focusProj, asOf)
		if bErr != nil {
			return bErr
		}
		if pErr := persistAgentStateTx(ctx, tx, agent, newCursor, focus.TaskID, focusProj); pErr != nil {
			return pErr
		}
		b.FocusRule = focus.Rule
		b.Cursor = CursorWindow{Old: state.Cursor, New: maxInt64(state.Cursor, newCursor)}
		b.Deltas = deltas
		packet = b
		return nil
	})
	if err != nil {
		return "", err
	}
	return finalizePacket(packet)
}

// finalizePacket marshals once for approx_tokens, sets it, then marshals final.
func finalizePacket(b *BriefPacket) (string, error) {
	s, err := marshalJSON(b)
	if err != nil {
		return "", err
	}
	b.ApproxTokens = approxTokens(s)
	return marshalJSON(b)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
