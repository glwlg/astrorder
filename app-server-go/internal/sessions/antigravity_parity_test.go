package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type parityMockRuntimeServer struct {
	*mockDaemonServer
	queryNativeCb func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error)
}

func (p *parityMockRuntimeServer) QueryNative(_ context.Context, r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	return p.queryNativeCb(r)
}

func TestUnboundAntigravityModelBackfillsFromNativeSessionUsage(t *testing.T) {
	rt := &parityMockRuntimeServer{
		mockDaemonServer: &mockDaemonServer{},
		queryNativeCb: func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
			if r.GetAntigravitySessionUsage() == nil {
				t.Fatalf("unexpected native query: %T", r.Query)
			}
			raw, _ := json.Marshal(map[string]any{
				"context_available": true,
				"model":             "gemini-4-carbon",
				"default_model":     "Gemini 4 Carbon (High)",
			})
			return &pb.NativeQueryResponse{
				Target: r.Target,
				Result: &pb.NativeJson{Schema: "antigravity.session.usage", Utf8Json: raw},
			}, nil
		},
	}

	h, st, _, cleanup := setupTestServerWithCustomRuntime(t, rt.mockDaemonServer, rt)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":     "local-antigravity",
		"kind":   "antigravity",
		"name":   "Antigravity",
		"status": "ready",
	}); err != nil {
		t.Fatal(err)
	}
	sid := "66666666-6666-4666-8666-666666666666"
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":            sid,
		"agent_id":      "local-antigravity",
		"status":        "idle",
		"control_state": "unbound",
		"updated_at":    "2026-11-18T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/model?agent_id=local-antigravity", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("model query failed: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["provider"] != "antigravity" || body["model"] != "gemini-4-carbon" {
		t.Fatalf("expected backfilled model gemini-4-carbon, got: %s", rec.Body.String())
	}
}

func TestProjectHistoryItemAntigravityToolFallback(t *testing.T) {
	item := map[string]any{
		"id":   "antigravity:s:1",
		"role": "tool",
		"kind": "tool",
		"text": "",
		"tool": map[string]any{
			"id":     "antigravity:s:1",
			"name":   "read_file",
			"status": "completed",
			"input":  `{"Path":"/src/app.go"}`,
			"output": `{"content":"package main","total_lines":1}`,
		},
	}
	rows := projectHistoryItem(item, "local", "s", "s", "native", "antigravity")
	if len(rows) != 1 {
		t.Fatalf("expected 1 projected row, got %d", len(rows))
	}
	if rows[0]["text"] != `{"content":"package main","total_lines":1}` {
		t.Fatalf("expected text backfilled from tool output, got: %q", rows[0]["text"])
	}
	tool := rows[0]["tool"].(map[string]any)
	args, ok := tool["arguments"].(map[string]any)
	if !ok || args["Path"] != "/src/app.go" {
		t.Fatalf("expected parsed tool.arguments map, got: %+v", tool["arguments"])
	}
}
