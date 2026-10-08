package hermes

// queueEvent preserves native notification order without putting projection IO
// on the RPC response reader. Overflow fails the transport, never drops frames.
func (a *Adapter) queueEvent(msg *rpcMessage) bool {
	select {
	case <-a.eventStop:
		return false
	case a.eventQueue <- msg:
		return true
	default:
		a.mu.Lock()
		a.failed = true
		for _, s := range a.sessions {
			s.Status = "error"
		}
		stdin, cmd := a.stdin, a.cmd
		a.mu.Unlock()
		if stdin != nil {
			stdin.Close()
		}
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
		return false
	}
}
func (a *Adapter) dispatchEvents() {
	defer close(a.eventDone)
	for {
		select {
		case <-a.eventStop:
			return
		case msg := <-a.eventQueue:
			if msg.Method == "event" {
				a.handleGatewayEvent(msg.Params)
			} else {
				a.handleServerRequest(msg)
			}
		}
	}
}
