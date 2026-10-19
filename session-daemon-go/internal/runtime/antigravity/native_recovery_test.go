package antigravity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestNativeExactRecoveryAcceptance(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_RECOVERY_TEST") != "1" {
		t.Skip("explicit native recovery acceptance")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "AppData", "Local", "hermes", "cache", "scratch", "agy-next", "native-workspace")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Executable: filepath.Join(home, "AppData", "Local", "agy", "bin", "agy.exe"), Workspace: cwd, Allowed: []string{cwd}, Home: home})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"model": "group/luozhi"}})
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		nonce := fmt.Sprintf("AGY_RECOVERY_%d_%d", time.Now().UnixNano(), round)
		sent, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.send", Fields: map[string]any{"text": "只回复 " + nonce + "，不要调用工具，不要修改文件。", "command_id": nonce}})
		if err != nil || sent.Payload["accepted"] != true {
			t.Fatalf("send: %v", err)
		}
		for a.Snapshot()[created.SessionID].Status == "running" && ctx.Err() == nil {
			time.Sleep(100 * time.Millisecond)
		}
		if a.Snapshot()[created.SessionID].Status != "idle" {
			t.Fatal("native turn did not finish")
		}
		items, _, err := readHistory(ctx, a.historyDir(), created.SessionID, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item["role"] == "assistant" && str(item, "text") == nonce {
				found = true
			}
		}
		if !found {
			t.Fatal("native history did not confirm response")
		}
		if round == 0 {
			a.mu.Lock()
			old := a.sessions[created.SessionID]
			a.mu.Unlock()
			if err := old.owner.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-old.done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, err := a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.model.read"}); !errors.Is(err, core.ErrSessionNotOwned) {
				t.Fatalf("dead read: %v", err)
			}
			resumed, err := a.Spawn(ctx, core.Request{SessionID: created.SessionID})
			if err != nil {
				t.Fatal(err)
			}
			if resumed.SessionID != created.SessionID || resumed.Status != "idle" {
				t.Fatalf("exact resume: %+v", resumed)
			}
		}
		t.Logf("round=%d native_reply_confirmed=true", round)
	}
	t.Log("same_uuid_recovered=true uncertain_command_replayed=false")
}
