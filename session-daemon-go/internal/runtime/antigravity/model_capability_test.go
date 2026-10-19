package antigravity

import (
	"context"
	"os"
	"testing"
)

func TestModelListDoesNotInventEfforts(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: dir, Allowed: []string{dir}})
	items, err := a.models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if len(item["efforts"].([]string)) != 0 {
			t.Fatalf("model listing invented mutable effort: %+v", item)
		}
	}
}
