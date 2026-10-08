package remote

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"os"
	"testing"
	"time"
)

func TestRealHermesControlFromSavedSSHSettings(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_REAL_SSH") != "1" {
		t.Skip("opt-in native SSH handshake")
	}
	for _, host := range []string{"192.168.1.100", "127.0.0.1"} {
		t.Run(host, func(t *testing.T) {
			name := map[string]string{"192.168.1.100": "Debian", "127.0.0.1": "WSL"}[host]
			a, err := NewRemoteHermesFactory(nil)("connection-probe", map[string]any{"host": host, "user": "luwei", "port": 22, "profile_name": "default", "display_name": name})
			if err != nil {
				t.Fatal(err)
			}
			defer a.(interface{ Close() error }).Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			r, err := a.(core.Spawner).Spawn(ctx, core.Request{SessionID: "connection-control-probe", Fields: map[string]any{"params": map[string]any{"runtime_control": true}}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Payload["connection_id"] != "connection-probe" {
				t.Fatal("connection identity missing")
			}
			if r.Payload["name"] != name+" · Hermes" {
				t.Fatalf("wrong remote display name: %v", r.Payload["name"])
			}
			if _, err := a.(core.Querier).Query(ctx, core.Request{Fields: map[string]any{"method": "session.list", "request_params": map[string]any{"limit": 1}}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
