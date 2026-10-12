package grpcapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// The native runtime is a synthetic fixture. Both real WS and authenticated
// TCP/gRPC transports must reach this same registered adapter, without Create.
type nativeQueryFixture struct {
	mu       sync.Mutex
	requests []core.Request
	creates  int
}

func (q *nativeQueryFixture) Create(context.Context, core.Request) (core.Result, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.creates++
	return core.Result{SessionID: "unexpected"}, nil
}

func (q *nativeQueryFixture) Query(_ context.Context, request core.Request) (map[string]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.requests = append(q.requests, request)
	return map[string]any{"thread": map[string]any{"id": "native-thread", "seq": json.Number("9007199254740993")}, "extensions": []any{false, nil, ""}}, nil
}

func TestQueryNativeSharesLegacyRuntimeOverAuthenticatedTCP(t *testing.T) {
	f := startFixture(t)
	q := &nativeQueryFixture{}
	f.daemon.RegisterRuntime("codex", q)
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	typed := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX},
		Query:  &sessiondv1.NativeQueryRequest_CodexThreadRead{CodexThreadRead: &sessiondv1.CodexThreadReadQuery{ThreadId: "native-thread", IncludeTurns: ptr(false)}},
	}
	if _, err := client.QueryNative(context.Background(), typed); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated query: %v", err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	response, err := client.QueryNative(ctx, typed)
	if err != nil {
		t.Fatalf("typed native read: %v", err)
	}
	if !proto.Equal(response.Target, typed.Target) {
		t.Fatalf("native response lost validated target: %v", response.Target)
	}
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(response.Result.GetUtf8Json())))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result["thread"].(map[string]any)["seq"] != json.Number("9007199254740993") || len(result["extensions"].([]any)) != 3 {
		t.Fatalf("lossy native response: %s", response.Result.GetUtf8Json())
	}

	wsServer := httptest.NewServer(f.daemon.Handler())
	defer wsServer.Close()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	wsCall := func(request map[string]any) map[string]any {
		t.Helper()
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		_, raw, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]any
		if err := json.Unmarshal(raw, &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if r := wsCall(map[string]any{"action": "daemon.handshake", "secret": "legacy-ws-secret"}); r["action"] != "daemon.handshake.result" {
		t.Fatalf("handshake: %v", r)
	}
	r := wsCall(map[string]any{"action": "runtime.request", "agent_type": "codex", "method": "thread/read", "request_params": map[string]any{"threadId": "native-thread", "includeTurns": false}})
	if r["action"] != "runtime.request.result" {
		t.Fatalf("legacy native read: %v", r)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.creates != 0 || len(q.requests) != 2 {
		t.Fatalf("native calls=%d creates=%d", len(q.requests), q.creates)
	}
	for _, request := range q.requests {
		if request.AgentType != "codex" || request.Action != "runtime.request" || request.SessionID != "" || request.Fields["method"] != "thread/read" || !reflect.DeepEqual(request.Fields["request_params"], map[string]any{"threadId": "native-thread", "includeTurns": false}) {
			t.Fatalf("different native request: %#v", request)
		}
	}
	authority, err := f.daemon.AuthorityStatus()
	if err != nil || len(authority.Sessions) != 0 || authority.PendingOperations != 0 {
		t.Fatalf("query authority mutation: %+v %v", authority, err)
	}
	caps, err := sessiondv1.NewDaemonControlClient(f.conn).GetCapabilities(ctx, &sessiondv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, name := range caps.Services {
		found = found || name == "astrorder.sessiond.v1.RuntimeQuery"
	}
	if !found {
		t.Fatalf("registered query service absent from capabilities: %v", caps.Services)
	}
	if _, err := client.SetReasoning(ctx, &sessiondv1.SetReasoningRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty reasoning mutation was not rejected: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
