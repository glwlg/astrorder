package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only a fresh native gateway/profile and an empty native session are used.
// This opt-in test never sends an LLM prompt or mutates an existing profile.
func TestRealHermesCreateResumeHistoryRenameDelete(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_HERMES_PYTHON")
	source := os.Getenv("ASTRORDER_REAL_HERMES_SOURCE")
	if executable == "" || source == "" {
		t.Skip("explicit native Hermes executable/source required")
	}
	home, workspace := t.TempDir(), t.TempDir()
	a := New(Config{Executable: executable, Arguments: []string{"-m", "tui_gateway.entry"}, Environment: []string{"HERMES_HOME=" + home, "PYTHONPATH=" + source, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1"}, Workspace: workspace, Allowed: []string{workspace}, StateDBPath: filepath.Join(home, "state.db"), AgentID: "isolated-hermes-native"})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	session, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "title": "Isolated Go native validation"}})
	if err != nil {
		t.Fatal("native create", err)
	}
	if session.SessionID == "" {
		t.Fatal("native create omitted durable identity")
	}
	defer func() {
		if _, owned := a.getSession(session.SessionID); owned {
			if _, err := a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.delete"}); err != nil {
				t.Errorf("native cleanup: %v", err)
			}
		}
	}()
	handle, err := a.ensureHandle(ctx, a.sessions[session.SessionID])
	if err != nil || handle == "" {
		t.Fatal("native resume", err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.models"}); err != nil {
		t.Fatal("native models", err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.history_page", Fields: map[string]any{"limit": 20}}); err != nil {
		t.Fatal("native empty history", err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.rename", Fields: map[string]any{"title": "Renamed isolated Go validation"}}); err != nil {
		t.Fatal("native rename", err)
	}
	// Model catalog access may initialize the native agent asynchronously.
	// Do not broaden the deletion safety policy to accept its transient states.
	for {
		listing, listErr := a.rpc(ctx, "session.active_list", nil)
		if listErr != nil {
			t.Fatal(listErr)
		}
		safe := true
		rows, _ := listing["sessions"].([]any)
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			if row["session_key"] == session.SessionID && row["status"] != "idle" {
				safe = false
			}
		}
		if safe {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native initialization did not reconcile to idle", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if _, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: "session.delete"}); err != nil {
		t.Fatal("native delete", err)
	}
	_, err = a.rpc(ctx, "session.resume", map[string]any{"session_id": session.SessionID, "lazy": true, "omit_messages": true})
	if err == nil {
		t.Fatal("deleted native session still resumed")
	}
}
