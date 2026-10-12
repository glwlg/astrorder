package protocol

// The caller holds d.mu, serializing binding with connector ingress.
func (d *Daemon) connectorStatusLocked(sessionID string, event map[string]any) (string, error) {
	if raw, exists := event["status"]; exists {
		value, ok := raw.(string)
		if !ok || !validFacadeStatus(value) {
			return "", ErrConnectorEventInvalid
		}
		return value, nil
	}
	if event["type"] == "session.upsert" {
		if data, ok := event["data"].(map[string]any); ok {
			if raw, exists := data["status"]; exists {
				value, ok := raw.(string)
				if !ok || !validFacadeStatus(value) {
					return "", ErrConnectorEventInvalid
				}
				return value, nil
			}
		}
	}
	if current, exists := d.registry.Sessions()[sessionID]; exists {
		if !validFacadeStatus(current.Status) {
			return "", ErrConnectorEventInvalid
		}
		return current.Status, nil
	}
	if current, exists := d.journal.Statuses()[sessionID]; exists {
		value, ok := current["status"].(string)
		if !ok || !validFacadeStatus(value) {
			return "", ErrConnectorEventInvalid
		}
		return value, nil
	}
	return "idle", nil
}
