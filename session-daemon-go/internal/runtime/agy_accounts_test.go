package runtime

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type agySwitchTestAdapter struct {
	restartScopeAdapter
	steps       []string
	stopErr     error
	duringApply func()
}

func (a *agySwitchTestAdapter) Query(_ context.Context, request Request) (map[string]any, error) {
	method := request.Fields["method"].(string)
	a.steps = append(a.steps, method)
	if method == "account.vault.check" {
		return map[string]any{"ready": true}, nil
	}
	if method == "account.vault.apply" && a.duringApply != nil {
		a.duringApply()
	}
	return map[string]any{"account_id": "next"}, nil
}

func (a *agySwitchTestAdapter) ForceShutdown(context.Context) ([]string, error) {
	a.steps = append(a.steps, "stop")
	return nil, a.stopErr
}

func TestAgySwitchRequiresConfirmedStopAndKeepsAdmissionClosed(t *testing.T) {
	ctx := context.Background()
	registry := NewRegistry()
	adapter := &agySwitchTestAdapter{stopErr: errors.New("stop unconfirmed")}
	registry.Register("antigravity", adapter)
	request := Request{AgentType: "antigravity", Action: "agy.account", Fields: map[string]any{"method": "account.switch"}}
	if _, _, err := registry.SwitchAgyAccount(ctx, request, "", true); err == nil {
		t.Fatal("switch succeeded without a confirmed stop")
	}
	if !slices.Equal(adapter.steps, []string{"account.vault.check", "stop"}) {
		t.Fatalf("unexpected sequence: %v", adapter.steps)
	}
	adapter.stopErr = nil
	adapter.steps = nil
	adapter.duringApply = func() {
		if _, err := registry.Spawn(ctx, Request{AgentType: "antigravity", SessionID: "concurrent"}); !errors.Is(err, ErrRuntimeBusy) {
			t.Fatalf("spawn during token replacement returned %v", err)
		}
	}
	result, _, err := registry.SwitchAgyAccount(ctx, request, "", true)
	if err != nil || result["account_id"] != "next" {
		t.Fatalf("switch returned %v, %v", result, err)
	}
	if !slices.Equal(adapter.steps, []string{"account.vault.check", "stop", "account.vault.apply"}) {
		t.Fatalf("unexpected sequence: %v", adapter.steps)
	}
}
