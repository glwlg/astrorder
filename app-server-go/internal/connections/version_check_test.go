package connections

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestVersionComparisonUsesActualRelease(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		update          bool
	}{
		{"0.163.0", "0.163.0", false}, {"0.163.0", "0.162.0", false},
		{"0.162.0", "0.163.0", true}, {"1.0.9", "1.0.60", true},
		{"1.0.60-alpha.9", "1.0.60-alpha.10", true}, {"1.0.60-alpha.1", "1.0.60", true},
		{"1.0.60", "1.0.60-alpha.1", false},
	} {
		got := compareVersionCheck(tc.current, tc.latest)
		if got.Error != "" || got.HasUpdate != tc.update {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	if got := compareVersionCheck("1.0.60", "latest"); got.Error == "" || got.HasUpdate {
		t.Fatal(got)
	}
}

func TestGrokNativeVersionCheck(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		update, failed bool
	}{
		{`{"currentVersion":"1.0.60","latestVersion":"1.0.60","updateAvailable":false,"error":null}`, false, false},
		{`{"currentVersion":"1.0.59","latestVersion":"1.0.60","updateAvailable":true,"error":null}`, true, false},
		{`{"currentVersion":"1.0.60","latestVersion":null,"updateAvailable":false,"error":"offline"}`, false, true},
		{`{"currentVersion":"1.0.60","latestVersion":"1.0.60"}`, false, true},
	} {
		got := parseGrokVersionCheck([]byte(tc.raw))
		if got.HasUpdate != tc.update || (got.Error != "") != tc.failed {
			t.Fatalf("%s: %+v", tc.raw, got)
		}
	}
}

func TestCodexRegistryVersionAndFailures(t *testing.T) {
	for _, body := range []string{`{"version":"0.163.0"}`, `{"version":"latest"}`, `invalid`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		got, err := fetchCodexLatest(context.Background(), server.Client(), server.URL)
		server.Close()
		if body == `{"version":"0.163.0"}` {
			if err != nil || got != "0.163.0" {
				t.Fatal(got, err)
			}
		} else if err == nil {
			t.Fatal("invalid registry accepted", got)
		}
	}
}

type unknownVersion struct{}

func (unknownVersion) GetVersion(context.Context, string, string, map[string]any) (string, bool, error) {
	return "0.163.0", false, nil
}
func TestUpgradeTargetDoesNotInventLatestOrUpdate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "versions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.UpsertAgent(context.Background(), map[string]any{"id": "codex", "name": "Codex", "kind": "codex", "connection_id": "local", "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Options{Store: st, VersionExecutor: unknownVersion{}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/agents/upgrade/targets", nil))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if contains := response.Body.String(); !strings.Contains(contains, `"has_update":false`) || strings.Contains(contains, `"latest_version":"latest"`) || !strings.Contains(contains, `"version_error"`) {
		t.Fatal(contains)
	}
}
