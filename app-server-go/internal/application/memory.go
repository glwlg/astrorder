package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/memory"
	"astrorder.dev/app-server/internal/store"
)

// AppTargetResolver resolves targets starting with the local machine target.
type AppTargetResolver struct {
	Store         *store.Store
	SSHBinary     string
	HelperPath    string
	WSLBinary     string
	WSLHelperPath string

	homeMu sync.RWMutex
	homes  map[string]string // connectionID -> cached home path
}

func (r *AppTargetResolver) resolveSSHHome(ctx context.Context, conn map[string]any) (string, error) {
	id, _ := conn["id"].(string)
	if id == "" {
		return "", errors.New("connection id is required")
	}

	settings, _ := conn["settings"].(map[string]any)
	if settings == nil {
		settings = conn
	}
	host, _ := settings["host"].(string)
	alias, _ := settings["ssh_config_alias"].(string)
	user, _ := settings["user"].(string)
	port := 22
	if p, ok := settings["port"].(int); ok && p > 0 {
		port = p
	} else if p, ok := settings["port"].(int64); ok && p > 0 {
		port = int(p)
	} else if p, ok := settings["port"].(float64); ok && p > 0 {
		port = int(p)
	} else if p, ok := settings["port"].(json.Number); ok {
		if v, err := p.Int64(); err == nil && v > 0 {
			port = int(v)
		}
	}
	identity, _ := settings["identity_file"].(string)

	fingerprint := fmt.Sprintf("%s:%s:%s:%d:%s", id, host, alias, port, user)

	r.homeMu.RLock()
	cached, ok := r.homes[fingerprint]
	r.homeMu.RUnlock()
	if ok {
		return cached, nil
	}

	remoteHome, _ := conn["remote_home"].(string)
	if remoteHome == "" {
		remoteHome, _ = conn["home"].(string)
	}
	if remoteHome == "" {
		if settings != nil {
			remoteHome, _ = settings["workspace"].(string)
			if remoteHome == "" {
				remoteHome, _ = settings["remote_home"].(string)
			}
		}
	}
	if remoteHome != "" {
		resolved := filepath.ToSlash(remoteHome)
		r.homeMu.Lock()
		if r.homes == nil {
			r.homes = make(map[string]string)
		}
		r.homes[fingerprint] = resolved
		r.homeMu.Unlock()
		return resolved, nil
	}

	sshBin := r.SSHBinary
	if sshBin == "" {
		sshBin = "ssh"
	}
	targetHost := alias
	if targetHost == "" {
		targetHost = host
	}
	if targetHost == "" {
		return "", fmt.Errorf("connection %q has no host or alias", id)
	}

	helper := r.HelperPath
	if helper == "" {
		helper = "astrorder-memory-helper"
	}

	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if port > 0 {
		args = append(args, "-p", strconv.Itoa(port))
	}
	if user != "" {
		args = append(args, "-l", user)
	}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	args = append(args, targetHost, helper, "home")

	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, sshBin, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve home on SSH target %q: %w", id, err)
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" || !strings.HasPrefix(resolved, "/") {
		return "", fmt.Errorf("resolve home on SSH target %q returned invalid path %q", id, resolved)
	}

	r.homeMu.Lock()
	if r.homes == nil {
		r.homes = make(map[string]string)
	}
	r.homes[fingerprint] = resolved
	r.homeMu.Unlock()
	return resolved, nil
}

func (r *AppTargetResolver) discoverWSLDistros(ctx context.Context) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	wslBin := r.WSLBinary
	if wslBin == "" {
		wslBin = "wsl.exe"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, wslBin, "-l", "-q").Output()
	if err != nil {
		return nil
	}
	// WSL outputs UTF-16LE text on Windows
	var distros []string
	runes := make([]rune, 0, len(out)/2)
	for i := 0; i+1 < len(out); i += 2 {
		rVal := rune(out[i]) | (rune(out[i+1]) << 8)
		if rVal == 0 {
			continue
		}
		runes = append(runes, rVal)
	}
	text := string(runes)
	if len(runes) == 0 {
		text = string(out)
	}
	for _, line := range strings.Split(text, "\n") {
		d := strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if d != "" && !strings.Contains(d, " ") {
			distros = append(distros, d)
		}
	}
	return distros
}

func (r *AppTargetResolver) ResolveTargets(ctx context.Context) ([]memory.Target, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	home = filepath.ToSlash(home)

	targets := []memory.Target{
		{
			ID:     memory.TargetLocal,
			Name:   "Local",
			Kind:   memory.TargetLocal,
			Agents: []string{memory.TargetAppCodex, memory.TargetAppHermes},
			Home:   home,
		},
	}

	// WSL targets discovery
	for _, distro := range r.discoverWSLDistros(ctx) {
		targets = append(targets, memory.Target{
			ID:     "wsl:" + distro,
			Name:   "WSL · " + distro,
			Kind:   memory.TargetWSL,
			Distro: distro,
			Home:   "/home",
			Agents: []string{memory.TargetAppCodex, memory.TargetAppHermes},
		})
	}

	if r.Store != nil {
		connections, err := r.Store.ListSSHConnections(ctx)
		if err != nil {
			return nil, fmt.Errorf("list ssh connections: %w", err)
		}
		for _, conn := range connections {
			id, _ := conn["id"].(string)
			name, _ := conn["name"].(string)
			if id == "" {
				continue
			}
			if name == "" {
				name = id
			}
			remoteHome, err := r.resolveSSHHome(ctx, conn)
			if err != nil || remoteHome == "" {
				continue
			}
			targets = append(targets, memory.Target{
				ID:           "ssh:" + id,
				Name:         name,
				Kind:         memory.TargetSSH,
				ConnectionID: id,
				Home:         filepath.ToSlash(remoteHome),
				Agents:       []string{memory.TargetAppCodex},
			})
		}
	}

	return targets, nil
}

func (r *AppTargetResolver) SelectTargets(ctx context.Context, targetIDs []string) ([]memory.Target, error) {
	all, err := r.ResolveTargets(ctx)
	if err != nil {
		return nil, err
	}
	if targetIDs == nil {
		return all, nil
	}
	if len(targetIDs) == 0 {
		return []memory.Target{}, nil
	}
	byId := make(map[string]memory.Target, len(all))
	for _, t := range all {
		byId[t.ID] = t
	}
	selected := make([]memory.Target, 0, len(targetIDs))
	seen := make(map[string]bool, len(targetIDs))
	for _, id := range targetIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		t, ok := byId[id]
		if !ok {
			return nil, fmt.Errorf("target %q not found", id)
		}
		selected = append(selected, t)
	}
	return selected, nil
}

// DispatchingRunner routes commands based on Target.Kind.
type DispatchingRunner struct {
	Store         *store.Store
	LocalRunner   memory.NativeRunner
	SSHBinary     string
	WSLBinary     string
	WSLHelperPath string
}

func (r DispatchingRunner) Run(ctx context.Context, cmd memory.NativeCommand) (memory.NativeCommandResult, error) {
	switch cmd.Target.Kind {
	case "", memory.TargetLocal:
		if r.LocalRunner != nil {
			return r.LocalRunner.Run(ctx, cmd)
		}
		return LocalRunner{}.Run(ctx, cmd)

	case memory.TargetWSL:
		wslBin := r.WSLBinary
		if wslBin == "" {
			wslBin = "wsl.exe"
		}
		args := []string{"-d", cmd.Target.Distro, "--", cmd.Name}
		args = append(args, cmd.Args...)
		c := exec.CommandContext(ctx, wslBin, args...)
		if len(cmd.Env) > 0 {
			env := os.Environ()
			for k, v := range cmd.Env {
				env = append(env, k+"="+v)
			}
			c.Env = env
		}
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return memory.NativeCommandResult{
					Stdout:   stdout.String(),
					Stderr:   stderr.String(),
					ExitCode: exitErr.ExitCode(),
				}, nil
			}
			return memory.NativeCommandResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: -1,
			}, fmt.Errorf("execute WSL %s: %w", cmd.Name, err)
		}
		return memory.NativeCommandResult{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: 0,
		}, nil

	case memory.TargetSSH:
		sshBin := r.SSHBinary
		if sshBin == "" {
			sshBin = "ssh"
		}
		var targetHost, user, identity string
		var port int = 22
		if cmd.Target.ConnectionID == "" {
			return memory.NativeCommandResult{}, fmt.Errorf("SSH target %q requires ConnectionID", cmd.Target.ID)
		}
		if r.Store == nil {
			return memory.NativeCommandResult{}, fmt.Errorf("SSH target %q lookup requires store", cmd.Target.ID)
		}
		conn, err := r.Store.GetSSHConnection(ctx, cmd.Target.ConnectionID)
		if err != nil || conn == nil {
			return memory.NativeCommandResult{}, fmt.Errorf("SSH target connection %q not found: %w", cmd.Target.ConnectionID, err)
		}
		settings, _ := conn["settings"].(map[string]any)
		if settings == nil {
			settings = conn
		}
		targetHost, _ = settings["ssh_config_alias"].(string)
		if targetHost == "" {
			targetHost, _ = settings["host"].(string)
		}
		user, _ = settings["user"].(string)
		identity, _ = settings["identity_file"].(string)
		if p, ok := settings["port"].(int); ok && p > 0 {
			port = p
		} else if p, ok := settings["port"].(int64); ok && p > 0 {
			port = int(p)
		} else if p, ok := settings["port"].(float64); ok && p > 0 {
			port = int(p)
		}
		if targetHost == "" {
			return memory.NativeCommandResult{}, fmt.Errorf("SSH target %q host or alias is not configured", cmd.Target.ID)
		}
		args := []string{"-o", "BatchMode=yes"}
		if port > 0 {
			args = append(args, "-p", strconv.Itoa(port))
		}
		if user != "" {
			args = append(args, "-l", user)
		}
		if identity != "" {
			args = append(args, "-i", identity)
		}
		args = append(args, targetHost, cmd.Name)
		args = append(args, cmd.Args...)
		c := exec.CommandContext(ctx, sshBin, args...)
		configureChildProcess(c)
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err = c.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return memory.NativeCommandResult{
					Stdout:   stdout.String(),
					Stderr:   stderr.String(),
					ExitCode: exitErr.ExitCode(),
				}, nil
			}
			return memory.NativeCommandResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: -1,
			}, fmt.Errorf("execute SSH %s: %w", cmd.Name, err)
		}
		return memory.NativeCommandResult{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: 0,
		}, nil

	default:
		return memory.NativeCommandResult{}, fmt.Errorf("remote execution for target kind %q requires configured remote helper runner", cmd.Target.Kind)
	}
}

// DispatchingTargetFS routes filesystem operations based on Target.Kind.
type DispatchingTargetFS struct {
	Store         *store.Store
	LocalFS       memory.TargetFS
	SSHBinary     string
	HelperPath    string
	WSLBinary     string
	WSLHelperPath string
}

func (f DispatchingTargetFS) ReadFile(ctx context.Context, target memory.Target, filename string) ([]byte, error) {
	switch target.Kind {
	case "", memory.TargetLocal:
		if f.LocalFS != nil {
			return f.LocalFS.ReadFile(ctx, target, filename)
		}
		return memory.LocalTargetFS{}.ReadFile(ctx, target, filename)

	case memory.TargetWSL:
		transport := memory.WSLTransport{
			Executable: f.WSLBinary,
			HelperPath: f.WSLHelperPath,
		}
		if transport.HelperPath == "" {
			transport.HelperPath = "astrorder-memory-helper"
		}
		framed := memory.FramedTargetFS{Transport: transport}
		return framed.ReadFile(ctx, target, filename)

	case memory.TargetSSH:
		sshBin := f.SSHBinary
		if sshBin == "" {
			sshBin = "ssh"
		}
		helperPath := f.HelperPath
		if helperPath == "" {
			helperPath = "astrorder-memory-helper"
		}
		var targetHost, user, identity string
		var port int = 22
		if target.ConnectionID == "" {
			return nil, fmt.Errorf("SSH target %q requires ConnectionID", target.ID)
		}
		if f.Store == nil {
			return nil, fmt.Errorf("SSH target %q lookup requires store", target.ID)
		}
		conn, err := f.Store.GetSSHConnection(ctx, target.ConnectionID)
		if err != nil || conn == nil {
			return nil, fmt.Errorf("SSH target connection %q not found: %w", target.ConnectionID, err)
		}
		settings, _ := conn["settings"].(map[string]any)
		if settings == nil {
			settings = conn
		}
		targetHost, _ = settings["ssh_config_alias"].(string)
		if targetHost == "" {
			targetHost = settings["host"].(string)
		}
		user, _ = settings["user"].(string)
		identity, _ = settings["identity_file"].(string)
		if p, ok := settings["port"].(int); ok && p > 0 {
			port = p
		} else if p, ok := settings["port"].(int64); ok && p > 0 {
			port = int(p)
		} else if p, ok := settings["port"].(float64); ok && p > 0 {
			port = int(p)
		}
		if targetHost == "" {
			return nil, fmt.Errorf("SSH target %q host or alias is not configured", target.ID)
		}
		args := []string{"-o", "BatchMode=yes"}
		if port > 0 {
			args = append(args, "-p", strconv.Itoa(port))
		}
		if user != "" {
			args = append(args, "-l", user)
		}
		if identity != "" {
			args = append(args, "-i", identity)
		}
		args = append(args, targetHost, helperPath, "target-fs-v1")
		transport := sshFramedTransport{binary: sshBin, args: args}
		framed := memory.FramedTargetFS{Transport: transport}
		return framed.ReadFile(ctx, target, filename)

	default:
		return nil, fmt.Errorf("remote target filesystem for target kind %q requires configured remote transport", target.Kind)
	}
}

func (f DispatchingTargetFS) WriteFileAtomic(ctx context.Context, target memory.Target, filename string, raw []byte, mode uint32) error {
	switch target.Kind {
	case "", memory.TargetLocal:
		if f.LocalFS != nil {
			return f.LocalFS.WriteFileAtomic(ctx, target, filename, raw, mode)
		}
		return memory.LocalTargetFS{}.WriteFileAtomic(ctx, target, filename, raw, mode)

	case memory.TargetWSL:
		transport := memory.WSLTransport{
			Executable: f.WSLBinary,
			HelperPath: f.WSLHelperPath,
		}
		if transport.HelperPath == "" {
			transport.HelperPath = "astrorder-memory-helper"
		}
		framed := memory.FramedTargetFS{Transport: transport}
		return framed.WriteFileAtomic(ctx, target, filename, raw, mode)

	case memory.TargetSSH:
		sshBin := f.SSHBinary
		if sshBin == "" {
			sshBin = "ssh"
		}
		helperPath := f.HelperPath
		if helperPath == "" {
			helperPath = "astrorder-memory-helper"
		}
		var targetHost, user, identity string
		var port int = 22
		if target.ConnectionID == "" {
			return fmt.Errorf("SSH target %q requires ConnectionID", target.ID)
		}
		if f.Store == nil {
			return fmt.Errorf("SSH target %q lookup requires store", target.ID)
		}
		conn, err := f.Store.GetSSHConnection(ctx, target.ConnectionID)
		if err != nil || conn == nil {
			return fmt.Errorf("SSH target connection %q not found: %w", target.ConnectionID, err)
		}
		settings, _ := conn["settings"].(map[string]any)
		if settings == nil {
			settings = conn
		}
		targetHost, _ = settings["ssh_config_alias"].(string)
		if targetHost == "" {
			targetHost = settings["host"].(string)
		}
		user, _ = settings["user"].(string)
		identity, _ = settings["identity_file"].(string)
		if p, ok := settings["port"].(int); ok && p > 0 {
			port = p
		} else if p, ok := settings["port"].(int64); ok && p > 0 {
			port = int(p)
		} else if p, ok := settings["port"].(float64); ok && p > 0 {
			port = int(p)
		}
		if targetHost == "" {
			return fmt.Errorf("SSH target %q host or alias is not configured", target.ID)
		}
		args := []string{"-o", "BatchMode=yes"}
		if port > 0 {
			args = append(args, "-p", strconv.Itoa(port))
		}
		if user != "" {
			args = append(args, "-l", user)
		}
		if identity != "" {
			args = append(args, "-i", identity)
		}
		args = append(args, targetHost, helperPath, "target-fs-v1")
		transport := sshFramedTransport{binary: sshBin, args: args}
		framed := memory.FramedTargetFS{Transport: transport}
		return framed.WriteFileAtomic(ctx, target, filename, raw, mode)

	default:
		return fmt.Errorf("remote target filesystem for target kind %q requires configured remote transport", target.Kind)
	}
}

type sshFramedTransport struct {
	binary string
	args   []string
}

func (t sshFramedTransport) Exchange(ctx context.Context, _ memory.Target, frame []byte) ([]byte, error) {
	c := exec.CommandContext(ctx, t.binary, t.args...)
	c.Stdin = bytes.NewReader(frame)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("SSH helper transport failed: %s", msg)
	}
	return stdout.Bytes(), nil
}
// LocalRunner executes commands on the local host.
type LocalRunner struct{}

func (r LocalRunner) Run(ctx context.Context, cmd memory.NativeCommand) (memory.NativeCommandResult, error) {
	if cmd.Target.Kind != "" && cmd.Target.Kind != memory.TargetLocal {
		return memory.NativeCommandResult{}, fmt.Errorf("LocalRunner cannot run commands for non-local target kind %q", cmd.Target.Kind)
	}
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	if len(cmd.Env) > 0 {
		env := os.Environ()
		for k, v := range cmd.Env {
			env = append(env, k+"="+v)
		}
		c.Env = env
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return memory.NativeCommandResult{
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
		return memory.NativeCommandResult{
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			ExitCode: -1,
		}, fmt.Errorf("execute %s: %w", cmd.Name, err)
	}
	return memory.NativeCommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}, nil
}
