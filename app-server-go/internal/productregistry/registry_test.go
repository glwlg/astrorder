package productregistry

import (
	"testing"

	"astrorder.dev/app-server/internal/agentcli"
	"astrorder.dev/app-server/internal/httpapi"
	"astrorder.dev/app-server/internal/parity"
)

func TestSnapshotUsesExecutableHTTPAndCLIRegistries(t *testing.T) {
	actual := Snapshot()
	want := len(httpapi.Registrations()) + len(agentcli.Registrations())
	if len(actual) != want {
		t.Fatalf("registry count=%d want=%d", len(actual), want)
	}
	seen := map[string]parity.Registration{}
	for _, item := range actual {
		if _, ok := seen[item.EntryID]; ok {
			t.Fatalf("duplicate registry entry: %+v", item)
		}
		seen[item.EntryID] = item
		if item.Name != item.EntryID || item.Owner != "go-app" {
			t.Fatalf("wrong registration: %+v", item)
		}
	}
	for id, kind := range map[string]string{
		"http:get-health:74fac65f35":               "http_routes",
		"http:post-api-v1-auth-session:89fe2dc88b": "http_routes",
		"cli:sessions-list:4126147f53":             "cli_commands",
	} {
		item, ok := seen[id]
		if !ok || item.Kind != kind {
			t.Fatalf("missing executable registration: %s => %+v", id, item)
		}
	}
	actual[0].EntryID = "caller-mutated"
	for _, item := range Snapshot() {
		if item.EntryID == "caller-mutated" {
			t.Fatal("snapshot mutation changed product registration")
		}
	}
}
