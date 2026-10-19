package main

import (
	"astrorder.dev/app-server/internal/kernelcontrol"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runKernelOperation(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("kernel-operation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "installation root")
	state := flags.String("state-dir", "", "durable state")
	id := flags.String("id", "", "operation id")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*root) || !filepath.IsAbs(*state) || *id == "" {
		return cliFailure(output, errors.New("kernel-operation requires absolute root, state-dir and id"))
	}
	executable, err := os.Executable()
	if err != nil {
		return cliFailure(output, err)
	}
	s := &kernelcontrol.Service{Root: *root, StateDir: *state, Controller: executable}
	s.Run = func(ctx context.Context, target, action string) (map[string]any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var buffer bytes.Buffer
		args := []string{action, "--root", *root, "--state-dir", *state}
		if target == "daemon" && (action == "restart" || action == "update") {
			args = append(args, "--confirm-active")
		}
		code := 0
		if target == "app" {
			code = runAppContext(ctx, args, &buffer)
		} else {
			code = runDaemonContext(ctx, args, &buffer)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(buffer.Bytes(), &envelope); err != nil {
			return nil, err
		}
		if code != 0 {
			var detail string
			_ = json.Unmarshal(envelope["error"], &detail)
			return nil, fmt.Errorf("%s", detail)
		}
		var result map[string]any
		if err := json.Unmarshal(envelope[target], &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	// Let the initiating HTTP response flush before restarting its own App.
	time.Sleep(500 * time.Millisecond)
	if err = s.Execute(ctx, *id); err != nil {
		return cliFailure(output, err)
	}
	op, err := s.Get(*id)
	if err != nil {
		return cliFailure(output, err)
	}
	return cliSuccess(output, op)
}
