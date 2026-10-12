package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

type preferenceFixture struct {
	value    store.Preference
	writes   []any
	writeErr error
}

func (f *preferenceFixture) GetPreference(context.Context, string) (store.Preference, error) {
	return f.value, nil
}

func (f *preferenceFixture) SetPreference(_ context.Context, key string, value any, overwrite bool) error {
	if key != OpenVikingConfigKey || !overwrite {
		panic("unexpected preference write")
	}
	f.writes = append(f.writes, value)
	return f.writeErr
}

func TestConfigReadsLegacyStringModernMapAndPrivateFallback(t *testing.T) {
	t.Parallel()
	privateHome := t.TempDir()
	fallbackPath := filepath.Join(privateHome, ".openviking", "ovcli.conf")
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallbackPath, []byte(`{"url":"http://fallback.test:1933","api_key":"fallback-secret","future":9007199254740993}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		preference store.Preference
		wantURL    string
		wantKey    string
	}{
		{"legacy JSON string", store.Preference{Exists: true, Value: `{"url":"http://legacy.test:1933","api_key":"abcdefghijkl"}`}, "http://legacy.test:1933", "abcdefghijkl"},
		{"modern map", store.Preference{Exists: true, Value: map[string]any{"url": "http://modern.test:1933", "api_key": "modern-key"}}, "http://modern.test:1933", "modern-key"},
		{"malformed preference falls back", store.Preference{Exists: true, Value: "{"}, "http://fallback.test:1933", "fallback-secret"},
		{"missing preference falls back", store.Preference{}, "http://fallback.test:1933", "fallback-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preferences := &preferenceFixture{value: tc.preference}
			service, err := NewConfigService(ConfigServiceOptions{Preferences: preferences, FallbackPath: fallbackPath})
			if err != nil {
				t.Fatal(err)
			}
			config, err := service.Active(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if config.URL != tc.wantURL || config.APIKey != tc.wantKey {
				t.Fatalf("config = %#v", config)
			}
			view, err := service.View(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if view.URL != tc.wantURL || view.MaskedKey != MaskAPIKey(tc.wantKey) {
				t.Fatalf("view = %#v", view)
			}
		})
	}
}

func TestConfigUpdatePresenceAndLegacyStorageShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		apiKey  *string
		wantKey string
	}{
		{"absent retains", nil, "existing-secret"},
		{"explicit empty clears", pointer(""), ""},
		{"present replaces", pointer(" replacement-secret "), " replacement-secret "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preferences := &preferenceFixture{value: store.Preference{Exists: true, Value: map[string]any{
				"url": "http://old.test", "api_key": "existing-secret", "unknown": "not-carried-by-legacy-contract",
			}}}
			service, err := NewConfigService(ConfigServiceOptions{Preferences: preferences, FallbackPath: filepath.Join(t.TempDir(), "missing.conf")})
			if err != nil {
				t.Fatal(err)
			}
			view, err := service.Update(context.Background(), ConfigUpdate{URL: "  http://next.test:1933/path/  ", APIKey: tc.apiKey})
			if err != nil {
				t.Fatal(err)
			}
			if view.URL != "http://next.test:1933/path/" || view.MaskedKey != MaskAPIKey(tc.wantKey) {
				t.Fatalf("view = %#v", view)
			}
			if len(preferences.writes) != 1 {
				t.Fatalf("writes = %#v", preferences.writes)
			}
			legacy, ok := preferences.writes[0].(string)
			if !ok {
				t.Fatalf("preference was not legacy JSON string: %T", preferences.writes[0])
			}
			var saved Config
			if err := json.Unmarshal([]byte(legacy), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.URL != view.URL || saved.APIKey != tc.wantKey {
				t.Fatalf("saved = %#v", saved)
			}
		})
	}
}

func TestConfigNullRetainsAndMaskedOutputNeverLeaksSecret(t *testing.T) {
	t.Parallel()
	preferences := &preferenceFixture{value: store.Preference{Exists: true, Value: `{"url":"http://old.test","api_key":"super-secret-value"}`}}
	service, err := NewConfigService(ConfigServiceOptions{Preferences: preferences, FallbackPath: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	var payload ConfigUpdate
	if err := json.Unmarshal([]byte(`{"url":"http://new.test","api_key":null}`), &payload); err != nil {
		t.Fatal(err)
	}
	view, err := service.Update(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	if stringsContain(string(raw), "super-secret-value") || view.MaskedKey != "supe...alue" {
		t.Fatalf("unsafe view = %s", raw)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	service, err := NewConfigService(ConfigServiceOptions{Preferences: &preferenceFixture{}, FallbackPath: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []ConfigUpdate{{URL: ""}, {URL: string(make([]byte, 513)), APIKey: nil}, {URL: "http://ok", APIKey: pointer(string(make([]byte, 513)))}} {
		if _, err := service.Update(context.Background(), update); err == nil {
			t.Errorf("accepted %#v", update)
		}
	}
}

func TestConfigPydanticCompatibleUnicodeLengths(t *testing.T) {
	t.Parallel()
	preferences := &preferenceFixture{}
	service, err := NewConfigService(ConfigServiceOptions{Preferences: preferences, FallbackPath: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	longUnicode := strings.Repeat("界", 512)
	if _, err := service.Update(context.Background(), ConfigUpdate{URL: longUnicode, APIKey: pointer(longUnicode)}); err != nil {
		t.Fatalf("rejected 512 code points: %v", err)
	}
	if _, err := service.Update(context.Background(), ConfigUpdate{URL: strings.Repeat("界", 513)}); err == nil {
		t.Fatal("accepted 513 code points")
	}
}

func pointer(value string) *string { return &value }
func stringsContain(value, needle string) bool {
	return len(needle) > 0 && len(value) >= len(needle) && func() bool {
		for index := 0; index+len(needle) <= len(value); index++ {
			if value[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}
