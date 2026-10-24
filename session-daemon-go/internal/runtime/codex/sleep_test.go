package codex

import "testing"

func TestSleepSafetyProtectsBackgroundAndChildren(t *testing.T) {
	for _, tc := range []struct {
		name string
		item map[string]any
		safe bool
	}{
		{"completed", map[string]any{"type": "commandExecution", "status": "completed"}, true},
		{"background", map[string]any{"type": "commandExecution", "status": "inProgress"}, false},
		{"child", map[string]any{"type": "collabAgentToolCall", "status": "completed"}, false},
		{"pending", map[string]any{"type": "tool", "status": "pending"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thread := map[string]any{"id": "s", "turns": []any{map[string]any{"status": "completed", "items": []any{tc.item}}}}
			if sleepSafeThread(thread, "s") != tc.safe {
				t.Fatal("unsafe decision")
			}
			if sleepSafeThread(thread, "foreign") {
				t.Fatal("foreign identity accepted")
			}
		})
	}
	if sleepSafeThread(map[string]any{"id": "s"}, "s") {
		t.Fatal("missing native history accepted")
	}
}
