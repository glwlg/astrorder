package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
	"github.com/coder/websocket"
)

func TestLiveTranscriptCrossesWebSocketWithoutPersistenceOrReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := NewEventHub(st)
	if err := hub.Publish(ctx, "fixture.ready", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hub.ServeWebSocket(w, r, nil) }))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http:", "ws:", 1)+"?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func(c *websocket.Conn) store.Event {
		t.Helper()
		_, raw, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Cursor int64          `json:"cursor"`
			Type   string         `json:"type"`
			Data   map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		return store.Event{Cursor: wire.Cursor, Type: wire.Type, Data: wire.Data}
	}
	if got := read(conn); got.Type != "fixture.ready" {
		t.Fatal(got)
	}
	for _, kind := range []string{"message.delta", "message.upsert", "task.live", "hermes.stream", "codex.notification", "grok.notification"} {
		if err := hub.Publish(ctx, kind, map[string]any{"text": "private transcript"}); err != nil {
			t.Fatal(err)
		}
		if got := read(conn); got.Type != kind || got.Cursor != 0 || got.Data["text"] != "private transcript" {
			t.Fatalf("live payload lost: %+v", got)
		}
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE data LIKE '%private transcript%'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("transcript persisted: %d %v", count, err)
	}
	if err := hub.Publish(ctx, "fixture.done", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := read(conn); got.Type != "fixture.done" || got.Cursor != 2 {
		t.Fatal(got)
	}
	replay, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http:", "ws:", 1)+"?after=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.CloseNow()
	if got := read(replay); got.Type != "fixture.done" {
		t.Fatalf("transcript replayed: %+v", got)
	}
}

func TestLegacyTranscriptReplayAdvancesCursorWithoutReturningContent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.DB().ExecContext(ctx, `INSERT INTO events(event_id,type,data,created_at) VALUES('legacy','message.upsert','{"text":"legacy private"}','2026-10-14T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	resync, events, err := st.ReplayEvents(ctx, 0)
	if err != nil || resync || len(events) != 1 || events[0].Cursor != 1 || events[0].Type != "event.cursor" || len(events[0].Data) != 0 {
		t.Fatalf("legacy payload returned: %+v %v", events, err)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE data LIKE '%legacy private%'").Scan(&count); err != nil || count != 1 {
		t.Fatal("existing data was deleted", count, err)
	}
}
