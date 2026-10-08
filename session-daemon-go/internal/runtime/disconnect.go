package runtime

import (
	"context"
)

// ConnectionDisconnector is implemented by multiplexed runtimes that can release
// an isolated connection and its bound sessions.
type ConnectionDisconnector interface {
	DisconnectConnection(ctx context.Context, connectionID string) ([]string, error)
	SessionsForConnection(connectionID string) []string
}

// RuntimeDisconnector is implemented by single-instance runtimes (e.g. local codex)
// that can be shut down gracefully.
type RuntimeDisconnector interface {
	Shutdown(ctx context.Context) ([]string, error)
}
