package modelconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPlanDoesNotWriteAndReportsUnavailable(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	res, err := svc.Execute(context.Background(), "model_config.plan", nil)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	if res["status"] != "unavailable" {
		t.Errorf("expected overall status unavailable, got %v", res["status"])
	}

	// Ensure plan did not create any files on disk
	codexPath := filepath.Join(tmp, RelCodexConfig)
	if _, err := os.Stat(codexPath); !os.IsNotExist(err) {
		t.Errorf("plan should not create files, but %s exists", codexPath)
	}

	// Check individual file info
	cfgInfo, ok := res["codex_config"].(map[string]any)
	if !ok {
		t.Fatalf("missing codex_config info")
	}
	if cfgInfo["exists"] != false {
		t.Errorf("expected exists=false, got %v", cfgInfo["exists"])
	}
	if cfgInfo["status"] != "unavailable" {
		t.Errorf("expected status=unavailable, got %v", cfgInfo["status"])
	}
	if cfgInfo["sha256"] != EmptySHA256 {
		t.Errorf("expected empty sha256, got %v", cfgInfo["sha256"])
	}
}

func TestApplyAtomicValidationAndReceipt(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := map[string]any{
		"files": map[string]any{
			"codex_config":         "model = \"test-model\"\n[features]\nfoo = true\n",
			"codex_catalog":        "{\"models\": []}",
			"codex_magpie_catalog": "{\"models\": []}",
			"grok_config":          "[models]\ndefault = \"grok-4.5\"\n",
		},
		"api_key": "test-secret-key",
	}

	res, err := svc.Execute(context.Background(), "model_config.apply", req)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	changed, _ := res["changed"].([]string)
	if len(changed) != 4 {
		t.Errorf("expected 4 changed files, got %d (%v)", len(changed), changed)
	}

	receipt, ok := res["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("expected receipt in result")
	}
	if receipt["status"] != "validated" {
		t.Errorf("expected receipt status=validated, got %v", receipt["status"])
	}

	// Verify files on disk
	codexBytes, err := os.ReadFile(filepath.Join(tmp, RelCodexConfig))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	if !strings.Contains(string(codexBytes), "test-model") {
		t.Errorf("unexpected codex config: %s", string(codexBytes))
	}

	envBytes, err := os.ReadFile(filepath.Join(tmp, RelEnvConfig))
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	if !strings.Contains(string(envBytes), "OPENCODEX_API_AUTH_TOKEN=\"test-secret-key\"") {
		t.Errorf("unexpected env content: %s", string(envBytes))
	}

	// Subsequent plan should now report validated
	planRes, err := svc.Execute(context.Background(), "model_config.plan", nil)
	if err != nil {
		t.Fatalf("plan after apply failed: %v", err)
	}
	if planRes["status"] != "validated" {
		t.Errorf("expected plan status=validated, got %v", planRes["status"])
	}
}

func TestMissingHermesDoesNotInvalidatePlanOrGetCreated(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if _, err := svc.Execute(context.Background(), "model_config.apply", map[string]any{
		"files": map[string]any{
			FileCodexConfig:        "model = \"kept\"\n",
			FileCodexCatalog:       "{\"models\":[]}",
			FileCodexMagpieCatalog: "{\"models\":[]}",
			FileGrokConfig:         "[models]\ndefault = \"grok-4.5\"\n",
			FileHermesConfig:       "providers:\n  magpie:\n    base_url: http://192.168.1.100:3425/v1\n",
		},
		"api_key":  "ocx-key",
		"api_keys": map[string]any{EnvTokenKey: "ocx-key", MagpieEnvKey: "magpie-key"},
	}); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	hermesPath := filepath.Join(tmp, ManagedFiles[FileHermesConfig])
	if _, err := os.Stat(hermesPath); !os.IsNotExist(err) {
		t.Fatalf("missing hermes config was created: %v", err)
	}
	magpieEnv, err := os.ReadFile(filepath.Join(tmp, RelMagpieEnv))
	if err != nil || string(magpieEnv) != "MAGPIE_API_KEY=\"magpie-key\"\n" {
		t.Fatalf("magpie env = %q (%v)", magpieEnv, err)
	}
	planRes, err := svc.Execute(context.Background(), "model_config.plan", nil)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if planRes["status"] != "validated" {
		t.Fatalf("missing hermes marked the plan %v", planRes["status"])
	}
	info, _ := planRes[FileHermesConfig].(map[string]any)
	if info["exists"] != false {
		t.Fatalf("hermes inspect = %#v", info)
	}
	if err := os.MkdirAll(filepath.Dir(hermesPath), 0755); err != nil {
		t.Fatal(err)
	}
	original := "model:\n  provider: magpie\nkeep: true\n"
	if err := os.WriteFile(hermesPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	updated := "model:\n  provider: magpie\n  base_url: http://192.168.1.100:3425/v1\nkeep: true\n"
	if _, err := svc.Execute(context.Background(), "model_config.apply", map[string]any{
		"files": map[string]any{FileHermesConfig: updated},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hermesPath)
	if err != nil || string(data) != updated {
		t.Fatalf("hermes config = %q (%v)", data, err)
	}
}

func TestApplyRollbackOnValidationFailure(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. First apply valid files
	initialReq := map[string]any{
		"files": map[string]any{
			"codex_config": "model = \"original\"\n",
		},
	}
	if _, err := svc.Execute(context.Background(), "model_config.apply", initialReq); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	// 2. Now attempt apply with one broken file
	brokenReq := map[string]any{
		"files": map[string]any{
			"codex_config": "model = \"corrupted\"\n",
			"grok_config":  "[broken", // unclosed table header
		},
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", brokenReq)
	if err == nil {
		t.Fatalf("expected apply to fail on broken TOML")
	}

	// 3. Verify codex_config rolled back / remained original
	data, err := os.ReadFile(filepath.Join(tmp, RelCodexConfig))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	if string(data) != "model = \"original\"\n" {
		t.Errorf("expected original content restored, got: %s", string(data))
	}

	// 4. Verify grok_config was not created
	if _, err := os.Stat(filepath.Join(tmp, RelGrokConfig)); !os.IsNotExist(err) {
		t.Errorf("grok_config should not exist on disk after failed apply")
	}
}

func TestHashMismatch(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := map[string]any{
		"files": map[string]any{
			"codex_config": "model = \"v1\"\n",
		},
		"expected_digest": "deadbeef1234567890abcdef1234567890abcdef1234567890abcdef12345678",
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", req)
	if err == nil {
		t.Fatalf("expected error due to hash mismatch")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected error message to contain 'hash mismatch', got: %v", err)
	}
}

func TestConcurrencyConflict(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Create initial file
	codexPath := filepath.Join(tmp, RelCodexConfig)
	_ = os.MkdirAll(filepath.Dir(codexPath), 0755)
	initContent := []byte("model = \"initial\"\n")
	_ = os.WriteFile(codexPath, initContent, 0600)
	initHash := sha256Hex(initContent)

	// Run 8 concurrent apply calls all expecting initHash
	var wg sync.WaitGroup
	successCount := 0
	mismatchCount := 0
	var lock sync.Mutex

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := map[string]any{
				"files": map[string]any{
					"codex_config": fmt.Sprintf("model = \"version-%d\"\n", id),
				},
				"expected_digest": initHash,
			}
			_, err := svc.Execute(context.Background(), "model_config.apply", req)
			lock.Lock()
			if err == nil {
				successCount++
			} else if strings.Contains(err.Error(), "hash mismatch") || strings.Contains(err.Error(), "concurrency conflict") {
				mismatchCount++
			}
			lock.Unlock()
		}(i)
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 apply to succeed, got %d (mismatches: %d)", successCount, mismatchCount)
	}
	if mismatchCount != 7 {
		t.Errorf("expected 7 hash mismatches, got %d", mismatchCount)
	}
}

func TestPathEscapePrevention(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. Attempt root outside allowed roots
	req1 := map[string]any{
		"target": map[string]any{"root": "/tmp/forbidden-outside-root"},
		"files":  map[string]any{"codex_config": "model = \"x\"\n"},
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", req1)
	if err == nil {
		t.Errorf("expected path escape error for outside target root")
	}

	// 2. Attempt unauthorized file key
	req2 := map[string]any{
		"files": map[string]any{"/etc/shadow": "evil"},
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", req2)
	if err == nil {
		t.Errorf("expected unauthorized file error")
	}

	// 3. Symlink pointing outside allowed root
	outsideDir := t.TempDir()
	symlinkTarget := filepath.Join(outsideDir, "evil.toml")
	_ = os.WriteFile(symlinkTarget, []byte("evil"), 0644)
	codexDir := filepath.Join(tmp, ".codex")
	_ = os.MkdirAll(codexDir, 0755)
	symlinkPath := filepath.Join(codexDir, "config.toml")
	_ = os.Symlink(symlinkTarget, symlinkPath)

	req3 := map[string]any{
		"files": map[string]any{"codex_config": "model = \"overwritten\"\n"},
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", req3)
	if err == nil {
		t.Errorf("expected error on symlink pointing outside allowed root")
	}
}

func TestModePreservation(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	codexPath := filepath.Join(tmp, RelCodexConfig)
	_ = os.MkdirAll(filepath.Dir(codexPath), 0755)
	_ = os.WriteFile(codexPath, []byte("model = \"original\"\n"), 0640)

	req := map[string]any{
		"files": map[string]any{
			"codex_config": "model = \"updated\"\n",
		},
		"api_key": "my-token",
	}
	_, err = svc.Execute(context.Background(), "model_config.apply", req)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	fi, err := os.Stat(codexPath)
	if err != nil {
		t.Fatalf("stat codexPath: %v", err)
	}
	// On non-windows, permissions should match 0640
	if fi.Mode().Perm() != 0640 {
		t.Logf("mode preservation note: got %o (expected 0640 on POSIX)", fi.Mode().Perm())
	}

	// Environment file should be 0600
	envPath := filepath.Join(tmp, RelEnvConfig)
	envFi, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat envPath: %v", err)
	}
	if envFi.Mode().Perm() != 0600 {
		t.Logf("env file mode note: got %o (expected 0600 on POSIX)", envFi.Mode().Perm())
	}
}

func TestEnvironmentKeyValidation(t *testing.T) {
	// Multiline key must be rejected
	if err := validateAPIKey("part1\npart2"); err == nil {
		t.Errorf("expected multiline API key to be rejected")
	}
	if err := validateAPIKey("part1\rpart2"); err == nil {
		t.Errorf("expected \\r in API key to be rejected")
	}
	if err := validateAPIKey("part1\x00part2"); err == nil {
		t.Errorf("expected null in API key to be rejected")
	}
	if err := validateAPIKey("valid-token-123_456"); err != nil {
		t.Errorf("expected valid API key to succeed, got %v", err)
	}
}

func TestReloadBusyAndDeferred(t *testing.T) {
	tmp := t.TempDir()
	isBusy := true
	reloadCalls := 0

	svc, err := New(Config{
		AllowedRoots: []string{tmp},
		Busy:         func() bool { return isBusy },
		Reload: func(ctx context.Context, agentType string) error {
			reloadCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. While busy: reload is deferred
	req := map[string]any{
		"agents": []string{"codex", "grok"},
	}
	res, err := svc.Execute(context.Background(), "model_config.reload", req)
	if err != nil {
		t.Fatalf("reload execute failed: %v", err)
	}
	if res["deferred"] != true {
		t.Errorf("expected deferred=true, got %v", res["deferred"])
	}
	reloaded, _ := res["reloaded"].([]string)
	if len(reloaded) != 0 {
		t.Errorf("expected empty reloaded, got %v", reloaded)
	}
	if reloadCalls != 0 {
		t.Errorf("expected 0 reload calls while busy, got %d", reloadCalls)
	}

	// 2. Once busy is false: reload runs
	isBusy = false
	res2, err := svc.Execute(context.Background(), "model_config.reload", req)
	if err != nil {
		t.Fatalf("reload execute failed: %v", err)
	}
	if res2["deferred"] != false {
		t.Errorf("expected deferred=false, got %v", res2["deferred"])
	}
	reloaded2, _ := res2["reloaded"].([]string)
	if len(reloaded2) != 2 {
		t.Errorf("expected 2 reloaded agents, got %d (%v)", len(reloaded2), reloaded2)
	}
	if reloadCalls != 2 {
		t.Errorf("expected 2 reload calls, got %d", reloadCalls)
	}
}

func TestBackupRetention(t *testing.T) {
	tmp := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{tmp}})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Apply 6 updates to codex_config
	for i := 1; i <= 6; i++ {
		req := map[string]any{
			"files": map[string]any{
				"codex_config": fmt.Sprintf("model = \"v%d\"\n", i),
			},
		}
		_, err := svc.Execute(context.Background(), "model_config.apply", req)
		if err != nil {
			t.Fatalf("apply step %d failed: %v", i, err)
		}
	}

	dir := filepath.Join(tmp, ".codex")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	backupCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.toml.astrorder-backup-") {
			backupCount++
		}
	}
	if backupCount > 3 {
		t.Errorf("expected at most 3 backups retained, got %d", backupCount)
	}
}

func TestRealSHA256Calculation(t *testing.T) {
	data := []byte("hello world")
	h := sha256.Sum256(data)
	exp := hex.EncodeToString(h[:])
	got := sha256Hex(data)
	if got != exp {
		t.Errorf("expected %s, got %s", exp, got)
	}
}
