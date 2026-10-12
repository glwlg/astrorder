package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/lifecycle"
)

func TestProvisionCLIRequiresExplicitInstallationPaths(t *testing.T) {
	var output bytes.Buffer
	if code := run([]string{"provision"}, &output); code == 0 || !strings.Contains(output.String(), "root") {
		t.Fatalf("unsafe implicit provision: %s", output.String())
	}
}

func TestProvisionCLIWritesAndPreservesPrivateInstallation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("current-user DPAPI integration is Windows only")
	}
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	data := filepath.Join(state, "data")
	app := filepath.Join(root, "bin", "astrorder-app.exe")
	for _, dir := range []string{filepath.Dir(app), filepath.Join(root, "frontend", "dist")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(app, []byte("isolated provision executable reference"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"provision", "--root", root, "--state-dir", state, "--data-dir", data, "--app-executable", app, "--grpc-target", "127.0.0.1:45678"}
	var output bytes.Buffer
	if code := run(args, &output); code != 0 {
		t.Fatalf("provision failed: %s", output.String())
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result["ok"] != true {
		t.Fatalf("provision JSON: %s err=%v", output.String(), err)
	}
	before, err := lifecycle.OpenEnvironment(state)
	if err != nil {
		t.Fatal(err)
	}
	if before["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"] != "127.0.0.1:45678" {
		t.Fatal("explicit target lost")
	}
	output.Reset()
	if code := run(args, &output); code != 0 {
		t.Fatalf("repeat provision: %s", output.String())
	}
	after, err := lifecycle.OpenEnvironment(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ASTRORDER_BROWSER_SECRET", "ASTRORDER_CONNECTOR_SECRET", "ASTRORDER_SESSION_DAEMON_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"} {
		if before[key] == "" || after[key] != before[key] {
			t.Fatalf("credential rotated: %s", key)
		}
		if strings.Contains(output.String(), after[key]) {
			t.Fatalf("credential printed: %s", key)
		}
	}
}

func TestProcessEnvironmentRemovesInheritedAstrorderValues(t *testing.T) {
	for key, value := range map[string]string{
		"ASTRORDER_BROWSER_SECRET":             "inherited-browser-secret",
		"ASTRORDER_CONNECTOR_SECRET":           "inherited-connector-secret",
		"ASTRORDER_MIGRATION_UPSTREAM":         "http://127.0.0.1:65534",
		"ASTRORDER_MIGRATION_BROWSER_SECRET":   "inherited-upstream-secret",
		"ASTRORDER_SESSION_DAEMON_GRPC_TARGET": "127.0.0.1:65533",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("ASTRORDER_ENV_FIXTURE_KEEP", "saved-value")
	t.Setenv("NON_ASTRORDER_LIFECYCLE_FIXTURE", "preserved")

	environment := processEnvironment(map[string]string{
		"ASTRORDER_BROWSER_SECRET":   "saved-browser-secret",
		"ASTRORDER_BROWSER_secret":   "public-shadow-secret",
		"ASTRORDER_CONNECTOR_SECRET": "saved-connector-secret",
		"ASTRORDER_ENV_FIXTURE_KEEP": "saved-value",
	})
	values := make(map[string]string)
	counts := make(map[string]int)
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		normalized := key
		if runtime.GOOS == "windows" {
			normalized = strings.ToUpper(key)
		}
		values[normalized] = value
		counts[normalized]++
	}
	if values["ASTRORDER_BROWSER_SECRET"] != "saved-browser-secret" || values["ASTRORDER_CONNECTOR_SECRET"] != "saved-connector-secret" {
		t.Fatal("saved credentials did not replace inherited credentials")
	}
	if counts["ASTRORDER_BROWSER_SECRET"] != 1 {
		t.Fatal("case-insensitive environment contained duplicate browser credential keys")
	}
	for _, key := range []string{"ASTRORDER_MIGRATION_UPSTREAM", "ASTRORDER_MIGRATION_BROWSER_SECRET", "ASTRORDER_SESSION_DAEMON_GRPC_TARGET"} {
		if _, exists := values[key]; exists {
			t.Fatalf("inherited installation setting survived clean environment build: %s", key)
		}
	}
	if values["NON_ASTRORDER_LIFECYCLE_FIXTURE"] != "preserved" {
		t.Fatal("non-Astrorder environment was not preserved")
	}
}

func TestAppStatusCLIRejectsMissingExplicitPaths(t *testing.T) {
	var output bytes.Buffer
	if code := run([]string{"app", "status"}, &output); code == 0 || !strings.Contains(output.String(), "root") {
		t.Fatalf("implicit App query accepted: %s", output.String())
	}
}
