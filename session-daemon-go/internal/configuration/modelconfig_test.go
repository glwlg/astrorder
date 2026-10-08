package configuration

import (
	"astrorder.dev/session-daemon/internal/protocol"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfiguredModelHomeRoutesOverAuthenticatedWebSocket(t *testing.T) {
	home := t.TempDir()
	raw, err := json.Marshal(map[string]any{"model_config": map[string]any{"home": home}, "runtimes": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	d := protocol.New("isolated-key")
	defer d.Close()
	if err := cfg.Register(d); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	call := func(request map[string]any) map[string]any {
		data, _ := json.Marshal(request)
		if err := socket.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		_, data, err = socket.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := call(map[string]any{"action": "daemon.handshake", "secret": "isolated-key"}); result["action"] != "daemon.handshake.result" {
		t.Fatal(result)
	}
	result := call(map[string]any{"action": "model_config.plan", "request_id": "plan", "target": map[string]any{"kind": "local"}})
	if result["action"] != "model_config.plan.result" || result["result"].(map[string]any)["_home"] != home {
		t.Fatal("configured home not routed", result)
	}
}
