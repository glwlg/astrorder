package notifications

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/preferences"
	"astrorder.dev/app-server/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.UpsertAgent(context.Background(), map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "connected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(context.Background(), map[string]any{"id": "s", "agent_id": "a", "title": "通知测试", "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCanonicalNoticesDeduplicateNativeFrames(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	cases := []struct {
		name, eventType, kind string
		data                  map[string]any
	}{
		{"completion", "codex.notification", "task_completed", map[string]any{"frame": map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "t", "status": "completed"}}}}},
		{"async question", "message.upsert", "approval_pending", map[string]any{"id": "q", "role": "assistant", "delivery": "async", "questions": []any{map[string]any{"title": "private question"}}}},
		{"sync question", "approval.upsert", "approval_pending", map[string]any{"id": "rpc", "state": "pending", "method": "item/tool/requestUserInput"}},
		{"background completion", "task.upsert", "task_completed", map[string]any{"id": "build", "kind": "background", "status": "completed"}},
		{"failure", "task.upsert", "task_failed", map[string]any{"id": "build", "kind": "background", "status": "failed"}},
		{"tool", "task.upsert", "", map[string]any{"id": "tool", "kind": "tool", "status": "completed"}},
		{"codex command", "task.upsert", "", map[string]any{"id": "codex:background:x", "kind": "background", "status": "failed"}},
		{"cleared goal", "task.upsert", "", map[string]any{"id": "goal", "status": "cancelled", "progress": map[string]any{"type": "goal"}}},
		{"steer receipt", "command.upsert", "", map[string]any{"id": "steer", "action": "send", "state": "completed"}},
		{"resolved approval", "approval.upsert", "", map[string]any{"id": "rpc", "state": "approved"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := store.Event{ID: "raw", Type: tc.eventType, AgentID: "a", SessionID: "s", Data: tc.data}
			notices, err := Events(ctx, st.DB(), []store.Event{event})
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "" {
				if len(notices) != 0 {
					t.Fatal(notices)
				}
				return
			}
			if len(notices) != 1 || notices[0].Data["kind"] != tc.kind {
				t.Fatal(notices)
			}
			if strings.Contains(text(notices[0].Data, "message"), "private question") {
				t.Fatal("question content leaked")
			}
			first, err := st.AppendEvents(ctx, notices)
			if err != nil || len(first) != 1 || first[0].Cursor == 0 {
				t.Fatalf("persist: %v %v", first, err)
			}
			repeated, err := Events(ctx, st.DB(), []store.Event{event})
			if err != nil {
				t.Fatal(err)
			}
			second, err := st.AppendEvents(ctx, repeated)
			if err != nil || len(second) != 0 {
				t.Fatalf("duplicate: %v %v", second, err)
			}
		})
	}
	_, err := st.DB().Exec(`UPDATE sessions SET ephemeral=1 WHERE agent_id='a' AND id='s'`)
	if err != nil {
		t.Fatal(err)
	}
	notices, err := Events(ctx, st.DB(), []store.Event{{Type: "task.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "ephemeral", "status": "completed"}}})
	if err != nil || len(notices) != 0 {
		t.Fatal(notices, err)
	}
}

func browserSubscription(t *testing.T) subscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authKey := make([]byte, 16)
	if _, err = rand.Read(authKey); err != nil {
		t.Fatal(err)
	}
	return subscription{Subscription: webpush.Subscription{Endpoint: "https://push.example.test/send/device", Keys: webpush.Keys{Auth: base64.RawURLEncoding.EncodeToString(authKey), P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}}, Origin: "https://astrorder.example.test", Mobile: true}
}

type httpFunc func(*http.Request) (*http.Response, error)

func (f httpFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestPushPersistenceDeliveryRetryAndRenewal(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	authorizer := auth.New(config.Config{BrowserSecret: "test-secret"}, nil)
	s, err := New(ctx, st, authorizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // Drive delivery deterministically, without a timer.
	next, err := New(ctx, st, authorizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	if s.keys != next.keys {
		t.Fatal("VAPID changed after restart")
	}
	sub := browserSubscription(t)
	key := subscriptionKey(sub.Endpoint)
	if err := st.SetPreference(ctx, key, sub, true); err != nil {
		t.Fatal(err)
	}
	notice := store.Event{ID: "push-1", Type: EventType, AgentID: "a", SessionID: "s", Data: map[string]any{"key": "push-1", "kind": "task_completed", "title": "完成", "message": "通知测试", "created_at": time.Now().UTC().Format(time.RFC3339Nano)}}
	ev, err := st.AppendEvent(ctx, notice)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	status := 503
	s.client = httpFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		payload, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("Authorization") == "" || strings.Contains(string(payload), "通知测试") {
			t.Fatal("push payload not encrypted/authenticated")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.readSubscription(ctx, key, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Cursor != 0 || sub.Failures != 1 || sub.RetryAt.IsZero() {
		t.Fatalf("failed push advanced: %+v", sub)
	}
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("backoff ignored")
	}
	sub.RetryAt = time.Time{}
	if err = st.SetPreference(ctx, key, sub, true); err != nil {
		t.Fatal(err)
	}
	status = 201
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.readSubscription(ctx, key, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Cursor != ev.Cursor || sub.Failures != 0 {
		t.Fatalf("successful push cursor=%d", sub.Cursor)
	}
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("delivered event resent")
	}
	notice.ID = "push-2"
	if _, err = st.AppendEvent(ctx, notice); err != nil {
		t.Fatal(err)
	}
	s.client = httpFunc(func(r *http.Request) (*http.Response, error) {
		renewed := sub
		renewed.Mobile = false
		if err := st.SetPreference(ctx, key, renewed, true); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.readSubscription(ctx, key, &sub); err != nil {
		t.Fatal("renewal lost", err)
	}
	s.client = httpFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 410, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err = s.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.readSubscription(ctx, key, &sub); err != sql.ErrNoRows {
		t.Fatalf("expired subscription retained: %v", err)
	}
	public, err := preferences.Read(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), s.keys.Private) || strings.Contains(string(raw), "web_push") {
		t.Fatal("push credentials exposed")
	}
}

func TestPushAPIAndBoundaryValidation(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	s, err := New(ctx, st, auth.New(config.Config{BrowserSecret: "test-secret"}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sub := browserSubscription(t)
	raw, _ := json.Marshal(sub)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", pushPath, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", pushPath, "", "test-secret"); w.Code != 200 || strings.Contains(w.Body.String(), s.keys.Private) {
		t.Fatal(w.Body.String())
	}
	if w := call("PUT", pushPath, string(raw), "test-secret"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var stored subscription
	if err := s.readSubscription(ctx, subscriptionKey(sub.Endpoint), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Cursor != 0 || !stored.Mobile {
		t.Fatal("subscription not saved")
	}
	for _, endpoint := range []string{"http://push.example.test", "https://127.0.0.1/push", "https://[::1]/push", "https://10.0.0.1/push", "https://100.64.0.1/push", "https://user:pass@push.example.test", "https://push.example.test:8443"} {
		bad := sub.Subscription
		bad.Endpoint = endpoint
		if validateSubscription(bad, sub.Origin, sub.Origin) == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	if validateSubscription(sub.Subscription, sub.Origin, "https://evil.test") == nil {
		t.Fatal("cross origin accepted")
	}
	if publicIP(net.ParseIP("192.168.1.1")) || !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address check")
	}
	if w := call("DELETE", pushPath, string(raw), "test-secret"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", pushPath+"/test", string(raw), "test-secret"); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestResolvedAndExpiredQuestionsAreNotPushed(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	s := &Service{store: st}
	n := Notice{Source: "message.upsert", Kind: "approval_pending", AgentID: "a", SessionID: "s", TargetID: "q", CreatedAt: time.Now()}
	if ok, err := s.stillRelevant(ctx, n); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, _, err := st.CreateCommand(ctx, map[string]any{"id": "answer", "agent_id": "a", "session_id": "s", "action": "send", "text": "answer", "target_id": "q"}, "completed"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.stillRelevant(ctx, n); err != nil || ok {
		t.Fatal("answered question", ok, err)
	}
	n.TargetID = "new"
	n.CreatedAt = time.Now().Add(-6 * time.Minute)
	if ok, err := s.stillRelevant(ctx, n); err != nil || ok {
		t.Fatal("expired question", ok, err)
	}
}

func TestLatestNoticesIgnoreSessionMetadataAndRespectSnapshotCursor(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	first, err := st.AppendEvent(ctx, store.Event{ID: "n1", Type: EventType, AgentID: "a", SessionID: "s", Data: map[string]any{"key": "n1", "agent_id": "a", "session_id": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AppendEvent(ctx, store.Event{ID: "metadata", Type: "session.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "s", "agent_id": "a", "status": "error", "updated_at": "2099-01-01T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := Latest(ctx, st, first.Cursor+1)
	if err != nil || len(rows) != 1 || rows[0]["key"] != "n1" || rows[0]["cursor"] != first.Cursor {
		t.Fatalf("metadata became unread: %v %v", rows, err)
	}
	second, err := st.AppendEvent(ctx, store.Event{ID: "n2", Type: EventType, AgentID: "a", SessionID: "s", Data: map[string]any{"key": "n2", "agent_id": "a", "session_id": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = Latest(ctx, st, first.Cursor)
	if err != nil || len(rows) != 1 || rows[0]["key"] != "n1" {
		t.Fatal("snapshot skipped an unseen event", rows, err)
	}
	rows, err = Latest(ctx, st, second.Cursor)
	if err != nil || len(rows) != 1 || rows[0]["key"] != "n2" {
		t.Fatal(rows, err)
	}
}
