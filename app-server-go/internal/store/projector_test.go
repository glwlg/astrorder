package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProjectorRegistrationRetainsClosureAndRejectsUnregisteredHandle(t *testing.T) {
	ctx := context.Background()
	first, err := Open(filepath.Join(t.TempDir(), "first.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(filepath.Join(t.TempDir(), "second.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	marker := new(string)
	*marker = "retained closure"
	projector, err := first.RegisterProjector("closure", func(tx *sql.Tx) ([]Event, error) {
		_, err := tx.ExecContext(ctx, "INSERT INTO workspace_preferences(key,value) VALUES(?,?)", "registered", `"`+*marker+`"`)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.RegisterProjector("closure", func(*sql.Tx) ([]Event, error) { t.Fatal("duplicate name callback executed"); return nil, nil }); err == nil {
		t.Fatal("duplicate projection name accepted")
	}
	if _, err := first.RegisterProjector("nil", nil); err == nil {
		t.Fatal("nil projector accepted")
	}
	runtime.GC()
	if _, err := first.ApplyFrame(ctx, "epoch", "stream", 1, nil); err == nil {
		t.Fatal("missing projector accepted")
	}
	if _, err := second.ApplyFrame(ctx, "epoch", "stream", 1, projector); err == nil {
		t.Fatal("foreign Store projector accepted")
	}
	if _, err := first.ApplyFrame(ctx, "epoch", "stream", 1, projector); err != nil {
		t.Fatal(err)
	}
	preference, err := first.GetPreference(ctx, "registered")
	if err != nil || !preference.Exists || preference.Value != "retained closure" {
		t.Fatalf("closure state lost: preference=%+v err=%v", preference, err)
	}
	checkpoint, err := second.GetCheckpoint(ctx, "epoch", "stream")
	if err != nil || checkpoint != 0 {
		t.Fatalf("foreign projector advanced checkpoint: %d err=%v", checkpoint, err)
	}
}
