package modelconfig

import (
	"context"
	"fmt"
	"os"
)

// ExecuteHelper executes model configuration logic locally for the helper process.
// It is invoked by cmd/astrorder-model-helper.
func ExecuteHelper(payload map[string]any) (map[string]any, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to determine user home directory: %w", err)
	}

	// Home is the allowed root for the helper process
	svc, err := New(Config{
		AllowedRoots: []string{homeDir},
	})
	if err != nil {
		return nil, err
	}

	action, _ := payload["action"].(string)
	if action != "plan" && action != "apply" {
		return nil, fmt.Errorf("unsupported helper action: %q", action)
	}

	// Ensure root points to homeDir
	req := map[string]any{
		"target": map[string]any{"kind": "local", "root": homeDir},
	}
	if files, ok := payload["files"].(map[string]any); ok {
		req["files"] = files
	}
	if ak, ok := payload["api_key"].(string); ok {
		req["api_key"] = ak
	}
	if aks, ok := payload["api_keys"].(map[string]any); ok {
		req["api_keys"] = aks
	}
	if ed, ok := payload["expected_digests"].(map[string]any); ok {
		req["expected_digests"] = ed
	}

	return svc.Execute(context.Background(), "model_config."+action, req)
}
