package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChildHistoryRestoresNewestStateWithoutOverwritingLiveState(t *testing.T) {
	srv := &mockDaemonServer{getHistoryPageCb: func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		items := []*pb.NativeJson{}
		for _, status := range []string{"running", "completed"} {
			items = append(items, &pb.NativeJson{Schema: "codex.item.v1", Utf8Json: []byte(fmt.Sprintf(`{"id":"spawn-%s","type":"collabAgentToolCall","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"%s"}}}`, status, status))})
		}
		return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": "a", "id": "parent", "title": "test", "status": "idle", "control_state": "owned", "history_state": "available", "updated_at": "2026-10-13T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/parent/messages?agent_id=a", nil)
		r.Header.Set("Authorization", "Bearer browser-sec")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	read()
	rows, err := st.ListTasks(ctx, "a", "parent")
	if err != nil || len(rows) != 1 || rows[0]["status"] != "completed" {
		t.Fatal("older spawn snapshot won", rows, err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE tasks SET status='running' WHERE agent_id='a' AND session_id='parent'`); err != nil {
		t.Fatal(err)
	}
	read()
	rows, err = st.ListTasks(ctx, "a", "parent")
	if err != nil || len(rows) != 1 || rows[0]["status"] != "running" {
		t.Fatal("history overwrote live child", rows, err)
	}
}
