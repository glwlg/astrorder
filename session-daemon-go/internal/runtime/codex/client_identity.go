package codex

// Codex records the initialize clientInfo.name as the thread originator and
// sends it to model gateways as the User-Agent / originator header. Astrorder
// only schedules the real Codex app-server, so it must not claim a name of its
// own: gateways and the Codex UI would then classify its traffic as a different
// client from the Codex it drives.
const (
	clientName    = "codex"
	clientTitle   = "Codex"
	clientVersion = "0.1.0"
)

func clientInfo() map[string]any {
	return map[string]any{"name": clientName, "title": clientTitle, "version": clientVersion}
}
