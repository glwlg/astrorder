package antigravity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestDeleteRequiresIdleOwnerAndRemovesConversation(t *testing.T) {
	home := t.TempDir()
	a := New(Config{Home: home})
	id := "11111111-1111-4111-8111-111111111111"
	if err := os.MkdirAll(a.historyDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	convFile := filepath.Join(a.historyDir(), id+".db")
	if err := os.WriteFile(convFile, []byte("sqlite-placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE conversation_summaries(conversation_id TEXT PRIMARY KEY, not_fully_idle INTEGER DEFAULT 0, killed INTEGER DEFAULT 0); INSERT INTO conversation_summaries VALUES(?, 0, 0)", id); err != nil {
		t.Fatal(err)
	}

	s := newSession(id, home, nil)
	s.status = "running"
	s.promptRunning = true
	a.sessions[id] = s

	req := core.Request{SessionID: id, Action: "session.delete"}
	if _, err := a.Command(context.Background(), req); err == nil {
		t.Fatal("active deletion accepted")
	}

	s.status = "idle"
	s.promptRunning = false
	result, err := a.Command(context.Background(), req)
	if err != nil || result.Payload["deleted"] != id || a.sessions[id] != nil {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if _, err := os.Stat(convFile); !os.IsNotExist(err) {
		t.Fatal("conversation .db file was not removed")
	}

	// Verify a conversation settled in "error" (no active turn) can also be deleted.
	id2 := "22222222-2222-4222-8222-222222222222"
	if _, err := db.Exec("INSERT INTO conversation_summaries VALUES(?, 0, 0)", id2); err != nil {
		t.Fatal(err)
	}
	s2 := newSession(id2, home, nil)
	s2.status = "error"
	a.sessions[id2] = s2
	res2, err := a.Command(context.Background(), core.Request{SessionID: id2, Action: "session.delete"})
	if err != nil || res2.Payload["deleted"] != id2 || a.sessions[id2] != nil {
		t.Fatalf("settled error delete failed: %+v %v", res2, err)
	}
}
