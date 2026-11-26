package grok

import (
	"context"
	"fmt"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func (a *Adapter) verifySessionWorkspace(ctx context.Context, sessionID, targetWS string) error {
	c, err := a.startClient(ctx, targetWS, nil)
	if err != nil {
		return err
	}
	if _, err := c.request(ctx, "initialize", initializeParams()); err != nil {
		_ = c.close()
		return err
	}
	loadedResp, err := c.request(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        targetWS,
		"mcpServers": []any{},
	})
	if err != nil {
		_ = c.close()
		return err
	}
	if sid, _ := loadedResp["sessionId"].(string); sid != "" && sid != sessionID {
		_ = c.close()
		return fmt.Errorf("grok loaded a different session")
	}
	if err := c.close(); err != nil {
		return err
	}
	return nil
}

func (a *Adapter) handleMoveWorkspace(ctx context.Context, owned *ownedSession, sessionID string, req core.Request) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	rawWorkspace := ""
	if req.Fields != nil {
		if w, ok := req.Fields["workspace"].(string); ok {
			rawWorkspace = w
		}
		if strings.TrimSpace(rawWorkspace) == "" {
			if c, ok := req.Fields["cwd"].(string); ok {
				rawWorkspace = c
			}
		}
	}
	if strings.TrimSpace(rawWorkspace) == "" {
		return core.Result{}, core.RejectBeforeDispatch("Grok target workspace is required")
	}
	targetWS, err := a.workspace(rawWorkspace)
	if err != nil {
		return core.Result{}, core.RejectBeforeDispatch(err.Error())
	}

	owned.mu.Lock()
	if owned.control {
		owned.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Grok control session is not a native chat session")
	}
	if owned.status == "running" || owned.status == "waiting_approval" || owned.promptRunning || len(owned.pendingApprovals) > 0 {
		owned.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Grok session must be idle before moving workspace")
	}
	owned.closed = true
	if owned.promptCancel != nil {
		owned.promptCancel()
	}
	oldClient := owned.client
	owned.mu.Unlock()

	if oldClient != nil {
		if err := oldClient.close(); err != nil {
			owned.mu.Lock()
			owned.status = "error"
			owned.failed = true
			owned.lastError = err.Error()
			owned.mu.Unlock()
			return core.Result{}, err
		}
	}

	a.mu.Lock()
	if a.sessions[sessionID] == owned {
		delete(a.sessions, sessionID)
	}
	a.mu.Unlock()

	if err := a.verifySessionWorkspace(ctx, sessionID, targetWS); err != nil {
		return core.Result{}, fmt.Errorf("Grok failed to verify target workspace: %w", err)
	}

	return core.Result{
		SessionID: sessionID,
		Status:    "idle",
		Payload: map[string]any{
			"workspace": targetWS,
			"moved":     true,
			"closed":    true,
		},
	}, nil
}
