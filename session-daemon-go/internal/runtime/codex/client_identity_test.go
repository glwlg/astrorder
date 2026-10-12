package codex

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"astrorder.dev/session-daemon/internal/transport/ssh"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The initialize handshake decides the originator Codex records on every thread
// and every model request, so it must identify as Codex, not as Astrorder.
func TestInitializeIdentifiesAsCodex(t *testing.T) {
	names := make(chan string, 1)
	adapter := NewSSHAdapter(Config{Executable: "/usr/bin/codex", Workspace: "/audit", Allowed: []string{"/audit"}}, nil,
		func(ctx context.Context, spec ssh.CommandSpec) (SSHSession, error) {
			fake := newFakeSSHSession()
			go func() {
				decoder, encoder := json.NewDecoder(fake.stdinR), json.NewEncoder(fake.stdoutW)
				for {
					var request map[string]any
					if decoder.Decode(&request) != nil {
						return
					}
					if request["method"] == "initialized" {
						continue
					}
					if request["method"] == "initialize" {
						params, _ := request["params"].(map[string]any)
						info, _ := params["clientInfo"].(map[string]any)
						name, _ := info["name"].(string)
						select {
						case names <- name:
						default:
						}
					}
					if encoder.Encode(map[string]any{"id": request["id"], "result": map[string]any{}}) != nil {
						return
					}
				}
			}()
			return fake, nil
		})
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := any(adapter).(core.Querier).Query(ctx, core.Request{Fields: map[string]any{"method": "initialize"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-names:
		if name != "codex" {
			t.Fatalf("clientInfo.name = %q, want codex", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initialize was not observed")
	}
}
