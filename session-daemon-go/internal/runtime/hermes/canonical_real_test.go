package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestRealCanonicalOwnerLifecycle(t *testing.T) {
	launcher := os.Getenv("ASTRORDER_CANONICAL_LAUNCHER")
	if launcher == "" {
		t.Skip("explicit installed launcher required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, launcher, "--print-runtime-command", "--module", "tui_gateway.entry").Output()
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if json.Unmarshal(data, &argv) != nil || len(argv) < 2 {
		t.Fatal("invalid launcher command")
	}
	fixtureRoot, err := os.MkdirTemp("C:/Users/luwei/AppData/Local/hermes/cache/scratch", "canonical-native-")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(fixtureRoot, "profiles", filepath.Base(fixtureRoot))
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	t.Log("isolated home", home)
	if err = os.WriteFile(filepath.Join(home, "config.yaml"), []byte("gateway:\n  standalone: true\nmodel:\n  default: test-model\n  provider: openai\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Borrow only immutable dependency generations, never configuration or sessions.
	installedHome := filepath.Dir(filepath.Dir(launcher))
	installs, err := os.ReadDir(filepath.Join(installedHome, "installs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range installs {
		facts, readErr := os.ReadFile(filepath.Join(installedHome, "installs", entry.Name(), "facts.json"))
		if readErr != nil {
			continue
		}
		target := filepath.Join(home, "installs", entry.Name())
		if err = os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if err = json.Unmarshal(facts, &record); err != nil {
			t.Fatal(err)
		}
		packages, _ := record["packages"].(map[string]any)
		venv, _ := packages["venv"].(map[string]any)
		selected, _ := venv["environment"].(string)
		if selected == "" {
			continue
		}
		destination := filepath.Join(target, "environments", "fixture", "venv")
		err = filepath.WalkDir(selected, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, _ := filepath.Rel(selected, path)
			to := filepath.Join(destination, relative)
			if entry.IsDir() {
				return os.MkdirAll(to, 0700)
			}
			return os.Link(path, to)
		})
		if err != nil {
			t.Fatal(err)
		}
		venv["environment"] = destination
		facts, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(target, "facts.json"), facts, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := New(Config{Executable: argv[0], Arguments: argv[1:], Environment: []string{"HERMES_HOME=" + home, "HERMES_DISABLE_AUTO_UPDATE=1", "HERMES_DISABLE_LAZY_INSTALLS=1", "HERMES_RUNTIME_DIR=" + filepath.Join(filepath.Dir(filepath.Dir(launcher)), "tools")}, Workspace: workspace, Allowed: []string{workspace}, StateDBPath: filepath.Join(home, "state.db"), AgentID: "canonical-fixture"})
	defer a.Close()
	session, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "title": "Canonical lifecycle fixture", "model": "test-model", "provider": "openai"}})
	if err != nil {
		t.Fatal("create", err)
	}
	duplicate, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "title": "Canonical lifecycle fixture", "model": "test-model", "provider": "openai"}})
	if err != nil || duplicate.SessionID == session.SessionID {
		t.Fatalf("duplicate title reused session: %v", err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: duplicate.SessionID, Action: "session.delete"}); err != nil {
		t.Fatal(err)
	}
	fixtureDB, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	var boundary int64
	for _, text := range []string{"included fixture message", "excluded later fixture message"} {
		result, insertErr := fixtureDB.Exec("INSERT INTO messages(session_id,role,content,timestamp) VALUES(?,'assistant',?,1)", session.SessionID, text)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		if boundary == 0 {
			boundary, _ = result.LastInsertId()
		}
	}
	child, err := a.Create(ctx, core.Request{Fields: map[string]any{"parent_session_id": session.SessionID, "target_message_id": boundary, "cwd": workspace, "title": "Branched canonical fixture"}})
	if err != nil || child.SessionID == session.SessionID {
		t.Fatalf("branch failed: %v", err)
	}
	var copied int
	if err = fixtureDB.QueryRow("SELECT COUNT(*) FROM messages WHERE session_id=?", child.SessionID).Scan(&copied); err != nil || copied != 1 {
		t.Fatalf("branch boundary copied %d: %v", copied, err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.close"}); err != nil {
		t.Fatal("close branch", err)
	}
	reader := New(a.config)
	defer reader.Close()
	if _, err = reader.Spawn(ctx, core.Request{SessionID: child.SessionID, Fields: map[string]any{"cwd": workspace}}); err != nil {
		t.Fatal("fresh viewer resume after close", err)
	}
	if _, err = reader.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.history_page", Fields: map[string]any{"limit": 200}}); err != nil {
		t.Fatal("history after close", err)
	}
	if _, err = reader.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.close"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Spawn(ctx, core.Request{SessionID: child.SessionID, Fields: map[string]any{"cwd": workspace}}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.delete"}); err != nil {
		t.Fatal(err)
	}
	t.Log("created isolated canonical session")
	for _, action := range []string{"session.model.read", "session.rename", "session.delete"} {
		_, err = a.Command(ctx, core.Request{SessionID: session.SessionID, Action: action, Fields: map[string]any{"title": "Renamed canonical fixture"}})
		if err != nil {
			t.Fatal(action, err)
		}
		t.Log(action, "PASS")
	}
	if _, err = a.rpc(ctx, "session.resume", map[string]any{"session_id": session.SessionID, "omit_messages": true}); err == nil {
		t.Fatal("deleted session resumed")
	}
	t.Log("deleted session independently refused")
}
