package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"astrorder.dev/session-daemon/internal/configuration"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
)

func main() {
	secret := os.Getenv("ASTRORDER_SESSION_DAEMON_SECRET")
	if secret == "" {
		log.Fatal("ASTRORDER_SESSION_DAEMON_SECRET is required")
	}
	path := os.Getenv("ASTRORDER_SESSION_DAEMON_DB")
	if path == "" {
		log.Fatal("ASTRORDER_SESSION_DAEMON_DB is required")
	}
	daemon, err := protocol.Open(secret, path)
	if err != nil {
		log.Fatal(err)
	}
	defer daemon.Close()
	if connectorSecret := os.Getenv("ASTRORDER_SESSION_DAEMON_CONNECTOR_SECRET"); connectorSecret != "" {
		daemon.SetConnectorSecret(connectorSecret)
	}
	if path := os.Getenv("ASTRORDER_SESSION_DAEMON_CONFIG"); path != "" {
		config, err := configuration.Load(path)
		if err != nil {
			log.Print(err)
			return
		}
		if err = config.Register(daemon); err != nil {
			log.Print(err)
			return
		}
	}

	grpcTarget := os.Getenv("ASTRORDER_SESSION_DAEMON_GRPC_TARGET")
	grpcCredential := os.Getenv("ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL")
	if err := validateGRPCConfig(grpcTarget, grpcCredential); err != nil {
		log.Print(err)
		return
	}

	var grpcServer *grpc.Server
	var grpcListener net.Listener
	if grpcTarget != "" {
		grpcServer, err = grpcapi.NewServer(grpcapi.Config{Credential: grpcCredential}, daemon)
		if err != nil {
			log.Print(err)
			return
		}
		grpcListener, err = net.Listen("tcp", grpcTarget)
		if err != nil {
			log.Print(err)
			return
		}
		defer grpcServer.Stop()
		defer grpcListener.Close()
		go func() {
			if serveErr := grpcServer.Serve(grpcListener); serveErr != nil {
				log.Print(serveErr)
			}
		}()
	}

	finished := make(chan struct{})
	defer close(finished)
	go monitorMemory(path, finished)
	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-daemon.ShutdownRequested():
			if grpcServer != nil {
				drainGRPC(grpcServer, 3*time.Second)
			}
			if grpcListener != nil {
				_ = grpcListener.Close()
			}
			if err := daemon.Close(); err != nil {
				log.Print(err)
			}
			close(shutdownDone)
		case <-finished:
		}
	}()
	<-shutdownDone
}

func validateGRPCConfig(target, credential string) error {
	if target == "" || credential == "" {
		return fmt.Errorf("gRPC target and credential must be configured together")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("invalid gRPC target: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("gRPC target must use an explicit loopback IP")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("gRPC target must use an explicit non-zero port")
	}
	return nil
}
