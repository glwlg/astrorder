package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
)

func runProvision(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("provision", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "absolute installation root")
	state := flags.String("state-dir", "", "absolute durable state directory")
	data := flags.String("data-dir", "", "absolute durable data directory below state")
	executable := flags.String("app-executable", "", "absolute App executable under installation root")
	desktop := flags.Bool("desktop-origin", false, "allow the built-in Wails desktop origin")
	target := flags.String("grpc-target", "", "explicit loopback daemon gRPC target")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, errors.New("unexpected provision positional arguments"))
	}
	if *root == "" || *state == "" || *data == "" || *executable == "" {
		return cliFailure(output, errors.New("provision requires --root, --state-dir, --data-dir and --app-executable"))
	}
	result, err := lifecycle.Provision(lifecycle.Options{RootDir: *root, StateDir: *state, DataDir: *data, AppExecutable: *executable, GRPCTarget: *target, DesktopOrigin: *desktop})
	if err != nil {
		return cliFailure(output, err)
	}
	return cliSuccess(output, map[string]any{"ok": true, "config_path": result.ConfigPath, "credentials_path": result.CredentialsPath, "data_dir": result.DataDir})
}

func runApp(args []string, output io.Writer) int {
	return runAppContext(context.Background(), args, output)
}

func runAppContext(parent context.Context, args []string, output io.Writer) int {
	if len(args) == 0 {
		return cliFailure(output, errors.New("usage: astrorderctl app status|start|stop|restart --root PATH --state-dir PATH"))
	}
	operation := args[0]
	switch operation {
	case "status", "start", "stop", "restart":
	default:
		return cliFailure(output, errors.New("unknown App lifecycle operation"))
	}
	flags := flag.NewFlagSet("app "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "absolute installation root")
	state := flags.String("state-dir", "", "absolute durable state directory")
	if err := flags.Parse(args[1:]); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, errors.New("unexpected App positional arguments"))
	}
	if *root == "" || *state == "" || !filepath.IsAbs(*root) || !filepath.IsAbs(*state) {
		return cliFailure(output, errors.New("App controls require absolute --root and --state-dir"))
	}
	values, err := publicEnvironment(filepath.Join(*state, "production.json"))
	if err != nil {
		return cliFailure(output, err)
	}
	if operation != "status" {
		values, err = lifecycle.OpenEnvironment(*state)
		if err != nil {
			return cliFailure(output, err)
		}
	}
	executable := values["ASTRORDER_APP_EXECUTABLE"]
	if !filepath.IsAbs(executable) || !withinRoot(*root, executable) {
		return cliFailure(output, errors.New("configured App executable must be inside --root"))
	}
	port, err := strconv.Atoi(values["ASTRORDER_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return cliFailure(output, errors.New("configured App port must be between 1 and 65535"))
	}
	host := values["ASTRORDER_HOST"]
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	options := lifecycle.AppOptions{ExecutablePath: executable, Arguments: []string{"--config", filepath.Join(*state, "production.json")}, WorkingDir: *root, StateDir: *state, Address: net.JoinHostPort(host, strconv.Itoa(port)), Environment: processEnvironment(values), BrowserSecret: values["ASTRORDER_BROWSER_SECRET"], StartupTimeout: 30 * time.Second, ShutdownTimeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	var result lifecycle.AppStatusResult
	switch operation {
	case "status":
		result, err = lifecycle.AppStatus(ctx, options)
	case "start":
		result, err = lifecycle.StartApp(ctx, options)
	case "stop":
		result, err = lifecycle.StopApp(ctx, options)
	case "restart":
		result, err = lifecycle.RestartApp(ctx, options)
	}
	if err != nil {
		return cliFailure(output, err)
	}
	return cliSuccess(output, map[string]any{"ok": true, "app": result})
}

func publicEnvironment(filename string) (map[string]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("read public installation configuration: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var envelope struct {
		Environment map[string]any `json:"environment"`
	}
	if err := decoder.Decode(&envelope); err != nil || envelope.Environment == nil {
		return nil, errors.New("public installation configuration requires an environment object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("public installation configuration has trailing content")
	}
	values := make(map[string]string, len(envelope.Environment))
	for key, raw := range envelope.Environment {
		if key != strings.ToUpper(key) || !strings.HasPrefix(key, "ASTRORDER_") || strings.HasSuffix(key, "SECRET") {
			return nil, fmt.Errorf("invalid public setting %s", key)
		}
		switch value := raw.(type) {
		case string:
			values[key] = value
		case bool:
			values[key] = strconv.FormatBool(value)
		case json.Number:
			values[key] = value.String()
		default:
			return nil, fmt.Errorf("invalid public setting type %s", key)
		}
	}
	return values, nil
}

func processEnvironment(values map[string]string) []string {
	return lifecycle.ProcessEnvironment(values)
}

func withinRoot(root, filename string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func cliFailure(output io.Writer, err error) int {
	_ = json.NewEncoder(output).Encode(map[string]any{"ok": false, "error": err.Error()})
	return 1
}
func cliSuccess(output io.Writer, value any) int {
	if err := json.NewEncoder(output).Encode(value); err != nil {
		return 1
	}
	return 0
}
