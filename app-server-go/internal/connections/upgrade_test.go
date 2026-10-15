package connections_test

import (
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/store"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type upgradeExecutor struct {
	calls int
	code  int
	err   error
}

func (u *upgradeExecutor) Upgrade(ctx context.Context, kind, connID string, conn map[string]any, init func(string), output io.Writer) (int, error) {
	u.calls++
	init(kind + " update")
	_, _ = io.WriteString(output, "upgrading "+connID+"\n")
	return u.code, u.err
}

func TestAgentUpgradeExecutesAndStreamsCompletion(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		err  error
	}{{"success", 0, nil}, {"exit failure", 7, nil}, {"start failure", -1, errors.New("executable missing")}} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			_, err = st.UpsertAgent(context.Background(), map[string]any{"id": "local-codex", "name": "Codex", "kind": "codex", "status": "ready", "connection_id": "local"})
			if err != nil {
				t.Fatal(err)
			}
			u := &upgradeExecutor{code: tc.code, err: tc.err}
			h, err := connections.NewHandler(connections.Options{Store: st, VersionExecutor: &probeRecorder{failures: map[string]error{"codex|local": errors.New("version lookup must not gate execution")}}, UpgradeExecutor: u})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agents/local-codex/upgrade", nil))
			if u.calls != 1 || w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/x-ndjson") {
				t.Fatalf("upgrade was not executed as a stream: calls=%d status=%d body=%s", u.calls, w.Code, w.Body.String())
			}
			dec := json.NewDecoder(w.Body)
			var frames []map[string]any
			for {
				var f map[string]any
				if err := dec.Decode(&f); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				frames = append(frames, f)
			}
			last := frames[len(frames)-1]
			if last["type"] != "done" || last["exit_code"] != float64(tc.code) || last["ok"] != (tc.code == 0 && tc.err == nil) {
				t.Fatalf("incorrect completion: %v", frames)
			}
			if tc.err != nil && frames[len(frames)-2]["error"] != tc.err.Error() {
				t.Fatalf("execution error hidden: %v", frames)
			}
		})
	}
}

func TestUpgradeTargetsExcludeRemovedEnvironmentRegistrations(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.SaveSSHConnection(ctx, map[string]any{"id": "wsl-current", "display_name": "WSL", "host": "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []map[string]any{
		{"id": "hermes-current", "name": "WSL · Hermes", "kind": "hermes", "status": "ready", "connection_id": "wsl-current"},
		{"id": "hermes-old", "name": "WSL", "kind": "hermes", "status": "disconnected", "connection_id": "ssh-removed"},
	} {
		if _, err = st.UpsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	// Historical sessions must remain accessible, but cannot make a removed machine upgradeable.
	if _, err = st.UpsertSession(ctx, map[string]any{"id": "history", "agent_id": "hermes-old", "title": "retained", "status": "idle", "updated_at": "2026-10-15T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	probe := &probeRecorder{}
	h, err := connections.NewHandler(connections.Options{Store: st, VersionExecutor: probe})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents/upgrade/targets", nil))
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0]["id"] != "hermes-current" {
		t.Fatalf("removed WSL must not be an upgrade target: %s", w.Body.String())
	}
	if calls := probe.callList(); len(calls) != 1 || calls[0] != "hermes|wsl-current" {
		t.Fatalf("orphan must not be probed: %v", calls)
	}
	if old, _ := st.GetAgent(ctx, "hermes-old"); old == nil {
		t.Fatal("history registration deleted")
	}
}
