package application

import (
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/memory"
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// MemoryEnvironment shares the proven settings transport instead of requiring
// an unshipped memory-specific executable on every remote machine.
type MemoryEnvironment struct{ Resolver *envtarget.Resolver }

func (m MemoryEnvironment) ResolveTargets(ctx context.Context) ([]memory.Target, error) {
	rows, err := m.Resolver.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]memory.Target, 0, len(rows))
	for _, row := range rows {
		env, err := m.Resolver.Open(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		paths, err := env.Paths(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve memory target %s: %w", row.ID, err)
		}
		out = append(out, memory.Target{ID: row.ID, Name: row.Name, Kind: row.Kind, ConnectionID: row.ConnectionID, Distro: row.Distro, State: row.State, Home: paths.Home, HermesProfile: row.Profile, Agents: []string{memory.TargetAppCodex, memory.TargetAppHermes}})
	}
	return out, nil
}
func (m MemoryEnvironment) SelectTargets(ctx context.Context, ids []string) ([]memory.Target, error) {
	all, err := m.ResolveTargets(ctx)
	if err != nil || ids == nil {
		return all, err
	}
	byID := make(map[string]memory.Target, len(all))
	for _, target := range all {
		byID[target.ID] = target
	}
	selected := make([]memory.Target, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		target, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("target %q not found", id)
		}
		selected = append(selected, target)
		seen[id] = true
	}
	return selected, nil
}

func (m MemoryEnvironment) ReadFile(ctx context.Context, t memory.Target, name string) ([]byte, error) {
	env, err := m.Resolver.Open(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	data, err := env.Read(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, memory.ErrTargetFileNotFound
	}
	return data, err
}
func (m MemoryEnvironment) WriteFileAtomic(ctx context.Context, t memory.Target, name string, data []byte, mode uint32) error {
	env, err := m.Resolver.Open(ctx, t.ID)
	if err != nil {
		return err
	}
	return env.Write(ctx, name, data, fs.FileMode(mode))
}
func (m MemoryEnvironment) Run(ctx context.Context, c memory.NativeCommand) (memory.NativeCommandResult, error) {
	env, err := m.Resolver.Open(ctx, c.Target.ID)
	if err != nil {
		return memory.NativeCommandResult{}, err
	}
	result, err := env.Run(ctx, envtarget.Command{Name: c.Name, Args: c.Args, Env: c.Env})
	return memory.NativeCommandResult{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.Code}, err
}
