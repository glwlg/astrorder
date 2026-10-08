package codex_test

import (
	"context"
	"os"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/codex"
)

func TestRealCodexCreateResumeAndDelete(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	creator := codex.New(codex.Config{Executable: executable, Workspace: root, Allowed: []string{root}})
	created, err := creator.Create(ctx, codex.Request{Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	creator.Close()

	resumer := codex.New(codex.Config{Executable: executable, Workspace: root, Allowed: []string{root}})
	resumed, err := resumer.Resume(ctx, codex.Request{Cwd: root, SessionID: created.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != created.SessionID {
		t.Fatalf("resumed the wrong thread: created=%q resumed=%q", created.SessionID, resumed.SessionID)
	}
	if _, err := resumer.Command(ctx, codex.Command{Action: "session.delete", SessionID: created.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := resumer.Command(ctx, codex.Command{Action: "session.send", SessionID: created.SessionID, Text: "must fail"}); err == nil {
		t.Fatal("deleted thread remained owned by the runtime")
	}
	resumer.Close()
}
