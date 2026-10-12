package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// nativeCodexServer plays the Codex catalog: thread/list pages and thread/read.
type nativeCodexServer struct {
	fakeCombinedDaemonServer
	mu      sync.Mutex
	threads []map[string]any
	// kindOnly threads are returned only when the query names sourceKinds, the
	// way Codex returns `codex exec` threads.
	kindOnly []map[string]any
	lists    []string // option JSON seen on each thread/list
	reads    int
	// items are the thread/items/list rows, newest first, served two per page.
	items []map[string]any
}

// ReadModel fails the way the daemon does when Codex refuses thread/resume
// because another client is the active writer.
func (s *nativeCodexServer) ReadModel(ctx context.Context, req *sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error) {
	return nil, status.Error(codes.FailedPrecondition, "thread already has an active writer")
}

func (s *nativeCodexServer) QueryNative(ctx context.Context, req *sessiondv1.NativeQueryRequest) (*sessiondv1.NativeQueryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body any
	switch query := req.Query.(type) {
	case *sessiondv1.NativeQueryRequest_CodexThreadList:
		options := ""
		if o := query.CodexThreadList.Options; o != nil {
			options = string(o.Utf8Json)
		}
		s.lists = append(s.lists, options)
		items := make([]any, 0, len(s.threads)+len(s.kindOnly))
		for _, item := range s.threads {
			items = append(items, item)
		}
		if strings.Contains(options, "sourceKinds") {
			for _, item := range s.kindOnly {
				items = append(items, item)
			}
		}
		body = map[string]any{"data": items, "nextCursor": nil}
	case *sessiondv1.NativeQueryRequest_CodexThreadRead:
		s.reads++
		body = map[string]any{}
		for _, item := range s.threads {
			if item["id"] == query.CodexThreadRead.ThreadId {
				body = map[string]any{"thread": item}
			}
		}
	case *sessiondv1.NativeQueryRequest_CodexThreadItemsList:
		start := 0
		if c := query.CodexThreadItemsList.Cursor; c != nil {
			fmt.Sscanf(c.GetValue(), "%d", &start)
		}
		end := start + 2
		if end > len(s.items) {
			end = len(s.items)
		}
		page := map[string]any{"data": s.items[start:end], "nextCursor": nil}
		if end < len(s.items) {
			page["nextCursor"] = fmt.Sprint(end)
		}
		body = page
	default:
		body = map[string]any{}
	}
	raw, _ := json.Marshal(body)
	return &sessiondv1.NativeQueryResponse{Result: &sessiondv1.NativeJson{Utf8Json: raw}}, nil
}

func newNativeFixture(t *testing.T, threads ...map[string]any) (*Handler, *store.Store, *nativeCodexServer, *mockPublisher) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "codex-1", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	srv := &nativeCodexServer{threads: threads}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	sessiondv1.RegisterDaemonControlServer(grpcServer, srv)
	sessiondv1.RegisterRuntimeQueryServer(grpcServer, srv)
	go grpcServer.Serve(lis)
	t.Cleanup(grpcServer.Stop)
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: lis.Addr().String(), Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	publisher := &mockPublisher{}
	return &Handler{store: st, client: client, publisher: publisher}, st, srv, publisher
}

func fakeThread(id, name, preview, source string, updated int64) map[string]any {
	item := map[string]any{"id": id, "preview": preview, "cwd": `P:\workspace\proj`, "source": source, "updatedAt": updated, "ephemeral": false}
	if name != "" {
		item["name"] = name
	}
	return item
}

func TestRefreshListsCodexThreadsStartedOutsideAstrorder(t *testing.T) {
	now := time.Now().Unix()
	subagent := fakeThread("t-sub", "", "helper", "vscode", now)
	subagent["source"] = map[string]any{"subagent": map[string]any{"other": "guardian"}}
	ephemeral := fakeThread("t-eph", "", "x", "vscode", now)
	ephemeral["ephemeral"] = true
	h, st, srv, pub := newNativeFixture(t,
		fakeThread("t-desktop", "Fix the build", "ignored preview", "vscode", now),
		fakeThread("t-preview-only", "", "first line of the prompt\nsecond line", "cli", now-10),
		subagent,
		fakeThread("t-guardian", "", "You are the one whose request action you are assessing", "vscode", now),
		ephemeral,
	)
	ctx := context.Background()
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.ListSessions(ctx, "codex-1")
	got := map[string]string{}
	for _, row := range rows {
		got[stringValue(row, "id")] = stringValue(row, "title")
	}
	if len(got) != 2 || got["t-desktop"] != "Fix the build" || got["t-preview-only"] != "first line of the prompt" {
		t.Fatalf("registered %v, want only the two top-level conversations", got)
	}
	row, _ := st.GetSession(ctx, "codex-1", "t-desktop")
	if row["status"] != "idle" || row["native_kind"] != "vscode" || row["control_state"] != "owned" || row["history_state"] != "available" || row["project_name"] != "proj" {
		t.Fatalf("row shape: %v", row)
	}
	if len(pub.events) != 2 {
		t.Fatalf("published %d events, want one session.upsert per new session", len(pub.events))
	}
	if len(srv.lists) == 0 || srv.lists[0] != `{"modelProviders":[]}` {
		t.Fatalf("thread/list must ask for every model provider, got %v", srv.lists)
	}
	before := len(pub.events)
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	if len(pub.events) != before {
		t.Fatalf("second refresh published %d extra events", len(pub.events)-before)
	}
}

func TestRefreshKeepsExistingSessionsAndUpgradesOnlyPlaceholderTitles(t *testing.T) {
	now := time.Now().Unix()
	h, st, _, _ := newNativeFixture(t,
		fakeThread("made-here", "Renamed in Codex", "p", "vscode", now),
		fakeThread("named", "Codex name", "p", "vscode", now))
	ctx := context.Background()
	st.UpsertSession(ctx, map[string]any{"id": "made-here", "agent_id": "codex-1", "title": "New Session", "status": "running", "native_kind": "rpc", "updated_at": "2026-10-11T00:00:00Z"})
	st.UpsertSession(ctx, map[string]any{"id": "named", "agent_id": "codex-1", "title": "My own title", "status": "idle", "native_kind": "rpc", "updated_at": "2026-10-11T00:00:00Z"})
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetSession(ctx, "codex-1", "made-here")
	b, _ := st.GetSession(ctx, "codex-1", "named")
	if a["title"] != "Renamed in Codex" || a["status"] != "running" || a["native_kind"] != "rpc" {
		t.Fatalf("placeholder session: %v", a)
	}
	if b["title"] != "My own title" {
		t.Fatalf("a title the user kept was overwritten: %v", b)
	}
}

func TestRegisterNativeSessionFromHookReadsOneThreadOnce(t *testing.T) {
	now := time.Now().Unix()
	h, st, srv, _ := newNativeFixture(t, fakeThread("hooked", "From a hook", "p", "vscode", now))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		h.RegisterNativeSession("codex-1", "hooked") // five hooks for the same new session
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if row, _ := st.GetSession(ctx, "codex-1", "hooked"); row != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, _ := st.GetSession(ctx, "codex-1", "hooked")
	if row == nil || row["title"] != "From a hook" {
		t.Fatalf("hooked session was not listed: %v", row)
	}
	time.Sleep(100 * time.Millisecond)
	srv.mu.Lock()
	reads := srv.reads
	srv.mu.Unlock()
	if reads != 1 {
		t.Fatalf("thread/read ran %d times for one session, want 1", reads)
	}
	h.RegisterNativeSession("codex-1", "missing")
	time.Sleep(150 * time.Millisecond)
	if row, _ := st.GetSession(ctx, "codex-1", "missing"); row != nil {
		t.Fatalf("unreadable thread was listed: %v", row)
	}
}

func TestRemovedSessionIsNotListedAgain(t *testing.T) {
	now := time.Now().Unix()
	h, st, _, _ := newNativeFixture(t, fakeThread("gone", "Removed by the user", "p", "vscode", now))
	ctx := context.Background()
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	if row, _ := st.GetSession(ctx, "codex-1", "gone"); row == nil {
		t.Fatal("session was not listed first")
	}
	if deleted, err := st.DeleteSession(ctx, "codex-1", "gone"); err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	if row, _ := st.GetSession(ctx, "codex-1", "gone"); row != nil {
		t.Fatalf("a session the user removed came back: %v", row)
	}
	h.RegisterNativeSession("codex-1", "gone")
	time.Sleep(150 * time.Millisecond)
	if row, _ := st.GetSession(ctx, "codex-1", "gone"); row != nil {
		t.Fatalf("a hook brought a removed session back: %v", row)
	}
}

func TestHookDoesNotListAThreadThatHasNoContentYet(t *testing.T) {
	now := time.Now().Unix()
	empty := fakeThread("opening", "", "", "exec", now)
	h, st, srv, _ := newNativeFixture(t, empty)
	ctx := context.Background()
	h.RegisterNativeSession("codex-1", "opening") // SessionStart: nothing to name it by
	time.Sleep(300 * time.Millisecond)
	if row, _ := st.GetSession(ctx, "codex-1", "opening"); row != nil {
		t.Fatalf("an empty thread was listed under its id: %v", row)
	}
	srv.mu.Lock()
	srv.threads[0]["preview"] = "the first prompt"
	srv.mu.Unlock()
	h.nativeRetry.Delete("codex-1\x00opening")    // the retry window has passed
	h.RegisterNativeSession("codex-1", "opening") // UserPromptSubmit
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if row, _ := st.GetSession(ctx, "codex-1", "opening"); row != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if row, _ := st.GetSession(ctx, "codex-1", "opening"); row == nil || row["title"] != "the first prompt" {
		t.Fatalf("session not listed with its prompt as title: %v", row)
	}
}

func TestRefreshRenamesASessionListedUnderItsId(t *testing.T) {
	now := time.Now().Unix()
	h, st, _, _ := newNativeFixture(t, fakeThread("by-id", "", "now it has a prompt", "exec", now))
	ctx := context.Background()
	st.UpsertSession(ctx, map[string]any{"id": "by-id", "agent_id": "codex-1", "title": "by-id", "status": "idle", "native_kind": "exec", "updated_at": "2026-10-11T00:00:00Z"})
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	if row, _ := st.GetSession(ctx, "codex-1", "by-id"); row["title"] != "now it has a prompt" {
		t.Fatalf("title not repaired: %v", row)
	}
}

func TestRefreshMergesBothThreadListQueries(t *testing.T) {
	now := time.Now().Unix()
	h, st, srv, _ := newNativeFixture(t, fakeThread("plain", "Plain", "p", "vscode", now))
	srv.kindOnly = []map[string]any{fakeThread("from-exec", "", "ran codex exec", "exec", now)}
	ctx := context.Background()
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	for id, title := range map[string]string{"plain": "Plain", "from-exec": "ran codex exec"} {
		if row, _ := st.GetSession(ctx, "codex-1", id); row == nil || row["title"] != title {
			t.Fatalf("%s not listed with title %q: %v", id, title, row)
		}
	}
	if rows, _ := st.ListSessions(ctx, "codex-1"); len(rows) != 2 {
		t.Fatalf("a thread returned by both queries was duplicated: %d rows", len(rows))
	}
}

func codexItem(kind, id, text string) map[string]any {
	item := map[string]any{"type": kind, "id": id}
	if kind == "userMessage" {
		item["content"] = []any{map[string]any{"type": "text", "text": text}}
	} else {
		item["text"] = text
	}
	return map[string]any{"turnId": "turn-1", "item": item}
}

// A thread another Codex client is writing cannot be resumed by the daemon, so
// binding fails. Its transcript must still be readable, in order and with paging.
func TestHistoryIsReadableWhenTheThreadCannotBeBound(t *testing.T) {
	h, st, srv, _ := newNativeFixture(t)
	srv.items = []map[string]any{
		codexItem("agentMessage", "a2", "second answer"), codexItem("userMessage", "u2", "second question"),
		codexItem("agentMessage", "a1", "first answer"), codexItem("userMessage", "u1", "first question"),
	}
	ctx := context.Background()
	if _, err := st.UpsertSession(ctx, map[string]any{"agent_id": "codex-1", "id": "t-busy", "title": "busy", "status": "idle", "history_state": "available", "control_state": "native", "updated_at": "2026-10-11T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	read := func(before string) ([]string, string) {
		path := "/api/v1/sessions/t-busy/messages?agent_id=codex-1&limit=2"
		if before != "" {
			path += "&before=" + before
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer browser-sec")
		w := httptest.NewRecorder()
		h.handleListMessages(w, r, "t-busy")
		if w.Code != 200 {
			t.Fatalf("history not readable: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Items []struct {
				Text string `json:"text"`
			} `json:"items"`
			Next *string `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		texts := []string{}
		for _, item := range body.Items {
			texts = append(texts, item.Text)
		}
		next := ""
		if body.Next != nil {
			next = *body.Next
		}
		return texts, next
	}
	newest, cursor := read("")
	if strings.Join(newest, "|") != "second question|second answer" || cursor == "" {
		t.Fatalf("newest page wrong: %v cursor=%q", newest, cursor)
	}
	older, cursor := read(cursor)
	if strings.Join(older, "|") != "first question|first answer" || cursor != "" {
		t.Fatalf("older page wrong: %v cursor=%q", older, cursor)
	}
	if rows, _, _ := st.ListMessages(ctx, "codex-1", "t-busy", "", 10); len(rows) != 0 {
		t.Fatalf("read-only history was copied into the local store: %d rows", len(rows))
	}
}
