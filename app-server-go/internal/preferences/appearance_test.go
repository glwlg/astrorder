package preferences

import (
	"context"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestExpandedAppearanceAndBoundIllustrationColorsPersist(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "appearance.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	entries := map[string]any{
		"cpu": map[string]any{"icon": "cpu", "color": "coral"},
		"git": map[string]any{"icon": "git-branch", "color": "lavender"},
	}
	for icon := range artIconColors {
		// A previous line-icon color must not override the illustration's accent.
		entries[icon] = map[string]any{"icon": icon, "color": "green"}
	}
	if _, err := Apply(ctx, st, Patch{Appearance: entries}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	for icon, color := range artIconColors {
		value := got.Appearance[icon].(map[string]any)
		if value["icon"] != icon || value["color"] != color {
			t.Fatalf("%s: stored appearance = %#v, want color %s", icon, value, color)
		}
	}
	if got.Appearance["cpu"].(map[string]any)["color"] != "coral" || got.Appearance["git"].(map[string]any)["color"] != "lavender" {
		t.Fatalf("new line-icon colors were not saved: %#v", got.Appearance)
	}
}
