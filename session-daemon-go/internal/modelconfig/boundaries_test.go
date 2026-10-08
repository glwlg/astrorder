package modelconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileBackupsRemainBounded(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fixture-one", "fixture-two", "fixture-three", "fixture-four", "fixture-five", "fixture-six"} {
		if _, err := s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{}, "api_key": key}); err != nil {
			t.Fatal(err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(root, RelEnvConfig) + ".astrorder-backup-*")
	if err != nil || len(backups) != 3 {
		t.Fatal("credential backups unbounded", len(backups), err)
	}
}

func TestApplyRejectsNonStringCredentialBeforeFileWrites(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{FileCodexConfig: "model=\"invalid-request\"\n"}, "api_key": []any{}}); err == nil {
		t.Fatal("non-string api_key silently ignored")
	}
	if _, err := os.Stat(filepath.Join(root, RelCodexConfig)); !os.IsNotExist(err) {
		t.Fatal("invalid request wrote managed file", err)
	}
}

func TestReloadDefersOnlyBusyAgent(t *testing.T) {
	var reloaded []string
	s, err := New(Config{AllowedRoots: []string{t.TempDir()}, BusyFor: func(agent string) bool { return agent == "grok" }, Reload: func(_ context.Context, agent string) error { reloaded = append(reloaded, agent); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Execute(context.Background(), "model_config.reload", map[string]any{"agents": []string{"grok", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || reloaded[0] != "codex" || result["deferred"] != true {
		t.Fatal("unrelated Agent activity blocked reload", result, reloaded)
	}
	pending := result["pending"].([]string)
	if len(pending) != 1 || pending[0] != "grok" {
		t.Fatal("pending agents mismatch", pending)
	}
}

func TestReloadRequiresNativeCallbacks(t *testing.T) {
	s, err := New(Config{AllowedRoots: []string{t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), "model_config.reload", map[string]any{"agents": []string{"codex"}}); err == nil {
		t.Fatal("reload without native callback reported success")
	}
}

func TestRollbackReportsFailureAndIgnoresPredictableSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.rollbackFile(&replacedFile{path: root, existed: true, current: []byte("original")}); err == nil {
		t.Fatal("rollback replacement error swallowed")
	}
	target := filepath.Join(root, "config.toml")
	victim := filepath.Join(outside, "victim")
	os.WriteFile(victim, []byte("untouched"), 0600)
	if err := os.Symlink(victim, filepath.Join(root, ".config.toml.astrorder-rollback")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if err := s.rollbackFile(&replacedFile{path: target, existed: true, current: []byte("original")}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "untouched" {
		t.Fatal("rollback staging escaped authorized root", err)
	}
}

func TestCancelledApplyCannotWriteManagedFiles(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Execute(ctx, "model_config.apply", map[string]any{"files": map[string]any{FileCodexConfig: "model=\"cancelled\"\n"}})
	if err == nil {
		t.Fatal("cancelled operation modified managed files")
	}
	if _, err = os.Stat(filepath.Join(root, RelCodexConfig)); !os.IsNotExist(err) {
		t.Fatal("cancelled operation wrote file", err)
	}
}

func TestStagingCannotFollowPredictableTemporarySymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim")
	os.WriteFile(victim, []byte("untouched"), 0600)
	if err := os.Symlink(victim, filepath.Join(dir, ".config.toml.astrorder-tmp")); err != nil {
		t.Skip("native symlinks unavailable", err)
	}
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{FileCodexConfig: "model=\"safe\"\n"}})
	data, readErr := os.ReadFile(victim)
	if readErr != nil || string(data) != "untouched" {
		t.Fatal("staging escaped the authorized root", err, readErr)
	}
}

func TestManagedCatalogParityIncludesMagpieAndBOM(t *testing.T) {
	root := t.TempDir()
	s, err := New(Config{AllowedRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, text string }{{"codex_magpie_catalog", "\ufeff{\"models\":[]}"}, {FileCodexConfig, "\ufeffmodel=\"bom-model\"\n"}, {FileCodexCatalog, "\ufeff{\"models\":[]}"}} {
		if _, err := s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{entry.name: entry.text}}); err != nil {
			t.Fatal("existing managed-file contract rejected", entry.name, err)
		}
	}
	result, err := s.Execute(context.Background(), "model_config.plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result["codex_magpie_catalog"]; !ok {
		t.Fatal("plan omitted managed Magpie catalog")
	}
}

func TestManagedTOMLUsesCompleteGrammarBeforeWriting(t *testing.T) {
	for _, text := range []string{"model = nonsense\n", "model=\"first\"\nmodel=\"second\"\n", "x = [1 2]\n"} {
		root := t.TempDir()
		s, err := New(Config{AllowedRoots: []string{root}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Execute(context.Background(), "model_config.apply", map[string]any{"files": map[string]any{FileCodexConfig: text}}); err == nil {
			t.Fatalf("accepted invalid TOML %q", text)
		}
		if _, err = os.Stat(filepath.Join(root, RelCodexConfig)); !os.IsNotExist(err) {
			t.Fatal("invalid content was written", err)
		}
	}
}
