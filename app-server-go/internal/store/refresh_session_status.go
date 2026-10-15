package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RefreshSessionStatusOutcome classifies the guarded status write result.
type RefreshSessionStatusOutcome uint8

const (
	// RefreshSessionStatusUnspecified is the zero value and never returned by
	// a successful call.
	RefreshSessionStatusUnspecified RefreshSessionStatusOutcome = iota
	// RefreshSessionStatusApplied means the daemon status was written.
	RefreshSessionStatusApplied
	// RefreshSessionStatusCurrent means the stored status already matched and
	// nothing was written.
	RefreshSessionStatusCurrent
	// RefreshSessionStatusGone means the row was deleted while the caller's
	// authority read was in flight.
	RefreshSessionStatusGone
	// RefreshSessionStatusRebound means the binding fields changed while the
	// caller's authority read was in flight.
	RefreshSessionStatusRebound
)

// RefreshSessionStatusExpectation pins the composite binding observed before
// an authority read so a late status write cannot land on a rebound row.
type RefreshSessionStatusExpectation struct {
	NativeID     string
	ConnectionID string
	ControlState string
	NativeKind   string
}

// RefreshSessionStatus applies one daemon-confirmed status to the exact
// composite session. The row is re-read and its binding re-checked inside the
// write transaction, so a deletion or rebinding that raced the caller's
// authority read is rejected instead of overwritten: a deleted row is never
// resurrected, a rebound row is never touched, and only status is written.
// Reconciliation preserves activity time and concurrent metadata edits
// (title, workspace, project, source). The UPDATE always targets the exact
// (agent_id, id) composite key, never a source identity.
func (s *Store) RefreshSessionStatus(ctx context.Context, agentID, sessionID string, expect RefreshSessionStatusExpectation, status string) (map[string]any, RefreshSessionStatusOutcome, error) {
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, RefreshSessionStatusUnspecified, errors.New("agent ID and session ID are required")
	}
	if strings.TrimSpace(expect.NativeID) == "" {
		return nil, RefreshSessionStatusUnspecified, errors.New("expected native session ID is required")
	}
	if strings.TrimSpace(status) == "" {
		return nil, RefreshSessionStatusUnspecified, errors.New("status is required")
	}
	var (
		row     map[string]any
		outcome = RefreshSessionStatusUnspecified
	)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			outcome = RefreshSessionStatusGone
			return nil
		}
		if !sessionBindingMatchesExpectation(rows[0], sessionID, expect) {
			outcome = RefreshSessionStatusRebound
			return nil
		}
		if refreshRowText(rows[0], "status") == status {
			outcome = RefreshSessionStatusCurrent
			row, err = sessionWire(rows[0])
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE sessions SET status=? WHERE agent_id=? AND id=?",
			status, agentID, sessionID); err != nil {
			return err
		}
		rows, err = queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, sessionID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			outcome = RefreshSessionStatusGone
			return nil
		}
		outcome = RefreshSessionStatusApplied
		row, err = sessionWire(rows[0])
		return err
	})
	if err != nil {
		return nil, RefreshSessionStatusUnspecified, err
	}
	if outcome == RefreshSessionStatusApplied || outcome == RefreshSessionStatusCurrent {
		return row, outcome, nil
	}
	return nil, outcome, nil
}

// sessionBindingMatchesExpectation compares the latest stored binding against
// the expectation using the same effective-identity rules as the projection:
// an absent source_session_id falls back to the session ID, and the local
// connection alias folds onto the empty connection. The runtime_owner JSON
// blob is not byte-compared (its formatting is writer-dependent); the binding
// fields compared here are the ones that select the daemon snapshot.
func sessionBindingMatchesExpectation(row map[string]any, sessionID string, expect RefreshSessionStatusExpectation) bool {
	nativeID := refreshRowText(row, "source_session_id")
	if nativeID == "" {
		nativeID = sessionID
	}
	if nativeID != expect.NativeID {
		return false
	}
	if normalizeRefreshConnection(refreshRowText(row, "connection_id")) != normalizeRefreshConnection(expect.ConnectionID) {
		return false
	}
	if refreshRowText(row, "control_state") != expect.ControlState {
		return false
	}
	return refreshRowText(row, "native_kind") == expect.NativeKind
}

func normalizeRefreshConnection(value string) string {
	if value == "local" {
		return ""
	}
	return value
}

// refreshRowText reads one raw row column as text; NULL becomes empty.
func refreshRowText(row map[string]any, key string) string {
	value := row[key]
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
