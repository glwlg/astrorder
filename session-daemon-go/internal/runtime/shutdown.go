package runtime

// BeginShutdown closes native-work admission before checking the owned-session
// snapshot. Requests already admitted remain visible through inflight/Busy.
func (r *Registry) BeginShutdown(confirmActive bool) error {
	r.mu.Lock()
	if r.draining || !confirmActive && r.inflight > 0 {
		r.mu.Unlock()
		return ErrRuntimeBusy
	}
	r.draining = true
	r.mu.Unlock()
	if !confirmActive && r.Busy() {
		r.CancelShutdown()
		return ErrRuntimeBusy
	}
	return nil
}

// CancelShutdown releases a provisional gate when authorization or the
// transport fails. A successfully signaled daemon keeps admission closed.
func (r *Registry) CancelShutdown() {
	r.mu.Lock()
	r.draining = false
	r.mu.Unlock()
}
