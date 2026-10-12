package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/store"
)

func TestSSHSaveValidatesTargetBeforeWriting(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h, err := connections.NewHandler(connections.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{}`, `{"host":"-oProxyCommand=unsafe"}`, `{"host":"bad..host"}`,
		`{"host":"host.invalid","profile_name":"../other"}`,
		`{"ssh_config_alias":"-proxy"}`, `{"host":"host.invalid","user":"bad user"}`,
		`{"host":"host.invalid","identity_file":"relative.key"}`,
		`{"host":"host.invalid","workspace":"relative/path"}`,
		`{"host":"host.invalid","hermes_path":"relative/bin"}`,
		`{"host":"host.invalid","display_name":"bad\u0001name"}`,
		`{"host":"host.invalid","port":22.5}`, `{"host":"host.invalid","port":true}`,
		`{"host":"host.invalid","port":0}`, `{"host":"host.invalid","port":65536}`,
		`{"host":"host.invalid"} {"host":"second.invalid"}`,
	} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/connections/ssh", strings.NewReader(body)))
			if w.Code < 400 || w.Code >= 500 {
				t.Errorf("invalid target accepted: HTTP %d %s", w.Code, w.Body.String())
			}
			rows, err := st.ListSSHConnections(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatalf("invalid target wrote a connection: count=%d error=%v", len(rows), err)
			}
			t.Cleanup(func() {
				for _, row := range rows {
					_, _ = st.DeleteSSHConnection(context.Background(), row["id"].(string))
				}
			})
		})
	}
}

func TestSSHSavePreservesProfileAndConfigIdentity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h, err := connections.NewHandler(connections.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"host":"host.invalid","port":"2222","profile_name":"dev","display_name":"Private host","hermes_path":"/opt/hermes","workspace":"/srv/work","state":"connected","agent_id":"forged"}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/connections/ssh", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var row map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	settings, _ := row["settings"].(map[string]any)
	if row["state"] != "configured" || row["agent_id"] != nil || settings["profile_name"] != "dev" || settings["display_name"] != "Private host" || settings["connection_id"] != row["id"] {
		t.Fatalf("save lost identity or accepted client runtime state: %v", row)
	}
	id := row["id"].(string)
	w = httptest.NewRecorder()
	body = `{"connection_id":"` + id + `","host":"updated.invalid","profile_name":"dev"}`
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/connections/ssh", strings.NewReader(body)))
	rows, err := st.ListSSHConnections(context.Background())
	if w.Code != http.StatusOK || err != nil || len(rows) != 1 || rows[0]["id"] != id {
		t.Fatalf("connection_id update created another row: status=%d rows=%v err=%v", w.Code, rows, err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/connections/ssh/missing", strings.NewReader(`{"host":"host.invalid"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatal("missing update target was created", w.Code)
	}
}
