package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
)

func slashFixture(t *testing.T, dispatch func(map[string]any) map[string]any) (*Adapter, func() []string) {
	t.Helper()
	transport := newMockRemoteSession()
	adapter := NewWithSSHStarter(Config{Executable: "/fixture/hermes", Workspace: "/work", Allowed: []string{"/work"}, AgentID: "fixture"}, func(context.Context, ssh.CommandSpec) (SSHSession, error) {
		return transport, nil
	})
	t.Cleanup(func() { _ = adapter.Close() })
	var mu sync.Mutex
	var methods []string
	go func() {
		encoder := json.NewEncoder(transport.stdoutW)
		_ = encoder.Encode(map[string]any{"method": "event", "params": map[string]any{"type": "gateway.ready"}})
		decoder := json.NewDecoder(transport.stdinR)
		for {
			var request map[string]any
			if decoder.Decode(&request) != nil {
				return
			}
			method, _ := request["method"].(string)
			mu.Lock()
			methods = append(methods, method)
			mu.Unlock()
			response := map[string]any{"id": request["id"]}
			if method == "command.dispatch" {
				for key, value := range dispatch(request["params"].(map[string]any)) {
					response[key] = value
				}
			} else {
				response["result"] = map[string]any{"type": "output", "output": "fallback"}
			}
			if encoder.Encode(response) != nil {
				return
			}
		}
	}()
	if err := adapter.startProcess(context.Background()); err != nil {
		t.Fatal(err)
	}
	return adapter, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func TestSlashDispatchDoesNotRetryNativeExecutionFailure(t *testing.T) {
	adapter, methods := slashFixture(t, func(map[string]any) map[string]any {
		return map[string]any{"error": map[string]any{"code": 5009, "message": "compress failed after native work"}}
	})
	_, err := adapter.handleSlash(context.Background(), "fixture-handle", "/compress")
	if err == nil {
		t.Fatal("native compression failure was replaced by a second execution")
	}
	if got := methods(); len(got) != 1 || got[0] != "command.dispatch" {
		t.Fatalf("native execution was retried: %v", got)
	}
}

func TestSlashDispatchDoesNotRetryExpiredWait(t *testing.T) {
	adapter, methods := slashFixture(t, func(map[string]any) map[string]any {
		time.Sleep(80 * time.Millisecond)
		return map[string]any{"result": map[string]any{"type": "output", "output": "late native result"}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := adapter.handleSlash(ctx, "fixture-handle", "/compress"); err == nil {
		t.Fatal("expired wait returned success")
	}
	// The reader is independent of dispatch; let any incorrectly queued fallback
	// reach the wire before asserting its absence.
	time.Sleep(100 * time.Millisecond)
	if got := methods(); len(got) != 1 || got[0] != "command.dispatch" {
		t.Fatalf("uncertain native operation was dispatched twice: %v", got)
	}
}

func TestSlashDispatchFallsBackOnlyForUnrecognizedCommand(t *testing.T) {
	for _, code := range []int{-32601, 4018} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			adapter, methods := slashFixture(t, func(map[string]any) map[string]any {
				return map[string]any{"error": map[string]any{"code": code, "message": "unrecognized command"}}
			})
			result, err := adapter.handleSlash(context.Background(), "fixture-handle", "/fixture")
			if err != nil || result["completed"] != true || result["output"] != "fallback" {
				t.Fatalf("unrecognized command fallback: %v %v", result, err)
			}
			if got := methods(); len(got) != 2 || got[0] != "command.dispatch" || got[1] != "slash.exec" {
				t.Fatalf("fallback calls: %v", got)
			}
		})
	}
}

func TestTypedHermesCompactUsesNativeCompression(t *testing.T) {
	params := make(chan map[string]any, 1)
	adapter, _ := slashFixture(t, func(p map[string]any) map[string]any {
		params <- p
		return map[string]any{"result": map[string]any{"type": "output", "output": "summary committed"}}
	})
	adapter.mu.Lock()
	adapter.sessions["fixture-session"] = &SessionState{SessionID: "fixture-session", Handle: "fixture-handle", Status: "idle"}
	adapter.handleToSession["fixture-handle"] = "fixture-session"
	adapter.mu.Unlock()
	result, err := adapter.Command(context.Background(), core.Request{SessionID: "fixture-session", Action: "session.compact", Fields: map[string]any{"instructions": "preserve decisions"}})
	if err != nil || result.Payload["completed"] != true || result.Payload["output"] != "summary committed" {
		t.Fatalf("typed compact: %v %v", result, err)
	}
	p := <-params
	if p["session_id"] != "fixture-handle" || p["name"] != "compress" || p["arg"] != "preserve decisions" {
		t.Fatalf("native compact request: %v", p)
	}
}
