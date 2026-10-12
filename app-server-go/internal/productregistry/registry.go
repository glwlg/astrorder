// Package productregistry collects entrypoints from executable product
// definitions. Each provider shares its definitions with dispatch or parsing.
// Domains without providers remain uncovered in the final parity check.
package productregistry

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"astrorder.dev/app-server/internal/agentcli"
	"astrorder.dev/app-server/internal/httpapi"
	"astrorder.dev/app-server/internal/parity"
)

// Snapshot returns the currently implemented Go HTTP and Agent CLI surfaces.
// It never loads the frozen inventory or trusts migration-matrix declarations.
// New domain providers must derive entries from their executable registries.
func Snapshot() []parity.Registration {
	result := make([]parity.Registration, 0)
	for _, route := range httpapi.Registrations() {
		kind, prefix, key := "http_routes", "http", route.Method+" "+route.Path
		if route.Kind == httpapi.KindWebSocket {
			kind, prefix, key = "websocket_routes", "ws", route.Path
		}
		id := stableID(prefix, key)
		result = append(result, parity.Registration{EntryID: id, Kind: kind, Name: id, Owner: "go-app"})
	}
	for _, command := range agentcli.Registrations() {
		id := stableID("cli", command.Command)
		result = append(result, parity.Registration{EntryID: id, Kind: "cli_commands", Name: id, Owner: "go-app"})
	}
	return result
}

// stableID implements the frozen inventory's identity format. The slug uses
// only ASCII a-z and 0-9; the digest covers the exact original key bytes.
func stableID(prefix, key string) string {
	var slug strings.Builder
	separator := false
	for _, char := range strings.ToLower(key) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			slug.WriteRune(char)
			separator = false
		} else {
			separator = true
		}
	}
	text := slug.String()
	if len(text) > 72 {
		text = text[:72]
	}
	if text == "" {
		text = "entry"
	}
	digest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s:%s:%x", prefix, text, digest[:5])
}
