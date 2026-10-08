package hermes

// failTransport invalidates native identity without claiming that the process
// was killed. Error stays maintenance-blocking until explicit reconciliation.
func (a *Adapter) exitedForReattach() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.failed || a.processDone == nil || a.cleanupErr != nil {
		return false
	}
	select {
	case <-a.processDone:
		select {
		case <-a.eventDone:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (a *Adapter) failTransport(err error) {
	a.mu.Lock()
	a.failed = true
	for _, session := range a.sessions {
		session.Handle = ""
		if !a.closed {
			session.Status = "error"
			if session.Metadata == nil {
				session.Metadata = map[string]any{}
			}
			session.Metadata["transport_error"] = err.Error()
		}
	}
	a.handleToSession = map[string]string{}
	for id, request := range a.pending {
		delete(a.pending, id)
		close(request.ch)
	}
	a.pendingApprovals = map[string]*approvalRecord{}
	a.mu.Unlock()
	a.eventStopOnce.Do(func() { close(a.eventStop) })
}
