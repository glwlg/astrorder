package targetfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteFileSystemRunsVersionedHelperInSubprocess(t *testing.T) {
	root := t.TempDir()
	filesystem, err := NewRemote(RemoteOptions{
		Workspace: root, AllowedRoots: []string{root},
		Command:     []string{os.Args[0], "-test.run=TestRemoteHelperProcess", "--"},
		Environment: []string{"GO_WANT_F07_HELPER=1"},
		StageRoots:  []string{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "远程 binary.bin")
	payload := []byte{0, 1, 0xff, '\n'}
	info, err := filesystem.WriteAtomic(context.Background(), path, bytes.NewReader(payload), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(payload)) {
		t.Fatalf("write info=%+v", info)
	}
	read, err := filesystem.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(read.Reader)
	closeErr := read.Reader.Close()
	if err != nil || closeErr != nil || !bytes.Equal(got, payload) {
		t.Fatalf("read=%v err=%v close=%v", got, err, closeErr)
	}
	list, err := filesystem.List(context.Background(), ListRequest{Path: root, Depth: 1})
	if err != nil || len(list.Items) != 1 || list.Items[0].Name != "远程 binary.bin" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}

func TestRemoteHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_F07_HELPER") != "1" {
		return
	}
	if err := ServeHelper(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
