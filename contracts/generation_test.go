package contracts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestGenerationIsRepeatableAndMatchesCheckedInOutputs(t *testing.T) {
	if os.Getenv("ASTRORDER_VERIFY_GENERATION") != "1" {
		t.Skip("set ASTRORDER_VERIFY_GENERATION=1 for pinned generator verification")
	}
	buf := os.Getenv("ASTRORDER_BUF")
	if buf == "" {
		buf = "buf"
	}
	goPlugin := os.Getenv("ASTRORDER_PROTOC_GEN_GO")
	if goPlugin == "" {
		goPlugin = "protoc-gen-go"
	}
	grpcPlugin := os.Getenv("ASTRORDER_PROTOC_GEN_GO_GRPC")
	if grpcPlugin == "" {
		grpcPlugin = "protoc-gen-go-grpc"
	}
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	directories := []string{"gen/go"}
	for _, output := range []string{first, second} {
		if err := os.MkdirAll(output, 0o755); err != nil {
			t.Fatal(err)
		}
		template := writeRuntimeBufTemplate(t, output, goPlugin, grpcPlugin)
		run(t, buf, "generate", "--template", template, "--output", output)
	}
	firstHashes := generatedHashes(t, first, directories...)
	secondHashes := generatedHashes(t, second, directories...)
	checkedIn := generatedHashes(t, ".", directories...)
	if strings.Join(firstHashes, "\n") != strings.Join(secondHashes, "\n") {
		t.Fatalf("repeat generation is not deterministic\nfirst: %v\nsecond: %v", firstHashes, secondHashes)
	}
	if strings.Join(firstHashes, "\n") != strings.Join(checkedIn, "\n") {
		t.Fatalf("checked-in generated sources are stale\ngenerated: %v\nchecked-in: %v", firstHashes, checkedIn)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	cmd.Env = toolEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}

func toolEnv() []string {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home = os.Getenv("HOME")
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"PATHEXT=" + os.Getenv("PATHEXT"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"WINDIR=" + os.Getenv("WINDIR"),
		"TEMP=" + os.Getenv("TEMP"),
		"TMP=" + os.Getenv("TMP"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"HOME=" + home,
		"USERPROFILE=" + os.Getenv("USERPROFILE"),
		"GOCACHE=" + os.Getenv("GOCACHE"),
		"GOMODCACHE=" + os.Getenv("GOMODCACHE"),
		"GOPATH=" + os.Getenv("GOPATH"),
	}
}

func writeRuntimeBufTemplate(t *testing.T, output, goPlugin, grpcPlugin string) string {
	t.Helper()
	type plugin struct {
		Local string   `json:"local"`
		Out   string   `json:"out"`
		Opt   []string `json:"opt"`
	}
	template := struct {
		Version string   `json:"version"`
		Plugins []plugin `json:"plugins"`
	}{
		Version: "v2",
		Plugins: []plugin{
			{Local: absoluteToolPath(t, goPlugin), Out: "gen/go", Opt: []string{"paths=source_relative"}},
			{Local: absoluteToolPath(t, grpcPlugin), Out: "gen/go", Opt: []string{"paths=source_relative"}},
		},
	}
	raw, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(output, "buf.gen.runtime.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func absoluteToolPath(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(absolute)
}

func generatedHashes(t *testing.T, root string, directories ...string) []string {
	t.Helper()
	var rows []string
	for _, directory := range directories {
		base := filepath.Join(root, filepath.FromSlash(directory))
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(raw)
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rows = append(rows, filepath.ToSlash(rel)+" "+hex.EncodeToString(sum[:]))
			return nil
		})
		if err != nil {
			t.Fatalf("hash %s on %s: %v", directory, runtime.GOOS, err)
		}
	}
	sort.Strings(rows)
	return rows
}
