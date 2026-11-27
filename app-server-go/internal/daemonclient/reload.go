package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// RuntimeReloadServiceName is the gRPC service name exposed by session-daemon-go
	// for dynamic Agent runtime configuration hot-reloading.
	RuntimeReloadServiceName = "astrorder.sessiond.v1.RuntimeReload"

	// ReloadConfigFullMethodName is the full gRPC method path for ReloadConfig.
	ReloadConfigFullMethodName = "/astrorder.sessiond.v1.RuntimeReload/ReloadConfig"

	reloadConfigRequestSchema = "astrorder.daemon.reload-config-request.v1"
)

// ErrReloadUnsupported is returned when the connected session-daemon does not
// implement dynamic runtime config hot-reload (e.g., an older running
// astrorder-sessiond.exe returning gRPC codes.Unimplemented).
var ErrReloadUnsupported = errors.New("session daemon does not support dynamic config reload")

// ReloadConfigOptions controls optional behavior of ReloadConfigWithOptions.
type ReloadConfigOptions struct {
	// ProbePath asks the session daemon to refresh PATH and probe for newly
	// installed local Agent executables not yet listed in session-daemon.json.
	// When nil, the daemon defaults to true.
	ProbePath *bool `json:"probe_path,omitempty"`

	// ExtraPathDirs supplies additional directories (for example, freshly
	// unpacked portable <DataDir>/runtimes/node or uv bin directories) to
	// merge into the daemon's PATH before probing and launching new sessions.
	ExtraPathDirs []string `json:"extra_path_dirs,omitempty"`
}

// ReloadConfigResult describes the outcome of a daemon runtime config reload.
type ReloadConfigResult struct {
	Supported         bool     `json:"supported"`
	DaemonID          string   `json:"daemon_id,omitempty"`
	ConfigPath        string   `json:"config_path,omitempty"`
	ReloadedAt        string   `json:"reloaded_at,omitempty"`
	AddedRuntimes     []string `json:"added_runtimes,omitempty"`
	UpdatedRuntimes   []string `json:"updated_runtimes,omitempty"`
	RemovedRuntimes   []string `json:"removed_runtimes,omitempty"`
	UnchangedRuntimes []string `json:"unchanged_runtimes,omitempty"`
	Registered        []string `json:"registered_runtimes,omitempty"`
	PreservedSessions int      `json:"preserved_sessions"`
	Warnings          []string `json:"warnings,omitempty"`
}

// SupportsReloadConfig reports whether the connected daemon advertised the
// dynamic runtime reload service during GetCapabilities.
func (c *Client) SupportsReloadConfig() bool {
	if c == nil || c.capabilities == nil {
		return false
	}
	for _, svc := range c.capabilities.Services {
		if svc == RuntimeReloadServiceName {
			return true
		}
	}
	return false
}

// ReloadConfig requests a thread-safe hot-reload of Agent runtime configuration
// (re-reading session-daemon.json, refreshing PATH, and probing newly installed
// runtimes) without interrupting any active sessions.
//
// If the running daemon is an older version that returns gRPC codes.Unimplemented,
// ReloadConfig returns (&ReloadConfigResult{Supported: false, DaemonID: c.DaemonID()}, ErrReloadUnsupported).
func (c *Client) ReloadConfig(ctx context.Context) (*ReloadConfigResult, error) {
	return c.ReloadConfigWithOptions(ctx, ReloadConfigOptions{})
}

// ReloadConfigWithOptions is like ReloadConfig but allows passing explicit
// options such as ExtraPathDirs or overriding ProbePath.
func (c *Client) ReloadConfigWithOptions(ctx context.Context, opts ReloadConfigOptions) (*ReloadConfigResult, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("daemon client is not connected")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, DefaultRequestTimeout)
		defer cancel()
	}

	rawReq, err := json.Marshal(opts)
	if err != nil {
		return nil, fmt.Errorf("encode ReloadConfig request: %w", err)
	}
	if len(rawReq) > MaxCompatBusinessBytes {
		return nil, errors.New("ReloadConfig request exceeds business limit")
	}

	reqMsg := &sessiondv1.NativeJson{
		Schema:   reloadConfigRequestSchema,
		Utf8Json: rawReq,
	}
	respMsg := new(sessiondv1.NativeJson)

	if err := c.conn.Invoke(callCtx, ReloadConfigFullMethodName, reqMsg, respMsg); err != nil {
		if status.Code(err) == codes.Unimplemented {
			return &ReloadConfigResult{
				Supported: false,
				DaemonID:  c.daemonID,
			}, ErrReloadUnsupported
		}
		return nil, fmt.Errorf("daemon ReloadConfig failed: %w", err)
	}

	result := &ReloadConfigResult{
		Supported: true,
		DaemonID:  c.daemonID,
	}
	if len(respMsg.GetUtf8Json()) > 0 {
		if err := json.Unmarshal(respMsg.GetUtf8Json(), result); err != nil {
			return nil, fmt.Errorf("decode ReloadConfig response: %w", err)
		}
	}
	result.Supported = true
	if result.DaemonID == "" {
		result.DaemonID = c.daemonID
	}
	return result, nil
}
