package application

import (
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/memory"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var _ memory.TargetSelector = MemoryEnvironment{}
var _ memory.TargetFS = MemoryEnvironment{}
var _ memory.NativeRunner = MemoryEnvironment{}

func TestMemoryEnvironmentLocalFileRoundTrip(t *testing.T) {
	m := MemoryEnvironment{Resolver: &envtarget.Resolver{}}
	target := memory.Target{ID: "local", Kind: memory.TargetLocal}
	name := filepath.Join(t.TempDir(), "config.json")
	ctx := context.Background()
	if _, err := m.ReadFile(ctx, target, name); !errors.Is(err, memory.ErrTargetFileNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := m.WriteFileAtomic(ctx, target, name, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := m.ReadFile(ctx, target, name)
	if err != nil || string(data) != "{}" {
		t.Fatalf("%s %v", data, err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatal(err)
	}
}
