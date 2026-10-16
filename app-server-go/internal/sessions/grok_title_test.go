package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGrokRenamePersistsAstrorderTitleWithoutUnsupportedNativeMutation(t *testing.T) {
	called := false
	srv := &mockDaemonServer{renameCb: func(*pb.RenameSessionRequest) (*pb.CommandReceipt, error) {
		called = true
		return nil, status.Error(codes.Unimplemented, "Grok ACP has no rename")
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "grok", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "原标题", "status": "idle", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/s", strings.NewReader(`{"agent_id":"a","title":"我的 Grok 标题"}`))
	req.Header.Set("Authorization", "Bearer browser-sec")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	row, err := st.GetSession(ctx, "a", "s")
	if w.Code != http.StatusOK || called || err != nil || row["title"] != "我的 Grok 标题" {
		t.Fatalf("local Grok title edit failed: status=%d native_called=%v row=%v err=%v body=%s", w.Code, called, row, err, w.Body.String())
	}
}
