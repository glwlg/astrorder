package main

import "time"

type grpcDrainer interface {
	GracefulStop()
	Stop()
}

// Drain accepted responses before the process owner closes the daemon. Stalled
// clients cannot keep an explicitly authorized shutdown alive indefinitely.
func drainGRPC(server grpcDrainer, timeout time.Duration) {
	done := make(chan struct{})
	go func() { server.GracefulStop(); close(done) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		server.Stop()
		<-done
	}
}
