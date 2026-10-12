package targetfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

// TestSSHEndToEnd runs only when an owned endpoint has been prepared by the
// caller. It intentionally consumes no production settings or credentials.
func TestSSHEndToEnd(t *testing.T) {
	alias := os.Getenv("ASTRORDER_TEST_SSH_ALIAS")
	configPath := os.Getenv("ASTRORDER_TEST_SSH_CONFIG")
	helperPath := os.Getenv("ASTRORDER_TEST_SSH_HELPER")
	workspace := os.Getenv("ASTRORDER_TEST_SSH_WORKSPACE")
	if alias == "" || configPath == "" || helperPath == "" || workspace == "" {
		t.Skip("owned SSH test endpoint is not configured")
	}
	command, err := BuildSSHCommand(SSHConfig{Alias: alias, ConfigFile: configPath, HelperPath: helperPath})
	if err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewRemote(RemoteOptions{Workspace: workspace, AllowedRoots: []string{workspace}, Command: command, StageRoots: []string{workspace}})
	if err != nil {
		t.Fatal(err)
	}
	path := workspace + "/SSH 空 格.bin"
	payload := []byte{0, 1, 2, 0xff, '\n'}
	if _, err := filesystem.WriteAtomic(context.Background(), path, bytes.NewReader(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := filesystem.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	actual, readErr := io.ReadAll(read.Reader)
	closeErr := read.Reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("read=%v readErr=%v closeErr=%v", actual, readErr, closeErr)
	}
	search, err := filesystem.Search(context.Background(), SearchRequest{Root: workspace, Query: "空 格", Limit: 10})
	if err != nil || len(search.Items) != 1 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
}
