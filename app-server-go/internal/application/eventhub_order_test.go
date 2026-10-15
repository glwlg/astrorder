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

func TestCommittedEventsPublishInDurableCursorOrder(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := NewEventHub(st)
	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()
	first, err := st.AppendEvent(context.Background(), store.Event{ID: "first", Type: "session.upsert", AgentID: "agent", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.AppendEvent(context.Background(), store.Event{ID: "second", Type: "command.upsert", AgentID: "agent", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	hub.PublishCommitted([]store.Event{*second})
	hub.PublishCommitted([]store.Event{*first})
	for _, want := range []int64{first.Cursor, second.Cursor} {
		select {
		case got := <-ch:
			if got.Cursor != want {
				t.Fatalf("cursor=%d want=%d", got.Cursor, want)
			}
		case <-time.After(time.Second):
			t.Fatal("missing committed event")
		}
	}
	select {
	case <-ch:
		t.Fatal("duplicate committed event broadcast")
	default:
	}
}

func TestEventWebSocketUsesLegacyWireAndNullIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := NewEventHub(st)
	if err = hub.Publish(ctx, "fixture.event", map[string]any{"number": json.Number("9007199254740993")}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hub.ServeWebSocket(w, r, nil) }))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http:", "ws:", 1)+"?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err = decoder.Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire["type"] != "fixture.event" || wire["cursor"] == nil || wire["agent_id"] != nil || wire["session_id"] != nil || wire["Type"] != nil {
		t.Fatalf("invalid wire field names or nulls: %v", wire)
	}
	if wire["data"].(map[string]any)["number"].(json.Number).String() != "9007199254740993" {
		t.Fatal("large number changed")
	}
}
