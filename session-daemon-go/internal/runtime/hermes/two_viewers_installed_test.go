package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestInstalledTwoCanonicalViewersRecoverReadOnly(t *testing.T) {
	launcher := os.Getenv("ASTRORDER_INSTALLED_CANONICAL_READ_ONLY")
	if launcher == "" {
		t.Skip("explicit installed read-only validation required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, launcher, "--print-runtime-command", "--module", "tui_gateway.entry").Output()
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if json.Unmarshal(data, &argv) != nil || len(argv) < 2 {
		t.Fatal("invalid launcher command")
	}
	p := NewIsolated(Config{Executable: argv[0], Arguments: argv[1:], AgentID: "local-hermes-default", Workspace: "P:/workspace/glwlg/ai/astrorder"})
	defer p.Close()
	ids := []string{"local-22d9b1551bba115ab44679ca49d8c8e71fca0cdaebfe99c14b49d8cd2a2cabb8", "20261008_202804_f6e273"}
	for _, id := range ids {
		if _, err = p.Spawn(ctx, core.Request{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	first := p.workers[ids[0]]
	if err = first.canonicalSocket.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.processDone:
	case <-ctx.Done():
		t.Fatal("viewer exit unconfirmed")
	}
	select {
	case <-first.eventDone:
	case <-ctx.Done():
		t.Fatal("viewer projection exit unconfirmed")
	}
	if _, err = p.Spawn(ctx, core.Request{SessionID: ids[0]}); err != nil {
		t.Fatal("viewer recovery", err)
	}
	for range 2 {
		for _, id := range ids {
			r, e := p.Command(ctx, core.Request{SessionID: id, Action: "session.model.read"})
			if e != nil || r.Payload["model"] == "" {
				t.Fatalf("model read %s: %v %v", id, r, e)
			}
		}
	}
	t.Log("two exact session viewers and isolated viewer recovery passed; no prompts sent")
}
