package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"fmt"
)

func (a *Adapter) Query(ctx context.Context, request core.Request) (result map[string]any, err error) {
	method, _ := request.Fields["method"].(string)
	if method != "initialize" && method != "session/list" {
		return nil, fmt.Errorf("Grok runtime request is unsupported")
	}
	workspaceHint := ""
	if a.sshStarter != nil {
		workspaceHint = a.config.Workspace
	}
	workspace, err := a.workspace(workspaceHint)
	if err != nil {
		return nil, err
	}
	c, err := a.startClient(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.close()) }()
	initialized, err := c.request(ctx, "initialize", initializeParams())
	if err != nil {
		return nil, err
	}
	if method == "initialize" {
		return initialized, nil
	}
	caps, _ := initialized["agentCapabilities"].(map[string]any)
	sessionCaps, _ := caps["sessionCapabilities"].(map[string]any)
	if _, supported := sessionCaps["list"]; !supported {
		return nil, fmt.Errorf("Grok does not advertise session/list capability")
	}
	params := map[string]any{}
	raw, _ := request.Fields["request_params"].(map[string]any)
	if raw == nil {
		raw = request.Fields
	}
	for _, key := range []string{"cwd", "cursor"} {
		if value, present := raw[key]; present {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid Grok list %s", key)
			}
			params[key] = text
		}
	}
	return c.request(ctx, "session/list", params)
}
