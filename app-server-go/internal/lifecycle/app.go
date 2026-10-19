package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	StateStopped   = "stopped"
	StateRunning   = "running"
	StateUnhealthy = "unhealthy"
	StateConflict  = "conflict"
)

const processMetadataName = "app-process.json"

// ProcessIdentity resists PID reuse by pairing the native executable path with
// the Windows process creation timestamp.
type ProcessIdentity struct {
	PID            int    `json:"pid"`
	ExecutablePath string `json:"executable_path"`
	CreationTime   uint64 `json:"creation_time"`
}

// AppStatusResult separates HTTP health from native process/port ownership.
type AppStatusResult struct {
	State            string          `json:"state"`
	Address          string          `json:"address"`
	Process          ProcessIdentity `json:"process,omitempty"`
	IdentityVerified bool            `json:"identity_verified"`
	Healthy          bool            `json:"healthy"`
	PortOwned        bool            `json:"port_owned"`
}

// AppOptions contains explicit lifecycle inputs. Secrets are used only as HTTP
// request headers and are never persisted in process metadata.
type AppOptions struct {
	ExecutablePath  string
	Arguments       []string
	WorkingDir      string
	Environment     []string
	StateDir        string
	Address         string
	BrowserSecret   string
	LogPath         string
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

// AppStatus validates saved exact identity, current native identity, listener
// ownership, and the HTTP health contract independently.
func AppStatus(ctx context.Context, options AppOptions) (AppStatusResult, error) {
	validated, err := validateAppOptions(options)
	if err != nil {
		return AppStatusResult{}, err
	}
	return appStatus(ctx, validated)
}

// StartApp serializes start attempts and returns an existing exact owned App
// process instead of creating a duplicate.
func StartApp(ctx context.Context, options AppOptions) (AppStatusResult, error) {
	validated, err := validateAppOptions(options)
	if err != nil {
		return AppStatusResult{}, err
	}
	if validated.BrowserSecret == "" {
		return AppStatusResult{}, errors.New("browser credential is required for App start")
	}
	if err := privateDir(validated.StateDir); err != nil {
		return AppStatusResult{}, fmt.Errorf("create App state directory: %w", err)
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return AppStatusResult{}, err
	}
	defer unlock()
	return startAppLocked(ctx, validated)
}

func startAppLocked(ctx context.Context, validated validatedAppOptions) (AppStatusResult, error) {
	status, err := appStatus(ctx, validated)
	if err != nil {
		return AppStatusResult{}, err
	}
	if status.State == StateRunning {
		return status, nil
	}
	if status.State != StateStopped {
		return AppStatusResult{}, fmt.Errorf("refusing App start while lifecycle state is %s", status.State)
	}

	if err := privateDir(filepath.Dir(validated.LogPath)); err != nil {
		return AppStatusResult{}, fmt.Errorf("create App log directory: %w", err)
	}
	logFile, err := os.OpenFile(validated.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return AppStatusResult{}, fmt.Errorf("open App log: %w", err)
	}
	command := exec.Command(validated.ExecutablePath, validated.Arguments...)
	command.Dir = validated.WorkingDir
	command.Env = append([]string(nil), validated.Environment...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := configureIndependentProcess(command); err != nil {
		logFile.Close()
		return AppStatusResult{}, err
	}
	if err := command.Start(); err != nil {
		logFile.Close()
		return AppStatusResult{}, fmt.Errorf("start App process: %w", err)
	}
	_ = logFile.Close()
	started := command.Process
	identity, err := InspectProcess(started.Pid)
	if err != nil {
		terminateSpawned(command)
		return AppStatusResult{}, fmt.Errorf("inspect started App process: %w", err)
	}
	if !samePath(identity.ExecutablePath, validated.ExecutablePath) {
		terminateSpawned(command)
		return AppStatusResult{}, errors.New("started App executable identity did not match requested executable")
	}
	if err := writeProcessMetadata(validated.StateDir, identity); err != nil {
		terminateSpawned(command)
		return AppStatusResult{}, err
	}

	deadline := time.Now().Add(validated.StartupTimeout)
	for {
		status, statusErr := appStatus(ctx, validated)
		if statusErr == nil && status.State == StateRunning && status.Process == identity {
			_ = started.Release()
			return status, nil
		}
		if process, inspectErr := InspectProcess(identity.PID); inspectErr != nil || process != identity {
			_ = command.Wait()
			removeProcessMetadata(validated.StateDir, identity)
			return AppStatusResult{}, errors.New("App process exited during startup; inspect the configured App log")
		}
		if err := ctx.Err(); err != nil {
			terminateSpawned(command)
			removeProcessMetadata(validated.StateDir, identity)
			return AppStatusResult{}, err
		}
		if time.Now().After(deadline) {
			terminateSpawned(command)
			removeProcessMetadata(validated.StateDir, identity)
			return AppStatusResult{}, errors.New("App did not become healthy before the startup deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// StopApp sends only the authenticated graceful App shutdown and waits for the
// exact owned process to exit and the listener to be released. It never kills
// an App or daemon process.
func StopApp(ctx context.Context, options AppOptions) (AppStatusResult, error) {
	validated, err := validateAppOptions(options)
	if err != nil {
		return AppStatusResult{}, err
	}
	if validated.BrowserSecret == "" {
		return AppStatusResult{}, errors.New("browser credential is required for App stop")
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return AppStatusResult{}, err
	}
	defer unlock()
	return stopAppLocked(ctx, validated)
}

func stopAppLocked(ctx context.Context, validated validatedAppOptions) (AppStatusResult, error) {
	status, err := appStatus(ctx, validated)
	if err != nil {
		return AppStatusResult{}, err
	}
	if status.State == StateStopped {
		return status, nil
	}
	if !status.IdentityVerified || !status.PortOwned {
		return AppStatusResult{}, errors.New("refusing to stop an App without exact process and listener ownership")
	}
	owned := status.Process
	if err := requestAppShutdown(ctx, validated); err != nil {
		return AppStatusResult{}, err
	}

	deadline := time.Now().Add(validated.ShutdownTimeout)
	for {
		processExited := false
		current, inspectErr := InspectProcess(owned.PID)
		if errors.Is(inspectErr, os.ErrNotExist) || (inspectErr == nil && current != owned) {
			processExited = true
		} else if inspectErr != nil && !errors.Is(inspectErr, os.ErrPermission) {
			return AppStatusResult{}, fmt.Errorf("verify App process exit: %w", inspectErr)
		}
		// Windows can temporarily deny inspection while a process exits.
		// Continue waiting; success still requires proven exit and listener release.
		pids, listenerErr := ListeningPIDs(validated.port)
		if listenerErr != nil {
			return AppStatusResult{}, fmt.Errorf("verify App listener release: %w", listenerErr)
		}
		if processExited && len(pids) == 0 {
			removeProcessMetadata(validated.StateDir, owned)
			return AppStatusResult{State: StateStopped, Address: validated.Address}, nil
		}
		if err := ctx.Err(); err != nil {
			return AppStatusResult{}, err
		}
		if time.Now().After(deadline) {
			return AppStatusResult{}, errors.New("App did not exit and release its listener before the shutdown deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// RestartApp holds the lifecycle lock across the complete graceful stop/start
// transaction, so another caller cannot insert a process between its phases.
func RestartApp(ctx context.Context, options AppOptions) (AppStatusResult, error) {
	validated, err := validateAppOptions(options)
	if err != nil {
		return AppStatusResult{}, err
	}
	if validated.BrowserSecret == "" {
		return AppStatusResult{}, errors.New("browser credential is required for App restart")
	}
	if err := privateDir(validated.StateDir); err != nil {
		return AppStatusResult{}, fmt.Errorf("create App state directory: %w", err)
	}
	unlock, err := acquireLifecycleLock(ctx, validated.StateDir)
	if err != nil {
		return AppStatusResult{}, err
	}
	defer unlock()
	if _, err := stopAppLocked(ctx, validated); err != nil {
		return AppStatusResult{}, err
	}
	return startAppLocked(ctx, validated)
}

func appStatus(ctx context.Context, options validatedAppOptions) (AppStatusResult, error) {
	result := AppStatusResult{State: StateStopped, Address: options.Address}
	pids, err := ListeningPIDs(options.port)
	if err != nil {
		return AppStatusResult{}, fmt.Errorf("inspect App listener: %w", err)
	}
	result.Healthy = appHealthy(ctx, options)
	identity, exists, err := readProcessMetadata(options.StateDir)
	if err != nil {
		return AppStatusResult{}, err
	}
	if !exists {
		if len(pids) != 0 || result.Healthy {
			result.State = StateConflict
		}
		return result, nil
	}
	result.Process = identity
	current, inspectErr := InspectProcess(identity.PID)
	if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
		return AppStatusResult{}, fmt.Errorf("inspect saved App process: %w", inspectErr)
	}
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
	if result.Healthy {
		result.State = StateRunning
	} else {
		result.State = StateUnhealthy
	}
	return result, nil
}

type validatedAppOptions struct {
	AppOptions
	port int
}

func validateAppOptions(options AppOptions) (validatedAppOptions, error) {
	for label, path := range map[string]string{
		"App executable":      options.ExecutablePath,
		"App state directory": options.StateDir,
	} {
		if err := requireAbsolute(label, path); err != nil {
			return validatedAppOptions{}, err
		}
	}
	options.ExecutablePath = filepath.Clean(options.ExecutablePath)
	options.StateDir = filepath.Clean(options.StateDir)
	if resolvedExecutable, err := canonicalExistingPath(options.ExecutablePath); err != nil || !samePath(resolvedExecutable, options.ExecutablePath) {
		return validatedAppOptions{}, errors.New("App executable path must be canonical and must not traverse a reparse point")
	}
	if resolvedState, _, err := resolveExistingPath(options.StateDir); err != nil || !samePath(resolvedState, options.StateDir) {
		return validatedAppOptions{}, errors.New("App state directory must be canonical and must not traverse a reparse point")
	}
	if options.StateDir == filepath.Clean(filepath.VolumeName(options.StateDir)+string(filepath.Separator)) {
		return validatedAppOptions{}, errors.New("App state directory must not be a volume root")
	}
	if info, err := os.Stat(options.ExecutablePath); err != nil || !info.Mode().IsRegular() {
		return validatedAppOptions{}, errors.New("App executable must be an existing regular file")
	}
	if options.WorkingDir == "" {
		options.WorkingDir = filepath.Dir(options.ExecutablePath)
	}
	if err := requireAbsolute("App working directory", options.WorkingDir); err != nil {
		return validatedAppOptions{}, err
	}
	if info, err := os.Stat(options.WorkingDir); err != nil || !info.IsDir() {
		return validatedAppOptions{}, errors.New("App working directory must be an existing directory")
	}
	if options.LogPath == "" {
		options.LogPath = filepath.Join(options.StateDir, "logs", "app.log")
	}
	if err := requireAbsolute("App log path", options.LogPath); err != nil {
		return validatedAppOptions{}, err
	}
	options.LogPath = filepath.Clean(options.LogPath)
	if !pathWithin(options.StateDir, options.LogPath) {
		return validatedAppOptions{}, errors.New("App log path must be inside the App state directory")
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	host, rawPort, err := net.SplitHostPort(options.Address)
	if err != nil || rawPort == "" {
		return validatedAppOptions{}, errors.New("App address must contain an explicit loopback port")
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return validatedAppOptions{}, errors.New("App lifecycle address must be loopback")
		}
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return validatedAppOptions{}, errors.New("App lifecycle port must be between 1 and 65535")
	}
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = 30 * time.Second
	}
	if options.ShutdownTimeout <= 0 {
		options.ShutdownTimeout = 10 * time.Second
	}
	return validatedAppOptions{AppOptions: options, port: port}, nil
}

func appHealthy(ctx context.Context, options validatedAppOptions) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+options.Address+"/health", nil)
	if err != nil {
		return false
	}
	client := isolatedHTTPClient(2 * time.Second)
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var body struct {
		Status          string `json:"status"`
		Service         string `json:"service"`
		ProtocolVersion int    `json:"protocol_version"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if response.StatusCode != http.StatusOK || decoder.Decode(&body) != nil || body.Status != "ok" || body.Service != "astrorder" || body.ProtocolVersion != 1 {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func requestAppShutdown(ctx context.Context, options validatedAppOptions) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+options.Address+"/_desktop/shutdown", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+options.BrowserSecret)
	response, err := isolatedHTTPClient(3 * time.Second).Do(request)
	if err != nil {
		return errors.New("App did not accept authenticated graceful shutdown")
	}
	defer response.Body.Close()
	var body struct {
		Stopping bool `json:"stopping"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if response.StatusCode != http.StatusOK || decoder.Decode(&body) != nil || !body.Stopping || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("App did not confirm authenticated graceful shutdown")
	}
	return nil
}

func isolatedHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: timeout}).DialContext,
		},
	}
}

func writeProcessMetadata(stateDir string, identity ProcessIdentity) error {
	body, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(stateDir, processMetadataName), append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write App process identity: %w", err)
	}
	return nil
}

func readProcessMetadata(stateDir string) (ProcessIdentity, bool, error) {
	body, err := os.ReadFile(filepath.Join(stateDir, processMetadataName))
	if errors.Is(err, os.ErrNotExist) {
		return ProcessIdentity{}, false, nil
	}
	if err != nil {
		return ProcessIdentity{}, false, fmt.Errorf("read App process identity: %w", err)
	}
	var identity ProcessIdentity
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil || identity.PID <= 0 || identity.CreationTime == 0 || !filepath.IsAbs(identity.ExecutablePath) {
		return ProcessIdentity{}, false, errors.New("saved App process identity is invalid")
	}
	return identity, true, nil
}

func removeProcessMetadata(stateDir string, identity ProcessIdentity) {
	current, exists, err := readProcessMetadata(stateDir)
	if err == nil && exists && current == identity {
		_ = os.Remove(filepath.Join(stateDir, processMetadataName))
	}
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbsolute), filepath.Clean(rightAbsolute))
}

func terminateSpawned(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Kill()
	_ = command.Wait()
}
