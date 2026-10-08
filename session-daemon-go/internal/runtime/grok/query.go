package grok

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"fmt"
)

func (a *Adapter) Query(ctx context.Context, request core.Request) (result map[string]any, err error) {
	if request.Fields["method"] != "initialize" {
		return nil, fmt.Errorf("Grok runtime request is unsupported")
	}
	workspace, err := a.workspace("")
	if err != nil {
		return nil, err
	}
	c, err := a.startClient(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.close()) }()
	return c.request(ctx, "initialize", initializeParams())
}
