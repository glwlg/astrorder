package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityModelSwitchUsesNativeCapability(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{readModelCb: func(*pb.ReadModelRequest) (*pb.ModelState, error) {
		return &pb.ModelState{Native: &pb.NativeJson{Utf8Json: []byte(`{"model_switch_supported":true}`)}}, nil
	}})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "local-antigravity", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "local-antigravity", "status": "idle", "control_state": "owned", "updated_at": "2026-10-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/native/model?agent_id=local-antigravity", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var binding map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &binding) != nil {
		t.Fatalf("model: %d %s", rec.Code, rec.Body.String())
	}
	if binding["model_read_only"] == true {
		t.Fatal("native-confirmed switching is disabled")
	}
	if binding["reasoning_read_only"] != true {
		t.Fatal("unsupported reasoning must remain disabled")
	}
}
