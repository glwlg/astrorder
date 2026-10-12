package preferences

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestPatchImportTombstonesDedupAndValidation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "preferences.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	legacy := Patch{
		Appearance:     map[string]any{"project:a\x00b": map[string]any{"icon": "cloud", "color": "orange"}},
		SessionPins:    map[string]any{"a\x00session": true},
		PinnedProjects: list("p", "p", "q"),
		ProjectOrder:   list("p", "q", "p"),
	}
	got, err := Import(ctx, st, legacy)
	if err != nil {
		t.Fatal(err)
	}
	want := State{
		Appearance:     map[string]any{"project:a\x00b": map[string]any{"icon": "cloud", "color": "orange"}},
		SessionPins:    map[string]any{"a\x00session": true},
		PinnedProjects: []any{"p", "q"},
		ProjectOrder:   []any{"p", "q"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("import=%#v want %#v", got, want)
	}

	removed, err := Apply(ctx, st, Patch{
		Appearance:     map[string]any{"project:a\x00b": nil},
		SessionPins:    map[string]any{"a\x00session": false, "other": true},
		PinnedProjects: list(),
		ProjectOrder:   list("q", "p", "q"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Appearance) != 0 || !reflect.DeepEqual(removed.SessionPins, map[string]any{"a\x00session": false, "other": true}) || !reflect.DeepEqual(removed.PinnedProjects, []any{}) || !reflect.DeepEqual(removed.ProjectOrder, []any{"q", "p"}) {
		t.Fatalf("removed=%#v", removed)
	}
	if _, err := Import(ctx, st, legacy); err != nil {
		t.Fatal(err)
	}
	persisted, err := Read(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, removed) {
		t.Fatalf("import overwrote tombstones: got %#v want %#v", persisted, removed)
	}

	for name, invalid := range map[string]Patch{
		"bad color":   {Appearance: map[string]any{"p": map[string]any{"color": "url(evil)", "icon": "folder"}}},
		"bad icon":    {Appearance: map[string]any{"p": map[string]any{"color": "blue", "icon": "shell"}}},
		"bad pin":     {SessionPins: map[string]any{"p": "true"}},
		"empty key":   {Appearance: map[string]any{"": map[string]any{"color": "", "icon": "folder"}}},
		"long key":    {SessionPins: map[string]any{string(make([]byte, 1025)): true}},
		"empty order": {ProjectOrder: list("")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Apply(ctx, st, invalid); err == nil {
				t.Fatal("invalid patch was accepted")
			}
		})
	}
	afterInvalid, err := Read(ctx, st)
	if err != nil || !reflect.DeepEqual(afterInvalid, removed) {
		t.Fatalf("invalid patch mutated state: %#v err=%v", afterInvalid, err)
	}

	rows, err := st.QueryRows(ctx, "SELECT key,value FROM workspace_preferences ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, row := range rows {
		raw, err := json.Marshal(row["value"])
		if err != nil {
			t.Fatal(err)
		}
		values[row["key"].(string)] = string(raw)
	}
	if values["appearance:project:a\x00b"] != "null" || values["session_pins:a\x00session"] != "false" || values["pinned_projects"] != "[]" {
		t.Fatalf("tombstones not durably represented: %#v", values)
	}
}

func TestEmptyImportDoesNotInitializeAwayExistingLists(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "import.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := Apply(ctx, st, Patch{PinnedProjects: list("p"), ProjectOrder: list("p", "q")}); err != nil {
		t.Fatal(err)
	}
	got, err := Import(ctx, st, Patch{PinnedProjects: list(), ProjectOrder: list()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.PinnedProjects, []any{"p"}) || !reflect.DeepEqual(got.ProjectOrder, []any{"p", "q"}) {
		t.Fatalf("empty import overwrote lists: %#v", got)
	}
}

func list(values ...string) *[]string { return &values }

func TestPatchReadFailureRollsBackChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "readback.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES('corrupt-fixture','invalid-json')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, st, Patch{SessionPins: map[string]any{"new-pin": true}}); err == nil {
		t.Fatal("invalid readback JSON accepted")
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM workspace_preferences WHERE "key"='session_pins:new-pin'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed preference write committed changes: count=%d", count)
	}
}
