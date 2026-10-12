package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceCommandFlatteningAndValidation(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"service"}, &output)
	if code == 0 {
		t.Fatal("expected failure on empty service invocation")
	}
	var errResult map[string]any
	if err := json.Unmarshal(output.Bytes(), &errResult); err != nil || errResult["ok"] != false {
		t.Fatalf("expected json error result: %v", output.String())
	}
}

func TestLoginCommandRejectsMissingPath(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"login"}, &output)
	if code == 0 {
		t.Fatal("expected failure on missing state dir")
	}
}

func TestSyncCommandValidatesRoots(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"sync"}, &output)
	if code == 0 {
		t.Fatal("expected failure on missing roots")
	}
}

func TestSyncCommandAtomicCopy(t *testing.T) {
	devRoot := t.TempDir()
	installedRoot := t.TempDir()

	srcDist := filepath.Join(devRoot, "frontend", "dist")
	if err := os.MkdirAll(srcDist, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDist, "index.html"), []byte("<html>v1</html>"), 0644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	code := run([]string{"sync", "--dev-root", devRoot, "--installed-root", installedRoot}, &output)
	if code != 0 {
		t.Fatalf("sync failed: %s", output.String())
	}

	destIndex := filepath.Join(installedRoot, "frontend", "dist", "index.html")
	body, err := os.ReadFile(destIndex)
	if err != nil || string(body) != "<html>v1</html>" {
		t.Fatalf("unexpected synced body: %s err: %v", string(body), err)
	}
}

func TestDaemonCommandRejectsMissingExplicitPaths(t *testing.T) {
	var output bytes.Buffer
	code := run([]string{"daemon", "status"}, &output)
	if code == 0 || !strings.Contains(output.String(), "root") {
		t.Fatalf("implicit daemon query accepted: %s", output.String())
	}
}

func TestLoginCommandReadsToken(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI required")
	}
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	data := filepath.Join(state, "data")
	app := filepath.Join(root, "bin", "astrorder-app.exe")
	for _, dir := range []string{filepath.Dir(app), filepath.Join(root, "frontend", "dist")} {
		_ = os.MkdirAll(dir, 0700)
	}
	_ = os.WriteFile(app, []byte("app fixture"), 0600)
	_ = os.WriteFile(filepath.Join(root, "frontend", "dist", "index.html"), []byte("index fixture"), 0600)

	var pOutput bytes.Buffer
	pCode := run([]string{"provision", "--root", root, "--state-dir", state, "--data-dir", data, "--app-executable", app, "--grpc-target", "127.0.0.1:45678"}, &pOutput)
	if pCode != 0 {
		t.Fatalf("provision failed: %s", pOutput.String())
	}

	var loginOut bytes.Buffer
	lCode := run([]string{"login", "--state-dir", state}, &loginOut)
	if lCode != 0 || len(loginOut.String()) < 10 {
		t.Fatalf("login failed: code=%d out=%s", lCode, loginOut.String())
	}
}
