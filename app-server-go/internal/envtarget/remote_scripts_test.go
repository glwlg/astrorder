package envtarget

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/targetfs"
)

// shRemote runs the remote scripts through a local POSIX sh so the exact script
// text that is sent over SSH is exercised, not a re-implementation of it.
type shRemote struct{ sh string }

func (r shRemote) Shell(ctx context.Context, _ string, input []byte, script string, args ...string) (targetfs.ShellResult, error) {
	cmd := exec.CommandContext(ctx, r.sh, append([]string{"-c", script, "sh"}, args...)...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := targetfs.ShellResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if exit, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}

func remoteForTest(t *testing.T) (Env, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on this machine")
	}
	resolver := &Resolver{Remote: shRemote{sh}}
	env := &remoteEnv{target: Target{ID: "ssh:x", Kind: "ssh", Name: "fake"}, connection: "x", shell: resolver.Remote, cache: &resolver.pathCache}
	return env, strings.ReplaceAll(t.TempDir(), `\`, "/")
}

func TestRemoteScriptsRoundTrip(t *testing.T) {
	env, dir := remoteForTest(t)
	ctx := context.Background()

	file := dir + "/conf/a b.txt"
	payload := []byte("line1\r\nline2 \"quoted\" $HOME `x`\n")
	if err := env.Write(ctx, file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := env.Read(ctx, file)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := env.Read(ctx, dir+"/nope"); err == nil {
		t.Fatal("missing file must fail")
	}
	many, err := env.ReadMany(ctx, []string{file, dir + "/nope", dir + "/conf"})
	if err != nil || len(many) != 1 || !bytes.Equal(many[file], payload) {
		t.Fatalf("%q %v", many, err)
	}
	if !env.IsDir(ctx, dir+"/conf") || env.IsDir(ctx, file) {
		t.Fatal("IsDir")
	}
	entries, err := env.ListDir(ctx, dir+"/conf")
	if err != nil || len(entries) != 1 || entries[0].Name != "a b.txt" || entries[0].Dir {
		t.Fatalf("%+v %v", entries, err)
	}

	for rel, text := range map[string]string{"skills/alpha/SKILL.md": "name: alpha\nx\n", "skills/alpha/ref/r.txt": "R", "skills/beta/SKILL.md": "name: beta\n"} {
		if err := env.Write(ctx, dir+"/"+rel, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found, err := env.Find(ctx, []string{dir + "/skills", dir + "/absent"}, "SKILL.md", 3, 1)
	if err != nil || len(found) != 2 {
		t.Fatalf("%+v %v", found, err)
	}
	for _, hit := range found {
		if !strings.HasPrefix(hit.Head, "name: ") || strings.Contains(hit.Head, "\nx") {
			t.Fatalf("head not limited to one line: %+v", hit)
		}
	}

	archive, err := env.PackDir(ctx, dir+"/skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Write(ctx, dir+"/dest/alpha/stale", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := env.UnpackDir(ctx, dir+"/dest", "alpha", archive); err != nil {
		t.Fatal(err)
	}
	if data, err := env.Read(ctx, dir+"/dest/alpha/ref/r.txt"); err != nil || string(data) != "R" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := env.Read(ctx, dir+"/dest/alpha/stale"); err == nil {
		t.Fatal("stale file survived the replace")
	}
	if left, _ := env.ListDir(ctx, dir+"/dest"); len(left) != 1 {
		t.Fatalf("staging left behind: %+v", left)
	}

	if err := env.Rename(ctx, dir+"/skills/beta", dir+"/skills/.archive/beta-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Read(ctx, dir+"/skills/.archive/beta-1/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	if err := env.RemoveAll(ctx, dir+"/skills/alpha"); err != nil {
		t.Fatal(err)
	}
	if env.IsDir(ctx, dir+"/skills/alpha") {
		t.Fatal("RemoveAll")
	}
	if err := env.Remove(ctx, file); err != nil {
		t.Fatal(err)
	}
	if err := env.Remove(ctx, file); err != nil {
		t.Fatalf("removing twice must not fail: %v", err)
	}
}

func TestRemoteUnpackRejectsDirectoryNamesWithSeparators(t *testing.T) {
	env, dir := remoteForTest(t)
	archive := archiveOf(t)
	for _, name := range []string{"", "..", "a/b"} {
		if err := env.UnpackDir(context.Background(), dir+"/dest", name, archive); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}
