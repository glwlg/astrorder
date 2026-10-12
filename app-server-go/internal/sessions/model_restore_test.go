package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

func startModelRestoreTarget(t *testing.T, srv *modelRestoreServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDaemonControlServer(server, srv)
	pb.RegisterRuntimeQueryServer(server, srv)
	go func() { _ = server.Serve(lis) }()
	return lis.Addr().String(), func() { server.Stop(); _ = lis.Close() }
}

func dialModelRestoreTarget(t *testing.T, addr string) *daemonclient.Client {
	t.Helper()
	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: addr, Credential: "test-secret", RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type modelRestoreServer struct {
	mockDaemonServer
	provider    string
	model       string
	setReadback string
	setCalls    int
}

func (s *modelRestoreServer) ReadModel(context.Context, *pb.ReadModelRequest) (*pb.ModelState, error) {
	return &pb.ModelState{Provider: &s.provider, Model: &s.model}, nil
}

func (s *modelRestoreServer) SetModel(_ context.Context, req *pb.SetModelRequest) (*pb.CommandReceipt, error) {
	s.setCalls++
	s.provider = req.Provider
	s.model = s.setReadback
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
}

func TestSavedModelAcceptsOnlyItsProviderQualification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		initial    string
		readback   string
		wantStatus int
		wantSend   int
		wantSet    int
	}{
		{"already qualified", "magpie/antigravity/gemini-3.8-flash", "", 200, 1, 0},
		{"qualified after restore", "old-model", "magpie/antigravity/gemini-3.8-flash", 200, 1, 1},
		{"tiered is distinct", "old-model", "magpie/antigravity/gemini-3.8-flash-tiered", 500, 0, 1},
		{"foreign prefix is distinct", "old-model", "foreign/antigravity/gemini-3.8-flash", 500, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &modelRestoreServer{provider: "magpie", model: tc.initial, setReadback: tc.readback}
			h, st, dc, cleanup := setupTestServer(t, &srv.mockDaemonServer)
			defer cleanup()
			// Replace only RuntimeQuery; use the normal HTTP handler and Send counter.
			addr, stop := startModelRestoreTarget(t, srv)
			defer stop()
			client := dialModelRestoreTarget(t, addr)
			defer client.Close()
			h.client.Runtime = client.Runtime
			_ = dc
			ctx := context.Background()
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "idle", "control_state": "owned", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetSessionModelBinding(ctx, "a", "s", "magpie", "antigravity/gemini-3.8-flash"); err != nil {
				t.Fatal(err)
			}
			commandSendCalls = 0
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/commands", bytes.NewBufferString(`{"id":"model-restore","agent_id":"a","session_id":"s","action":"send","text":"fixture"}`))
			r.Header.Set("Authorization", "Bearer browser-sec")
			h.ServeHTTP(w, r)
			if w.Code != tc.wantStatus || commandSendCalls != tc.wantSend || srv.setCalls != tc.wantSet {
				t.Fatalf("status=%d sends=%d sets=%d body=%s", w.Code, commandSendCalls, srv.setCalls, w.Body.String())
			}
		})
	}
}

func TestModelSwitchRejectsUnrelatedNamespaceSuffix(t *testing.T) {
	srv := &modelRestoreServer{provider: "magpie", model: "old-model", setReadback: "foreign/antigravity/gemini-3.8-flash"}
	h, st, _, cleanup := setupTestServer(t, &srv.mockDaemonServer)
	defer cleanup()
	addr, stop := startModelRestoreTarget(t, srv)
	defer stop()
	client := dialModelRestoreTarget(t, addr)
	defer client.Close()
	h.client.Runtime = client.Runtime
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "fixture", "status": "idle", "control_state": "owned", "updated_at": "2026-10-12T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"agent_id": "a", "provider": "magpie", "model": "antigravity/gemini-3.8-flash"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/s/model", bytes.NewReader(body))
	h.handleSetSessionModel(w, r, "s")
	binding, err := st.GetSessionModelBinding(ctx, "a", "s")
	if w.Code != 502 || err != nil || binding != nil {
		t.Fatalf("status=%d binding=%v err=%v", w.Code, binding, err)
	}
}
