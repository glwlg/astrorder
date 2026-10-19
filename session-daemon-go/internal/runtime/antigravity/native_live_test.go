package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeLiveConversationAndModelSwitch(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_LIVE_TEST") != "1" {
		t.Skip("explicit real model acceptance")
	}
	home, _ := os.UserHomeDir()
	cwd := filepath.Join(home, "AppData", "Local", "hermes", "cache", "scratch", "agy-live")
	a := New(Config{Executable: filepath.Join(home, "AppData", "Local", "agy", "bin", "agy.exe"), Workspace: cwd, Allowed: []string{cwd}, Home: home})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	initial, err := a.Create(ctx, core.Request{Fields: map[string]any{"model": "gemini-3.8-flash-low"}})
	if err != nil {
		t.Fatal(err)
	}
	for index, model := range []string{"gemini-3.8-flash-low", "claude-sonnet-4-6", "gemini-3.8-flash-medium"} {
		if index > 0 {
			changed, e := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.model.set", Fields: map[string]any{"model": model, "provider": "antigravity"}})
			if e != nil || changed.SessionID != initial.SessionID || changed.Payload["model"] != model {
				t.Fatalf("switch: %+v %v", changed, e)
			}
		}
		nonce := fmt.Sprintf("AGY_%d_%d", time.Now().UnixNano(), index)
		sent, e := a.Command(ctx, core.Request{SessionID: initial.SessionID, Action: "session.send", Fields: map[string]any{"text": "只回复 " + nonce + "，不要调用工具，不要修改文件。", "command_id": nonce}})
		if e != nil || sent.Payload["accepted"] != true {
			t.Fatalf("send: %+v %v", sent, e)
		}
		deadline := time.Now().Add(65 * time.Second)
		for a.Snapshot()[initial.SessionID].Status == "running" && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if a.Snapshot()[initial.SessionID].Status != "idle" {
			t.Fatal("native turn did not finish")
		}
		items, _, e := readHistory(ctx, a.historyDir(), initial.SessionID, "", 50)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, item := range items {
			if item["role"] == "assistant" && str(item, "text") == nonce {
				found = true
			}
		}
		if !found {
			t.Fatalf("native history missing round %d reply", index)
		}
		t.Logf("round=%d conversation=%s model=%s native_reply_confirmed=true", index, initial.SessionID, model)
	}
}
