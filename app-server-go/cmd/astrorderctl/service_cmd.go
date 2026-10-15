package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/lifecycle"
)

func runService(args []string, output io.Writer) int {
	if len(args) < 2 {
		return cliFailure(output, errors.New("usage: astrorderctl service <app|daemon> <status|start|stop|restart|update-check|update|install> [--confirm-active] --root PATH --state-dir PATH"))
	}
	target := args[0]
	action := args[1]
	rest := args[2:]

	flags := flag.NewFlagSet("service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmActive := flags.Bool("confirm-active", false, "force stop/restart despite active sessions")
	root := flags.String("root", "", "installation root")
	state := flags.String("state-dir", "", "state dir")
	if err := flags.Parse(rest); err != nil {
		return cliFailure(output, fmt.Errorf("service flag parse: %w", err))
	}
	if flags.NArg() != 0 {
		return cliFailure(output, fmt.Errorf("unexpected service arguments: %v", flags.Args()))
	}

	switch target {
	case "app":
		appArgs := []string{action}
		if *root != "" {
			appArgs = append(appArgs, "--root", *root)
		}
		if *state != "" {
			appArgs = append(appArgs, "--state-dir", *state)
		}
		var buf bytes.Buffer
		code := runApp(appArgs, &buf)
		return flattenServiceResponse(output, buf.Bytes(), target, action, code)
	case "daemon":
		daemonArgs := []string{action}
		if *confirmActive {
			daemonArgs = append(daemonArgs, "--confirm-active")
		}
		if *root != "" {
			daemonArgs = append(daemonArgs, "--root", *root)
		}
		if *state != "" {
			daemonArgs = append(daemonArgs, "--state-dir", *state)
		}
		var buf bytes.Buffer
		code := runDaemon(daemonArgs, &buf)
		return flattenServiceResponse(output, buf.Bytes(), target, action, code)
	default:
		return cliFailure(output, fmt.Errorf("unknown service target %q", target))
	}
}

func flattenServiceResponse(output io.Writer, raw []byte, target, action string, code int) int {
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		return cliFailure(output, fmt.Errorf("invalid service result: %s", strings.TrimSpace(string(raw))))
	}

	result := map[string]any{
		"ok":     resp["ok"] == true,
		"target": target,
		"action": action,
	}

	if val, ok := resp[target].(map[string]any); ok {
		for k, v := range val {
			result[k] = v
		}
	} else if errMsg, ok := resp["error"].(string); ok {
		result["message"] = errMsg
		result["state"] = "unknown"
	}

	_ = json.NewEncoder(output).Encode(result)
	return code
}

func runLogin(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", "", "absolute state dir")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if *state == "" || !filepath.IsAbs(*state) {
		return cliFailure(output, errors.New("login requires absolute --state-dir"))
	}

	allEnv, err := lifecycle.OpenEnvironment(*state)
	if err != nil {
		return cliFailure(output, err)
	}
	token := allEnv["ASTRORDER_BROWSER_SECRET"]
	if token == "" {
		return cliFailure(output, errors.New("browser secret not found in credentials"))
	}

	_, _ = io.WriteString(output, token)
	return 0
}
