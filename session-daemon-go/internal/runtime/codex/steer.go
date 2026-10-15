package codex

import (
	"context"
	"fmt"
)

// steer delivers follow-up input to the current turn without changing the
// original turn's command identity or inventing another turn's completion.
func (runtime *Runtime) steer(ctx context.Context, command Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	runtime.mu.Lock()
	active := runtime.activeTurnID
	runtime.mu.Unlock()
	if command.TurnID == "" || command.TurnID != active {
		return Result{}, fmt.Errorf("codex turn is not the active daemon-owned turn")
	}
	inputs := command.Input
	if len(inputs) == 0 && command.Text != "" {
		inputs = []map[string]any{{"type": "text", "text": command.Text}}
	}
	if len(inputs) == 0 {
		return Result{}, fmt.Errorf("codex guidance must be non-empty")
	}
	params := map[string]any{"threadId": command.SessionID, "expectedTurnId": command.TurnID, "input": inputs}
	if command.CommandID != "" {
		params["clientUserMessageId"] = command.CommandID
	}
	response, err := runtime.request(ctx, "turn/steer", params)
	if err != nil {
		return Result{}, err
	}
	if response["turnId"] != command.TurnID {
		return Result{}, fmt.Errorf("codex guidance acknowledgement did not match the active turn")
	}
	return Result{SessionID: command.SessionID, TurnID: command.TurnID, Status: runtime.Status(), Accepted: true, Completed: true}, nil
}
