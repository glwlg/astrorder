package projection

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/notifications"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type noticePublisher struct{ events []store.Event }

func (p *noticePublisher) PublishCommitted(events []store.Event) {
	p.events = append(p.events, events...)
}

func TestNativeNotificationSharesCheckpointAndDoesNotReplay(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "connected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "测试", "status": "running", "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	pub := &noticePublisher{}
	p, err := New(&daemonclient.Client{}, st, pub)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]int64{}
	for seq, method := range []string{"item/started", "item/completed", "turn/completed"} {
		params := map[string]any{"threadId": "s", "turnId": "t"}
		if method == "turn/completed" {
			params["turn"] = map[string]any{"id": "t", "status": "completed"}
		} else {
			params["item"] = map[string]any{"id": "q", "type": "agentMessage", "text": "private question", "delivery": "async", "questions": []any{map[string]any{"title": "private question", "options": []any{"yes", "no"}}}}
		}
		raw, _ := json.Marshal(map[string]any{"agent_id": "a", "session_id": "s", "frame": map[string]any{"method": method, "params": params}})
		aid := "a"
		env := &pb.EventEnvelope{DaemonId: "epoch", Session: &pb.SessionRef{SessionId: "s", AgentId: &aid}, SeqId: int64(seq + 1), EventType: "codex.notification", NativePayload: &pb.NativeJson{Schema: "astrorder.event.payload.v1", Utf8Json: raw}}
		if seq == 0 {
			if _, err = st.DB().Exec(`CREATE TRIGGER reject_notice BEFORE INSERT ON events WHEN NEW.type='notification.created' BEGIN SELECT RAISE(ABORT,'notification fixture'); END`); err != nil {
				t.Fatal(err)
			}
			if err = p.processEnvelope(ctx, "epoch", env, expected); err == nil {
				t.Fatal("failed notice committed")
			}
			cp, e := st.GetCheckpoint(ctx, "epoch", "s")
			if e != nil || cp != 0 || len(pub.events) != 0 {
				t.Fatalf("partial commit: %d %v", cp, e)
			}
			if _, err = st.DB().Exec(`DROP TRIGGER reject_notice`); err != nil {
				t.Fatal(err)
			}
		}
		if err = p.processEnvelope(ctx, "epoch", env, expected); err != nil {
			t.Fatal(err)
		}
		if err = p.processEnvelope(ctx, "epoch", env, expected); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.QueryRows(ctx, `SELECT * FROM events WHERE type=? ORDER BY cursor`, notifications.EventType)
	if err != nil || len(rows) != 2 {
		t.Fatalf("wanted question and completion once, got %v %v", rows, err)
	}
	for _, r := range rows {
		if r["cursor"].(int64) <= 0 {
			t.Fatal("notice not durable")
		}
	}
	cp, err := st.GetCheckpoint(ctx, "epoch", "s")
	if err != nil || cp != 3 {
		t.Fatal(cp, err)
	}
}
