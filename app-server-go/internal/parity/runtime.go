package parity

import (
	"fmt"
	"sort"
)

// Registration is an entry emitted by a constructed product registry. It must
// come from the live handler/service/command definitions, never from the matrix.
type Registration struct {
	EntryID string `json:"entry_id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Owner   string `json:"owner"`
}

// VerifyRuntime checks actual product registrations in addition to documentary
// evidence. Missing providers fail closed; the matrix cannot supply its own
// registration set. Legacy source paths remain historical references only.
func VerifyRuntime(root string, manifest Manifest, entries []Entry, actual []Registration) Result {
	result := Verify(root, manifest, entries, true)
	add := func(id, code, detail string) {
		result.OK = false
		result.Issues = append(result.Issues, Issue{ItemID: id, Code: code, Detail: detail})
	}
	frozen := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		frozen[entry.ID] = entry
	}
	seen := make(map[string]bool, len(actual))
	for _, registration := range actual {
		if seen[registration.EntryID] {
			add(registration.EntryID, "duplicate_registration", "entry has multiple registrations")
		}
		seen[registration.EntryID] = true
		entry, exists := frozen[registration.EntryID]
		if !exists {
			add(registration.EntryID, "unfrozen_registration", registration.Name)
			continue
		}
		if registration.Kind != entry.Kind || registration.Name != entry.Name {
			add(entry.ID, "registration_mismatch", fmt.Sprintf("expected %s %s, registered %s %s", entry.Kind, entry.Name, registration.Kind, registration.Name))
		}
		switch registration.Owner {
		case "go-app", "go-daemon", "agent-host-asset", "non-runtime-tool":
		default:
			add(entry.ID, "legacy_registration", "unsupported runtime owner: "+registration.Owner)
		}
	}
	for _, entry := range entries {
		if !seen[entry.ID] {
			add(entry.ID, "unregistered_entrypoint", entry.Name)
		}
	}
	sort.Slice(result.Issues, func(i, j int) bool {
		a, b := result.Issues[i], result.Issues[j]
		return fmt.Sprint(a.ItemID, a.Code, a.Detail) < fmt.Sprint(b.ItemID, b.Code, b.Detail)
	})
	return result
}
