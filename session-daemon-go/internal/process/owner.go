package process

func ReportOwner(emit func(sessionID, event string, payload map[string]any, status string) error, sessionID, status string, pid int) error {
	if emit == nil || sessionID == "" || pid <= 0 {
		return nil
	}
	return emit(sessionID, "runtime.owner", map[string]any{"owner_pid": pid}, "")
}
