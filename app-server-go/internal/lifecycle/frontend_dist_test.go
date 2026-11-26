package lifecycle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeValidBrowserDist(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := `<!doctype html><html><head><script type="module" crossorigin src="/assets/index-abc123.js"></script><link rel="stylesheet" crossorigin href="/assets/index-def456.css"></head><body><div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-def456.css"), []byte("body{margin:0}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sw-astrorder.js"), []byte("self.addEventListener('fetch',()=>{})"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateStaticDirRejectsBrokenDist(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if err := ValidateStaticDir(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected error for missing directory")
		}
	})

	t.Run("public files only without index.html", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "favicon.ico"), []byte("ico"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err == nil || !strings.Contains(err.Error(), "index.html") {
			t.Fatalf("expected index.html error, got %v", err)
		}
	})

	t.Run("empty index.html", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("expected empty index.html error, got %v", err)
		}
	})

	t.Run("unbuilt dev source index.html", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<script type="module" src="/src/main.tsx"></script>`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err == nil || !strings.Contains(err.Error(), "unbuilt /src/") {
			t.Fatalf("expected unbuilt /src/ error, got %v", err)
		}
	})

	t.Run("missing referenced asset", func(t *testing.T) {
		dir := t.TempDir()
		writeValidBrowserDist(t, dir)
		if err := os.Remove(filepath.Join(dir, "assets", "index-def456.css")); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err == nil || !strings.Contains(err.Error(), "index-def456.css") {
			t.Fatalf("expected missing referenced asset error, got %v", err)
		}
	})

	t.Run("symlink escape in assets rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation requires elevated privileges on Windows")
		}
		dir := t.TempDir()
		writeValidBrowserDist(t, dir)
		outside := filepath.Join(t.TempDir(), "secret.js")
		if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "assets", "index-abc123.js")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, target); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Fatalf("expected symlink escape error, got %v", err)
		}
	})

	t.Run("minimal test fixture passes static check", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateStaticDir(dir); err != nil {
			t.Fatalf("expected minimal fixture to pass ValidateStaticDir, got %v", err)
		}
	})
}

func TestValidateBrowserDistEnforcesFullPWAContract(t *testing.T) {
	t.Run("valid browser dist passes", func(t *testing.T) {
		dir := t.TempDir()
		writeValidBrowserDist(t, dir)
		if err := ValidateBrowserDist(dir); err != nil {
			t.Fatalf("expected valid browser dist to pass, got %v", err)
		}
	})

	t.Run("minimal fixture without compiled module entry rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>v1</html>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBrowserDist(dir); err == nil || !strings.Contains(err.Error(), "module script entry") {
			t.Fatalf("expected missing module entry error, got %v", err)
		}
	})

	t.Run("desktop wails build without sw-astrorder.js rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeValidBrowserDist(t, dir)
		if err := os.Remove(filepath.Join(dir, "sw-astrorder.js")); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBrowserDist(dir); err == nil || !strings.Contains(err.Error(), "sw-astrorder.js") {
			t.Fatalf("expected missing sw-astrorder.js error, got %v", err)
		}
	})
}
