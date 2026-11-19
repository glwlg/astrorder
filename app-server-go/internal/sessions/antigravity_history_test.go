package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestAntigravityHistoryProjectsThinkingBeforeResponse(t *testing.T) {
	item := map[string]any{"id": "agy-step-1", "role": "assistant", "kind": "message", "text": "answer", "thinking": "native thought", "created_at": "2026-10-18T01:00:00Z"}
	rows := projectHistoryItem(item, "agy", "session", "session", "agy", "antigravity")
	if len(rows) != 2 {
		t.Fatalf("native thought dropped: %+v", rows)
	}
	if rows[0]["kind"] != "thinking" || rows[0]["text"] != "native thought" || rows[0]["id"] == rows[1]["id"] {
		t.Fatalf("thinking identity: %+v", rows)
	}
	if rows[1]["id"] != item["id"] || rows[1]["text"] != "answer" {
		t.Fatalf("response changed: %+v", rows)
	}
	if again := projectHistoryItem(item, "agy", "session", "session", "agy", "antigravity"); again[0]["id"] != rows[0]["id"] {
		t.Fatal("unstable thought identity")
	}
}

func TestAntigravityEmptyThinkingDoesNotAddMessage(t *testing.T) {
	rows := projectHistoryItem(map[string]any{"id": "step", "role": "assistant", "text": "answer", "thinking": ""}, "agy", "session", "session", "agy", "antigravity")
	if len(rows) != 1 {
		t.Fatalf("empty thinking row: %+v", rows)
	}
}

func TestAntigravityHistoryRestoresUserCommandAttachmentsAndIsolatesTimestamps(t *testing.T) {
	ctx := context.Background()
	sid := "5f4b0925-6116-40d0-a2ab-8e2fc6e6a4fc"
	aid := "ssh-antigravity-wsl"

	raw1, _ := json.Marshal(map[string]any{
		"id":          "antigravity:" + sid + ":10",
		"role":        "user",
		"kind":        "message",
		"text":        "review-platform-role-mapping.sql查出来的内容十几万个字符，我发你前面几个字段吧",
		"attachments": []any{},
		"created_at":  "2026-11-18T08:14:08.100Z",
	})
	raw2, _ := json.Marshal(map[string]any{
		"id":          "antigravity:" + sid + ":20",
		"role":        "user",
		"kind":        "message",
		"text":        "继续",
		"attachments": []any{},
		"created_at":  "2026-11-18T10:35:05.000Z",
	})
	raw3, _ := json.Marshal(map[string]any{
		"id":   "antigravity:" + sid + ":30",
		"role": "user",
		"kind": "message",
		"text": "CLI本地传图",
		"attachments": []any{
			map[string]any{
				"id":         "antigravity-media-1",
				"name":       "uploaded_media_1.png",
				"media_type": "image/png",
				"url":        "data:image/png;base64,iVBORw0KGgo=",
			},
		},
		"created_at": "2026-11-18T10:40:00.000Z",
	})

	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{
		getHistoryPageCb: func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
			next := "10"
			return &pb.HistoryPageResponse{
				Session: r.Session,
				Items: []*pb.NativeJson{
					{Schema: "antigravity.item.v1", Utf8Json: raw1},
					{Schema: "antigravity.item.v1", Utf8Json: raw2},
					{Schema: "antigravity.item.v1", Utf8Json: raw3},
				},
				NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: next}},
			}, nil
		},
	})
	defer cleanup()

	if _, err := st.UpsertAgent(ctx, map[string]any{"id": aid, "kind": "antigravity-ssh", "name": "Antigravity · WSL", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":            sid,
		"agent_id":      aid,
		"status":        "idle",
		"history_state": "available",
		"control_state": "owned",
		"updated_at":    "2026-11-18T08:14:07Z",
	}); err != nil {
		t.Fatal(err)
	}

	// Insert both a matching 08:14 command with screenshot and an old 06:00 "继续" command with screenshot.
	for _, cmd := range []struct {
		id, text, at, attID string
	}{
		{"cmd-old-continue", "继续", "2026-11-18 06:00:00.000000", "old-att"},
		{"d3317b0b-d2d4-49ec-9f1a-c3a80b2f3c1f", "review-platform-role-mapping.sql查出来的内容十几万个字符，我发你前面几个字段吧", "2026-11-18 08:14:07.408559", "0e17daab410f903c9c5fbe88045f01ab"},
	} {
		if _, _, err := st.CreateCommand(ctx, map[string]any{
			"id":         cmd.id,
			"agent_id":   aid,
			"session_id": sid,
			"action":     "send",
			"text":       cmd.text,
			"attachments": []any{
				map[string]any{
					"id":         cmd.attID,
					"name":       "image.png",
					"media_type": "image/png",
					"url":        "/api/v1/attachments/" + cmd.attID,
				},
			},
		}, "completed"); err != nil {
			t.Fatal(err)
		}
		if err := st.WithTx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE commands SET created_at=?, updated_at=? WHERE id=?", cmd.at, cmd.at, cmd.id)
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("GET", "/api/v1/sessions/"+sid+"/messages?agent_id="+aid+"&limit=50", nil)
	rec := httptest.NewRecorder()
	h.handleListMessages(rec, req, sid)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(resp.Items))
	}

	// Item 0 (08:14): matched stored command attachment URL & command_id!
	atts0, _ := resp.Items[0]["attachments"].([]any)
	if len(atts0) != 1 || atts0[0].(map[string]any)["url"] != "/api/v1/attachments/0e17daab410f903c9c5fbe88045f01ab" || resp.Items[0]["command_id"] != "d3317b0b-d2d4-49ec-9f1a-c3a80b2f3c1f" {
		t.Fatalf("expected restored attachment on 08:14 user message, got %+v", resp.Items[0])
	}
	// Item 1 (10:35 "继续"): must NOT inherit 06:00 "继续" attachment!
	atts1, _ := resp.Items[1]["attachments"].([]any)
	if len(atts1) != 0 {
		t.Fatalf("expected 0 attachments on later unrelated '继续' message, got %+v", atts1)
	}
	// Item 2 (native CLI data URI): preserved!
	atts2, _ := resp.Items[2]["attachments"].([]any)
	if len(atts2) != 1 || atts2[0].(map[string]any)["name"] != "uploaded_media_1.png" {
		t.Fatalf("expected native CLI attachment preserved, got %+v", resp.Items[2])
	}
}
