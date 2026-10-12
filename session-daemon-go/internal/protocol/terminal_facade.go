package protocol

import (
	"context"
	"errors"
	"fmt"
)

// OpenTerminalSession spawns/starts a PTY session using ExecuteSessionControl.
func (d *Daemon) OpenTerminalSession(ctx context.Context, sessionID string, cwd string, executable string, args []string, cols, rows uint32) error {
	if sessionID == "" || len(sessionID) > 256 {
		return errors.New("invalid session id")
	}
	fields := map[string]any{
		"action":     "session.spawn",
		"agent_type": "pty",
		"session_id": sessionID,
		"cwd":        cwd,
	}
	if executable != "" {
		fields["executable"] = executable
	}
	if len(args) > 0 {
		fields["args"] = args
	}
	if cols > 0 {
		fields["cols"] = int(cols)
	}
	if rows > 0 {
		fields["rows"] = int(rows)
	}

	res := d.ExecuteSessionControl(ctx, fields)
	if action, _ := res["action"].(string); action == "error" {
		msg, _ := res["error"].(string)
		if detail, ok := res["detail"].(string); ok && detail != "" {
			return fmt.Errorf("%s: %s", msg, detail)
		}
		if msg == "" {
			msg = "failed to open terminal session"
		}
		return errors.New(msg)
	}
	return nil
}

// TerminalInput sends input bytes to the PTY session, capped at 64KB.
func (d *Daemon) TerminalInput(ctx context.Context, sessionID string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > 65536 {
		return errors.New("terminal input exceeds 64KB limit")
	}
	fields := map[string]any{
		"action":     "session.send",
		"session_id": sessionID,
		"input":      string(data),
	}
	res := d.ExecuteSessionControl(ctx, fields)
	if action, _ := res["action"].(string); action == "error" {
		msg, _ := res["error"].(string)
		if detail, ok := res["detail"].(string); ok && detail != "" {
			return fmt.Errorf("%s: %s", msg, detail)
		}
		return errors.New(msg)
	}
	return nil
}

// TerminalResize modifies the dimensions of the PTY session.
func (d *Daemon) TerminalResize(ctx context.Context, sessionID string, cols, rows uint32) error {
	fields := map[string]any{
		"action":     "session.resize",
		"session_id": sessionID,
		"cols":       int(cols),
		"rows":       int(rows),
	}
	res := d.ExecuteSessionControl(ctx, fields)
	if action, _ := res["action"].(string); action == "error" {
		msg, _ := res["error"].(string)
		if detail, ok := res["detail"].(string); ok && detail != "" {
			return fmt.Errorf("%s: %s", msg, detail)
		}
		return errors.New(msg)
	}
	return nil
}

// TerminalInterrupt sends an interrupt to the PTY session.
func (d *Daemon) TerminalInterrupt(ctx context.Context, sessionID string) error {
	fields := map[string]any{
		"action":     "session.interrupt",
		"session_id": sessionID,
	}
	res := d.ExecuteSessionControl(ctx, fields)
	if action, _ := res["action"].(string); action == "error" {
		msg, _ := res["error"].(string)
		if detail, ok := res["detail"].(string); ok && detail != "" {
			return fmt.Errorf("%s: %s", msg, detail)
		}
		return errors.New(msg)
	}
	return nil
}

// TerminalClose explicitly closes the PTY session.
func (d *Daemon) TerminalClose(ctx context.Context, sessionID string, reason string) error {
	fields := map[string]any{
		"action":     "session.close",
		"session_id": sessionID,
		"reason":     reason,
	}
	res := d.ExecuteSessionControl(ctx, fields)
	if action, _ := res["action"].(string); action == "error" {
		msg, _ := res["error"].(string)
		if detail, ok := res["detail"].(string); ok && detail != "" {
			return fmt.Errorf("%s: %s", msg, detail)
		}
		return errors.New(msg)
	}
	return nil
}
