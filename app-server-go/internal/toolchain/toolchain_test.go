package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
	"astrorder.dev/app-server/internal/store"
)

func TestResolveInstallOrderWithDeps(t *testing.T) {
	steps, err := ResolveInstallOrder([]string{"codex"}, true, map[string]bool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "node" || steps[1].ID != "codex" {
		t.Fatalf("expected [node codex], got %+v", steps)
	}

	steps, err = ResolveInstallOrder([]string{"codex"}, true, map[string]bool{"node": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 || steps[0].ID != "codex" {
		t.Fatalf("expected [codex], got %+v", steps)
	}

	steps, err = ResolveInstallOrder([]string{"hermes"}, true, map[string]bool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "uv" || steps[1].ID != "hermes" {
		t.Fatalf("expected [uv hermes], got %+v", steps)
	}
}

func TestRecipesForAllCatalogItems(t *testing.T) {
	for _, spec := range Catalog() {
		for _, osName := range []string{"windows", "linux", "darwin"} {
			recipes := RecipesFor(spec.ID, osName)
			if len(recipes) == 0 {
				t.Fatalf("missing recipe for %s on %s", spec.ID, osName)
			}
			for _, r := range recipes {
				if strings.TrimSpace(r.Command) == "" {
					t.Fatalf("empty command for %s (%s) on %s", spec.ID, r.ID, osName)
				}
			}
		}
	}
}

func TestValidateAndSaveLocalExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture uses POSIX sh")
	}
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(tmp, "fake-codex")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho \"codex 0.42.1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Options{
		Store:            st,
		StateDir:         stateDir,
		DefaultWorkspace: tmp,
	})
	defer svc.Close(context.Background())

	val, err := svc.ValidateExecutable(context.Background(), "local", "codex", fakeBin)
	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if !val.Valid || val.Version != "0.42.1" {
		t.Fatalf("expected valid 0.42.1, got %+v", val)
	}

	// Non-existent path must fail cleanly
	invalid, err := svc.ValidateExecutable(context.Background(), "local", "codex", filepath.Join(tmp, "missing"))
	if err != nil {
		t.Fatalf("validate missing returned error: %v", err)
	}
	if invalid.Valid {
		t.Fatalf("expected invalid result for missing file")
	}

	saved, err := svc.SaveExecutable(context.Background(), "local", "codex", fakeBin)
	if err != nil {
		t.Fatalf("save executable: %v", err)
	}
	if !saved.Saved || !saved.RequiresDaemonRestart {
		t.Fatalf("expected saved=true requires_daemon_restart=true, got %+v", saved)
	}
	if got := st.ReadLocalExecutableOverride(context.Background(), "agent-codex"); got != fakeBin {
		t.Fatalf("expected plugin config override %q, got %q", fakeBin, got)
	}
	if got := lifecycle.ReadLocalRuntimeExecutable(stateDir, "codex"); got != fakeBin {
		t.Fatalf("expected daemon config executable %q, got %q", fakeBin, got)
	}
}

func TestNetworkSettingsAndAgentEngineRegistry(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := NewService(Options{Store: st, StateDir: tmp, DefaultWorkspace: tmp})
	defer svc.Close(context.Background())

	engines := RegisteredAgentEngines()
	if len(engines) != 4 {
		t.Fatalf("expected 4 registered agent engines, got %d", len(engines))
	}
	if spec, ok := LookupAgentEngine("codex-ssh"); !ok || spec.Kind != "codex" {
		t.Fatalf("expected alias codex-ssh -> codex, got %+v %v", spec, ok)
	}

	// Extra tools exist and python depends on uv
	steps, err := ResolveInstallOrder([]string{"python"}, true, map[string]bool{})
	if err != nil {
		t.Fatalf("python dependency resolution failed: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "uv" || steps[1].ID != "python" {
		t.Fatalf("expected [uv python], got %+v", steps)
	}

	// Persist default & per-env network settings
	saved, err := svc.SaveNetworkSettings(context.Background(), "default", NetworkProfile{
		ProxyURL: "http://127.0.0.1:7890",
		Mirror:   "cn",
	})
	if err != nil {
		t.Fatalf("save default network: %v", err)
	}
	if saved.Default.ProxyURL != "http://127.0.0.1:7890" || saved.Default.Mirror != "cn" {
		t.Fatalf("unexpected default network: %+v", saved)
	}
	eff := svc.ResolveEffectiveNetwork(context.Background(), "local", "", "")
	if eff.ProxyURL != "http://127.0.0.1:7890" || eff.Mirror != "cn" {
		t.Fatalf("expected inherited default network, got %+v", eff)
	}
	override := svc.ResolveEffectiveNetwork(context.Background(), "local", "http://127.0.0.1:1080", "")
	if override.ProxyURL != "http://127.0.0.1:1080" || override.Mirror != "cn" {
		t.Fatalf("expected override proxy + inherited mirror, got %+v", override)
	}
}

func TestHandlerStatusAndInstallJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("custom command uses sh")
	}
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := NewService(Options{Store: st, StateDir: tmp, DefaultWorkspace: tmp})
	defer svc.Close(context.Background())
	h := NewHandler(svc, nil)

	// GET /api/v1/toolchain/status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/toolchain/status?environment=local", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status returned %d: %s", rec.Code, rec.Body.String())
	}
	var report EnvironmentReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(report.Items) != len(Catalog()) {
		t.Fatalf("expected %d items, got %d", len(Catalog()), len(report.Items))
	}

	// POST /api/v1/toolchain/install/jobs with harmless custom command
	payload := `{"environment_id":"local","tool_ids":["uv"],"include_deps":false,"custom_command":"echo installed-ok"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/toolchain/install/jobs", bytes.NewBufferString(payload))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start job returned %d: %s", rec.Code, rec.Body.String())
	}
	var started map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	jobID, _ := started["id"].(string)
	if jobID == "" {
		t.Fatalf("missing job id")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := svc.GetInstallJob(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if snap["status"] != "running" {
			if snap["status"] != "completed" {
				t.Fatalf("expected completed, got %+v", snap)
			}
			if !strings.Contains(snap["log"].(string), "installed-ok") {
				t.Fatalf("expected log to include output, got %q", snap["log"])
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("install job timed out")
}
