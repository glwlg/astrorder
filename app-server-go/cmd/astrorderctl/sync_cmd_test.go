package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSyncDistFixture(t *testing.T, root, version string) string {
	t.Helper()
	dist := filepath.Join(root, "frontend", "dist")
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	index := `<!doctype html><html><head><script type="module" crossorigin src="/assets/index-` + version + `.js"></script><link rel="stylesheet" crossorigin href="/assets/index-` + version + `.css"></head><body>` + version + `</body></html>`
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "index-"+version+".js"), []byte("console.log('"+version+"')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "index-"+version+".css"), []byte("body{content:'"+version+"'}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "sw-astrorder.js"), []byte("// sw "+version), 0o644); err != nil {
		t.Fatal(err)
	}
	return dist
}

func TestSyncRejectsIncompleteDistAndPreservesInstalled(t *testing.T) {
	installedRoot := t.TempDir()
	writeSyncDistFixture(t, installedRoot, "v1")

	cases := []struct {
		name      string
		prepare   func(t *testing.T, devRoot string)
		wantError string
	}{
		{
			name: "public files only without index.html",
			prepare: func(t *testing.T, devRoot string) {
				dist := filepath.Join(devRoot, "frontend", "dist")
				if err := os.MkdirAll(dist, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dist, "favicon.ico"), []byte("ico"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "index.html",
		},
		{
			name: "missing referenced bundle asset",
			prepare: func(t *testing.T, devRoot string) {
				dist := writeSyncDistFixture(t, devRoot, "v2")
				if err := os.Remove(filepath.Join(dist, "assets", "index-v2.js")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "index-v2.js",
		},
		{
			name: "desktop wails dist without sw-astrorder.js",
			prepare: func(t *testing.T, devRoot string) {
				dist := writeSyncDistFixture(t, devRoot, "v2")
				if err := os.Remove(filepath.Join(dist, "sw-astrorder.js")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "sw-astrorder.js",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devRoot := t.TempDir()
			tc.prepare(t, devRoot)
			var output bytes.Buffer
			code := run([]string{"sync", "--dev-root", devRoot, "--installed-root", installedRoot}, &output)
			if code == 0 {
				t.Fatalf("expected sync to fail, got ok: %s", output.String())
			}
			if !strings.Contains(output.String(), tc.wantError) {
				t.Fatalf("expected error mentioning %q, got %s", tc.wantError, output.String())
			}
			body, err := os.ReadFile(filepath.Join(installedRoot, "frontend", "dist", "index.html"))
			if err != nil || !strings.Contains(string(body), "index-v1.js") {
				t.Fatalf("existing installed dist was damaged: body=%q err=%v", string(body), err)
			}
		})
	}

	t.Run("overlapping source and destination rejected", func(t *testing.T) {
		var output bytes.Buffer
		code := run([]string{"sync", "--dev-root", installedRoot, "--installed-root", installedRoot}, &output)
		if code == 0 || !strings.Contains(output.String(), "overlap") {
			t.Fatalf("expected overlap rejection, code=%d out=%s", code, output.String())
		}
	})

	t.Run("verify-only checks installed dist", func(t *testing.T) {
		var okOut bytes.Buffer
		if code := run([]string{"sync", "--verify-only", "--installed-root", installedRoot}, &okOut); code != 0 {
			t.Fatalf("verify-only failed on valid dist: %s", okOut.String())
		}
		brokenRoot := t.TempDir()
		if err := os.MkdirAll(filepath.Join(brokenRoot, "frontend", "dist"), 0o755); err != nil {
			t.Fatal(err)
		}
		var badOut bytes.Buffer
		if code := run([]string{"sync", "--verify-only", "--installed-root", brokenRoot}, &badOut); code == 0 {
			t.Fatalf("verify-only accepted broken dist: %s", badOut.String())
		}
	})
}

func TestCopyDirAtomicRevalidatesStagedCopyBeforeSwap(t *testing.T) {
	devRoot := t.TempDir()
	installedRoot := t.TempDir()
	srcDist := writeSyncDistFixture(t, devRoot, "v2")
	destDist := writeSyncDistFixture(t, installedRoot, "v1")

	err := copyDirAtomic(srcDist, destDist, func(string) error {
		return errors.New("simulated mid-copy corruption")
	})
	if err == nil || !strings.Contains(err.Error(), "simulated mid-copy corruption") {
		t.Fatalf("expected staged validation failure, got %v", err)
	}
	body, readErr := os.ReadFile(filepath.Join(destDist, "index.html"))
	if readErr != nil || !strings.Contains(string(body), "index-v1.js") {
		t.Fatalf("installed dist was modified despite failed staged validation: %q %v", string(body), readErr)
	}
	entries, readDirErr := os.ReadDir(filepath.Dir(destDist))
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sync-tmp-") || strings.HasSuffix(entry.Name(), ".old") {
			t.Fatalf("unexpected leftover staging entry: %s", entry.Name())
		}
	}
}
