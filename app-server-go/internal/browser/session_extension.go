package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type extensionCDP interface {
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
}

// ensureSessionExtension makes grouping available in the managed CDP browser.
// Edge disables unpacked extensions until its profile developer mode is enabled.
func ensureSessionExtension(ctx context.Context, client extensionCDP, enableDeveloperMode func(context.Context) error) error {
	path, err := filepath.Abs(ExtensionPath())
	if err != nil {
		return err
	}
	type extension struct {
		ID      string `json:"id"`
		Path    string `json:"path"`
		Enabled bool   `json:"enabled"`
	}
	lookup := func() (extension, error) {
		raw, err := client.Call(ctx, "Extensions.getExtensions", nil)
		if err != nil {
			return extension{}, err
		}
		var result struct {
			Extensions []extension `json:"extensions"`
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return extension{}, err
		}
		for _, e := range result.Extensions {
			if strings.EqualFold(filepath.Clean(e.Path), filepath.Clean(path)) {
				return e, nil
			}
		}
		return extension{}, nil
	}
	e, err := lookup()
	if err != nil {
		return fmt.Errorf("inspect session grouping extension: %w", err)
	}
	if e.Enabled {
		return nil
	}
	if err = enableDeveloperMode(ctx); err != nil {
		return fmt.Errorf("enable managed browser extension mode: %w", err)
	}
	if e.ID == "" {
		if _, err = client.Call(ctx, "Extensions.loadUnpacked", map[string]any{"path": path}); err != nil {
			return fmt.Errorf("load session grouping extension: %w", err)
		}
	}
	e, err = lookup()
	if err != nil {
		return err
	}
	if !e.Enabled {
		return fmt.Errorf("session grouping extension is disabled in the managed browser")
	}
	return nil
}

func (m *SessionManager) enableExtensionDeveloperMode(ctx context.Context, client extensionCDP) error {
	raw, err := client.Call(ctx, "Target.createTarget", map[string]any{"url": "edge://extensions/", "background": true})
	if err != nil {
		return err
	}
	var target struct {
		ID string `json:"targetId"`
	}
	if err = json.Unmarshal(raw, &target); err != nil {
		return err
	}
	if target.ID == "" {
		return fmt.Errorf("extension settings target is missing")
	}
	defer client.Call(ctx, "Target.closeTarget", map[string]any{"targetId": target.ID})
	pages, err := m.endpoints.ListPages(ctx)
	if err != nil {
		return err
	}
	for _, page := range pages {
		if page.ID != target.ID {
			continue
		}
		connection, err := DialCDP(ctx, page.WebSocketDebuggerURL)
		if err != nil {
			return err
		}
		defer connection.Close()
		raw, err = connection.Call(ctx, "Runtime.evaluate", map[string]any{"expression": `(async()=>{const api=chrome.developerPrivate;if(!api)throw new Error('Extension management API unavailable');await api.updateProfileConfiguration({inDeveloperMode:true});const profile=await api.getProfileConfiguration();if(!profile.inDeveloperMode)throw new Error('Developer mode was not enabled');return true})()`, "awaitPromise": true, "returnByValue": true})
		if err != nil {
			return err
		}
		var result struct {
			Result struct {
				Value bool `json:"value"`
			} `json:"result"`
			ExceptionDetails json.RawMessage `json:"exceptionDetails"`
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return err
		}
		if len(result.ExceptionDetails) > 0 || !result.Result.Value {
			return fmt.Errorf("managed browser refused extension developer mode")
		}
		return nil
	}
	return fmt.Errorf("extension settings target is unavailable")
}
