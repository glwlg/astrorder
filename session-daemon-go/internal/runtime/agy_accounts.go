package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

// SwitchAgyAccount closes runtime admission from credential preflight through
// transport shutdown and token replacement. No session can start with the old
// credential in the window between shutdown and replacement.
func (r *Registry) SwitchAgyAccount(ctx context.Context, request Request, connectionID string, force bool) (map[string]any, []string, error) {
	if request.AgentType != "antigravity" && request.AgentType != "antigravity-ssh" {
		return nil, nil, errors.New("账号切换仅支持 AGY")
	}
	invoke := func(ctx context.Context, adapter Adapter, method string) (map[string]any, error) {
		querier, ok := adapter.(Querier)
		if !ok {
			return nil, errors.New("目标 Agent 不支持账号切换")
		}
		req := request
		req.Fields = maps.Clone(request.Fields)
		req.Fields["method"] = method
		return querier.Query(ctx, req)
	}
	var result map[string]any
	released, err := r.disconnectRuntime(ctx, request.AgentType, connectionID, force,
		func(ctx context.Context, adapter Adapter) error {
			result, err := invoke(ctx, adapter, "account.vault.check")
			if err != nil {
				return err
			}
			if result["ready"] != true {
				return errors.New("目标 AGY 凭证预检未确认")
			}
			return nil
		},
		func(ctx context.Context, adapter Adapter) error {
			var err error
			result, err = invoke(ctx, adapter, "account.vault.apply")
			if err != nil {
				return fmt.Errorf("Agent 已停止，账号切换未确认：%w", err)
			}
			return nil
		})
	return result, released, err
}
