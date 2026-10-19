package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Real installed CLI, isolated scratch HOME, local fixture HTTP gateway only.
func TestNativePermissionFixture(t *testing.T) {
	home := os.Getenv("ASTRORDER_AGY_PERMISSION_FIXTURE_HOME")
	exe := os.Getenv("ASTRORDER_AGY_PERMISSION_FIXTURE_EXE")
	if home == "" || exe == "" {
		t.Skip("explicit isolated native protocol fixture")
	}
	var mu sync.Mutex
	var toolItems []map[string]any
	a := New(Config{Executable: exe, Arguments: []string{"--gemini_dir=" + filepath.Join(home, ".gemini")}, Workspace: home, Allowed: []string{home}, Home: home, Environment: []string{"HOME=" + home, "USERPROFILE=" + home, "AGY_CLI_DISABLE_AUTO_UPDATE=1"}, Emit: func(id, event string, p map[string]any, status string) error {
		if p["type"] == "message.upsert" {
			d, _ := p["data"].(map[string]any)
			if d["kind"] == "tool" {
				mu.Lock()
				toolItems = append(toolItems, d)
				mu.Unlock()
			}
		}
		return nil
	}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	initial, err := a.Create(ctx, core.Request{Fields: map[string]any{"model": "probe"}})
	if err != nil {
		t.Fatal(err)
	}
	for i, mode := range []string{"manual", "full_access", "manual"} {
		if i > 0 {
			changed, e := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.approval.set", Fields: map[string]any{"mode": mode}})
			if e != nil || changed.SessionID != initial.SessionID {
				t.Fatalf("switch: %+v %v", changed, e)
			}
		}
		read, e := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.approval.read"})
		if e != nil || read.Payload["mode"] != mode {
			t.Fatalf("readback: %+v %v", read, e)
		}
		mu.Lock()
		toolItems = nil
		mu.Unlock()
		_, e = a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.send", Fields: map[string]any{"text": "Protocol fixture command test.", "command_id": mode}})
		if e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(25 * time.Second)
		for a.Snapshot()[initial.SessionID].Status == "running" && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if a.Snapshot()[initial.SessionID].Status != "idle" {
			t.Fatal("native tool result aborted turn")
		}
		mu.Lock()
		items := append([]map[string]any{}, toolItems...)
		mu.Unlock()
		found := false
		for _, item := range items {
			tool := item["tool"].(map[string]any)
			if tool["name"] != "run_command" {
				continue
			}
			if mode == "manual" && tool["status"] == "error" && tool["error"] != "" {
				found = true
			}
			if mode == "full_access" && tool["status"] == "completed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("mode %s native tool outcome missing: %+v", mode, items)
		}
		// agy ends a denied headless tool turn successfully without another
		// model call. Explicitly send the next user turn to verify continuity.
		_, e = a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.send", Fields: map[string]any{"text": "Continue fixture", "command_id": "continue"}})
		if e != nil {
			t.Fatal(e)
		}
		deadline = time.Now().Add(25 * time.Second)
		for a.Snapshot()[initial.SessionID].Status == "running" && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if a.Snapshot()[initial.SessionID].Status != "idle" {
			t.Fatal("next native turn did not finish")
		}
		history, _, e := readHistory(ctx, a.historyDir(), initial.SessionID, "", 100)
		if e != nil {
			t.Fatal(e)
		}
		responses := 0
		errors := 0
		for _, item := range history {
			if item["text"] == "Fixture finished." {
				responses++
			}
			if tool, ok := item["tool"].(map[string]any); ok && tool["error"] != "" && tool["error"] != nil {
				errors++
			}
		}
		if responses < i+1 {
			t.Fatalf("native history missing response: %d", responses)
		}
		if mode == "manual" && errors == 0 {
			t.Fatal("native history lost permission error")
		}
		t.Logf("round=%d mode=%s conversation=%s native_response_count=%d tool_outcome_confirmed=true", i, mode, initial.SessionID, responses)
	}
}
