package protocol

import (
	"strings"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

const defaultSessionControlTimeout = 30 * time.Second

func (d *Daemon) sessionControlTimeout(request map[string]any) time.Duration {
	const ordinary = defaultSessionControlTimeout
	id, _ := request["session_id"].(string)
	kind, err := d.registry.ResolveSessionOwner(id, core.SessionReadIdentity{})
	if err != nil || (kind != "hermes" && kind != "ssh") {
		return ordinary
	}
	action, _ := request["action"].(string)
	if action != "session.compact" {
		if action != "session.send" {
			return ordinary
		}
		switch attachments := request["attachments"].(type) {
		case nil:
		case []any:
			if len(attachments) != 0 {
				return ordinary
			}
		case []map[string]any:
			if len(attachments) != 0 {
				return ordinary
			}
		default:
			return ordinary
		}
		text, _ := request["text"].(string)
		words := strings.Fields(text)
		if len(words) == 0 || (words[0] != "/compress" && words[0] != "/compact") {
			return ordinary
		}
	}
	// Native compression waits synchronously for a summary and defaults to a
	// 600-second total ceiling. Leave time for native cleanup and the receipt.
	return 11 * time.Minute
}
