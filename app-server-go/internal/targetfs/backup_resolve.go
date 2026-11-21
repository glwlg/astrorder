package targetfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"
)

// WriteBackupFile sends a fixed-length payload so an interrupted upload cannot
// publish a partial file. Local writes already abort on any reader error.
func WriteBackupFile(ctx context.Context, filesystem FileSystem, path string, source io.Reader, mode fs.FileMode, size int64) (Info, error) {
	remote, ok := filesystem.(*remoteFileSystem)
	if !ok {
		return filesystem.WriteAtomic(ctx, path, source, mode)
	}
	data, err := remote.envelope(struct {
		Path string      `json:"path"`
		Mode fs.FileMode `json:"mode"`
	}{Path: path, Mode: mode})
	if err != nil {
		return Info{}, err
	}
	response, err := remote.invoke(ctx, Request{Version: ProtocolVersion, Action: "write_atomic", Data: data, PayloadSize: size}, source)
	if err != nil {
		return Info{}, err
	}
	var result Info
	if err := json.Unmarshal(response.Data, &result); err != nil {
		return Info{}, errorf(502, "invalid_helper_response", "Target helper returned invalid write metadata", err)
	}
	return result, nil
}

// RunBackupRestoreHook runs the explicit job command on its restored target.
func (r *Resolver) RunBackupRestoreHook(ctx context.Context, connectionID, dir, command string) error {
	if connectionID == "" || connectionID == LocalConnectionID {
		return errors.New("还原后命令需要远程连接")
	}
	run, err := r.remoteShellRunner(ctx, connectionID)
	if err != nil {
		return err
	}
	// Check the physical working directory before running the command. Send the
	// command as a single argument so its stdin need not stay connected.
	const script = `cd -P "$1" || exit 125
[ "$(pwd -P)" = "$1" ] || exit 125
exec /bin/sh -c "$2" </dev/null`
	cmd, err := run(ctx, script, dir, command)
	if err != nil {
		return err
	}
	configureChildProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	return cmd.Run()
}

// ResolveScopedRemote opens a remote filesystem strictly confined to rootDir,
// staging any exported directory trees under stageRoots. It is used by backup
// operations so neither remote source traversal nor local staging escapes the
// explicitly configured backup scope.
func (r *Resolver) ResolveScopedRemote(ctx context.Context, connectionID, rootDir string, stageRoots []string) (Resolved, error) {
	return r.resolveScopedRemote(ctx, connectionID, cleanInputPath(rootDir), stageRoots, false)
}

func (r *Resolver) ResolveBackupRestore(ctx context.Context, connectionID, rootDir string) (Resolved, error) {
	return r.resolveScopedRemote(ctx, connectionID, rootDir, nil, true)
}

func (r *Resolver) resolveScopedRemote(ctx context.Context, connectionID, root string, stageRoots []string, restorePaths bool) (Resolved, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" || connectionID == LocalConnectionID {
		return Resolved{}, errorf(422, "remote_connection_required", "Remote connection is required", nil)
	}
	if root == "" {
		return Resolved{}, errorf(422, "workspace_required", "Remote root directory is required", nil)
	}
	resolvedStageRoots := append([]string(nil), stageRoots...)
	if len(resolvedStageRoots) == 0 {
		resolvedStageRoots = append([]string(nil), r.options.LocalStageRoots...)
	}

	if strings.HasPrefix(connectionID, "wsl:") {
		distro := strings.TrimPrefix(connectionID, "wsl:")
		wslConfig := WSLConfig{Binary: r.options.WSLBinary, Distro: distro, HelperPath: r.options.WSLHelperPath}
		if wslConfig.HelperPath == "" {
			path, err := r.helperPath(ctx, connectionID, wslShellRunner(wslConfig))
			if err != nil {
				return Resolved{}, err
			}
			wslConfig.HelperPath = path
		}
		command, err := BuildWSLCommand(wslConfig)
		if err != nil {
			return Resolved{}, err
		}
		filesystem, err := NewRemote(RemoteOptions{
			Workspace:    root,
			AllowedRoots: []string{root},
			Command:      command,
			StageRoots:   resolvedStageRoots,
			RestorePaths: restorePaths,
		})
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{FileSystem: filesystem, Workspace: root, ConnectionID: connectionID, Kind: "wsl"}, nil
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
	sshConfig := SSHConfig{
		Binary:       r.options.SSHBinary,
		Host:         mapString(settings, "host"),
		Alias:        mapString(settings, "ssh_config_alias"),
		Port:         mapInt(settings, "port", 22),
		User:         mapString(settings, "user"),
		IdentityFile: mapString(settings, "identity_file"),
		ConfigFile:   mapString(settings, "config_file"),
		HelperPath:   r.options.HelperPath,
	}
	if sshConfig.HelperPath == "" {
		path, err := r.helperPath(ctx, connectionID, sshShellRunner(sshConfig))
		if err != nil {
			return Resolved{}, err
		}
		sshConfig.HelperPath = path
	}
	command, err := BuildSSHCommand(sshConfig)
	if err != nil {
		return Resolved{}, err
	}
	filesystem, err := NewRemote(RemoteOptions{
		Workspace:    root,
		AllowedRoots: []string{root},
		Command:      command,
		StageRoots:   resolvedStageRoots,
		RestorePaths: restorePaths,
	})
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{FileSystem: filesystem, Workspace: root, ConnectionID: connectionID, Kind: "ssh"}, nil
}
