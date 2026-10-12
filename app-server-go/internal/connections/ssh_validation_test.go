package connections_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/store"
)

// The subprocess is an OpenSSH argv/error fixture, not a remote connection.
func TestMain(m *testing.M) {
	if file := os.Getenv("ASTRORDER_SSH_OS_ARGV"); file != "" && len(os.Args) > 1 && os.Args[1] == "-o" && os.Args[len(os.Args)-1] == "uname -s" {
		raw, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(file, raw, 0600); err != nil {
			os.Exit(2)
		}
		os.Stdout.WriteString("Linux" + string([]byte{10}))
		os.Exit(0)
	}
	if file := os.Getenv("ASTRORDER_SSH_VERSION_ARGV"); file != "" && len(os.Args) > 1 && os.Args[1] == "-o" && os.Args[len(os.Args)-1] == "--version" {
		raw, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(file, raw, 0600); err != nil {
			os.Exit(2)
		}
		if os.Getenv("ASTRORDER_SSH_VERSION_FAIL") == "1" {
			os.Exit(1)
		}
		os.Stdout.WriteString("codex 1.2.3" + string([]byte{10}))
		os.Exit(0)
	}
	if file := os.Getenv("ASTRORDER_SSH_VALIDATION_ARGV"); file != "" && len(os.Args) > 1 && os.Args[1] == "-G" {
		raw, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(file, raw, 0600); err != nil {
			os.Exit(2)
		}
		if os.Getenv("ASTRORDER_SSH_VALIDATION_FAIL") == "1" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSSHValidationExecutesOpenSSHConfigAndPersistsOutcome(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid config", true: "invalid config"}[fail], func(t *testing.T) {
			root := t.TempDir()
			st, err := store.Open(filepath.Join(root, "validation.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			_, err = st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "ignored.invalid", "ssh_config_alias": "private-host", "user": "developer", "port": 2222, "identity_file": "/private/test.key", "state": "configured"})
			if err != nil {
				t.Fatal(err)
			}
			argvFile := filepath.Join(root, "argv.json")
			t.Setenv("ASTRORDER_SSH_VALIDATION_ARGV", argvFile)
			if fail {
				t.Setenv("ASTRORDER_SSH_VALIDATION_FAIL", "1")
			}
			h, err := connections.NewHandler(connections.Options{Store: st, SSHBinary: os.Args[0]})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/connections/ssh/test", nil))
			wantStatus, wantState := http.StatusOK, "validated"
			if fail {
				wantStatus, wantState = http.StatusUnprocessableEntity, "error"
			}
			if w.Code != wantStatus {
				t.Fatal(w.Code, w.Body.String())
			}
			raw, err := os.ReadFile(argvFile)
			if err != nil {
				t.Fatal("OpenSSH was not executed", err)
			}
			var argv []string
			if err := json.Unmarshal(raw, &argv); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(argv, []string{"-G", "-p", "2222", "-l", "developer", "-i", "/private/test.key", "private-host"}) {
				t.Fatal("SSH config validation changed target or executed a remote command", argv)
			}
			stored, err := st.GetSSHConnection(context.Background(), "saved")
			if err != nil || stored["state"] != wantState {
				t.Fatal("validation outcome not persisted", stored, err)
			}
		})
	}
}

func TestSSHValidationUnavailableDoesNotReportSuccess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "unavailable.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.SaveSSHConnection(context.Background(), map[string]any{"id": "saved", "host": "host.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := connections.NewHandler(connections.Options{Store: st, SSHBinary: filepath.Join(t.TempDir(), "missing-ssh")})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/connections/ssh/saved/test", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code, w.Body.String())
	}
}
