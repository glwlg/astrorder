package protocol

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/session-daemon/internal/parity"
	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type dummyManifestAdapter struct {
	handledActions []string
}

func (d *dummyManifestAdapter) Create(ctx context.Context, r core.Request) (core.Result, error) {
	d.handledActions = append(d.handledActions, r.Action)
	return core.Result{SessionID: "fix-sess-1", Status: "idle", Payload: map[string]any{"ok": true}}, nil
}

func (d *dummyManifestAdapter) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	d.handledActions = append(d.handledActions, r.Action)
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"ok": true}}, nil
}

func (d *dummyManifestAdapter) Command(ctx context.Context, r core.Request) (core.Result, error) {
	d.handledActions = append(d.handledActions, r.Action)
	return core.Result{SessionID: r.SessionID, Status: "idle", Payload: map[string]any{"action": r.Action, "executed": true}}, nil
}

func (d *dummyManifestAdapter) Snapshot() map[string]core.Result {
	return map[string]core.Result{
		"fix-sess-1": {SessionID: "fix-sess-1", Status: "idle"},
	}
}

func (d *dummyManifestAdapter) Close() error {
	return nil
}

func TestManifestAllActionsDispatchedAndExercised(t *testing.T) {
	var manifest struct {
		ControlActions []string `json:"control_actions"`
		SessionActions []string `json:"session_actions"`
	}
	if err := json.Unmarshal(parity.Manifest, &manifest); err != nil {
		t.Fatalf("failed to unmarshal manifest: %v", err)
	}

	secret := "parity-test-secret"
	d := New(secret)
	adapter := &dummyManifestAdapter{}
	d.registry.Register("codex", adapter)

	server := httptest.NewServer(d.Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial daemon websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Handshake
	if err := wsjson.Write(ctx, conn, map[string]any{
		"action":     "daemon.handshake",
		"request_id": "auth-1",
		"secret":     secret,
	}); err != nil {
		t.Fatalf("handshake write failed: %v", err)
	}
	var authResp map[string]any
	if err := wsjson.Read(ctx, conn, &authResp); err != nil {
		t.Fatalf("handshake read failed: %v", err)
	}
	if authResp["action"] != "daemon.handshake.result" {
		t.Fatalf("handshake failed: %#v", authResp)
	}

	// 1. First test session.create to get an active session
	if err := wsjson.Write(ctx, conn, map[string]any{
		"action":     "session.create",
		"request_id": "req-create",
		"agent_type": "codex",
	}); err != nil {
		t.Fatalf("session.create write failed: %v", err)
	}
	var createResp map[string]any
	if err := wsjson.Read(ctx, conn, &createResp); err != nil {
		t.Fatalf("session.create read failed: %v", err)
	}
	if createResp["action"] != "session.create.result" {
		t.Fatalf("session.create unexpected result: %#v", createResp)
	}

	// 2. Iterate through all session actions defined in manifest
	for idx, action := range manifest.SessionActions {
		if action == "session.create" {
			continue
		}
		req := map[string]any{
			"action":     action,
			"request_id": "req-sess-act",
			"session_id": "fix-sess-1",
			"agent_type": "codex",
		}
		if err := wsjson.Write(ctx, conn, req); err != nil {
			t.Fatalf("[%d] write %s failed: %v", idx, action, err)
		}
		var resp map[string]any
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			t.Fatalf("[%d] read %s response failed: %v", idx, action, err)
		}
		if resp["action"] != action+".result" {
			t.Fatalf("[%d] expected %s.result, got %#v", idx, action, resp)
		}
	}

	// 3. Verify control actions: daemon.status
	if err := wsjson.Write(ctx, conn, map[string]any{
		"action":     "daemon.status",
		"request_id": "req-status",
	}); err != nil {
		t.Fatalf("daemon.status write failed: %v", err)
	}
	var statusResp map[string]any
	if err := wsjson.Read(ctx, conn, &statusResp); err != nil {
		t.Fatalf("daemon.status read failed: %v", err)
	}
	if statusResp["action"] != "daemon.status.result" || statusResp["sessions"] == nil {
		t.Fatalf("daemon.status unexpected: %#v", statusResp)
	}

	// 4. Verify session.sync
	if err := wsjson.Write(ctx, conn, map[string]any{
		"action":     "session.sync",
		"request_id": "req-sync",
		"sessions":   map[string]any{"fix-sess-1": 0},
	}); err != nil {
		t.Fatalf("session.sync write failed: %v", err)
	}
	var syncResp map[string]any
	if err := wsjson.Read(ctx, conn, &syncResp); err != nil {
		t.Fatalf("session.sync read failed: %v", err)
	}
	if syncResp["action"] != "session.sync.result" {
		t.Fatalf("session.sync unexpected: %#v", syncResp)
	}
}
