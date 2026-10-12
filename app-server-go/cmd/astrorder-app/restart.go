package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

// Use the same persisted environment and authority-checked lifecycle as the CLI.
func daemonRestart(configPath string) func(context.Context, bool) error {
	return func(ctx context.Context, confirm bool) error {
		if configPath == "" {
			return fmt.Errorf("小内核重启需要显式的生产配置路径")
		}
		state := filepath.Dir(configPath)
		values, err := lifecycle.OpenEnvironment(state)
		if err != nil {
			return fmt.Errorf("读取生命周期配置失败: %w", err)
		}
		executable := values["ASTRORDER_SESSION_DAEMON_EXECUTABLE"]
		if executable == "" {
			return fmt.Errorf("未配置小内核可执行文件")
		}
		_, portText, err := net.SplitHostPort(values["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"])
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return err
		}
		working, err := os.Executable()
		if err != nil {
			return err
		}
		options := lifecycle.DaemonOptions{ExecutablePath: executable, ConfigPath: values["ASTRORDER_SESSION_DAEMON_CONFIG"], DatabasePath: values["ASTRORDER_SESSION_DAEMON_DB"], WorkingDir: filepath.Dir(working), StateDir: state, Port: port, GRPCTarget: values["ASTRORDER_SESSION_DAEMON_GRPC_TARGET"], Secret: values["ASTRORDER_SESSION_DAEMON_SECRET"], GRPCCredential: values["ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL"], StartupTimeout: 30 * time.Second}
		if options.ConfigPath == "" {
			options.ConfigPath = filepath.Join(state, "session-daemon.json")
		}
		if options.DatabasePath == "" {
			options.DatabasePath = filepath.Join(state, "data", "session-daemon.sqlite3")
		}
		_, err = lifecycle.RestartDaemonWithLifecycle(ctx, options, confirm)
		return err
	}
}
