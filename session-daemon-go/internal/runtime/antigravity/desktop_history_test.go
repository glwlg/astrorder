package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopConversationHistoryDoesNotLaunchCLI(t *testing.T) {
	home := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	dir := filepath.Join(home, ".gemini", "antigravity", "conversations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB); INSERT INTO steps VALUES(0,14,3,0,?,NULL)`, nativeText(14, "desktop history")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a := New(Config{Executable: filepath.Join(home, "must-not-launch.exe"), Home: home, Workspace: home, Allowed: []string{home}})
	defer a.Close()
	ctx := context.Background()
	result, err := a.Spawn(ctx, core.Request{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if result.Payload["control_state"] != "readonly" {
		t.Fatalf("ownership: %+v", result)
	}
	page, err := a.Command(ctx, core.Request{SessionID: id, Action: "session.history_page"})
	if err != nil {
		t.Fatal(err)
	}
	items := page.Payload["items"].([]map[string]any)
	if len(items) != 1 || items[0]["text"] != "desktop history" {
		t.Fatalf("history: %+v", items)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: id, Action: "session.send", Fields: map[string]any{"text": "hello"}}); err == nil {
		t.Fatal("desktop conversation silently sent through CLI")
	}
	model, err := a.Command(ctx, core.Request{SessionID: id, Action: "session.model.read"})
	if err != nil || model.Payload["read_only"] != true || model.Payload["model_switch_supported"] == true {
		t.Fatalf("capabilities: %+v %v", model, err)
	}
}
