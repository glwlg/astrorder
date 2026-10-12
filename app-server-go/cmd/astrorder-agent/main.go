package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"astrorder.dev/app-server/internal/agentcli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(agentcli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv))
}
