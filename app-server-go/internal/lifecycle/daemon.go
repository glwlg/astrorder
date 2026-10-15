package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const daemonMetadataName = "daemon-process.json"

// DaemonOptions contains explicit configuration for managing the Session Daemon lifecycle.
type DaemonOptions struct {
	ExecutablePath  string
	ConfigPath      string
	DatabasePath    string
	WorkingDir      string
	StateDir        string
	Port            int
	GRPCTarget      string
	Secret          string
	GRPCCredential  string
	LogPath         string
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

// DaemonStatusResult matches the JSON contract expected by the Desktop client.
type DaemonStatusResult struct {
	State            string            `json:"state"`
	Port             int               `json:"port"`
	Process          ProcessIdentity   `json:"process,omitempty"`
	IdentityVerified bool              `json:"identity_verified"`
	PortOwned        bool              `json:"port_owned"`
	StartupTask      string            `json:"startup_task,omitempty"`
	DaemonID         string            `json:"daemon_id,omitempty"`
	SessionCount     int               `json:"session_count,omitempty"`
	ActiveSessions   map[string]string `json:"active_sessions,omitempty"`
}

// DaemonStatus validates process identity, listener port, and authoritative gRPC status.
func DaemonStatus(ctx context.Context, options DaemonOptions) (DaemonStatusResult, error) {
	validated, err := validateDaemonOptions(options)
	if err != nil {
		return DaemonStatusResult{State: StateStopped}, err
	}
	return daemonStatus(ctx, validated)
}

// StartDaemon starts an independent Session Daemon process or reuses the verified running one.
func StartDaemon(ctx context.Context, options DaemonOptions) (DaemonStatusResult, error) {
	validated, err := validateDaemonOptions(options)
	if err != nil {
		return DaemonStatusResult{State: StateStopped}, err
	}
	if validated.Secret == "" {
		return DaemonStatusResult{}, errors.New("Session Daemon secret is required")
	}
	if validated.ExecutablePath == "" {
		return DaemonStatusResult{}, errors.New("Session Daemon executable is required; Python daemon is removed")
	}
	if err := privateDir(validated.StateDir); err != nil {
		return DaemonStatusResult{}, fmt.Errorf("create daemon state directory: %w", err)
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return DaemonStatusResult{}, err
	}
	defer unlock()

	return startDaemonLocked(ctx, validated)
}

// StopDaemonWithLifecycle rechecks authority immediately before stopping and requests graceful shutdown.
func StopDaemonWithLifecycle(ctx context.Context, options DaemonOptions, confirmActive bool) (DaemonStatusResult, error) {
	validated, err := validateDaemonOptions(options)
	if err != nil {
		return DaemonStatusResult{State: StateStopped}, err
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return DaemonStatusResult{}, err
	}
	defer unlock()

	return stopDaemonLocked(ctx, validated, confirmActive)
}

// RestartDaemonWithLifecycle stops and starts the daemon under the single lifecycle lock without deadlocking.
func RestartDaemonWithLifecycle(ctx context.Context, options DaemonOptions, confirmActive bool) (DaemonStatusResult, error) {
	validated, err := validateDaemonOptions(options)
	if err != nil {
		return DaemonStatusResult{State: StateStopped}, err
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return DaemonStatusResult{}, err
	}
	defer unlock()

	if _, err := stopDaemonLocked(ctx, validated, confirmActive); err != nil {
		return DaemonStatusResult{}, fmt.Errorf("stop daemon before restart: %w", err)
	}
	return startDaemonLocked(ctx, validated)
}

func stopDaemonLocked(ctx context.Context, validated validatedDaemonOptions, confirmActive bool) (DaemonStatusResult, error) {
	status, err := daemonStatus(ctx, validated)
	if err != nil {
		return DaemonStatusResult{}, err
	}
	if status.State == StateStopped {
		return status, nil
	}

	authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
	defer authCancel()
	authority, err := NewGRPCAuthority(authCtx, validated.GRPCTarget, validated.GRPCCredential)
	if err != nil {
		if !confirmActive {
			return DaemonStatusResult{}, fmt.Errorf("refusing unconfirmed daemon stop: authority unavailable: %w", err)
		}
	} else {
		defer authority.Close()
		if stopErr := StopDaemon(ctx, authority, confirmActive); stopErr != nil {
			return DaemonStatusResult{}, stopErr
		}
	}

	// Runtime teardown is sequential and can outlive RPC listener shutdown.
	// A closed listener alone must never permit replacement of a live image.
	waitCtx, cancelWait := context.WithTimeout(ctx, validated.ShutdownTimeout)
	defer cancelWait()
	owned := status.Process
	var inspectFailure error
	for {
		pids, listenerErr := ListeningPIDs(validated.Port)
		if listenerErr != nil {
			return DaemonStatusResult{}, fmt.Errorf("verify daemon listener release: %w", listenerErr)
		}
		processExited := owned.PID == 0
		if owned.PID > 0 {
			cur, inspectErr := InspectProcess(owned.PID)
			switch {
			case errors.Is(inspectErr, os.ErrNotExist):
				processExited = true
			case inspectErr != nil:
				// Windows can briefly deny process inspection during exit.
				// Keep the process unconfirmed and retry within the same bound.
				inspectFailure = inspectErr
			default:
				inspectFailure = nil
				processExited = cur != owned
			}
		}
		if processExited && len(pids) == 0 {
			removeDaemonProcessMetadata(validated.StateDir, owned)
			return DaemonStatusResult{State: StateStopped, Port: validated.Port, StartupTask: status.StartupTask}, nil
		}
		select {
		case <-waitCtx.Done():
			return DaemonStatusResult{}, fmt.Errorf("daemon shutdown is not yet confirmed (pid %d, process_exited=%t, listener_released=%t): %w", owned.PID, processExited, len(pids) == 0, errors.Join(waitCtx.Err(), inspectFailure))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func startDaemonLocked(ctx context.Context, validated validatedDaemonOptions) (DaemonStatusResult, error) {
	status, err := daemonStatus(ctx, validated)
	if err == nil && status.State == StateRunning {
		return status, nil
	}
	if status.State == StateConflict {
		return DaemonStatusResult{}, errors.New("daemon port or process conflict detected")
	}

	if err := privateDir(filepath.Dir(validated.LogPath)); err != nil {
		return DaemonStatusResult{}, fmt.Errorf("create daemon log directory: %w", err)
	}
	logFile, err := os.OpenFile(validated.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return DaemonStatusResult{}, fmt.Errorf("open daemon log: %w", err)
	}

	cmd := exec.Command(validated.ExecutablePath)
	cmd.Dir = validated.WorkingDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	cleanEnv := ProcessEnvironment(map[string]string{
		"ASTRORDER_SESSION_DAEMON_SECRET":          validated.Secret,
		"ASTRORDER_SESSION_DAEMON_PORT":            strconv.Itoa(validated.Port),
		"ASTRORDER_SESSION_DAEMON_DB":              validated.DatabasePath,
		"ASTRORDER_SESSION_DAEMON_CONFIG":          validated.ConfigPath,
		"ASTRORDER_SESSION_DAEMON_GRPC_TARGET":     validated.GRPCTarget,
		"ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL": validated.GRPCCredential,
		"ASTRORDER_SESSION_DAEMON_WS_DISABLED":     "1",
	})
	cmd.Env = cleanEnv

	if err := configureIndependentProcess(cmd); err != nil {
		_ = logFile.Close()
		return DaemonStatusResult{}, err
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return DaemonStatusResult{}, fmt.Errorf("start daemon process: %w", err)
	}
	_ = logFile.Close()

	started := cmd.Process
	identity, err := InspectProcess(started.Pid)
	if err != nil {
		terminateSpawned(cmd)
		return DaemonStatusResult{}, fmt.Errorf("inspect started daemon: %w", err)
	}

	if err := writeDaemonProcessMetadata(validated.StateDir, identity); err != nil {
		terminateSpawned(cmd)
		return DaemonStatusResult{}, err
	}

	deadline := time.Now().Add(validated.StartupTimeout)
	for {
		curStatus, statusErr := daemonStatus(ctx, validated)
		if statusErr == nil && curStatus.State == StateRunning && curStatus.Process == identity {
			_ = started.Release()
			return curStatus, nil
		}
		if proc, inspectErr := InspectProcess(identity.PID); inspectErr != nil || proc != identity {
			_ = cmd.Wait()
			removeDaemonProcessMetadata(validated.StateDir, identity)
			return DaemonStatusResult{}, errors.New("Session Daemon exited during startup; inspect the configured log")
		}
		if time.Now().After(deadline) {
			terminateSpawned(cmd)
			removeDaemonProcessMetadata(validated.StateDir, identity)
			return DaemonStatusResult{}, errors.New("Session Daemon did not become healthy before deadline")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func daemonStatus(ctx context.Context, options validatedDaemonOptions) (DaemonStatusResult, error) {
	result := DaemonStatusResult{
		State:       StateStopped,
		Port:        options.Port,
		StartupTask: QueryStartupTask(),
	}

	pids, err := ListeningPIDs(options.Port)
	if err != nil {
		return result, fmt.Errorf("inspect daemon listener: %w", err)
	}

	identity, exists, err := readDaemonProcessMetadata(options.StateDir)
	if err != nil {
		return result, err
	}

	if !exists {
		if len(pids) != 0 {
			result.State = StateConflict
		}
		return result, nil
	}

	result.Process = identity
	current, inspectErr := InspectProcess(identity.PID)
	result.IdentityVerified = inspectErr == nil && current == identity && samePath(current.ExecutablePath, options.ExecutablePath)
	result.PortOwned = result.IdentityVerified && len(pids) == 1 && pids[0] == identity.PID

	if !result.IdentityVerified || !result.PortOwned {
		if len(pids) == 0 && errors.Is(inspectErr, os.ErrNotExist) {
			result.State = StateStopped
		} else {
			result.State = StateConflict
		}
		return result, nil
	}

	authCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	authority, err := NewGRPCAuthority(authCtx, options.GRPCTarget, options.GRPCCredential)
	if err != nil {
		result.State = StateUnhealthy
		return result, nil
	}
	defer authority.Close()

	authState, err := authority.State(authCtx)
	if err != nil {
		result.State = StateUnhealthy
		return result, nil
	}

	result.State = authState.State
	result.DaemonID = authState.DaemonID
	result.SessionCount = len(authState.Sessions)
	result.ActiveSessions = authState.ActiveSessions
	return result, nil
}

type validatedDaemonOptions struct {
	DaemonOptions
}

func writeDaemonProcessMetadata(stateDir string, identity ProcessIdentity) error {
	body, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(stateDir, daemonMetadataName), append(body, '\n'), 0o600)
}

func readDaemonProcessMetadata(stateDir string) (ProcessIdentity, bool, error) {
	body, err := os.ReadFile(filepath.Join(stateDir, daemonMetadataName))
	if errors.Is(err, os.ErrNotExist) {
		return ProcessIdentity{}, false, nil
	}
	if err != nil {
		return ProcessIdentity{}, false, fmt.Errorf("read daemon process identity: %w", err)
	}
	var identity ProcessIdentity
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil || identity.PID <= 0 || identity.CreationTime == 0 || !filepath.IsAbs(identity.ExecutablePath) {
		return ProcessIdentity{}, false, errors.New("saved daemon process identity is invalid")
	}
	return identity, true, nil
}

func removeDaemonProcessMetadata(stateDir string, identity ProcessIdentity) {
	current, exists, err := readDaemonProcessMetadata(stateDir)
	if err == nil && exists && current == identity {
		_ = os.Remove(filepath.Join(stateDir, daemonMetadataName))
	}
}

func validateDaemonOptions(opts DaemonOptions) (validatedDaemonOptions, error) {
	if opts.ExecutablePath == "" {
		return validatedDaemonOptions{}, errors.New("daemon executable is required")
	}
	cleanExec := filepath.Clean(opts.ExecutablePath)
	if !filepath.IsAbs(cleanExec) {
		return validatedDaemonOptions{}, errors.New("daemon executable must be an absolute path")
	}
	info, err := os.Stat(cleanExec)
	if err != nil || !info.Mode().IsRegular() {
		return validatedDaemonOptions{}, fmt.Errorf("daemon executable must be an existing regular file: %w", err)
	}
	opts.ExecutablePath = cleanExec

	if opts.WorkingDir == "" {
		opts.WorkingDir = filepath.Dir(opts.ExecutablePath)
	}
	if !filepath.IsAbs(opts.WorkingDir) {
		return validatedDaemonOptions{}, errors.New("daemon working directory must be an absolute path")
	}

	if opts.StateDir == "" || !filepath.IsAbs(opts.StateDir) {
		return validatedDaemonOptions{}, errors.New("daemon state directory must be an absolute path")
	}
	opts.StateDir = filepath.Clean(opts.StateDir)

	if opts.GRPCTarget == "" {
		return validatedDaemonOptions{}, errors.New("daemon gRPC target is required")
	}
	host, portStr, err := net.SplitHostPort(opts.GRPCTarget)
	if err != nil || portStr == "" {
		return validatedDaemonOptions{}, fmt.Errorf("invalid daemon gRPC target: %w", err)
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return validatedDaemonOptions{}, fmt.Errorf("invalid daemon gRPC target port: %s", portStr)
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return validatedDaemonOptions{}, fmt.Errorf("daemon gRPC target must be loopback: %s", host)
		}
	}
	opts.Port = p

	if opts.Secret == "" {
		return validatedDaemonOptions{}, errors.New("daemon secret is required")
	}
	if opts.GRPCCredential == "" {
		return validatedDaemonOptions{}, errors.New("daemon gRPC credential is required")
	}

	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 60 * time.Second
	}
	if opts.StartupTimeout <= 0 {
		opts.StartupTimeout = 30 * time.Second
	}
	if opts.LogPath == "" {
		opts.LogPath = filepath.Join(opts.StateDir, "logs", "session-daemon.log")
	}
	opts.LogPath = filepath.Clean(opts.LogPath)

	return validatedDaemonOptions{DaemonOptions: opts}, nil
}
