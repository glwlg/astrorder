package hermes

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"astrorder.dev/session-daemon/internal/runtime/activity"
)

// ActivityDir returns the directory the Hermes plugin spools lifecycle events
// into: <hermes home>/astrorder-observer/events. HERMES_HOME wins, then the
// platform default (%LOCALAPPDATA%\hermes on Windows, ~/.hermes elsewhere).
func ActivityDir() string {
	home := strings.TrimSpace(os.Getenv("HERMES_HOME"))
	if home == "" {
		suffix := os.Getenv("HERMES_DATA_DIR_SUFFIX")
		if runtime.GOOS == "windows" {
			base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
			if base == "" {
				userHome, _ := os.UserHomeDir()
				base = filepath.Join(userHome, "AppData", "Local")
			}
			home = filepath.Join(base, "hermes"+suffix)
		} else {
			userHome, _ := os.UserHomeDir()
			home = filepath.Join(userHome, ".hermes"+suffix)
		}
	}
	return filepath.Join(home, "astrorder-observer", "events")
}

// StartActivityObserver reports user-launched Hermes activity, which writes the
// native state.db without telling Astrorder. It never changes session status.
func StartActivityObserver(ctx context.Context, agentID string, emit activity.Emit, known func(string) bool, poll time.Duration) {
	activity.Watch(ctx, activity.Options{
		Dir:     ActivityDir(),
		AgentID: agentID,
		Emit:    emit,
		Known:   known,
		Poll:    poll,
	})
}
