package hermes

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	platform "runtime"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func sameMoveWorkspacePath(a, b string) bool {
	na := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(a), "\\", "/"), "/")
	nb := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(b), "\\", "/"), "/")
	if na == "" && nb == "" {
		return true
	}
	if na == "" || nb == "" {
		return false
	}
	if platform.GOOS == "windows" {
		return strings.EqualFold(na, nb)
	}
	return na == nb
}

func updateHermesStateDBWorkspace(ctx context.Context, dbPath, sessionID, workspace string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open Hermes state.db: %w", err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Hermes workspace update: %w", err)
	}
	res, err := tx.ExecContext(ctx, "UPDATE sessions SET cwd = ? WHERE id = ?", workspace, sessionID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update Hermes workspace: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		_ = tx.Rollback()
		return core.RejectBeforeDispatch("Hermes session was not found in native state")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Hermes workspace update: %w", err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(cwd, '') FROM sessions WHERE id = ?", sessionID).Scan(&stored); err != nil {
		return fmt.Errorf("verify Hermes workspace update: %w", err)
	}
	if !sameMoveWorkspacePath(stored, workspace) {
		return fmt.Errorf("Hermes native workspace readback did not match")
	}
	return nil
}

func (a *Adapter) handleMoveWorkspace(ctx context.Context, session *SessionState, req core.Request) (core.Result, error) {
	rawWorkspace, _ := req.Fields["workspace"].(string)
	if strings.TrimSpace(rawWorkspace) == "" {
		rawWorkspace, _ = req.Fields["cwd"].(string)
	}
	if strings.TrimSpace(rawWorkspace) == "" {
		return core.Result{}, core.RejectBeforeDispatch("Hermes target workspace is required")
	}
	workspace, err := a.validateWorkspace(rawWorkspace)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}

	a.mu.Lock()
	if session.Status != "idle" || session.sending || len(a.pending) != 0 || len(a.pendingApprovals) != 0 {
		a.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Hermes session must be idle before moving workspace")
	}
	session.sending = true
	handle := session.Handle
	canonical := a.canonical
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		session.sending = false
		a.mu.Unlock()
	}()

	confirmedWS := workspace
	if canonical {
		if _, err := a.rpc(ctx, "session.move_workspace", map[string]any{
			"session_id": session.SessionID,
			"cwd":        workspace,
		}); err != nil {
			return core.Result{}, fmt.Errorf("Hermes native workspace mutation failed: %w", err)
		}
		res, err := a.rpc(ctx, "session.resume", map[string]any{
			"session_id":    session.SessionID,
			"omit_messages": true,
		})
		if err != nil {
			return core.Result{}, fmt.Errorf("Hermes workspace readback failed: %w", err)
		}
		info, _ := res["info"].(map[string]any)
		readback, _ := info["cwd"].(string)
		if strings.TrimSpace(readback) == "" {
			return core.Result{}, fmt.Errorf("Hermes workspace readback omitted cwd")
		}
		matches := sameMoveWorkspacePath(readback, workspace)
		if !matches && a.sshStarter == nil {
			if evalReadback, evalErr := filepath.EvalSymlinks(readback); evalErr == nil {
				matches = sameMoveWorkspacePath(evalReadback, workspace)
			}
		}
		if !matches {
			return core.Result{}, fmt.Errorf("Hermes workspace readback %q did not match %q", readback, workspace)
		}
		confirmedWS = readback
	} else {
		dbPath := a.historyDBPath()
		if dbPath == "" {
			return core.Result{}, core.RejectBeforeDispatch("当前 Hermes 远程连接未启用 Owner 模式，暂不支持原生移动项目")
		}
		if handle != "" {
			closed, err := a.rpc(ctx, "session.close", map[string]any{"session_id": handle})
			if err != nil {
				return core.Result{}, err
			}
			if closed["closed"] != true {
				return core.Result{}, fmt.Errorf("Hermes native close was not confirmed before workspace move")
			}
		}
		if err := updateHermesStateDBWorkspace(ctx, dbPath, session.SessionID, workspace); err != nil {
			return core.Result{}, err
		}
	}

	a.mu.Lock()
	session.Workspace = confirmedWS
	delete(a.sessions, session.SessionID)
	if handle != "" {
		delete(a.handleToSession, handle)
	}
	a.mu.Unlock()

	return core.Result{
		SessionID: session.SessionID,
		Status:    "idle",
		Payload: map[string]any{
			"workspace": confirmedWS,
			"moved":     true,
			"closed":    true,
		},
	}, nil
}
