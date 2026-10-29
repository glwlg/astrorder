package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExtractUVFromArchive(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("uv-platform/uv.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("test executable"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	binary, err := extractUV(archive.Bytes(), "uv.zip", "uv.exe")
	if err != nil || string(binary) != "test executable" {
		t.Fatal(string(binary), err)
	}
	if _, err := extractUV(archive.Bytes(), "uv.zip", "missing"); err == nil {
		t.Fatal("missing executable was accepted")
	}
}

func TestInstallUVIntoIsolatedCache(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_BROWSER_BOOTSTRAP") != "1" || runtime.GOOS != "windows" {
		t.Skip("requires a Windows network download")
	}
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-path"))
	t.Setenv("USERPROFILE", root)
	t.Setenv("LOCALAPPDATA", root)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	path, err := ensureUV(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		t.Fatal("uv was installed outside the isolated cache")
	}
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil || !strings.Contains(string(output), "uv 0.12.1") {
		t.Fatal("installed uv could not run", err)
	}
}
