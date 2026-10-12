package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

func runDaemon(args []string, output io.Writer) int {
	if len(args) == 0 {
		return cliFailure(output, errors.New("usage: astrorderctl daemon status|start|stop|restart|install|uninstall --root PATH --state-dir PATH [--confirm-active]"))
	}
	operation := args[0]
	switch operation {
	case "status", "start", "stop", "restart", "install", "uninstall":
	default:
		return cliFailure(output, errors.New("unknown daemon operation"))
	}

	flags := flag.NewFlagSet("daemon "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "absolute installation root")
	state := flags.String("state-dir", "", "absolute durable state directory")
	confirmActive := flags.Bool("confirm-active", false, "force stop/restart despite active sessions")
	if err := flags.Parse(args[1:]); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, errors.New("unexpected daemon positional arguments"))
	}
	if *root == "" || *state == "" || !filepath.IsAbs(*root) || !filepath.IsAbs(*state) {
		return cliFailure(output, errors.New("daemon controls require absolute --root and --state-dir"))
	}

	values, err := lifecycle.OpenEnvironment(*state)
	if err != nil {
		return cliFailure(output, fmt.Errorf("read environment and credentials: %w", err))
	}

	daemonExec := resolveDaemonExecutable(*root, values)
	if (operation == "start" || operation == "restart" || operation == "install") && daemonExec == "" {
		return cliFailure(output, errors.New("daemon executable not found or not executable under root"))
	}

	grpcTarget := values["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"]
	if grpcTarget == "" {
		return cliFailure(output, errors.New("ASTRORDER_SESSION_DAEMON_GRPC_TARGET is required; explicit loopback gRPC target must be configured"))
	}
	host, portStr, splitErr := net.SplitHostPort(grpcTarget)
	if splitErr != nil || portStr == "" {
		return cliFailure(output, fmt.Errorf("invalid gRPC target %q: must be host:port", grpcTarget))
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return cliFailure(output, fmt.Errorf("invalid gRPC target port %q", portStr))
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return cliFailure(output, fmt.Errorf("gRPC target must be loopback: %s", host))
		}
	}

	secret := values["ASTRORDER_SESSION_DAEMON_SECRET"]
	grpcCred := values["ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"]
	if grpcCred == "" {
		return cliFailure(output, errors.New("ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL is required"))
	}

	dbPath := values["ASTRORDER_SESSION_DAEMON_DB"]
	if dbPath == "" {
		dbPath = filepath.Join(*state, "data", "session-daemon.sqlite3")
	}
	cfgPath := values["ASTRORDER_SESSION_DAEMON_CONFIG"]
	if cfgPath == "" {
		cfgPath = filepath.Join(*state, "session-daemon.json")
	}

	options := lifecycle.DaemonOptions{
		ExecutablePath: daemonExec,
		ConfigPath:     cfgPath,
		DatabasePath:   dbPath,
		WorkingDir:     *root,
		StateDir:       *state,
		Port:           port,
		GRPCTarget:     grpcTarget,
		Secret:         secret,
		GRPCCredential: grpcCred,
		StartupTimeout: 30 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var result lifecycle.DaemonStatusResult
	switch operation {
	case "status":
		result, err = lifecycle.DaemonStatus(ctx, options)
	case "start":
		result, err = lifecycle.StartDaemon(ctx, options)
	case "stop":
		result, err = lifecycle.StopDaemonWithLifecycle(ctx, options, *confirmActive)
	case "restart":
		result, err = lifecycle.RestartDaemonWithLifecycle(ctx, options, *confirmActive)
	case "install":
		ctlExec, _ := os.Executable()
		if err := lifecycle.InstallStartupTask(ctlExec, *state, *root); err != nil {
			return cliFailure(output, fmt.Errorf("install startup task: %w", err))
		}
		result, err = lifecycle.StartDaemon(ctx, options)
	case "uninstall":
		if err := lifecycle.UninstallStartupTask(); err != nil {
			return cliFailure(output, fmt.Errorf("uninstall startup task: %w", err))
		}
		result, err = lifecycle.DaemonStatus(ctx, options)
	}

	if err != nil {
		return cliFailure(output, err)
	}
	return cliSuccess(output, map[string]any{"ok": true, "daemon": result})
}

func resolveDaemonExecutable(root string, values map[string]string) string {
	if configured := values["ASTRORDER_SESSION_DAEMON_EXECUTABLE"]; configured != "" {
		if filepath.IsAbs(configured) {
			if _, err := os.Stat(configured); err == nil {
				return configured
			}
		}
	}
	candidates := []string{
		filepath.Join(root, "bin", "astrorder-sessiond.exe"),
		filepath.Join(root, "session-daemon-go", ".runtime", "astrorder-sessiond.exe"),
		filepath.Join(root, "bin", "astrorder-sessiond"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
