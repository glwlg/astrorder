package antigravity

import (
	"context"
	"fmt"
	"strings"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func (a *Adapter) validateModelChoice(ctx context.Context, s *session, model string) error {
	s.mu.Lock()
	sessModels := cloneModelsSlice(s.models)
	s.mu.Unlock()

	items := sessModels
	if len(items) == 0 {
		var err error
		items, err = a.models(ctx)
		if err != nil {
			return core.RejectBeforeDispatch(err.Error())
		}
	}
	for _, item := range items {
		if str(item, "id") == model || str(item, "model") == model {
			return nil
		}
	}
	return core.RejectBeforeDispatch("unknown Antigravity model")
}

func inferEffortFromModelID(model string, levels []string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, lv := range levels {
		if strings.HasSuffix(lower, "-"+strings.ToLower(lv)) {
			return lv
		}
	}
	return ""
}

func (a *Adapter) applyModelOnOpen(ctx context.Context, s *session, c *acpClient, model string) error {
	if err := a.validateModelChoice(ctx, s, model); err != nil {
		return err
	}
	s.mu.Lock()
	same := s.model == model
	s.mu.Unlock()
	if same {
		return nil
	}
	if _, err := c.call(ctx, "session/set_model", map[string]any{
		"sessionId": s.id,
		"modelId":   model,
	}); err != nil {
		return mapRPCErrorToPreDispatch(err)
	}
	s.mu.Lock()
	s.model = model
	if inferred := inferEffortFromModelID(model, s.thoughtLevels); inferred != "" {
		s.effort = inferred
	} else if !modelSupportsThoughtLevel(model, model) {
		s.effort = ""
		s.thoughtLevels = nil
	}
	s.mu.Unlock()
	return nil
}

func (a *Adapter) applyEffortOnOpen(ctx context.Context, s *session, c *acpClient, effort string) error {
	resp, err := c.call(ctx, "session/set_config_option", map[string]any{
		"sessionId": s.id,
		"configId":  "thought_level",
		"value":     effort,
	})
	if err != nil {
		return mapRPCErrorToPreDispatch(err)
	}
	s.mu.Lock()
	if opts, ok := resp["configOptions"].([]any); ok {
		s.applyConfigOptionsLocked(opts)
	} else {
		s.effort = effort
	}
	s.mu.Unlock()
	return nil
}

func (a *Adapter) switchModel(ctx context.Context, s *session, p map[string]any) (core.Result, error) {
	model, provider := strings.TrimSpace(str(p, "model")), strings.TrimSpace(str(p, "provider"))
	if model == "" || (provider != "" && provider != "antigravity") {
		return core.Result{}, core.RejectBeforeDispatch("invalid Antigravity model selection")
	}
	if err := a.validateModelChoice(ctx, s, model); err != nil {
		return core.Result{}, err
	}

	s.mu.Lock()
	if s.control || s.readonly || s.deleteOnly || s.closed || s.status != "idle" || s.promptRunning {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity model switch requires an idle conversation")
	}
	c := s.client
	curModel := s.model
	s.mu.Unlock()

	if c == nil || c.exited() {
		return core.Result{}, core.ErrSessionNotOwned
	}
	if curModel == model {
		res := a.result(s)
		res.Payload["provider"] = "antigravity"
		res.Payload["accepted"] = true
		return res, nil
	}

	if _, err := c.call(ctx, "session/set_model", map[string]any{
		"sessionId": s.id,
		"modelId":   model,
	}); err != nil {
		return core.Result{}, mapRPCErrorToPreDispatch(err)
	}

	s.mu.Lock()
	s.model = model
	if inferred := inferEffortFromModelID(model, s.thoughtLevels); inferred != "" {
		s.effort = inferred
	} else if modelSupportsThoughtLevel(model, model) && len(s.thoughtLevels) == 0 {
		s.thoughtLevels = []string{"low", "medium", "high"}
		if inferred := inferEffortFromModelID(model, s.thoughtLevels); inferred != "" {
			s.effort = inferred
		}
	} else if !modelSupportsThoughtLevel(model, model) {
		s.effort = ""
		s.thoughtLevels = nil
	}
	s.mu.Unlock()

	_ = a.announce(s)
	result := a.result(s)
	result.Payload["provider"] = "antigravity"
	result.Payload["accepted"] = true
	return result, nil
}

func (a *Adapter) switchReasoning(ctx context.Context, s *session, p map[string]any) (core.Result, error) {
	effort := strings.TrimSpace(str(p, "effort"))
	if effort == "" {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity reasoning effort is required")
	}

	s.mu.Lock()
	if s.control || s.readonly || s.deleteOnly || s.closed || s.status != "idle" || s.promptRunning {
		s.mu.Unlock()
		return core.Result{}, core.RejectBeforeDispatch("Antigravity reasoning switch requires an idle conversation")
	}
	c := s.client
	levels := append([]string{}, s.thoughtLevels...)
	curEffort := s.effort
	s.mu.Unlock()

	if c == nil || c.exited() {
		return core.Result{}, core.ErrSessionNotOwned
	}
	if len(levels) == 0 {
		return core.Result{}, core.RejectBeforeDispatch("Antigravity current model does not support adjustable thought_level")
	}
	supported := false
	for _, lv := range levels {
		if lv == effort {
			supported = true
			break
		}
	}
	if !supported {
		return core.Result{}, core.RejectBeforeDispatch(fmt.Sprintf("Antigravity thought_level supports: %s", strings.Join(levels, ", ")))
	}
	if curEffort == effort {
		res := a.result(s)
		res.Payload["accepted"] = true
		return res, nil
	}

	resp, err := c.call(ctx, "session/set_config_option", map[string]any{
		"sessionId": s.id,
		"configId":  "thought_level",
		"value":     effort,
	})
	if err != nil {
		return core.Result{}, mapRPCErrorToPreDispatch(err)
	}

	s.mu.Lock()
	if opts, ok := resp["configOptions"].([]any); ok {
		s.applyConfigOptionsLocked(opts)
	} else {
		s.effort = effort
	}
	s.mu.Unlock()

	_ = a.announce(s)
	res := a.result(s)
	res.Payload["accepted"] = true
	return res, nil
}
