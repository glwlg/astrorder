package blackboard

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"path/filepath"
	"testing"
)

func TestNamespaceWildcardCannotReadOrDeleteNeighbour(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := NewService(st, nil)
	for _, namespace := range []string{"group:a_b", "group:axb"} {
		if _, err = service.Set(ctx, map[string]any{"namespace": namespace, "key": "fact", "value": "kept"}); err != nil {
			t.Fatal(err)
		}
	}
	value, err := service.Get(ctx, map[string]any{"namespace": "group:a_b"})
	if err != nil {
		t.Fatal(err)
	}
	if value["count"] != 1 {
		t.Fatalf("namespace escaped: %+v", value)
	}
	if _, err = service.Delete(ctx, map[string]any{"namespace": "group:a_b", "key": "*"}); err != nil {
		t.Fatal(err)
	}
	neighbour, err := st.GetPreference(ctx, "blackboard:group:axb:fact")
	if err != nil || !neighbour.Exists {
		t.Fatal("wildcard deleted another namespace")
	}
}
