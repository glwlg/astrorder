package agentremote

import (
	"context"
	"errors"
	"io"
)

// ErrMCPRetired is returned by the retired MCP stdio entrypoint.
var ErrMCPRetired = errors.New("astrorder-mcp is retired; use the astrorder-agent CLI or the HTTP Agent API")

// RunStdio is a tombstone for the retired MCP stdio server. It performs no
// protocol work and always returns ErrMCPRetired so old clients fail clearly
// instead of silently using a stale tool surface.
func RunStdio(ctx context.Context, in io.Reader, out io.Writer, client *Client) error {
	return ErrMCPRetired
}
