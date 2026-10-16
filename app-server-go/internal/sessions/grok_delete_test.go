package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGrokDeleteDoesNotHideFailedNativeDeletion(t *testing.T) {
	srv := &mockDaemonServer{deleteCb: func(*pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		return nil, status.Error(codes.NotFound, "session is not daemon-owned")
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "grok", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "保留", "status": "idle", "workspace": "C:/work", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/s?agent_id=a", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	row, err := st.GetSession(ctx, "a", "s")
	if w.Code == http.StatusOK || row == nil || err != nil {
		t.Fatalf("failed native deletion reported success: status=%d row=%v err=%v", w.Code, row, err)
	}
}
