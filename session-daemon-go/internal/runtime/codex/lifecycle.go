package codex

import (
	"fmt"
)

// failTransport reports a lost channel as an error, never as native completion.
// Queue behind existing notifications so stale output cannot follow the error.
func (runtime *Runtime) failTransport(err error) {
	runtime.mu.Lock()
	first := !runtime.transportFailed && !runtime.closed
	runtime.transportFailed, runtime.status = true, "error"
	sid := runtime.sessionID
	runtime.mu.Unlock()
	runtime.failPending(err)
	if !first {
		return
	}
	n := Notification{SessionID: sid, Method: "runtime/closed", Status: "error"}
	select {
	case runtime.notifications <- n:
	case <-runtime.done:
	}
}

func (runtime *Runtime) finishProcess(confirmed bool) {
	runtime.mu.Lock()
	runtime.exitConfirmed = confirmed
	done := runtime.processDone
	runtime.mu.Unlock()
	close(done)
}

// Recovery requires both confirmed native process exit and a retired event
// dispatcher. Losing SSH alone proves neither remote exit nor safe rebinding.
func (runtime *Runtime) recoverable() bool {
	runtime.mu.Lock()
	failed, confirmed, processDone := runtime.closed || runtime.transportFailed, runtime.exitConfirmed, runtime.processDone
	runtime.mu.Unlock()
	if !failed || !confirmed || processDone == nil {
		return false
	}
	select {
	case <-processDone:
	default:
		return false
	}
	select {
	case <-runtime.eventDone:
		return true
	default:
		return false
	}
}

func (runtime *Runtime) transportError() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || runtime.transportFailed {
		return fmt.Errorf("codex runtime is closed; native outcome is unconfirmed")
	}
	return nil
}
