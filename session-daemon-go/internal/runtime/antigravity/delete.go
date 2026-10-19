package antigravity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func (a *Adapter) deleteConversation(ctx context.Context, s *session) (core.Result, error) {
	s.mu.Lock()
	if s.control || s.readonly || s.closed || s.status != "idle" && !(s.deletePending && s.status == "error") {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity deletion requires an idle owned CLI conversation")
	}
	pending := s.deletePending
	s.status = "running"
	s.deletePending = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if !s.closed && s.status == "running" {
			s.status = "idle"
			if s.deletePending {
				s.status = "error"
			}
		}
		s.mu.Unlock()
	}()
	if !pending {
		if _, err := a.nativeCall(ctx, s, "DeleteCascadeTrajectory", map[string]any{"cascadeId": s.id}); err != nil {
			var rejected *core.RejectedBeforeDispatch
			if errors.As(err, &rejected) {
				s.mu.Lock()
				s.deletePending = false
				s.mu.Unlock()
			}
			return core.Result{}, err
		}
	}
	// The native catalog is authoritative; agy can retain an orphan history file.
	db, err := openNative(filepath.Join(a.config.Home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if err != nil {
		return core.Result{}, fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	defer db.Close()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var id string
		err = db.QueryRowContext(ctx, "SELECT conversation_id FROM conversation_summaries WHERE conversation_id=?", s.id).Scan(&id)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return core.Result{}, fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		}
		select {
		case <-ctx.Done():
			return core.Result{}, ctx.Err()
		case <-deadline.C:
			return core.Result{}, fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		case <-ticker.C:
		}
	}
	s.mu.Lock()
	s.closed = true
	s.status = "idle"
	s.mu.Unlock()
	if s.owner != nil {
		if err := s.owner.Close(); err != nil {
			return core.Result{}, err
		}
		<-s.done
	}
	a.mu.Lock()
	delete(a.sessions, s.id)
	delete(a.catalog, s.id)
	a.mu.Unlock()
	result := a.result(s)
	result.Payload["deleted"] = s.id
	return result, nil
}
