package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/notifications"
	"astrorder.dev/app-server/internal/store"
)

// EventHub manages durable event storage through store.Store and live dispatch
// to subscribed browser WebSockets.
type EventHub struct {
	store            *store.Store
	publishMu        sync.Mutex
	mu               sync.RWMutex
	subscribers      map[chan *store.Event]struct{}
	publishedThrough int64
	sessionCleanup   func(context.Context, string, string)
}

// NewEventHub creates an EventHub attached to the given durable store.
func NewEventHub(st *store.Store) *EventHub {
	return &EventHub{
		store:       st,
		subscribers: make(map[chan *store.Event]struct{}),
	}
}

// SetSessionCleanup connects resource release to committed session deletions.
func (h *EventHub) SetSessionCleanup(cleanup func(context.Context, string, string)) {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.sessionCleanup = cleanup
}

// PublishCommitted broadcasts already-persisted events directly without re-inserting them into store.
func (h *EventHub) PublishCommitted(events []store.Event) {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.broadcastCommitted(events)
}

// Publish stores the event in the durable store and broadcasts it to live subscribers.
func (h *EventHub) Publish(ctx context.Context, eventType string, data map[string]any) error {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()

	id := randomID()
	var agentID, sessionID string
	if data != nil {
		if aid, ok := data["agent_id"].(string); ok {
			agentID = aid
		}
		if sid, ok := data["session_id"].(string); ok {
			sessionID = sid
		}
	}

	ev := store.Event{
		ID:        id,
		Type:      eventType,
		AgentID:   agentID,
		SessionID: sessionID,
		Data:      data,
	}

	events := []store.Event{ev}
	if h.store != nil {
		notices, err := notifications.Events(ctx, h.store.DB(), events)
		if err != nil {
			return fmt.Errorf("create notification: %w", err)
		}
		events, err = h.store.AppendEvents(ctx, append(events, notices...))
		if err != nil {
			return fmt.Errorf("append event: %w", err)
		}
	}
	h.broadcastCommitted(events)
	return nil
}

// Commits from independent writers can arrive here in a different order.
// Read the durable cursor interval so an earlier event cannot be skipped by
// a browser that has already observed the later cursor.
func (h *EventHub) broadcastCommitted(events []store.Event) {
	transient := make([]store.Event, 0)
	for _, event := range events {
		if store.IsTransientEvent(event.Type) {
			transient = append(transient, event)
		}
	}
	if h.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resync, durable, err := h.store.ReplayEvents(ctx, h.publishedThrough)
		if err != nil || resync {
			latest, latestErr := h.store.LatestCursor(ctx)
			if latestErr == nil {
				h.publishedThrough = latest
			}
			h.broadcast(&store.Event{Cursor: latest, Type: "resync_required", Data: map[string]any{"reason": "event_cursor_gap"}})
			for i := range transient {
				h.broadcast(&transient[i])
			}
			return
		}
		events = durable
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Cursor < events[j].Cursor })
	for i := range events {
		if events[i].Cursor > 0 && events[i].Cursor <= h.publishedThrough {
			continue
		}
		if events[i].Type == "session.delete" && h.sessionCleanup != nil {
			aid, sid := events[i].AgentID, events[i].SessionID
			if aid == "" {
				aid, _ = events[i].Data["agent_id"].(string)
			}
			if sid == "" {
				sid, _ = events[i].Data["id"].(string)
			}
			if aid != "" && sid != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				h.sessionCleanup(ctx, aid, sid)
				cancel()
			}
		}
		h.broadcast(&events[i])
		if events[i].Cursor > h.publishedThrough {
			h.publishedThrough = events[i].Cursor
		}
	}
	if h.store != nil {
		for i := range transient {
			h.broadcast(&transient[i])
		}
	}
}

func eventWire(event store.Event) map[string]any {
	var agent, session any
	if event.AgentID != "" {
		agent = event.AgentID
	}
	if event.SessionID != "" {
		session = event.SessionID
	}
	return map[string]any{"id": event.ID, "cursor": event.Cursor, "type": event.Type,
		"agent_id": agent, "session_id": session, "data": event.Data}
}

func (h *EventHub) broadcast(ev *store.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- ev:
		default:
			// Overflow: drain and post resync_required
			for {
				select {
				case <-ch:
				default:
					goto DRAINED
				}
			}
		DRAINED:
			resync := &store.Event{
				ID:     fmt.Sprintf("resync-live-%d", ev.Cursor),
				Cursor: ev.Cursor,
				Type:   "resync_required",
				Data:   map[string]any{"reason": "live_queue_overflow"},
			}
			select {
			case ch <- resync:
			default:
			}
		}
	}
}

func (h *EventHub) subscribe() (chan *store.Event, func()) {
	ch := make(chan *store.Event, 256)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
		close(ch)
	}
	return ch, unsubscribe
}

// ServeWebSocket serves the /ws/v1/events subscription endpoint matching Python contract.
func (h *EventHub) ServeWebSocket(response http.ResponseWriter, request *http.Request, authorizer *auth.Authenticator) {
	if authorizer != nil {
		if failure := authorizer.AuthorizeBrowser(request); failure != nil {
			http.Error(response, failure.Detail, failure.Status)
			return
		}
	}

	rawAfter := request.URL.Query().Get("after")
	after := int64(0)
	if rawAfter != "" {
		parsed, err := strconv.ParseInt(rawAfter, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(response, "Invalid cursor", http.StatusBadRequest)
			return
		}
		after = parsed
	}

	conn, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := request.Context()
	ch, unsub := h.subscribe()
	defer unsub()

	lastCursor := after

	if h.store != nil {
		resync, replay, err := h.store.ReplayEvents(ctx, after)
		if err != nil {
			_ = conn.Close(websocket.StatusInternalError, "Replay failed")
			return
		}
		if resync {
			latest, err := h.store.LatestCursor(ctx)
			if err != nil {
				_ = conn.Close(websocket.StatusInternalError, "Latest cursor failed")
				return
			}
			resyncMsg := map[string]any{
				"id":         fmt.Sprintf("resync-%d", latest),
				"cursor":     latest,
				"type":       "resync_required",
				"agent_id":   nil,
				"session_id": nil,
				"data":       map[string]any{"reason": "cursor_out_of_range"},
			}
			raw, _ := json.Marshal(resyncMsg)
			writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				return
			}
			lastCursor = latest
		} else {
			for _, event := range replay {
				raw, err := json.Marshal(eventWire(event))
				if err == nil {
					writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					err = conn.Write(writeCtx, websocket.MessageText, raw)
					cancel()
					if err != nil {
						return
					}
					if event.Cursor > lastCursor {
						lastCursor = event.Cursor
					}
				}
			}
		}
	}

	readErrCh := make(chan error, 1)
	go func() {
		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				readErrCh <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-readErrCh:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Cursor <= lastCursor && ev.Type != "resync_required" && !store.IsTransientEvent(ev.Type) {
				continue
			}
			raw, err := json.Marshal(eventWire(*ev))
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				return
			}
			if ev.Cursor > lastCursor {
				lastCursor = ev.Cursor
			}
		}
	}
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
