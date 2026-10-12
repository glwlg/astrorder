package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"astrorder.dev/app-server/internal/agentremote"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := agentremote.ResolveConfig(os.LookupEnv, os.ReadFile)
	var client *agentremote.Client
	if err == nil {
		client, _ = agentremote.NewClient(cfg)
	}

	if err := agentremote.RunStdio(ctx, os.Stdin, os.Stdout, client); err != nil {
		fmt.Fprintf(os.Stderr, "astrorder-mcp: %v\n", err)
		os.Exit(1)
	}
}
