package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestDeleteRequiresNativeRemovalAndIdleOwner(t *testing.T) {
	home := t.TempDir()
	a := New(Config{Home: home})
	id := "11111111-1111-4111-8111-111111111111"
	os.MkdirAll(a.historyDir(), 0700)
	db, err := sql.Open("sqlite", filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE conversation_summaries(conversation_id TEXT); INSERT INTO conversation_summaries VALUES(?)", id); err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/exa.language_server_pb.LanguageServerService/DeleteCascadeTrajectory" {
			t.Errorf("wrong method: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["cascadeId"] != id {
			t.Error("wrong native identity")
		}
		if remove {
			if _, err := db.Exec("DELETE FROM conversation_summaries WHERE conversation_id=?", id); err != nil {
				t.Error(err)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()
	s := &session{id: id, status: "running", nativeURL: srv.URL}
	a.sessions[id] = s
	req := core.Request{SessionID: id, Action: "session.delete"}
	if _, err := a.Command(context.Background(), req); err == nil || calls != 0 {
		t.Fatal("active deletion dispatched")
	}
	s.status = "idle"
	if _, err := a.Command(context.Background(), req); err == nil || calls != 1 {
		t.Fatal("HTTP success falsely confirmed deletion")
	}
	if a.sessions[id] == nil {
		t.Fatal("unconfirmed owner removed")
	}
	if _, err := a.Command(context.Background(), req); err == nil || calls != 1 {
		t.Fatal("unconfirmed deletion was redispatched")
	}
	if _, err := a.Command(context.Background(), core.Request{SessionID: id, Action: "session.send", Fields: map[string]any{"text": "must stay blocked"}}); err == nil {
		t.Fatal("unconfirmed deletion unlocked sends")
	}
	remove = true
	if _, err := db.Exec("DELETE FROM conversation_summaries WHERE conversation_id=?", id); err != nil {
		t.Fatal(err)
	}
	result, err := a.Command(context.Background(), req)
	if err != nil || result.Payload["deleted"] != id || a.sessions[id] != nil || calls != 1 {
		t.Fatalf("delete: %+v %v", result, err)
	}
}
