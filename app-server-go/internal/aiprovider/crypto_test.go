package aiprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

func readRawPrefString(t *testing.T, st *store.Store, key string) string {
	t.Helper()
	var raw string
	if err := st.DB().QueryRow(`SELECT value FROM workspace_preferences WHERE "key"=?`, key).Scan(&raw); err != nil {
		t.Fatalf("read raw preference %q: %v", key, err)
	}
	return raw
}

func TestSaveConfigEncryptsSecretsAtRestAndPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "providers.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	apiKey := "sk-live-secret-openai-9876543210"
	mgmtKey := "mp-mgmt-secret-admin-55556666"
	savedCfg, savedProv, err := UpsertProvider(ctx, st, ProviderPatch{
		Name:          "公司 Magpie",
		Protocol:      llmcompat.ProtocolOpenAI,
		Vendor:        VendorMagpie,
		BaseURL:       "https://magpie.example/v1",
		APIKey:        &apiKey,
		ManagementURL: "https://magpie.example",
		ManagementKey: &mgmtKey,
		Models:        []string{"gpt-4o"},
		SetAsDefault:  true,
		DefaultModel:  "gpt-4o",
	})
	if err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if savedProv.APIKey != apiKey || savedProv.ManagementKey != mgmtKey {
		t.Fatalf("in-memory provider should hold plaintext secrets: %+v", savedProv)
	}

	// Verify key file exists in data dir, has 32 bytes, and 0600 permissions on Unix.
	keyPath := filepath.Join(dir, masterKeyFileName)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat master key file: %v", err)
	}
	if info.Size() != masterKeySize {
		t.Fatalf("master key size = %d, want %d", info.Size(), masterKeySize)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %o, want 0600", info.Mode().Perm())
	}
	keyBeforeRestart, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read master key: %v", err)
	}

	// Verify raw SQLite services:ai_providers row never stores plaintext secrets.
	raw1 := readRawPrefString(t, st, PrefAIProviders)
	if strings.Contains(raw1, apiKey) || strings.Contains(raw1, mgmtKey) {
		t.Fatalf("plaintext secret leaked into SQLite %s: %s", PrefAIProviders, raw1)
	}
	var stored1 Config
	if err := json.Unmarshal([]byte(raw1), &stored1); err != nil {
		t.Fatalf("unmarshal stored config: %v", err)
	}
	if len(stored1.Providers) != 1 {
		t.Fatalf("stored providers = %d, want 1", len(stored1.Providers))
	}
	if !IsEncryptedSecret(stored1.Providers[0].APIKey) || !IsEncryptedSecret(stored1.Providers[0].ManagementKey) {
		t.Fatalf("expected enc:v1: prefix on stored secrets: api=%q mgmt=%q", stored1.Providers[0].APIKey, stored1.Providers[0].ManagementKey)
	}

	// Saving again uses fresh random GCM nonces (ciphertext changes).
	if _, err := SaveConfig(ctx, st, savedCfg); err != nil {
		t.Fatalf("second SaveConfig: %v", err)
	}
	raw2 := readRawPrefString(t, st, PrefAIProviders)
	var stored2 Config
	if err := json.Unmarshal([]byte(raw2), &stored2); err != nil {
		t.Fatalf("unmarshal stored config 2: %v", err)
	}
	if stored2.Providers[0].APIKey == stored1.Providers[0].APIKey {
		t.Fatalf("re-encryption reused identical ciphertext/nonce: %s", stored2.Providers[0].APIKey)
	}

	// Close and reopen store to simulate process restart.
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	keyAfterRestart, err := os.ReadFile(keyPath)
	if err != nil || string(keyAfterRestart) != string(keyBeforeRestart) {
		t.Fatalf("master key changed across restart: err=%v", err)
	}

	reloaded, err := LoadConfig(ctx, st2)
	if err != nil {
		t.Fatalf("LoadConfig after restart: %v", err)
	}
	if len(reloaded.Providers) != 1 || reloaded.Providers[0].APIKey != apiKey || reloaded.Providers[0].ManagementKey != mgmtKey {
		t.Fatalf("decrypted secrets mismatch after restart: %+v", reloaded.Providers)
	}

	resolved := ResolveDefault(ctx, st2)
	if resolved.APIKey != apiKey || !resolved.IsConfigured() {
		t.Fatalf("ResolveDefault after restart failed: %+v", resolved)
	}

	pub := reloaded.ToPublic()
	if !pub.Providers[0].APIKeySet || pub.Providers[0].MaskedKey != MaskSecret(apiKey) || !pub.Providers[0].ManagementKeySet || pub.Providers[0].MaskedManagementKey != MaskSecret(mgmtKey) {
		t.Fatalf("ToPublic masking broken after restart: %+v", pub.Providers[0])
	}
}

func TestLegacyPlaintextCompatReadAndLazyMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	legacyAPIKey := "sk-legacy-plaintext-key-11223344"
	legacyMgmtKey := "k-legacy-plaintext-mgmt-55667788"
	legacyCfg := Config{
		DefaultProviderID: "prv_legacy",
		DefaultModel:      "deepseek-chat",
		DefaultReasoning:  "medium",
		Providers: []Provider{
			{
				ID:            "prv_legacy",
				Name:          "Legacy Provider",
				Protocol:      llmcompat.ProtocolOpenAI,
				Vendor:        VendorMagpie,
				BaseURL:       "https://legacy.example/v1",
				APIKey:        legacyAPIKey,
				ManagementURL: "https://legacy.example",
				ManagementKey: legacyMgmtKey,
				Models:        []string{"deepseek-chat"},
				Enabled:       true,
			},
			{
				ID:        "prv_agent",
				Name:      "Codex Agent",
				Protocol:  llmcompat.ProtocolAgent,
				AgentID:   "codex-local",
				AgentKind: "codex",
				Models:    []string{"gpt-5.4"},
				Enabled:   true,
			},
		},
	}
	// Persist directly in plaintext simulating pre-encryption local DB (no key file yet).
	if err := st.SetPreference(ctx, PrefAIProviders, legacyCfg, true); err != nil {
		t.Fatalf("seed legacy plaintext config: %v", err)
	}

	keyPath := filepath.Join(dir, masterKeyFileName)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file should not exist before migration, err=%v", err)
	}

	// First LoadConfig must transparently read plaintext and lazily migrate to enc:v1:.
	loaded, err := LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("LoadConfig on legacy plaintext DB failed: %v", err)
	}
	if len(loaded.Providers) != 2 {
		t.Fatalf("loaded providers = %d, want 2", len(loaded.Providers))
	}
	if loaded.Providers[0].APIKey != legacyAPIKey || loaded.Providers[0].ManagementKey != legacyMgmtKey {
		t.Fatalf("legacy plaintext secrets not preserved: %+v", loaded.Providers[0])
	}
	if loaded.Providers[1].Protocol != llmcompat.ProtocolAgent || loaded.Providers[1].APIKey != "" {
		t.Fatalf("agent provider altered during migration: %+v", loaded.Providers[1])
	}

	// SQLite row must now be encrypted in-place with zero plaintext remaining.
	rawAfter := readRawPrefString(t, st, PrefAIProviders)
	if strings.Contains(rawAfter, legacyAPIKey) || strings.Contains(rawAfter, legacyMgmtKey) {
		t.Fatalf("lazy migration left plaintext secret in SQLite: %s", rawAfter)
	}
	var stored Config
	if err := json.Unmarshal([]byte(rawAfter), &stored); err != nil {
		t.Fatalf("unmarshal migrated config: %v", err)
	}
	if !IsEncryptedSecret(stored.Providers[0].APIKey) || !IsEncryptedSecret(stored.Providers[0].ManagementKey) {
		t.Fatalf("expected enc:v1: after lazy migration: %+v", stored.Providers[0])
	}
	if stored.Providers[1].APIKey != "" || stored.Providers[1].ManagementKey != "" {
		t.Fatalf("empty agent secrets should remain empty in storage: %+v", stored.Providers[1])
	}

	// Explicit MigratePlaintextSecrets is now a no-op (idempotent).
	migrated, err := MigratePlaintextSecrets(ctx, st)
	if err != nil || migrated {
		t.Fatalf("expected idempotent MigratePlaintextSecrets=(false,nil), got (%v, %v)", migrated, err)
	}

	// Subsequent LoadConfig reads the encrypted row cleanly.
	loaded2, err := LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("second LoadConfig failed: %v", err)
	}
	if loaded2.Providers[0].APIKey != legacyAPIKey || loaded2.Providers[0].ManagementKey != legacyMgmtKey {
		t.Fatalf("secrets mismatch on second load: %+v", loaded2.Providers[0])
	}
}

func TestCorruptedCiphertextAndKeyErrorsFailLoudly(t *testing.T) {
	ctx := context.Background()

	makeStoreWithValidCipher := func(t *testing.T) (*store.Store, string, string) {
		t.Helper()
		dir := t.TempDir()
		st, err := store.Open(filepath.Join(dir, "corrupt.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		key := "sk-valid-secret-1234567890"
		if _, _, err := UpsertProvider(ctx, st, ProviderPatch{
			Name:     "OpenAI",
			Protocol: llmcompat.ProtocolOpenAI,
			BaseURL:  "https://api.openai.com/v1",
			APIKey:   &key,
		}); err != nil {
			t.Fatalf("seed provider: %v", err)
		}
		raw := readRawPrefString(t, st, PrefAIProviders)
		var stored Config
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return st, dir, stored.Providers[0].APIKey
	}

	cases := []struct {
		name      string
		mutate    func(t *testing.T, st *store.Store, dir, validCipher string)
		targetErr error
	}{
		{
			name: "empty payload after prefix",
			mutate: func(t *testing.T, st *store.Store, _ string, _ string) {
				_ = st.SetPreference(ctx, PrefAIProviders, Config{
					Providers: []Provider{{ID: "p1", Protocol: "openai", BaseURL: "https://x.test", APIKey: SecretCipherPrefix, Enabled: true}},
				}, true)
			},
			targetErr: ErrInvalidCiphertext,
		},
		{
			name: "invalid base64 payload",
			mutate: func(t *testing.T, st *store.Store, _ string, _ string) {
				_ = st.SetPreference(ctx, PrefAIProviders, Config{
					Providers: []Provider{{ID: "p1", Protocol: "openai", BaseURL: "https://x.test", APIKey: SecretCipherPrefix + "!!!not-base64!!!", Enabled: true}},
				}, true)
			},
			targetErr: ErrInvalidCiphertext,
		},
		{
			name: "truncated ciphertext",
			mutate: func(t *testing.T, st *store.Store, _ string, _ string) {
				short := SecretCipherPrefix + base64.StdEncoding.EncodeToString([]byte("short"))
				_ = st.SetPreference(ctx, PrefAIProviders, Config{
					Providers: []Provider{{ID: "p1", Protocol: "openai", BaseURL: "https://x.test", APIKey: short, Enabled: true}},
				}, true)
			},
			targetErr: ErrInvalidCiphertext,
		},
		{
			name: "tampered ciphertext auth tag",
			mutate: func(t *testing.T, st *store.Store, _ string, validCipher string) {
				blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(validCipher, SecretCipherPrefix))
				if err != nil {
					t.Fatal(err)
				}
				blob[len(blob)-1] ^= 0xFF
				tampered := SecretCipherPrefix + base64.StdEncoding.EncodeToString(blob)
				_ = st.SetPreference(ctx, PrefAIProviders, Config{
					Providers: []Provider{{ID: "p1", Protocol: "openai", BaseURL: "https://x.test", APIKey: tampered, Enabled: true}},
				}, true)
			},
			targetErr: ErrDecryptSecret,
		},
		{
			name: "corrupted management key",
			mutate: func(t *testing.T, st *store.Store, _ string, validCipher string) {
				_ = st.SetPreference(ctx, PrefAIProviders, Config{
					Providers: []Provider{{
						ID: "p1", Protocol: "openai", Vendor: VendorMagpie,
						BaseURL: "https://x.test", APIKey: validCipher,
						ManagementKey: SecretCipherPrefix + "%%%corrupt%%%", Enabled: true,
					}},
				}, true)
			},
			targetErr: ErrInvalidCiphertext,
		},
		{
			name: "missing master key file",
			mutate: func(t *testing.T, _ *store.Store, dir string, _ string) {
				if err := os.Remove(filepath.Join(dir, masterKeyFileName)); err != nil {
					t.Fatal(err)
				}
			},
			targetErr: ErrDecryptSecret,
		},
		{
			name: "wrong master key",
			mutate: func(t *testing.T, _ *store.Store, dir string, _ string) {
				wrong := make([]byte, masterKeySize)
				for i := range wrong {
					wrong[i] = byte(i + 1)
				}
				if err := os.WriteFile(filepath.Join(dir, masterKeyFileName), wrong, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			targetErr: ErrDecryptSecret,
		},
		{
			name: "invalid master key length",
			mutate: func(t *testing.T, _ *store.Store, dir string, _ string) {
				if err := os.WriteFile(filepath.Join(dir, masterKeyFileName), []byte("too-short"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			targetErr: ErrDecryptSecret,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dir, validCipher := makeStoreWithValidCipher(t)
			tc.mutate(t, st, dir, validCipher)
			before := readRawPrefString(t, st, PrefAIProviders)

			_, err := LoadConfig(ctx, st)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !errors.Is(err, tc.targetErr) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.targetErr)
			}

			// Corrupt row must not be wiped or silently overwritten.
			after := readRawPrefString(t, st, PrefAIProviders)
			if after != before {
				t.Fatalf("LoadConfig mutated corrupted config row")
			}

			// Missing key file must NOT be auto-created on decrypt failure.
			if tc.name == "missing master key file" {
				if _, statErr := os.Stat(filepath.Join(dir, masterKeyFileName)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("decrypt failure unexpectedly recreated key file: %v", statErr)
				}
			}
		})
	}
}

func TestSeedAndSyncPathsNeverStorePlaintextInAIProviders(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	llmSecret := "sk-seed-llm-secret-abcdef123456"
	gwSecret := "mp-seed-gw-secret-9876543210"
	gwMgmt := "mp-seed-mgmt-secret-00112233"

	if err := st.SetPreference(ctx, prefLegacyLLM, map[string]any{
		"base_url": "https://api.deepseek.com/v1",
		"api_key":  llmSecret,
		"model":    "deepseek-chat",
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{
		"gateway_type":   "magpie",
		"management_url": "https://magpie.corp",
		"inference_url":  "https://magpie.corp/v1",
		"api_key":        gwSecret,
		"management_key": gwMgmt,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Seed via LoadConfig
	cfg, err := LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("LoadConfig seed: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 seeded providers, got %d", len(cfg.Providers))
	}
	raw := readRawPrefString(t, st, PrefAIProviders)
	for _, secret := range []string{llmSecret, gwSecret, gwMgmt} {
		if strings.Contains(raw, secret) {
			t.Fatalf("seeded services:ai_providers leaked plaintext secret %q: %s", secret, raw)
		}
	}

	// 2. SyncFromModelGateway must also persist encrypted secrets
	rotatedGWKey := "mp-rotated-key-4455667788"
	rotatedMgmtKey := "mp-rotated-mgmt-8877665544"
	if err := SyncFromModelGateway(ctx, st, modelgateway.GatewayConfig{
		GatewayType:   "magpie",
		InferenceURL:  "https://magpie.corp/v1",
		ManagementURL: "https://magpie.corp",
		APIKey:        rotatedGWKey,
		ManagementKey: rotatedMgmtKey,
	}); err != nil {
		t.Fatalf("SyncFromModelGateway: %v", err)
	}
	raw = readRawPrefString(t, st, PrefAIProviders)
	if strings.Contains(raw, rotatedGWKey) || strings.Contains(raw, rotatedMgmtKey) {
		t.Fatalf("SyncFromModelGateway wrote plaintext secrets: %s", raw)
	}

	// 3. SyncFromLegacyLLM must also persist encrypted secrets
	rotatedLLMKey := "sk-rotated-llm-9988776655"
	if err := SyncFromLegacyLLM(ctx, st, map[string]string{
		"base_url":  "https://api.deepseek.com/v1",
		"api_key":   rotatedLLMKey,
		"model":     "deepseek-reasoner",
		"reasoning": "high",
	}); err != nil {
		t.Fatalf("SyncFromLegacyLLM: %v", err)
	}
	raw = readRawPrefString(t, st, PrefAIProviders)
	if strings.Contains(raw, rotatedLLMKey) {
		t.Fatalf("SyncFromLegacyLLM wrote plaintext secret: %s", raw)
	}

	// Verify everything decrypts back accurately
	finalCfg, err := LoadConfig(ctx, st)
	if err != nil {
		t.Fatalf("final LoadConfig: %v", err)
	}
	foundLLM, foundGW := false, false
	for _, p := range finalCfg.Providers {
		if p.APIKey == rotatedLLMKey {
			foundLLM = true
		}
		if p.Vendor == VendorMagpie && p.APIKey == rotatedGWKey && p.ManagementKey == rotatedMgmtKey {
			foundGW = true
		}
	}
	if !foundLLM || !foundGW {
		t.Fatalf("rotated secrets did not round-trip: %+v", finalCfg.Providers)
	}
}

func TestConcurrentLazyMigrationAndEncryptDecryptHelpers(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	// Direct EncryptSecret / DecryptSecret round-trip and legacy plaintext fallback
	enc, err := EncryptSecret(st, "sk-direct-helper-123456")
	if err != nil || !IsEncryptedSecret(enc) || strings.Contains(enc, "sk-direct-helper-123456") {
		t.Fatalf("EncryptSecret failed: enc=%q err=%v", enc, err)
	}
	dec, wasEnc, err := DecryptSecret(st, enc)
	if err != nil || !wasEnc || dec != "sk-direct-helper-123456" {
		t.Fatalf("DecryptSecret(enc) = (%q, %v, %v)", dec, wasEnc, err)
	}
	legacyDec, wasEnc, err := DecryptSecret(st, "sk-plain-legacy")
	if err != nil || wasEnc || legacyDec != "sk-plain-legacy" {
		t.Fatalf("DecryptSecret(plain) = (%q, %v, %v)", legacyDec, wasEnc, err)
	}

	// Concurrent lazy migration on a legacy plaintext config
	st2 := openTestStore(t)
	secret := "sk-concurrent-secret-abcdef1234"
	if err := st2.SetPreference(ctx, PrefAIProviders, Config{
		DefaultProviderID: "p1",
		DefaultModel:      "gpt-4o",
		Providers: []Provider{{
			ID:       "p1",
			Name:     "OpenAI",
			Protocol: llmcompat.ProtocolOpenAI,
			BaseURL:  "https://api.openai.com/v1",
			APIKey:   secret,
			Models:   []string{"gpt-4o"},
			Enabled:  true,
		}},
	}, true); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg, err := LoadConfig(ctx, st2)
			if err != nil {
				errs <- err
				return
			}
			if len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != secret {
				errs <- errors.New("unexpected decrypted api key")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent LoadConfig failed: %v", err)
	}

	raw := readRawPrefString(t, st2, PrefAIProviders)
	if strings.Contains(raw, secret) {
		t.Fatalf("concurrent lazy migration left plaintext key in SQLite: %s", raw)
	}
}
