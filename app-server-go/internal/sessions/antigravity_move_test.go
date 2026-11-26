package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAntigravityMoveWorkspaceUpdatesProjectionAndReleasesBinding(t *testing.T) {
	var sawDeleteOnly bool
	var movedTarget string
	srv := &mockDaemonServer{
		readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
			return nil, status.Error(codes.NotFound, "session is not daemon-owned")
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			var opts map[string]any
			if r.NativeOptions != nil && parseNativeJSON(r.NativeOptions, &opts) == nil && opts["delete_only"] == true {
				sawDeleteOnly = true
			}
			return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		moveWorkspaceCb: func(r *pb.MoveSessionWorkspaceRequest) (*pb.CommandReceipt, error) {
			movedTarget = r.Workspace
			native, _ := encodeNativeJSON("astrorder.move_workspace.v1", map[string]any{
				"workspace": "/workspace/target-proj",
				"moved":     true,
			})
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, Native: native}},
			}, nil
		},
	}

	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy-1", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertProject(ctx, map[string]any{
		"source_id":    "local",
		"project_id":   "proj-target",
		"project_name": "TargetProj",
		"workspace":    "/workspace/target-proj",
		"updated_at":   "2026-10-18T00:00:00Z",
	})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":            "11111111-1111-4111-8111-111111111111",
		"agent_id":      "agy-1",
		"title":         "Old Project Chat",
		"workspace":     "/workspace/old-proj",
		"project_id":    "proj-old",
		"project_name":  "OldProj",
		"status":        "idle",
		"control_state": "owned",
		"updated_at":    "2026-10-18T00:00:00Z",
	})

	body, _ := json.Marshal(map[string]any{
		"agent_id":  "agy-1",
		"workspace": "/workspace/target-proj",
	})
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/11111111-1111-4111-8111-111111111111", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer browser-sec")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 || !sawDeleteOnly || movedTarget != "/workspace/target-proj" {
		t.Fatalf("expected 200 move, code=%d sawDeleteOnly=%v movedTarget=%q body=%s", w.Code, sawDeleteOnly, movedTarget, w.Body.String())
	}

	updated, err := st.GetSession(ctx, "agy-1", "11111111-1111-4111-8111-111111111111")
	if err != nil || updated == nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stringValue(updated, "workspace") != "/workspace/target-proj" {
		t.Fatalf("workspace = %q, want /workspace/target-proj", stringValue(updated, "workspace"))
	}
	if stringValue(updated, "project_id") != "proj-target" || stringValue(updated, "project_name") != "TargetProj" {
		t.Fatalf("project = (%q, %q), want (proj-target, TargetProj)", stringValue(updated, "project_id"), stringValue(updated, "project_name"))
	}
	if stringValue(updated, "control_state") != "unbound" {
		t.Fatalf("control_state = %q, want unbound", stringValue(updated, "control_state"))
	}
}

func TestAntigravityMoveWorkspaceRejectsRunningAndUnimplementedDaemon(t *testing.T) {
	srv := &mockDaemonServer{
		moveWorkspaceCb: func(*pb.MoveSessionWorkspaceRequest) (*pb.CommandReceipt, error) {
			return nil, status.Error(codes.Unimplemented, "method MoveSessionWorkspace not implemented")
		},
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "agy-1", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{
		"id":            "22222222-2222-4222-8222-222222222222",
		"agent_id":      "agy-1",
		"workspace":     "/workspace/old",
		"status":        "running",
		"control_state": "owned",
		"updated_at":    "2026-10-18T00:00:00Z",
	})

	body, _ := json.Marshal(map[string]any{"agent_id": "agy-1", "workspace": "/workspace/new"})
	req := httptest.NewRequest("PATCH", "/api/v1/sessions/22222222-2222-4222-8222-222222222222", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "空闲") {
		t.Fatalf("expected 409 idle guard, got %d: %s", w.Code, w.Body.String())
	}

	// Mark idle -> hits Unimplemented daemon -> friendly tray upgrade hint.
	_, _ = st.UpdateSession(ctx, "agy-1", "22222222-2222-4222-8222-222222222222", map[string]any{"status": "idle"})
	req2 := httptest.NewRequest("PATCH", "/api/v1/sessions/22222222-2222-4222-8222-222222222222", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer browser-sec")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != 409 || !strings.Contains(w2.Body.String(), "更新小内核") {
		t.Fatalf("expected 409 tray upgrade message, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestCodexHermesGrokMoveWorkspaceUsesNativeRPC(t *testing.T) {
	for _, tc := range []struct {
		name    string
		agentID string
		kind    string
		sid     string
	}{
		{name: "codex", agentID: "codex-1", kind: "codex", sid: "codex-thread-1"},
		{name: "hermes", agentID: "hermes-1", kind: "hermes", sid: "hermes-sess-1"},
		{name: "grok", agentID: "grok-1", kind: "grok", sid: "grok-sess-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attached bool
			var movedTarget string
			srv := &mockDaemonServer{
				readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
					if attached {
						return &pb.ModelState{Model: optString("default")}, nil
					}
					return nil, status.Error(codes.NotFound, "session is not daemon-owned")
				},
				attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
					attached = true
					return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
				},
				moveWorkspaceCb: func(r *pb.MoveSessionWorkspaceRequest) (*pb.CommandReceipt, error) {
					movedTarget = r.Workspace
					native, _ := encodeNativeJSON("astrorder.move_workspace.v1", map[string]any{
						"workspace": "/workspace/target-proj",
						"moved":     true,
					})
					return &pb.CommandReceipt{
						OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
						Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, Native: native}},
					}, nil
				},
			}

			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			_, _ = st.UpsertAgent(ctx, map[string]any{"id": tc.agentID, "kind": tc.kind, "name": tc.name, "status": "ready"})
			_, _ = st.UpsertProject(ctx, map[string]any{
				"source_id":    "local",
				"project_id":   "proj-target",
				"project_name": "TargetProj",
				"workspace":    "/workspace/target-proj",
				"updated_at":   "2026-10-18T00:00:00Z",
			})
			_, _ = st.UpsertSession(ctx, map[string]any{
				"id":            tc.sid,
				"agent_id":      tc.agentID,
				"title":         "Old Chat",
				"workspace":     "/workspace/old-proj",
				"project_id":    "proj-old",
				"project_name":  "OldProj",
				"status":        "idle",
				"control_state": "owned",
				"updated_at":    "2026-10-18T00:00:00Z",
			})

			body, _ := json.Marshal(map[string]any{
				"agent_id":  tc.agentID,
				"workspace": "/workspace/target-proj",
			})
			req := httptest.NewRequest("PATCH", "/api/v1/sessions/"+tc.sid, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer browser-sec")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != 200 || !attached || movedTarget != "/workspace/target-proj" {
				t.Fatalf("expected 200 move, code=%d attached=%v movedTarget=%q body=%s", w.Code, attached, movedTarget, w.Body.String())
			}

			updated, err := st.GetSession(ctx, tc.agentID, tc.sid)
			if err != nil || updated == nil {
				t.Fatalf("GetSession: %v", err)
			}
			if stringValue(updated, "workspace") != "/workspace/target-proj" {
				t.Fatalf("workspace = %q, want /workspace/target-proj", stringValue(updated, "workspace"))
			}
			if stringValue(updated, "project_id") != "proj-target" || stringValue(updated, "project_name") != "TargetProj" {
				t.Fatalf("project = (%q, %q), want (proj-target, TargetProj)", stringValue(updated, "project_id"), stringValue(updated, "project_name"))
			}
			if stringValue(updated, "control_state") != "unbound" {
				t.Fatalf("control_state = %q, want unbound", stringValue(updated, "control_state"))
			}
		})
	}
}
