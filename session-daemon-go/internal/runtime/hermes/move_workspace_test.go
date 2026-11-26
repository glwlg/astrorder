package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
)

func TestHermesCanonicalMoveWorkspaceMutatesAndVerifiesReadback(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "proj-a")
	newDir := filepath.Join(root, "proj-b")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedOld, _ := filepath.EvalSymlinks(oldDir)
	resolvedNew, _ := filepath.EvalSymlinks(newDir)

	var mu sync.Mutex
	currentCwd := resolvedOld
	var sawMutateOp string
	var sawMutateCwd string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"hermes-gateway-v1"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var req map[string]any
			if err := json.Unmarshal(data, &req); err != nil {
				return
			}
			method, _ := req["method"].(string)
			params, _ := req["params"].(map[string]any)
			id := req["id"]
			var result map[string]any
			switch method {
			case "session.resume":
				mu.Lock()
				cwd := currentCwd
				mu.Unlock()
				result = map[string]any{
					"session_id":           "hermes-chat-1",
					"revision":             json.Number("7"),
					"execution_generation": json.Number("2"),
					"running":              false,
					"info":                 map[string]any{"cwd": cwd},
				}
			case "session.mutate":
				mu.Lock()
				sawMutateOp, _ = params["operation"].(string)
				if payload, ok := params["payload"].(map[string]any); ok {
					if c, ok := payload["cwd"].(string); ok {
						sawMutateCwd = c
						currentCwd = c
					}
				}
				mu.Unlock()
				result = map[string]any{"ok": true, "revision": json.Number("8")}
			default:
				result = map[string]any{"ok": true}
			}
			resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
			if err := conn.Write(r.Context(), websocket.MessageText, resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	transport, err := dialCanonical(ctx, "ws"+server.URL[4:], []string{"hermes-gateway-v1"})
	if err != nil {
		t.Fatal(err)
	}
	a := New(Config{
		AgentID:   "local-hermes",
		Workspace: resolvedOld,
		Allowed:   []string{root},
	})
	a.canonical = true
	a.canonicalSocket = transport
	a.stdin = transport
	a.stdout = transport
	a.readyCh = make(chan struct{})
	close(a.readyCh)
	go a.readStdout(transport)
	defer a.Close()

	a.sessions["hermes-chat-1"] = &SessionState{
		SessionID: "hermes-chat-1",
		Handle:    "hermes-chat-1",
		Status:    "idle",
		Workspace: resolvedOld,
		Metadata:  map[string]any{},
	}
	a.handleToSession["hermes-chat-1"] = "hermes-chat-1"

	res, err := a.Command(ctx, core.Request{
		SessionID: "hermes-chat-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": resolvedNew},
	})
	if err != nil {
		t.Fatalf("session.move_workspace failed: %v", err)
	}
	if res.Payload["moved"] != true || res.Payload["workspace"] != resolvedNew {
		t.Fatalf("unexpected payload: %+v", res.Payload)
	}
	mu.Lock()
	gotOp, gotCwd := sawMutateOp, sawMutateCwd
	mu.Unlock()
	if gotOp != "workspace" || gotCwd != resolvedNew {
		t.Fatalf("expected canonical session.mutate workspace=%q, got op=%q cwd=%q", resolvedNew, gotOp, gotCwd)
	}
	if len(a.Snapshot()) != 0 {
		t.Fatalf("expected detached viewer snapshot after move, got %+v", a.Snapshot())
	}
}

func TestHermesLegacyLocalMoveWorkspaceUpdatesStateDB(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "proj-a")
	newDir := filepath.Join(root, "proj-b")
	_ = os.MkdirAll(oldDir, 0o755)
	_ = os.MkdirAll(newDir, 0o755)
	resolvedOld, _ := filepath.EvalSymlinks(oldDir)
	resolvedNew, _ := filepath.EvalSymlinks(newDir)

	dbPath := filepath.Join(root, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT); INSERT INTO sessions (id, cwd) VALUES ('legacy-1', ?)`, resolvedOld); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a := New(Config{
		AgentID:     "local-hermes",
		Workspace:   resolvedOld,
		Allowed:     []string{root},
		StateDBPath: dbPath,
	})
	a.sessions["legacy-1"] = &SessionState{
		SessionID: "legacy-1",
		Status:    "idle",
		Workspace: resolvedOld,
		Metadata:  map[string]any{},
	}

	res, err := a.Command(context.Background(), core.Request{
		SessionID: "legacy-1",
		Action:    "session.move_workspace",
		Fields:    map[string]any{"workspace": resolvedNew},
	})
	if err != nil {
		t.Fatalf("legacy move failed: %v", err)
	}
	if res.Payload["moved"] != true || res.Payload["workspace"] != resolvedNew {
		t.Fatalf("unexpected payload: %+v", res.Payload)
	}

	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer verifyDB.Close()
	var stored string
	if err := verifyDB.QueryRow(`SELECT cwd FROM sessions WHERE id = 'legacy-1'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != resolvedNew {
		t.Fatalf("state.db cwd = %q, want %q", stored, resolvedNew)
	}
}
