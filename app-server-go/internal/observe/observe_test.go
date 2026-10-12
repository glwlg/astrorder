package observe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var spoolID = regexp.MustCompile(`^[a-f0-9]{32}(?:[a-f0-9]{32})?$`)

func hook(event string, extra map[string]any) []byte {
	payload := map[string]any{"hook_event_name": event, "session_id": "thread-1", "turn_id": "turn-1"}
	for key, value := range extra {
		payload[key] = value
	}
	raw, _ := json.Marshal(payload)
	return raw
}

func TestParseProducesIDsTheDaemonAccepts(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	for _, event := range Events {
		row, err := Parse(hook(event, map[string]any{"tool_call_id": "call-1"}), now)
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}
		if !spoolID.MatchString(row.ID) {
			t.Fatalf("%s: id %q does not match the daemon pattern", event, row.ID)
		}
	}
}

func TestStableIdentityDeduplicatesRetries(t *testing.T) {
	a, _ := Parse(hook("Stop", nil), time.Unix(1, 0))
	b, _ := Parse(hook("Stop", nil), time.Unix(2, 0))
	if a.ID != b.ID {
		t.Fatalf("retried Stop produced different ids %s %s", a.ID, b.ID)
	}
	c, _ := Parse(hook("PreToolUse", nil), time.Unix(1, 0))
	d, _ := Parse(hook("PreToolUse", nil), time.Unix(1, 0))
	if c.ID == d.ID {
		t.Fatal("tool events without a call id must not collapse into one")
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for name, raw := range map[string][]byte{
		"not json":      []byte("{"),
		"unknown event": hook("Nope", nil),
		"no session":    []byte(`{"hook_event_name":"Stop"}`),
		"control chars": hook("Stop", map[string]any{"session_id": "a\nb"}),
	} {
		if _, err := Parse(raw, time.Now()); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestRunWritesSpoolFileWithoutMessageText(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "events")
	if err := Run(spool, strings.NewReader(string(hook("UserPromptSubmit", map[string]any{"prompt": "secret text"}))), time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(spool)
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("spool = %v", entries)
	}
	raw, _ := os.ReadFile(filepath.Join(spool, entries[0].Name()))
	if strings.Contains(string(raw), "secret text") {
		t.Fatal("observation leaked the prompt")
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row["event"] != "UserPromptSubmit" || row["session_id"] != "thread-1" || row["observed_at"] == nil {
		t.Fatalf("row = %v", row)
	}
}

func TestExpiredEventsArePruned(t *testing.T) {
	spool := t.TempDir()
	old := filepath.Join(spool, strings.Repeat("a", 32)+".json")
	os.WriteFile(old, []byte("{}"), 0o600)
	stale := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, stale, stale)
	row, _ := Parse(hook("Stop", nil), time.Now())
	if ok, err := Write(spool, row, time.Now()); err != nil || !ok {
		t.Fatalf("write = %v %v", ok, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("expired event survived")
	}
}

func TestAfterAdvancesItsCursor(t *testing.T) {
	spool := t.TempDir()
	_, cur := After(spool, Cursor{})
	row, _ := Parse(hook("Stop", map[string]any{"turn_id": "t1"}), time.Now())
	Write(spool, row, time.Now())
	paths, cur := After(spool, cur)
	if len(paths) != 1 {
		t.Fatalf("paths = %v", paths)
	}
	if again, _ := After(spool, cur); len(again) != 0 {
		t.Fatalf("cursor did not advance: %v", again)
	}
}

func TestFullSpoolEvictsOldestInsteadOfDroppingNewEvents(t *testing.T) {
	spool := t.TempDir()
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxSpoolFiles; i++ {
		name := filepath.Join(spool, fmt.Sprintf("%032x.json", i))
		os.WriteFile(name, []byte("{}"), 0o600)
		stamp := base.Add(time.Duration(i) * time.Second)
		os.Chtimes(name, stamp, stamp)
	}
	row, _ := Parse(hook("Stop", map[string]any{"turn_id": "fresh"}), time.Now())
	ok, err := Write(spool, row, time.Now())
	if err != nil || !ok {
		t.Fatalf("write on full spool = %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(spool, row.ID+".json")); err != nil {
		t.Fatal("new event was not stored")
	}
	if _, err := os.Stat(filepath.Join(spool, fmt.Sprintf("%032x.json", 0))); !os.IsNotExist(err) {
		t.Fatal("oldest event was not evicted")
	}
	entries, _ := os.ReadDir(spool)
	if len(entries) != maxSpoolFiles {
		t.Fatalf("spool holds %d files, want %d", len(entries), maxSpoolFiles)
	}
}
