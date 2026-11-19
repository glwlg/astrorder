package antigravity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func openNativeRW(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(p)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(3000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func removeConversationFilesForHome(home, sessionID string) {
	if home == "" || !validID(sessionID) {
		return
	}
	base := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", sessionID+".db")
	for _, p := range []string{
		base,
		base + "-wal",
		base + "-shm",
		filepath.Join(home, ".gemini", "antigravity-cli", "annotations", sessionID+".pb"),
	} {
		_ = os.Remove(p)
	}
}

func isDesktopOnlyConversation(home, sessionID string) bool {
	if home == "" || !validID(sessionID) {
		return false
	}
	cliConv := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", sessionID+".db")
	if _, err := os.Stat(cliConv); err == nil {
		return false
	}
	for _, ext := range []string{".db", ".pb"} {
		desk := filepath.Join(home, ".gemini", "antigravity", "conversations", sessionID+ext)
		if _, err := os.Stat(desk); err == nil {
			return true
		}
	}
	return false
}

// deleteUnboundForHome removes an idle CLI conversation directly from the user's
// local Antigravity SQLite catalog and conversation files.
func deleteUnboundForHome(ctx context.Context, home, sessionID string) error {
	return deleteConversationForHome(ctx, home, sessionID, false)
}

func deleteConversationForHome(ctx context.Context, home, sessionID string, allowJustClosed bool) error {
	if !validID(sessionID) {
		return &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
	}
	sumPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	db, err := openNativeRW(sumPath)
	if os.IsNotExist(err) {
		if isDesktopOnlyConversation(home, sessionID) {
			return &RemoteHelperError{Message: "Antigravity deletion requires an idle owned CLI conversation", BeforeDispatch: true}
		}
		removeConversationFilesForHome(home, sessionID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	defer db.Close()

	var exists int
	err = db.QueryRowContext(ctx, "SELECT 1 FROM conversation_summaries WHERE conversation_id=?", sessionID).Scan(&exists)
	if err == sql.ErrNoRows {
		if isDesktopOnlyConversation(home, sessionID) {
			return &RemoteHelperError{Message: "Antigravity deletion requires an idle owned CLI conversation", BeforeDispatch: true}
		}
		removeConversationFilesForHome(home, sessionID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}

	if !allowJustClosed {
		var active bool
		if scanErr := db.QueryRowContext(ctx, "SELECT not_fully_idle FROM conversation_summaries WHERE conversation_id=?", sessionID).Scan(&active); scanErr == nil && active {
			return &RemoteHelperError{Message: "Antigravity deletion requires an idle owned CLI conversation", BeforeDispatch: true}
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	_, _ = tx.ExecContext(ctx, "DELETE FROM conversations_fts WHERE conversation_id=?", sessionID)
	_, _ = tx.ExecContext(ctx, "DELETE FROM conversation_parents WHERE conversation_id=?", sessionID)
	if _, err := tx.ExecContext(ctx, "DELETE FROM conversation_summaries WHERE conversation_id=?", sessionID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	db.Close()

	removeConversationFilesForHome(home, sessionID)
	return confirmDeletedForHome(ctx, home, sessionID)
}

func (a *Adapter) deleteConversationStorage(ctx context.Context, sessionID string, allowJustClosed bool) error {
	if a.remoteHelper != nil {
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":            "antigravity",
			"action":            "delete_unbound",
			"conversation_id":   sessionID,
			"allow_just_closed": allowJustClosed,
		})
		if err != nil {
			var rhe *RemoteHelperError
			if errors.As(err, &rhe) && rhe.BeforeDispatch {
				return core.RejectBeforeDispatch(rhe.Message)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		}
		if res["confirmed"] != true {
			return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		}
		return nil
	}
	err := deleteConversationForHome(ctx, a.config.Home, sessionID, allowJustClosed)
	var rhe *RemoteHelperError
	if errors.As(err, &rhe) && rhe.BeforeDispatch {
		return core.RejectBeforeDispatch(rhe.Message)
	}
	return err
}

func confirmDeletedForHome(ctx context.Context, home, sessionID string) error {
	db, err := openNative(filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
	}
	defer db.Close()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var id string
		err = db.QueryRowContext(ctx, "SELECT conversation_id FROM conversation_summaries WHERE conversation_id=?", sessionID).Scan(&id)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Antigravity native deletion is unconfirmed; reconcile before retry")
		case <-ticker.C:
		}
	}
}

func (a *Adapter) deleteConversation(ctx context.Context, s *session) (core.Result, error) {
	s.mu.Lock()
	if s.control || s.readonly || s.closed {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity deletion requires an idle owned CLI conversation")
	}
	settledError := s.status == "error" && !s.promptRunning && s.command == ""
	if s.status != "idle" && !settledError && !(s.deletePending && s.status == "error") {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity deletion requires an idle owned CLI conversation")
	}
	wasOwnedSession := !s.deleteOnly && s.client != nil
	c := s.client
	s.status = "running"
	s.deletePending = true
	s.mu.Unlock()

	if wasOwnedSession && c != nil && !c.exited() && c.hasClose {
		closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = c.call(closeCtx, "session/close", map[string]any{"sessionId": s.id})
		closeCancel()
	}

	if err := a.deleteConversationStorage(ctx, s.id, wasOwnedSession); err != nil {
		var rejected *core.RejectedBeforeDispatch
		s.mu.Lock()
		if errors.As(err, &rejected) {
			s.deletePending = false
			s.status = "idle"
		} else {
			s.status = "error"
		}
		s.mu.Unlock()
		return core.Result{}, err
	}

	s.mu.Lock()
	s.closed = true
	s.deletePending = false
	s.status = "idle"
	s.mu.Unlock()
	s.stopWorker()

	a.mu.Lock()
	delete(a.sessions, s.id)
	delete(a.catalog, s.id)
	a.mu.Unlock()

	result := a.result(s)
	result.Payload["deleted"] = s.id
	return result, nil
}
