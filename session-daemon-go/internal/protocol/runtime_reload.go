package protocol

import (
	"context"
	"errors"
	"fmt"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// ErrRuntimeReloadNotConfigured is returned when dynamic runtime configuration
// reload has not been wired on this Daemon instance.
var ErrRuntimeReloadNotConfigured = errors.New("runtime config reloader is not configured")

// RuntimeReloadOptions configures a dynamic runtime configuration reload.
type RuntimeReloadOptions struct {
	ProbePath     *bool    `json:"probe_path,omitempty"`
	ExtraPathDirs []string `json:"extra_path_dirs,omitempty"`
}

// RuntimeReloadResult summarizes the outcome of a dynamic runtime config reload.
type RuntimeReloadResult struct {
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

// RuntimeConfigReloadFunc performs a thread-safe hot-reload of runtime config
// without interrupting running sessions.
type RuntimeConfigReloadFunc func(ctx context.Context, opts RuntimeReloadOptions) (RuntimeReloadResult, error)

// SetRuntimeConfigReloader installs the callback invoked by ReloadRuntimeConfig.
func (d *Daemon) SetRuntimeConfigReloader(fn RuntimeConfigReloadFunc) {
	d.mu.Lock()
	d.runtimeConfigReload = fn
	d.mu.Unlock()
}

// ReloadRuntimeConfig triggers a thread-safe hot-reload of Agent runtime
// configuration while gating against daemon shutdown admission.
func (d *Daemon) ReloadRuntimeConfig(ctx context.Context, opts RuntimeReloadOptions) (RuntimeReloadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RuntimeReloadResult{}, err
	}
	if err := d.beginControl(); err != nil {
		return RuntimeReloadResult{}, fmt.Errorf("%w: %v", core.ErrRuntimeBusy, err)
	}
	defer d.endControl()

	d.mu.Lock()
	reloader := d.runtimeConfigReload
	d.mu.Unlock()
	if reloader == nil {
		return RuntimeReloadResult{}, ErrRuntimeReloadNotConfigured
	}

	result, err := reloader(ctx, opts)
	if err != nil {
		return RuntimeReloadResult{}, err
	}
	result.Supported = true
	result.DaemonID = d.daemonID
	result.Registered = d.registry.RegisteredKinds()
	result.PreservedSessions = d.registry.TotalOwnedSessions()
	return result, nil
}

// SwapRuntime atomically registers or replaces the active adapter for kind
// while keeping any live sessions on the previous adapter intact.
func (d *Daemon) SwapRuntime(kind string, adapter core.Adapter, onRetire func() error) (int, error) {
	return d.registry.SwapRuntime(kind, adapter, onRetire)
}

// UnregisterRuntime removes kind from future session admission without
// terminating any live sessions bound to the previous adapter.
func (d *Daemon) UnregisterRuntime(kind string) (int, bool, error) {
	return d.registry.UnregisterRuntime(kind)
}

// HasRuntime reports whether kind currently has an active adapter registered.
func (d *Daemon) HasRuntime(kind string) bool {
	return d.registry.HasRuntime(kind)
}

// RegisteredRuntimes returns the sorted list of currently active runtime kinds.
func (d *Daemon) RegisteredRuntimes() []string {
	return d.registry.RegisteredKinds()
}

// TotalOwnedSessions returns the count of currently live owned sessions.
func (d *Daemon) TotalOwnedSessions() int {
	return d.registry.TotalOwnedSessions()
}

// OwnsRuntimeSession reports whether any active or retired adapter for kind
// currently owns a live native session for id.
func (d *Daemon) OwnsRuntimeSession(kind, id string) bool {
	return d.registry.OwnsAdapterSession(kind, id)
}

// HasModelConfig reports whether the model configuration service is already configured.
func (d *Daemon) HasModelConfig() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.modelConfig != nil
}
