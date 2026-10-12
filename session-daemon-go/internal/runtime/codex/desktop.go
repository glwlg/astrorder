package codex

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/activity"
)

// StartDesktopStopObserver monitors the Codex hook spool for desktop stop
// notifications only. Prefer StartDesktopObserver, which also reports activity.
func StartDesktopStopObserver(ctx context.Context, config Config, emit Emitter, pollInterval time.Duration) {
	StartDesktopObserver(ctx, config, emit, nil, pollInterval)
}

// StartDesktopObserver monitors ~/.codex/astrorder-observer/events. A Stop hook
// becomes a turn/completed notification; every other hook becomes a
// native.activity event so the App can move the session's last-activity time
// without waiting for a poll. See activity.Options.Known for status handling.
func StartDesktopObserver(ctx context.Context, config Config, emit Emitter, known func(string) bool, pollInterval time.Duration) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".codex")
	}
	activity.Watch(ctx, activity.Options{
		Dir:     filepath.Join(home, "astrorder-observer", "events"),
		AgentID: config.AgentID,
		Emit:    activity.Emit(emit),
		Known:   known,
		Poll:    pollInterval,
		OnStop:  StopNotifier(config.AgentID, emit),
	})
}

// StopNotifier turns a Codex Stop hook into a turn/completed notification. Local
// and remote hosts share it so a finished turn looks the same everywhere.
func StopNotifier(agentID string, emit Emitter) func(sessionID, turnID, eventID string) {
	return func(sessionID, turnID, _ string) {
		if emit == nil {
			return
		}
		_ = emit(sessionID, "codex.notification", map[string]any{
			"agent_id": agentID,
			"frame": map[string]any{
				"method": "turn/completed",
				"params": map[string]any{"turn": map[string]any{"id": turnID, "status": "completed"}},
			},
		}, "idle")
	}
}
