package antigravity

import (
	"context"
	"os"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestSwitchModelAndReasoningOverACP(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	a := New(Config{
		Executable:  exe,
		Arguments:   []string{"-test.run=TestStreamHelper", "--"},
		Environment: []string{"ASTRORDER_AGY_HELPER=1"},
		Workspace:   dir,
		Allowed:     []string{dir},
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	initial, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}

	// Switch thought_level via session/set_config_option.
	reasoned, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.reasoning.set", Fields: map[string]any{"effort": "high"}})
	if err != nil || reasoned.Payload["effort"] != "high" {
		t.Fatalf("reasoning switch: %#v %v", reasoned, err)
	}
	read, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.read"})
	if err != nil || read.Payload["effort"] != "high" || read.Payload["reasoning_switch_supported"] != true {
		t.Fatalf("reasoning readback: %#v %v", read, err)
	}

	// Switch to non-thinking model via session/set_model.
	changed, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.set", Fields: map[string]any{"provider": "antigravity", "model": "claude-test"}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.SessionID != initial.SessionID || changed.Payload["model"] != "claude-test" {
		t.Fatalf("switch: %#v", changed)
	}
	read, err = a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.read"})
	if err != nil || read.Payload["model"] != "claude-test" || read.Payload["reasoning_switch_supported"] != false {
		t.Fatalf("readback: %#v %v", read, err)
	}
	sent, err := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.send", Fields: map[string]any{"text": "hello"}})
	if err != nil || sent.Payload["accepted"] != true {
		t.Fatalf("send after switch: %#v %v", sent, err)
	}
}

func TestApplyModelEffortStateForCatalogVariants(t *testing.T) {
	s := &session{
		models: []map[string]any{
			{"id": "gemini-4-neon-high", "label": "Gemini 4 Neon (High)"},
			{"id": "gemini-4-neon-xhigh", "label": "Gemini 4 Neon (XHigh)"},
			{"id": "gemini-4-neon-medium", "label": "Gemini 4 Neon (Medium)"},
			{"id": "gemini-4-neon-low", "label": "Gemini 4 Neon (Low)"},
			{"id": "gemini-4-carbon-high", "label": "Gemini 4 Carbon (High)"},
			{"id": "gemini-4-carbon-medium", "label": "Gemini 4 Carbon (Medium)"},
			{"id": "gemini-4-carbon-low", "label": "Gemini 4 Carbon (Low)"},
			{"id": "gemini-pro-orchestrator", "label": "Orchestrator (Carbon)"},
			{"id": "gpt-oss-120b-medium", "label": "GPT-OSS 120B (Medium)"},
		},
	}

	applyModelEffortStateLocked(s, "gemini-4-neon-xhigh")
	if s.effort != "xhigh" || len(s.thoughtLevels) != 4 || s.thoughtLevels[3] != "xhigh" {
		t.Fatalf("expected 4 thought levels including xhigh for Neon, got effort=%q levels=%v", s.effort, s.thoughtLevels)
	}

	applyModelEffortStateLocked(s, "gemini-4-carbon-high")
	if s.effort != "high" || len(s.thoughtLevels) != 3 || s.thoughtLevels[2] != "high" {
		t.Fatalf("expected 3 thought levels for Carbon, got effort=%q levels=%v", s.effort, s.thoughtLevels)
	}

	applyModelEffortStateLocked(s, "gpt-oss-120b-medium")
	if s.effort != "medium" || len(s.thoughtLevels) != 0 {
		t.Fatalf("expected fixed medium effort with no switchable levels for GPT-OSS, got effort=%q levels=%v", s.effort, s.thoughtLevels)
	}

	applyModelEffortStateLocked(s, "gemini-pro-orchestrator")
	if s.effort != "" || len(s.thoughtLevels) != 0 {
		t.Fatalf("expected empty effort/levels for Orchestrator, got effort=%q levels=%v", s.effort, s.thoughtLevels)
	}
}
