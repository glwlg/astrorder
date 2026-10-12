package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/store"
)

const OpenVikingConfigKey = "memory:openviking:config"

// PreferenceStore is deliberately narrow; *store.Store satisfies it directly.
type PreferenceStore interface {
	GetPreference(context.Context, string) (store.Preference, error)
	SetPreference(context.Context, string, any, bool) error
}

type Config struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}

type ConfigView struct {
	URL       string `json:"url"`
	MaskedKey string `json:"masked_key"`
}

type ConfigUpdate struct {
	URL    string  `json:"url"`
	APIKey *string `json:"-"`
}

func (update *ConfigUpdate) UnmarshalJSON(raw []byte) error {
	var payload map[string]json.RawMessage
	if err := decodeSingleJSON(raw, &payload); err != nil {
		return err
	}
	for key := range payload {
		if key != "url" && key != "api_key" {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	urlRaw, ok := payload["url"]
	if !ok || json.Unmarshal(urlRaw, &update.URL) != nil {
		return errors.New("url must be a string")
	}
	keyRaw, ok := payload["api_key"]
	if !ok || bytes.Equal(bytes.TrimSpace(keyRaw), []byte("null")) {
		update.APIKey = nil
		return nil
	}
	var key string
	if err := json.Unmarshal(keyRaw, &key); err != nil {
		return errors.New("api_key must be a string or null")
	}
	update.APIKey = &key
	return nil
}

type ConfigServiceOptions struct {
	Preferences  PreferenceStore
	FallbackPath string
}

type ConfigService struct {
	preferences  PreferenceStore
	fallbackPath string
}

func NewConfigService(options ConfigServiceOptions) (*ConfigService, error) {
	if options.Preferences == nil {
		return nil, errors.New("memory preference store is required")
	}
	if strings.TrimSpace(options.FallbackPath) == "" {
		return nil, errors.New("OpenViking fallback path is required")
	}
	return &ConfigService{preferences: options.Preferences, fallbackPath: options.FallbackPath}, nil
}

func (s *ConfigService) Active(ctx context.Context) (Config, error) {
	preference, err := s.preferences.GetPreference(ctx, OpenVikingConfigKey)
	if err != nil {
		return Config{}, fmt.Errorf("read OpenViking preference: %w", err)
	}
	if preference.Exists && preference.Value != nil {
		if config, decodeErr := decodeStoredConfig(preference.Value); decodeErr == nil {
			return withConfigDefaults(config), nil
		}
	}
	if raw, readErr := os.ReadFile(s.fallbackPath); readErr == nil {
		var config Config
		if decodeErr := decodeSingleJSON(raw, &config); decodeErr == nil {
			return withConfigDefaults(config), nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read OpenViking fallback: %w", readErr)
	}
	return Config{URL: DefaultOpenVikingURL}, nil
}

func (s *ConfigService) View(ctx context.Context) (ConfigView, error) {
	config, err := s.Active(ctx)
	if err != nil {
		return ConfigView{}, err
	}
	return ConfigView{URL: config.URL, MaskedKey: MaskAPIKey(config.APIKey)}, nil
}

func (s *ConfigService) Update(ctx context.Context, update ConfigUpdate) (ConfigView, error) {
	urlLength := utf8.RuneCountInString(update.URL)
	if !utf8.ValidString(update.URL) || urlLength < 1 || urlLength > 512 {
		return ConfigView{}, errors.New("OpenViking URL must contain 1-512 characters")
	}
	trimmedURL := strings.TrimSpace(update.URL)
	if update.APIKey != nil && (!utf8.ValidString(*update.APIKey) || utf8.RuneCountInString(*update.APIKey) > 512) {
		return ConfigView{}, errors.New("OpenViking API key must not exceed 512 characters")
	}
	current, err := s.Active(ctx)
	if err != nil {
		return ConfigView{}, err
	}
	key := current.APIKey
	if update.APIKey != nil {
		key = *update.APIKey
	}
	saved := Config{URL: trimmedURL, APIKey: key}
	raw, err := json.Marshal(saved)
	if err != nil {
		return ConfigView{}, errors.New("encode OpenViking configuration")
	}
	// The legacy row contains a JSON string in the table's JSON column. Keep that exact shape.
	if err := s.preferences.SetPreference(ctx, OpenVikingConfigKey, string(raw), true); err != nil {
		return ConfigView{}, fmt.Errorf("write OpenViking preference: %w", err)
	}
	return ConfigView{URL: saved.URL, MaskedKey: MaskAPIKey(saved.APIKey)}, nil
}

func decodeStoredConfig(value any) (Config, error) {
	var raw []byte
	switch typed := value.(type) {
	case string:
		raw = []byte(typed)
	case map[string]any:
		var err error
		raw, err = json.Marshal(typed)
		if err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("unsupported OpenViking preference type %T", value)
	}
	var config Config
	if err := decodeSingleJSON(raw, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func decodeSingleJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func withConfigDefaults(config Config) Config {
	if strings.TrimSpace(config.URL) == "" {
		config.URL = DefaultOpenVikingURL
	}
	return config
}

func MaskAPIKey(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 10 {
		return "********"
	}
	runes := []rune(value)
	if len(runes) <= 10 {
		return "********"
	}
	return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
}
