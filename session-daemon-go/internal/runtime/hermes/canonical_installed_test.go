package hermes

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Explicit opt-in: transport, catalog and native read only on the installed
// profile; no prompts, configuration writes or authority shutdown.
func TestInstalledCanonicalOwnerReadOnly(t *testing.T) {
	launcher := os.Getenv("ASTRORDER_INSTALLED_CANONICAL_READ_ONLY")
	if launcher == "" {
		t.Skip("explicit installed read-only validation required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, launcher, "--print-runtime-command", "--module", "tui_gateway.entry").Output()
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if json.Unmarshal(data, &argv) != nil {
		t.Fatal("invalid runtime argv")
	}
	a := New(Config{Executable: argv[0], Arguments: argv[1:]})
	defer a.Close()
	if err = a.startProcess(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.rpc(ctx, "ping", nil); err != nil {
		t.Fatal(err)
	}
	t.Log("canonical owner handshake PASS")
	if _, err = a.rpc(ctx, "model.options", map[string]any{"explicit_only": true, "include_unconfigured": false}); err != nil {
		t.Fatal(err)
	}
	t.Log("native model catalog PASS")
	listing, err := a.rpc(ctx, "session.list", map[string]any{"scope": "stored", "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := listing["sessions"].([]any)
	if len(rows) == 0 {
		t.Fatal("no stored session to verify")
	}
	row, _ := rows[0].(map[string]any)
	resumed, err := a.rpc(ctx, "session.resume", map[string]any{"session_id": row["session_id"], "lazy": true, "omit_messages": true})
	if err != nil {
		t.Fatal(err)
	}
	if resumed["id"] == nil {
		t.Fatal("missing native identity")
	}
	t.Log("stored session resume PASS")
}
