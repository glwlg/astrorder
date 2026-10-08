package protocol

import (
	"astrorder.dev/session-daemon/internal/modelconfig"
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type modelReloadFixture struct {
	status  string
	reloads int
}

func (a *modelReloadFixture) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "model-owned", Status: a.status}, nil
}
func (a *modelReloadFixture) Snapshot() map[string]core.Result {
	return map[string]core.Result{"model-owned": {SessionID: "model-owned", Status: a.status}}
}
func (a *modelReloadFixture) ReloadConfig(context.Context) error { a.reloads++; return nil }

type asynchronousModelFixture struct {
	idle     atomic.Bool
	reloaded chan struct{}
}

func (a *asynchronousModelFixture) Create(context.Context, core.Request) (core.Result, error) {
	return core.Result{SessionID: "async-model", Status: "running"}, nil
}
func (a *asynchronousModelFixture) Snapshot() map[string]core.Result {
	status := "running"
	if a.idle.Load() {
		status = "idle"
	}
	return map[string]core.Result{"async-model": {SessionID: "async-model", Status: status}}
}
func (a *asynchronousModelFixture) ReloadConfig(context.Context) error {
	select {
	case a.reloaded <- struct{}{}:
	default:
	}
	return nil
}
func TestPendingModelReloadDrainsAfterNativeIdleEvent(t *testing.T) {
	d := New("test-key")
	defer d.Close()
	a := &asynchronousModelFixture{reloaded: make(chan struct{}, 1)}
	d.RegisterRuntime("codex", a)
	d.registry.Create(context.Background(), core.Request{AgentType: "codex"})
	if err := d.ConfigureModelConfig(modelconfig.Config{AllowedRoots: []string{t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	auth := true
	response, _ := d.handle([]byte(`{"action":"model_config.reload","agents":["codex"]}`), &auth)
	if response["action"] != "model_config.reload.result" || response["result"].(map[string]any)["deferred"] != true {
		t.Fatal(response)
	}
	a.idle.Store(true)
	if err := d.Emit("async-model", "fixture.completed", map[string]any{}, "idle"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.reloaded:
	case <-time.After(time.Second):
		t.Fatal("pending reload required a new client request")
	}
}

func TestModelConfigReloadUsesOwningNativeActivity(t *testing.T) {
	home := t.TempDir()
	d := New("test-key")
	a := &modelReloadFixture{status: "running"}
	d.RegisterRuntime("codex", a)
	if _, err := d.registry.Create(context.Background(), core.Request{AgentType: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := d.ConfigureModelConfig(modelconfig.Config{AllowedRoots: []string{home}}); err != nil {
		t.Fatal(err)
	}
	auth := true
	request := []byte(`{"action":"model_config.reload","agents":["codex"],"request_id":"reload"}`)
	response, _ := d.handle(request, &auth)
	if response["action"] != "model_config.reload.result" {
		t.Fatal(response)
	}
	if response["result"].(map[string]any)["deferred"] != true || a.reloads != 0 {
		t.Fatal("active native transport was reloaded", response)
	}
	a.status = "idle"
	response, _ = d.handle(request, &auth)
	if response["action"] != "model_config.reload.result" || a.reloads != 1 || response["result"].(map[string]any)["deferred"] != false {
		t.Fatal("idle reload failed", response, a.reloads)
	}
}

func TestRemoteModelConfigTargetCannotSilentlyWriteLocalHome(t *testing.T) {
	home := t.TempDir()
	d := New("test-key")
	if err := d.ConfigureModelConfig(modelconfig.Config{AllowedRoots: []string{home}}); err != nil {
		t.Fatal(err)
	}
	auth := true
	for _, target := range []any{nil, []any{}, map[string]any{"kind": "ssh", "settings": map[string]any{"target": "fixture"}}, map[string]any{"kind": "wsl", "distro": "fixture"}, map[string]any{"kind": "unknown"}} {
		raw, _ := json.Marshal(map[string]any{"action": "model_config.apply", "target": target, "files": map[string]any{"codex_config": "model=\"wrong-host\"\n"}})
		response, _ := d.handle(raw, &auth)
		if response["action"] != "error" {
			t.Fatal("invalid target wrote local configuration", response)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("remote target touched local managed file", err)
	}
}

func TestModelConfigRoutesLocalApplyAndReadback(t *testing.T) {
	home := t.TempDir()
	d := New("test-key")
	if err := d.ConfigureModelConfig(modelconfig.Config{AllowedRoots: []string{home}}); err != nil {
		t.Fatal(err)
	}
	auth := true
	call := func(action string, fields map[string]any) map[string]any {
		fields["action"] = action
		fields["request_id"] = "model-request"
		fields["target"] = map[string]any{"kind": "local"}
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		response, _ := d.handleContext(context.Background(), raw, &auth)
		return response
	}
	text := "model = \"fixture-model\"\n"
	applied := call("model_config.apply", map[string]any{"files": map[string]any{"codex_config": text}})
	if applied["action"] != "model_config.apply.result" || applied["request_id"] != "model-request" || applied["daemon_id"] != d.daemonID {
		t.Fatal(applied)
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil || string(data) != text {
		t.Fatal("managed file readback", err)
	}
	planned := call("model_config.plan", map[string]any{})
	if planned["action"] != "model_config.plan.result" {
		t.Fatal(planned)
	}
	result := planned["result"].(map[string]any)
	file := result["codex_config"].(map[string]any)
	if file["content"] != text || file["exists"] != true || result["_home"] != home {
		t.Fatal(planned)
	}
	if len(d.journal.Statuses()) != 0 {
		t.Fatal("model config manufactured session ownership")
	}
}
