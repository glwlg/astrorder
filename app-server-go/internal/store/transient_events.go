package store

// IsTransientEvent identifies live transcript payloads which must never enter App SQLite.
func IsTransientEvent(kind string) bool {
	switch kind {
	case "message.upsert", "message.delta", "task.live", "bot_group.message", "hermes.stream", "hermes.notification", "codex.notification", "grok.notification", "hermes.command_complete", "grok.completed", "codex.command_complete", "hermes.compaction":
		return true
	}
	return false
}
