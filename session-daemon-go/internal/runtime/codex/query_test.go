package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogQueriesUseNativeTransportWithoutCreatingSession(t *testing.T) {
	var starts atomic.Int32
	var creates atomic.Int32
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}}, nil,
		func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			starts.Add(1)
			fake := newFakeSSHSession()
			go func() {
				decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					method := request["method"]
					if method == "initialized" {
						continue
					}
					result := map[string]any{}
					switch method {
					case "initialize":
						result["codexHome"] = "/audit/native-home"
					case "thread/list":
						result["data"] = []any{map[string]any{"id": "external-native-thread"}}
					case "thread/start":
						creates.Add(1)
					}
					if encoder.Encode(map[string]any{"id": request["id"], "result": result}) != nil {
						return
					}
				}
			}()
			return fake, nil
		})
	defer adapter.Close()
	query, ok := any(adapter).(core.Querier)
	if !ok {
		t.Fatal("Codex adapter does not implement runtime.request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := query.Query(ctx, core.Request{Fields: map[string]any{"method": "initialize"}})
	if err != nil || result["codexHome"] != "/audit/native-home" {
		t.Fatalf("initialize: %v %v", result, err)
	}
	result, err = query.Query(ctx, core.Request{Fields: map[string]any{"method": "thread/list", "request_params": map[string]any{"limit": 100}}})
	if err != nil || len(result["data"].([]any)) != 1 {
		t.Fatalf("catalog: %v %v", result, err)
	}
	for _, method := range []string{"thread/start", "turn/start", "thread/delete"} {
		if _, err = query.Query(ctx, core.Request{Fields: map[string]any{"method": method}}); err == nil {
			t.Fatalf("query accepted mutation %s", method)
		}
	}
	if starts.Load() != 1 || creates.Load() != 0 || len(adapter.Snapshot()) != 0 {
		t.Fatal("catalog query created a conversation or extra transport")
	}
	adapter.mu.Lock()
	catalog := adapter.catalog
	adapter.mu.Unlock()
	catalog.Close()
	result, err = query.Query(ctx, core.Request{Fields: map[string]any{"method": "initialize"}})
	if err != nil || result["codexHome"] != "/audit/native-home" || starts.Load() != 2 {
		t.Fatalf("closed catalog did not recover: %v %v", result, err)
	}
	adapter.Close()
	if _, err = query.Query(ctx, core.Request{Fields: map[string]any{"method": "initialize"}}); err == nil {
		t.Fatal("closed adapter accepted query")
	}
}
