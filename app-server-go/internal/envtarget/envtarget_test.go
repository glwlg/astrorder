package envtarget

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localEnvForTest(t *testing.T) (*localEnv, string) {
	t.Helper()
	dir := filepath.ToSlash(t.TempDir())
	return &localEnv{target: Target{ID: "local", Kind: "local"}}, dir
}

func TestLocalWriteReadRemoveKeepsMode(t *testing.T) {
	env, dir := localEnvForTest(t)
	ctx := context.Background()
	file := dir + "/a/b.txt"
	if err := env.Write(ctx, file, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.Write(ctx, file, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := env.Read(ctx, file)
	if err != nil || string(data) != "two" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := env.Read(ctx, dir+"/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file must report ErrNotExist, got %v", err)
	}
	many, err := env.ReadMany(ctx, []string{file, dir + "/missing"})
	if err != nil || len(many) != 1 || string(many[file]) != "two" {
		t.Fatalf("%v %v", many, err)
	}
	if err := env.Remove(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err := env.Remove(ctx, file); err != nil {
		t.Fatalf("removing twice must not fail: %v", err)
	}
}

func TestLocalFindSkipsDepthAndReadsHead(t *testing.T) {
	env, dir := localEnvForTest(t)
	ctx := context.Background()
	write := func(rel, text string) {
		if err := env.Write(ctx, dir+"/"+rel, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/a/SKILL.md", "name: a\nline2\nline3\n")
	write("skills/group/b/SKILL.md", "name: b\n")
	write("skills/deep/x/y/z/w/SKILL.md", "name: deep\n")
	found, err := env.Find(ctx, []string{dir + "/skills", dir + "/absent"}, "SKILL.md", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, hit := range found {
		got[strings.TrimPrefix(hit.Path, dir+"/skills/")] = hit.Head
	}
	if len(got) != 2 || got["a/SKILL.md"] != "name: a\nline2\n" || got["group/b/SKILL.md"] == "" {
		t.Fatalf("%v", got)
	}
}

func TestParseFound(t *testing.T) {
	text := findMarker + "/x/a/SKILL.md\nname: a\n\n" + findMarker + "/x/b/SKILL.md\nname: b\n\n"
	hits := ParseFound(text)
	if len(hits) != 2 || hits[0].Path != "/x/a/SKILL.md" || !strings.HasPrefix(hits[1].Head, "name: b") {
		t.Fatalf("%+v", hits)
	}
	if ParseFound("") != nil {
		t.Fatal("empty output has no hits")
	}
}

func TestPackAndUnpackRoundTripReplacesDirectory(t *testing.T) {
	env, dir := localEnvForTest(t)
	ctx := context.Background()
	for rel, text := range map[string]string{"skills/alpha/SKILL.md": "hello", "skills/alpha/sub/run.sh": "#!/bin/sh\n", "skills/alpha/.git/config": "secret"} {
		if err := env.Write(ctx, dir+"/"+rel, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := env.PackDir(ctx, dir+"/skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Write(ctx, dir+"/dest/alpha/stale.txt", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.UnpackDir(ctx, dir+"/dest", "alpha", archive); err != nil {
		t.Fatal(err)
	}
	if data, err := env.Read(ctx, dir+"/dest/alpha/sub/run.sh"); err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := env.Read(ctx, dir+"/dest/alpha/stale.txt"); err == nil {
		t.Error("old content must be replaced")
	}
	if _, err := env.Read(ctx, dir+"/dest/alpha/.git/config"); err == nil {
		t.Error(".git must not be packed")
	}
	entries, _ := os.ReadDir(dir + "/dest")
	if len(entries) != 1 {
		t.Errorf("staging directory left behind: %v", entries)
	}
}

func archiveOf(t *testing.T, headers ...*tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gz)
	for _, header := range headers {
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			archive.Write(bytes.Repeat([]byte("x"), int(header.Size)))
		}
	}
	archive.Close()
	gz.Close()
	return buffer.Bytes()
}

func TestUnpackRejectsEscapesAndSkipsLinks(t *testing.T) {
	env, dir := localEnvForTest(t)
	ctx := context.Background()
	for _, name := range []string{"../evil.txt", "alpha/../../evil.txt", "/abs.txt", "alpha/a:b"} {
		bad := archiveOf(t, &tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 1, Mode: 0o644})
		if err := env.UnpackDir(ctx, dir+"/dest", "alpha", bad); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
	if _, err := os.Stat(dir + "/evil.txt"); err == nil {
		t.Fatal("file escaped the destination")
	}
	withLink := archiveOf(t,
		&tar.Header{Name: "alpha/", Typeflag: tar.TypeDir, Mode: 0o755},
		&tar.Header{Name: "alpha/SKILL.md", Typeflag: tar.TypeReg, Size: 2, Mode: 0o644},
		&tar.Header{Name: "alpha/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	if err := env.UnpackDir(ctx, dir+"/dest", "alpha", withLink); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir + "/dest/alpha/link"); err == nil {
		t.Error("symlink must be skipped")
	}
	if err := env.UnpackDir(ctx, dir+"/dest", "other", withLink); err == nil {
		t.Error("archive without the named directory must fail")
	}
	for _, name := range []string{"", "..", "a/b", `a\b`} {
		if err := env.UnpackDir(ctx, dir+"/dest", name, withLink); err == nil {
			t.Errorf("directory name %q accepted", name)
		}
	}
	if err := env.UnpackDir(ctx, dir+"/dest", "alpha", []byte("not gzip")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestRenameCreatesParent(t *testing.T) {
	env, dir := localEnvForTest(t)
	ctx := context.Background()
	if err := env.Write(ctx, dir+"/a/f.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.Rename(ctx, dir+"/a", dir+"/b/.archive/a-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Read(ctx, dir+"/b/.archive/a-1/f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := env.Rename(ctx, dir+"/nope", dir+"/x"); err == nil {
		t.Error("renaming a missing path must fail")
	}
}

func TestResultOutputPrefersStdout(t *testing.T) {
	if (Result{Stdout: " out \n", Stderr: "err"}).Output() != "out" || (Result{Stderr: " err "}).Output() != "err" {
		t.Fatal("Output")
	}
}

type fakeStore struct{ connections []map[string]any }

func (f fakeStore) ListSSHConnections(context.Context) ([]map[string]any, error) {
	return f.connections, nil
}

func TestResolverListsAndOpensTargets(t *testing.T) {
	resolver := &Resolver{Store: fakeStore{[]map[string]any{
		{"id": "ssh-1", "display_name": "Debian", "state": "connected", "profile_name": "work", "settings": map[string]any{"host": "10.0.0.2"}},
		{"id": "", "display_name": "broken"},
	}}, WSLBinary: "definitely-not-a-binary"}
	targets, err := resolver.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].ID != "local" || targets[1].ID != "ssh:ssh-1" || targets[1].Name != "Debian" || targets[1].Profile != "work" {
		t.Fatalf("%+v", targets)
	}
	if env, err := resolver.Open(context.Background(), ""); err != nil || !env.IsLocal() {
		t.Fatalf("%v", err)
	}
	if _, err := resolver.Open(context.Background(), "ssh:nope"); err == nil {
		t.Fatal("unknown target opened")
	}
	if _, err := resolver.Open(context.Background(), "ssh:ssh-1"); err == nil {
		t.Fatal("remote target without a shell backend must be reported as unavailable")
	}
}
