package targetfs

import (
	"context"
	"strings"
	"time"
)

const runHelperTimeout = 90 * time.Second

// runHelperScript execs the helper path ($1) with the remaining arguments.
const runHelperScript = `exec "$@"`

// RunHelper runs `astrorder-helper <args...>` on a remote target (SSH or WSL)
// and returns its stdout. The helper is installed first when bundled assets
// exist, exactly as for file access. Local targets are not handled here.
func (r *Resolver) RunHelper(ctx context.Context, connectionID string, args ...string) ([]byte, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" || connectionID == LocalConnectionID {
		return nil, errorf(400, "remote_target_required", "RunHelper needs a remote connection", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, runHelperTimeout)
	defer cancel()
	run, err := r.remoteShellRunner(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	helper := r.helperPath(ctx, connectionID, run)
	return runShell(ctx, run, nil, runHelperScript, append([]string{helper}, args...)...)
}

func (r *Resolver) remoteShellRunner(ctx context.Context, connectionID string) (shellRunner, error) {
	if distro, ok := strings.CutPrefix(connectionID, "wsl:"); ok {
		return wslShellRunner(WSLConfig{Binary: r.options.WSLBinary, Distro: distro}), nil
	}
	if r.options.Store == nil {
		return nil, errorf(503, "connection_store_unavailable", "SSH connection lookup is unavailable", nil)
	}
	connection, err := r.options.Store.GetSSHConnection(ctx, connectionID)
	if err != nil {
		return nil, errorf(500, "connection_lookup_failed", "Failed to resolve SSH connection", err)
	}
	if connection == nil {
		return nil, errorf(404, "connection_not_found", "SSH 连接不存在", nil)
	}
	settings, ok := stringMap(connection["settings"])
	if !ok {
		settings = connection
	}
	return sshShellRunner(SSHConfig{
		Binary: r.options.SSHBinary, Host: mapString(settings, "host"), Alias: mapString(settings, "ssh_config_alias"),
		Port: mapInt(settings, "port", 22), User: mapString(settings, "user"), IdentityFile: mapString(settings, "identity_file"),
		ConfigFile: mapString(settings, "config_file"),
	}), nil
}
