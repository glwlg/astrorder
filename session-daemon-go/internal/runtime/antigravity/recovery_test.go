package antigravity

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestExitedTransportCanResumeExactConversationWithoutResending(t *testing.T) {
	exe, _ := os.Executable()
	home := t.TempDir()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE conversation_summaries(conversation_id TEXT,not_fully_idle INTEGER,killed INTEGER); INSERT INTO conversation_summaries VALUES('11111111-1111-4111-8111-111111111111',0,0)`); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1", "ASTRORDER_AGY_EOF=1"}, Workspace: home, Allowed: []string{home}, Home: home})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	old := a.sessions[created.SessionID]
	a.mu.Unlock()
	if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.send", Fields: map[string]any{"text": "uncertain", "command_id": "original"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = a.Command(ctx, core.Request{SessionID: created.SessionID, Action: "session.model.read"}); !errors.Is(err, core.ErrSessionNotOwned) {
		t.Fatalf("dead binding returned cached model: %v", err)
	}
	if _, err = db.Exec(`UPDATE conversation_summaries SET not_fully_idle=1`); err != nil {
		t.Fatal(err)
	}
	resumed, err := a.Spawn(ctx, core.Request{SessionID: created.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != created.SessionID || resumed.Status != "idle" {
		t.Fatalf("resume: %+v", resumed)
	}
	a.mu.Lock()
	current := a.sessions[created.SessionID]
	a.mu.Unlock()
	if current == old || current.cmd.Process.Pid == old.cmd.Process.Pid {
		t.Fatal("transport was not replaced")
	}
	current.mu.Lock()
	command := current.command
	current.mu.Unlock()
	if command != "" {
		t.Fatal("uncertain command was replayed")
	}
}

func TestErrorWhileTransportAliveDoesNotPermitReplacement(t *testing.T) {
	a := New(Config{})
	s := &session{id: "11111111-1111-4111-8111-111111111111", status: "error", done: make(chan struct{})}
	a.sessions[s.id] = s
	if _, err := a.Command(context.Background(), core.Request{SessionID: s.id, Action: "session.model.read"}); errors.Is(err, core.ErrSessionNotOwned) {
		t.Fatal("live transport was declared unowned")
	}
}
