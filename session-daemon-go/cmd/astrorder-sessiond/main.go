package main

import (
	"log"
	"net/http"
	"os"

	"astrorder.dev/session-daemon/internal/configuration"
	"astrorder.dev/session-daemon/internal/protocol"
)

func main() {
	secret := os.Getenv("ASTRORDER_SESSION_DAEMON_SECRET")
	if secret == "" {
		log.Fatal("ASTRORDER_SESSION_DAEMON_SECRET is required")
	}
	address := "127.0.0.1:30009"
	if port := os.Getenv("ASTRORDER_SESSION_DAEMON_PORT"); port != "" {
		address = "127.0.0.1:" + port
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
	server := &http.Server{Addr: address, Handler: daemon.Handler()}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-daemon.ShutdownRequested():
			if err := daemon.Close(); err != nil {
				log.Print(err)
			}
			_ = server.Close()
		case <-finished:
		}
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Print(err)
	}
}
