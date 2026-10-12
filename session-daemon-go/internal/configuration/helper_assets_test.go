package configuration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/session-daemon/internal/protocol"
)

func TestConfigurationBindsModelHelperAssets(t *testing.T) {
	home, assets := t.TempDir(), t.TempDir()
	data, err := json.Marshal(map[string]any{"model_config": map[string]any{"home": home, "helper_assets_dir": assets}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	d := protocol.New("private-config-fixture")
	defer d.Close()
	if err := cfg.Register(d); err != nil {
		t.Fatal(err)
	}
	result, err := d.ExecuteModelConfig(context.Background(), map[string]any{"action": "model_config.plan", "target": map[string]any{"kind": "local"}})
	if err != nil || result["_home"] != filepath.Clean(home) {
		t.Fatalf("model home not bound: %v", err)
	}
	invalid := Config{ModelConfig: &ModelConfig{Home: home, HelperAssetsDir: filepath.Join(assets, "missing")}}
	other := protocol.New("private-invalid-config-fixture")
	defer other.Close()
	if err := invalid.Register(other); err == nil {
		t.Fatal("missing configured helper assets accepted")
	}
}
