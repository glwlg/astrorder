package targetfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLocalFileSystemPreservesNamesBinaryAndTreeContract(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "工作 space")
	if err := os.MkdirAll(filepath.Join(workspace, "目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewLocal(LocalOptions{Workspace: workspace, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}

	binary := []byte{0, 1, 2, 0xff, '\n'}
	written, err := fsys.WriteAtomic(context.Background(), filepath.Join(workspace, "目录", "季度 周报.bin"), bytes.NewReader(binary), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if written.Name != "季度 周报.bin" || written.Size != int64(len(binary)) {
		t.Fatalf("unexpected write result: %+v", written)
	}
	if err := os.WriteFile(filepath.Join(workspace, "z.txt"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "A-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	read, err := fsys.Read(context.Background(), filepath.Join(workspace, "目录", "季度 周报.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Reader.Close()
	got, err := io.ReadAll(read.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binary) {
		t.Fatalf("binary changed: %v", got)
	}

	tree, err := fsys.List(context.Background(), ListRequest{Path: workspace, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	gotNames := make([]string, len(tree.Items))
	for i := range tree.Items {
		gotNames[i] = tree.Items[i].Name
	}
	if want := []string{"A-dir", "dist", "目录", "z.txt"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("tree order=%q want=%q", gotNames, want)
	}

	search, err := fsys.Search(context.Background(), SearchRequest{Root: workspace, Query: "周报", Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Items) != 1 || search.Items[0].Name != "季度 周报.bin" || search.Items[0].RelativePath != "目录/季度 周报.bin" {
		t.Fatalf("unexpected search: %+v", search)
	}
}

func TestLocalFileSystemScopeRejectsTraversalAndEscapingSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "allowed")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{filepath.Join(root, "..", "outside", "secret.txt"), secret} {
		_, err := fsys.Read(context.Background(), candidate)
		assertStatus(t, err, 403)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	_, err = fsys.Read(context.Background(), filepath.Join(link, "secret.txt"))
	assertStatus(t, err, 403)
	_, err = fsys.WriteAtomic(context.Background(), filepath.Join(link, "new.txt"), strings.NewReader("no"), 0o600)
	assertStatus(t, err, 403)
}

func TestListClampsDepthAndRevealOnlyExtendsTargetBranch(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a", "b", "c", "target.txt")
	other := filepath.Join(root, "x", "y", "z", "other.txt")
	for _, name := range []string{target, other} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fsys.List(context.Background(), ListRequest{Path: root, Depth: -100, RevealPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(tree.Items, target) {
		t.Fatalf("reveal target missing: %+v", tree)
	}
	if treeContains(tree.Items, other) {
		t.Fatalf("depth clamp traversed unrelated branch: %+v", tree)
	}
}

func TestWriteAtomicHonorsCancellationWithoutPublishingPartialFile(t *testing.T) {
	root := t.TempDir()
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(root, "cancelled.txt")
	_, err = fsys.WriteAtomic(ctx, target, strings.NewReader("must not appear"), 0o600)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context canceled", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("partial target exists: %v", statErr)
	}
}

func TestSSHAndWSLCommandsAreFixedAndPreserveConnectionConfiguration(t *testing.T) {
	ssh, err := BuildSSHCommand(SSHConfig{
		Binary: "ssh-custom", Host: "ignored.example", Alias: "prod-alias", Port: 2222,
		User: "operator", IdentityFile: `C:\Keys\id key`, HelperPath: "/opt/astrorder/v1/astrorder-helper",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSSH := []string{
		"ssh-custom", "-o", "BatchMode=yes", "-p", "2222", "-l", "operator", "-i", `C:\Keys\id key`,
		"--", "prod-alias", "exec '/opt/astrorder/v1/astrorder-helper' '--stdio' '--protocol' '1'",
	}
	if !reflect.DeepEqual(ssh, wantSSH) {
		t.Fatalf("ssh argv=%q want=%q", ssh, wantSSH)
	}
	joined := strings.Join(ssh, " ")
	if strings.Contains(joined, "StrictHostKeyChecking") || strings.Contains(joined, "UserKnownHostsFile") {
		t.Fatalf("host-key defaults were overridden: %q", ssh)
	}

	wsl, err := BuildWSLCommand(WSLConfig{Binary: "wsl.exe", Distro: "Ubuntu-24.04", HelperPath: "/usr/local/lib/astrorder/v1/astrorder-helper"})
	if err != nil {
		t.Fatal(err)
	}
	wantWSL := []string{"wsl.exe", "-d", "Ubuntu-24.04", "--exec", "/usr/local/lib/astrorder/v1/astrorder-helper", "--stdio", "--protocol", "1"}
	if !reflect.DeepEqual(wsl, wantWSL) {
		t.Fatalf("wsl argv=%q want=%q", wsl, wantWSL)
	}
}

func treeContains(items []Entry, path string) bool {
	for _, item := range items {
		if item.Path == path {
			return true
		}
		if item.Children != nil && treeContains(*item.Children, path) {
			return true
		}
	}
	return false
}

func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Status != status {
		t.Fatalf("error=%T %v want status %d", err, err, status)
	}
}

func TestListKeepsWorkingWithBrokenAndEscapingSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "work")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(root, "real", "sub"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "real", "sub", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "missing"), filepath.Join(root, "broken")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fsys.List(context.Background(), ListRequest{Path: root, Depth: 6})
	if err != nil {
		t.Fatalf("one odd entry must not fail the whole tree: %v", err)
	}
	names := map[string]Entry{}
	for _, item := range tree.Items {
		names[item.Name] = item
	}
	for _, name := range []string{"broken", "escape", "real"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("%s missing from %+v", name, tree.Items)
		}
	}
	if names["broken"].IsDir || names["broken"].Children != nil || names["escape"].IsDir || names["escape"].Children != nil {
		t.Fatalf("unresolvable and escaping links must be inert leaves: %+v %+v", names["broken"], names["escape"])
	}
	if !treeContains(tree.Items, filepath.Join(root, "real", "sub", "a.txt")) {
		t.Fatalf("regular entries still listed: %+v", tree.Items)
	}
}

func TestListBoundsHugeTreesAndLeavesDeepDirectoriesForOnDemandLoading(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 100; j++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", j)), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fsys.List(context.Background(), ListRequest{Path: root, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !tree.Truncated {
		t.Fatal("4000 entries must be reported as truncated")
	}
	total, unexpanded := 0, 0
	for _, item := range tree.Items {
		total++
		if item.Children == nil {
			unexpanded++
			continue
		}
		total += len(*item.Children)
	}
	if total > maxTreeEntries+100 || unexpanded == 0 || unexpanded == len(tree.Items) {
		t.Fatalf("total=%d unexpanded=%d of %d", total, unexpanded, len(tree.Items))
	}
	// Asking for an unexpanded directory directly returns its content.
	sub, err := fsys.List(context.Background(), ListRequest{Path: filepath.Join(root, "d39"), Depth: 1})
	if err != nil || len(sub.Items) != 100 || sub.Truncated {
		t.Fatalf("%d %v %v", len(sub.Items), sub.Truncated, err)
	}
}

func TestListRevealPathIsAlwaysExpandedEvenWhenTruncated(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		dir := filepath.Join(root, fmt.Sprintf("d%02d", i))
		if err := os.MkdirAll(filepath.Join(dir, "deep"), 0o755); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 100; j++ {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", j)), nil, 0o600)
		}
	}
	target := filepath.Join(root, "d39", "deep", "want.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fsys.List(context.Background(), ListRequest{Path: root, Depth: 1, RevealPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if !treeContains(tree.Items, target) {
		t.Fatal("reveal target must be present")
	}
}

func TestCleanPathPreservesPOSIXRootAndNormalizesWindowsDriveFileURIs(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: "file:///home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.png",
			want:  "/home/luwei/workspace/OpsCore/docs/designs/security-drill/03-drill-arena.png",
		},
		{
			input: "file://localhost/home/luwei/docs/a.png",
			want:  "/home/luwei/docs/a.png",
		},
		{
			input: "file:///C:/Users/fixture/output/chart.png",
			want:  "C:/Users/fixture/output/chart.png",
		},
		{
			input: "file://localhost/C:/Users/fixture/output/chart.png",
			want:  "C:/Users/fixture/output/chart.png",
		},
		{
			input: "FILE:///c:\\Users\\fixture\\chart.png",
			want:  "c:\\Users\\fixture\\chart.png",
		},
		{
			input: "file://C:/Users/fixture/chart.png",
			want:  "C:/Users/fixture/chart.png",
		},
		{
			input: "  </home/luwei/a.png>  ",
			want:  "/home/luwei/a.png",
		},
	}
	for _, tc := range cases {
		if got := CleanPath(tc.input); got != tc.want {
			t.Errorf("CleanPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestReadRangeAndHelperProtocol(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "clip.mp4")
	content := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if err := os.WriteFile(videoPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	fsys, err := NewLocal(LocalOptions{Workspace: root, AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	rr, ok := fsys.(RangeReader)
	if !ok {
		t.Fatal("localFileSystem must implement RangeReader")
	}
	res, err := rr.ReadRange(context.Background(), videoPath, 10, 6)
	if err != nil {
		t.Fatalf("ReadRange failed: %v", err)
	}
	defer res.Reader.Close()
	got, err := io.ReadAll(res.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ABCDEF" || res.Info.Size != int64(len(content)) || res.ContentType != "video/mp4" {
		t.Fatalf("got=%q size=%d ct=%q", string(got), res.Info.Size, res.ContentType)
	}

	// Verify helper stdio protocol read_range returns only the requested slice with full file size metadata.
	reqData, _ := json.Marshal(scopeRequest{
		Workspace:    root,
		AllowedRoots: []string{root},
		Request:      mustJSON(t, map[string]any{"path": videoPath, "offset": 2, "length": 5}),
	})
	headerBytes, _ := json.Marshal(Request{Version: ProtocolVersion, Action: "read_range", Data: reqData})
	var out bytes.Buffer
	if err := ServeHelper(context.Background(), bytes.NewReader(append(headerBytes, '\n')), &out); err != nil {
		t.Fatalf("ServeHelper read_range failed: %v", err)
	}
	idx := bytes.IndexByte(out.Bytes(), '\n')
	if idx < 0 {
		t.Fatalf("missing response header newline: %q", out.String())
	}
	var resp Response
	if err := json.Unmarshal(out.Bytes()[:idx], &resp); err != nil || !resp.OK || resp.PayloadSize != 5 {
		t.Fatalf("unexpected helper response: %+v err=%v", resp, err)
	}
	if body := string(out.Bytes()[idx+1:]); body != "23456" {
		t.Fatalf("helper read_range payload=%q, want 23456", body)
	}
}
