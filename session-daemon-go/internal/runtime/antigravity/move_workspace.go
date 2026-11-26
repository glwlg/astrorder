package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func isWindowsDrivePath(p string) bool {
	return len(p) >= 2 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':'
}

func workspaceToFileURI(workspace string, posixPaths bool) (string, string, error) {
	ws := strings.TrimSpace(workspace)
	if ws == "" || strings.ContainsRune(ws, 0) {
		return "", "", fmt.Errorf("Antigravity target workspace is required")
	}
	if posixPaths {
		if !strings.HasPrefix(ws, "/") {
			return "", "", fmt.Errorf("Antigravity remote target workspace must be an absolute POSIX path")
		}
		clean := path.Clean(ws)
		u := url.URL{Scheme: "file", Path: clean}
		return clean, u.String(), nil
	}
	if isWindowsDrivePath(ws) || runtime.GOOS == "windows" {
		slashed := strings.ReplaceAll(ws, "\\", "/")
		if isWindowsDrivePath(slashed) {
			drive := strings.ToUpper(slashed[:1])
			rest := slashed[2:]
			if rest == "" {
				rest = "/"
			}
			cleanRest := path.Clean("/" + rest)
			canonical := drive + ":" + cleanRest
			if runtime.GOOS == "windows" {
				canonical = filepath.FromSlash(canonical)
			}
			u := url.URL{Scheme: "file", Path: "/" + drive + ":" + cleanRest}
			return canonical, u.String(), nil
		}
		abs, err := filepath.Abs(ws)
		if err != nil {
			return "", "", fmt.Errorf("Antigravity target workspace is invalid")
		}
		clean := filepath.Clean(abs)
		uriPath := filepath.ToSlash(clean)
		if !strings.HasPrefix(uriPath, "/") {
			uriPath = "/" + uriPath
		}
		u := url.URL{Scheme: "file", Path: uriPath}
		return clean, u.String(), nil
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", "", fmt.Errorf("Antigravity target workspace is invalid")
	}
	clean := filepath.Clean(abs)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(clean)}
	return clean, u.String(), nil
}

// moveWorkspaceForHome atomically rewrites conversation_summaries.workspace_uris
// so an idle CLI Antigravity conversation belongs to the target workspace.
func moveWorkspaceForHome(ctx context.Context, home, sessionID, workspace string, posixPaths bool, allowJustClosed bool) (string, string, error) {
	if !validID(sessionID) {
		return "", "", &RemoteHelperError{Message: "invalid Antigravity conversation ID", BeforeDispatch: true}
	}
	cleanWS, targetURI, err := workspaceToFileURI(workspace, posixPaths)
	if err != nil {
		return "", "", &RemoteHelperError{Message: err.Error(), BeforeDispatch: true}
	}
	info, statErr := os.Stat(cleanWS)
	if statErr != nil || !info.IsDir() {
		return "", "", &RemoteHelperError{Message: "Antigravity target workspace must be an existing directory", BeforeDispatch: true}
	}

	sumPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	db, err := openNativeRW(sumPath)
	if os.IsNotExist(err) {
		if isDesktopOnlyConversation(home, sessionID) {
			return "", "", &RemoteHelperError{Message: "Antigravity workspace move requires an idle owned CLI conversation", BeforeDispatch: true}
		}
		return "", "", &RemoteHelperError{Message: "Antigravity conversation catalog not found", BeforeDispatch: true}
	}
	if err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	defer db.Close()

	var active, killed bool
	err = db.QueryRowContext(ctx, "SELECT not_fully_idle, killed FROM conversation_summaries WHERE conversation_id=?", sessionID).Scan(&active, &killed)
	if err == sql.ErrNoRows {
		if isDesktopOnlyConversation(home, sessionID) {
			return "", "", &RemoteHelperError{Message: "Antigravity workspace move requires an idle owned CLI conversation", BeforeDispatch: true}
		}
		return "", "", &RemoteHelperError{Message: "Antigravity conversation was not found in CLI catalog", BeforeDispatch: true}
	}
	if err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	if killed {
		return "", "", &RemoteHelperError{Message: "Antigravity conversation has already been deleted", BeforeDispatch: true}
	}
	if !allowJustClosed && active {
		return "", "", &RemoteHelperError{Message: "Antigravity workspace move requires an idle CLI conversation", BeforeDispatch: true}
	}

	encodedURIs, err := json.Marshal([]string{targetURI})
	if err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	res, err := tx.ExecContext(ctx, "UPDATE conversation_summaries SET workspace_uris=? WHERE conversation_id=? AND killed=0", string(encodedURIs), sessionID)
	if err != nil {
		_ = tx.Rollback()
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		_ = tx.Rollback()
		return "", "", &RemoteHelperError{Message: "Antigravity conversation was not found in CLI catalog", BeforeDispatch: true}
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}

	var storedURIs string
	if err := db.QueryRowContext(ctx, "SELECT workspace_uris FROM conversation_summaries WHERE conversation_id=?", sessionID).Scan(&storedURIs); err != nil {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	var parsed []string
	if json.Unmarshal([]byte(storedURIs), &parsed) != nil || len(parsed) == 0 || parsed[0] != targetURI {
		return "", "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
	}
	return cleanWS, targetURI, nil
}

func (a *Adapter) moveConversationStorage(ctx context.Context, sessionID, targetWorkspace string, allowJustClosed bool) (string, error) {
	if a.remoteHelper != nil {
		res, err := a.remoteHelper(ctx, map[string]any{
			"domain":            "antigravity",
			"action":            "move_workspace",
			"conversation_id":   sessionID,
			"workspace":         targetWorkspace,
			"allow_just_closed": allowJustClosed,
		})
		if err != nil {
			var rhe *RemoteHelperError
			if errors.As(err, &rhe) && rhe.BeforeDispatch {
				return "", core.RejectBeforeDispatch(rhe.Message)
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
		}
		if res["confirmed"] != true {
			return "", fmt.Errorf("Antigravity native workspace move is unconfirmed; reconcile before retry")
		}
		if ws := str(res, "workspace"); ws != "" {
			return ws, nil
		}
		return targetWorkspace, nil
	}
	cleanWS, _, err := moveWorkspaceForHome(ctx, a.config.Home, sessionID, targetWorkspace, false, allowJustClosed)
	var rhe *RemoteHelperError
	if errors.As(err, &rhe) && rhe.BeforeDispatch {
		return "", core.RejectBeforeDispatch(rhe.Message)
	}
	return cleanWS, err
}

func (a *Adapter) moveConversationWorkspace(ctx context.Context, s *session, rawWorkspace string) (core.Result, error) {
	if strings.TrimSpace(rawWorkspace) == "" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity target workspace is required")
	}
	targetCwd, err := a.workspace(rawWorkspace)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}

	s.mu.Lock()
	if s.control || s.readonly || s.closed || s.deletePending {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity workspace move requires an idle owned CLI conversation")
	}
	settledError := s.status == "error" && !s.promptRunning && s.command == ""
	if s.status != "idle" && !settledError {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity workspace move requires an idle owned CLI conversation")
	}
	wasOwnedSession := !s.deleteOnly && s.client != nil
	c := s.client
	s.status = "running"
	s.mu.Unlock()

	if wasOwnedSession && c != nil && !c.exited() && c.hasClose {
		closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
		_, closeErr := c.call(closeCtx, "session/close", map[string]any{"sessionId": s.id})
		closeCancel()
		if closeErr != nil && !c.exited() {
			var rpcErr *acpRPCError
			if !asACPRPCError(closeErr, &rpcErr) || rpcErr.Code != -32601 {
				s.mu.Lock()
				s.status = "idle"
				s.mu.Unlock()
				return core.Result{}, fmt.Errorf("Antigravity failed to release active session before workspace move: %w", closeErr)
			}
		}
	}

	confirmedWS, err := a.moveConversationStorage(ctx, s.id, targetCwd, wasOwnedSession)
	if err != nil {
		var rejected *core.RejectedBeforeDispatch
		s.mu.Lock()
		if errors.As(err, &rejected) {
			s.status = "idle"
		} else {
			s.status = "error"
		}
		s.mu.Unlock()
		return core.Result{}, err
	}

	s.mu.Lock()
	s.cwd = confirmedWS
	s.closed = true
	s.status = "idle"
	s.mu.Unlock()
	s.stopWorker()

	a.mu.Lock()
	delete(a.sessions, s.id)
	delete(a.catalog, s.id)
	a.mu.Unlock()

	result := a.result(s)
	result.Payload["workspace"] = confirmedWS
	result.Payload["moved"] = true
	result.Payload["closed"] = true
	return result, nil
}
