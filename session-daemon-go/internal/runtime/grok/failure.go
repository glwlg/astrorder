package grok

func (c *client) fail(err error) {
	c.mu.Lock()
	if c.transportFailed || c.closed {
		c.mu.Unlock()
		return
	}
	c.transportFailed = true
	failure := c.onFailure
	c.mu.Unlock()
	if failure != nil {
		failure(err)
	}
	c.failPending(err)
	c.stopOnce.Do(func() { close(c.eventStop) })
}
func (a *Adapter) failSession(owned *ownedSession, err error) {
	owned.mu.Lock()
	defer owned.mu.Unlock()
	if owned.closed || owned.failed {
		return
	}
	owned.failed = true
	owned.status = "error"
	owned.lastError = err.Error()
	owned.pendingApprovals = map[string]any{}
	owned.approvalKinds = map[string]string{}
}
