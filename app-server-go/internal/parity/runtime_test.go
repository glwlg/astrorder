package parity

import (
	"testing"
)

func TestRuntimeCoverageRejectsDeclaredDoneWithoutRegistrations(t *testing.T) {
	root, manifest, entries := validFixture(t)
	result := VerifyRuntime(root, manifest, entries, nil)
	if result.OK || !hasCode(result, "unregistered_entrypoint") {
		t.Fatalf("declared DONE passed without running registrations: %+v", result)
	}
}

func TestRuntimeCoverageRequiresDirectGoOwner(t *testing.T) {
	root, manifest, entries := validFixture(t)
	actual := []Registration{{EntryID: entries[0].ID, Kind: entries[0].Kind, Name: entries[0].Name, Owner: "python-proxy"}}
	result := VerifyRuntime(root, manifest, entries, actual)
	if result.OK || !hasCode(result, "legacy_registration") {
		t.Fatalf("Python proxy was accepted as Go behavior: %+v", result)
	}
}

func TestRuntimeCoverageDoesNotRequireRemovedLegacySource(t *testing.T) {
	root, manifest, entries := validFixture(t)
	actual := []Registration{{EntryID: entries[0].ID, Kind: entries[0].Kind, Name: entries[0].Name, Owner: "go-app"}}
	result := VerifyRuntime(root, manifest, entries, actual)
	if !result.OK {
		t.Fatalf("direct registered behavior with removed source failed: %+v", result)
	}
}

func TestRuntimeCoverageRejectsDuplicateAndUnknownRegistrations(t *testing.T) {
	root, manifest, entries := validFixture(t)
	direct := Registration{EntryID: entries[0].ID, Kind: entries[0].Kind, Name: entries[0].Name, Owner: "go-app"}
	result := VerifyRuntime(root, manifest, entries, []Registration{direct, direct, {EntryID: "unknown", Kind: "http", Name: "GET /extra", Owner: "go-app"}})
	if result.OK || !hasCode(result, "duplicate_registration") || !hasCode(result, "unfrozen_registration") {
		t.Fatalf("invalid registration set accepted: %+v", result)
	}
}

func TestRuntimeCoverageRejectsNameOrKindMismatch(t *testing.T) {
	root, manifest, entries := validFixture(t)
	result := VerifyRuntime(root, manifest, entries, []Registration{{EntryID: entries[0].ID, Kind: "mcp", Name: entries[0].Name, Owner: "go-app"}})
	if result.OK || !hasCode(result, "registration_mismatch") {
		t.Fatalf("wrong behavior matched by ID alone: %+v", result)
	}
}
