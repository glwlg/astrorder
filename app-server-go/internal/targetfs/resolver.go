package targetfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const LocalConnectionID = "local"

type SessionConnectionStore interface {
	FindSessionByID(context.Context, string) (map[string]any, error)
	GetSSHConnection(context.Context, string) (map[string]any, error)
}

type ResolverOptions struct {
	Store            SessionConnectionStore
	DefaultWorkspace string
	AllowedRoots     []string
	SSHBinary        string
	HelperPath       string
	WSLBinary        string
	WSLHelperPath    string
	LocalStageRoots  []string
	// HelperAssetsDir holds helpers/<platform>/astrorder-helper builds that are
	// installed on remote targets on first use. When empty or missing, the
	// helper is expected to be on the remote PATH.
	HelperAssetsDir string
}

type ResolveRequest struct {
	Workspace    string
	SessionID    string
	ConnectionID string
}

type Resolved struct {
	FileSystem   FileSystem
	Workspace    string
	ConnectionID string
	Kind         string
}

type Resolver struct {
	options   ResolverOptions
	installer *helperInstaller
}

func NewResolver(options ResolverOptions) *Resolver {
	options.DefaultWorkspace = cleanInputPath(options.DefaultWorkspace)
	options.AllowedRoots = append([]string(nil), options.AllowedRoots...)
	options.LocalStageRoots = append([]string(nil), options.LocalStageRoots...)
	resolver := &Resolver{options: options}
	if options.HelperAssetsDir != "" {
		if info, err := os.Stat(options.HelperAssetsDir); err == nil && info.IsDir() {
			resolver.installer = newHelperInstaller(options.HelperAssetsDir)
		}
	}
	return resolver
}

func (r *Resolver) Resolve(ctx context.Context, request ResolveRequest) (Resolved, error) {
	workspace := cleanInputPath(request.Workspace)
	connectionID := strings.TrimSpace(request.ConnectionID)
	if request.SessionID != "" {
		if r.options.Store == nil {
			return Resolved{}, errorf(503, "session_store_unavailable", "Session lookup is unavailable", nil)
		}
		session, err := r.options.Store.FindSessionByID(ctx, request.SessionID)
		if err != nil {
			return Resolved{}, errorf(500, "session_lookup_failed", "Failed to resolve session", err)
		}
		if session == nil {
			return Resolved{}, errorf(404, "session_not_found", "Session was not found", nil)
		}
		if workspace == "" {
			workspace = mapString(session, "workspace")
		}
		if connectionID == "" {
			connectionID = mapString(session, "connection_id")
		}
	}
	if workspace == "" && (connectionID == "" || connectionID == LocalConnectionID) {
		workspace = cleanInputPath(r.options.DefaultWorkspace)
	}
	if workspace == "" && (connectionID == "" || connectionID == LocalConnectionID) {
		return Resolved{}, errorf(422, "workspace_required", "Workspace is required", nil)
	}
	if connectionID == "" || connectionID == LocalConnectionID {
		filesystem, err := NewLocal(LocalOptions{Workspace: workspace, AllowedRoots: r.options.AllowedRoots})
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{FileSystem: filesystem, Workspace: workspace, ConnectionID: LocalConnectionID, Kind: "local"}, nil
	}
	if strings.HasPrefix(connectionID, "wsl:") {
		if workspace == "" {
			workspace = "/"
		}
		distro := strings.TrimPrefix(connectionID, "wsl:")
		wslConfig := WSLConfig{Binary: r.options.WSLBinary, Distro: distro, HelperPath: r.options.WSLHelperPath}
		if wslConfig.HelperPath == "" {
			wslConfig.HelperPath = r.helperPath(ctx, connectionID, wslShellRunner(wslConfig))
		}
		command, err := BuildWSLCommand(wslConfig)
		if err != nil {
			return Resolved{}, err
		}
		filesystem, err := NewRemote(RemoteOptions{Workspace: workspace, AllowedRoots: []string{workspace}, Command: command, StageRoots: r.options.LocalStageRoots})
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{FileSystem: filesystem, Workspace: workspace, ConnectionID: connectionID, Kind: "wsl"}, nil
	}
	if r.options.Store == nil {
		return Resolved{}, errorf(503, "connection_store_unavailable", "SSH connection lookup is unavailable", nil)
	}
	connection, err := r.options.Store.GetSSHConnection(ctx, connectionID)
	if err != nil {
		return Resolved{}, errorf(500, "connection_lookup_failed", "Failed to resolve SSH connection", err)
	}
	if connection == nil {
		return Resolved{}, errorf(404, "connection_not_found", "SSH 连接不存在", nil)
	}
	settings, ok := stringMap(connection["settings"])
	if !ok {
		settings = connection
	}
	connectionWorkspace := cleanInputPath(mapString(settings, "workspace"))
	if workspace == "" {
		workspace = connectionWorkspace
		if workspace == "" {
			workspace = "/"
		}
	}
	if connectionWorkspace == "" {
		connectionWorkspace = workspace
	}
	roots := []string{connectionWorkspace}
	sshConfig := SSHConfig{
		Binary: r.options.SSHBinary, Host: mapString(settings, "host"), Alias: mapString(settings, "ssh_config_alias"),
		Port: mapInt(settings, "port", 22), User: mapString(settings, "user"), IdentityFile: mapString(settings, "identity_file"),
		ConfigFile: mapString(settings, "config_file"), HelperPath: r.options.HelperPath,
	}
	if sshConfig.HelperPath == "" {
		sshConfig.HelperPath = r.helperPath(ctx, connectionID, sshShellRunner(sshConfig))
	}
	command, err := BuildSSHCommand(sshConfig)
	if err != nil {
		return Resolved{}, err
	}
	filesystem, err := NewRemote(RemoteOptions{Workspace: workspace, AllowedRoots: roots, Command: command, StageRoots: r.options.LocalStageRoots})
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{FileSystem: filesystem, Workspace: workspace, ConnectionID: connectionID, Kind: "ssh"}, nil
}

// helperPath returns the installed helper path for a remote target. Without
// bundled assets the helper is resolved from the remote PATH. Installation
// failures are returned as the PATH fallback so the helper start error carries
// the real cause.
func (r *Resolver) helperPath(ctx context.Context, key string, run shellRunner) string {
	if r.installer == nil {
		return helperBinaryName
	}
	path, err := r.installer.ensure(ctx, key, run)
	if err != nil {
		return helperBinaryName
	}
	return path
}

func CleanPath(value string) string {
	return cleanInputPath(value)
}

func cleanInputPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "<>\"'")
	if strings.HasPrefix(value, "file:///") {
		return value[8:]
	}
	if strings.HasPrefix(value, "file://") {
		return value[7:]
	}
	return value
}

func stringMap(value any) (map[string]any, bool) {
	if typed, ok := value.(map[string]any); ok {
		return typed, true
	}
	return nil, false
}

func mapString(value map[string]any, key string) string {
	if value == nil || value[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value[key]))
}

func mapInt(value map[string]any, key string, fallback int) int {
	if value == nil || value[key] == nil {
		return fallback
	}
	switch typed := value[key].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		parsed, err := strconv.Atoi(fmt.Sprint(typed))
		if err == nil {
			return parsed
		}
		return fallback
	}
}

// OpenLocal opens a path through the same canonical scope gate as Local.
func OpenLocal(path string, allowedRoots []string) (io.ReadCloser, Info, error) {
	workspace := filepath.Dir(path)
	filesystem, err := NewLocal(LocalOptions{Workspace: workspace, AllowedRoots: allowedRoots})
	if err != nil {
		return nil, Info{}, err
	}
	result, err := filesystem.Read(context.Background(), path)
	if err != nil {
		return nil, Info{}, err
	}
	return result.Reader, result.Info, nil
}

// IsNotFound reports a normalized targetfs not-found status.
func IsNotFound(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Status == 404
}

var _ = os.ErrNotExist
