package parity

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestManifestDeclaresTheCompletePythonDaemonContract(t *testing.T) {
	var manifest struct {
		ControlActions  []string `json:"control_actions"`
		SessionActions  []string `json:"session_actions"`
		RuntimeTypes    []string `json:"runtime_types"`
		SessionStatuses []string `json:"session_statuses"`
		ConnectorPath   string   `json:"connector_path"`
		DefaultPort     int      `json:"default_port"`
	}
	if err := json.Unmarshal(Manifest, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	required := map[string][]string{
		"control": {"daemon.handshake", "daemon.status", "daemon.shutdown", "session.sync", "session.create", "session.spawn", "session.observe_status", "runtime.request", "runtime.disconnect", "model_config.plan", "model_config.apply", "model_config.reload"},
		"session": {"session.send", "session.compact", "session.review", "session.steer", "session.interrupt", "session.approve", "session.settings", "session.resize", "session.disconnect", "session.history_page", "session.delete", "session.rename", "session.close", "session.models", "session.commands", "session.model.read", "session.model.set", "session.reasoning.set", "session.approval.read", "session.approval.set"},
		"runtime": {"codex", "pty", "hermes", "ssh", "codex-ssh", "grok", "grok-ssh"},
		"status":  {"running", "waiting_approval", "idle", "error"},
	}
	assertExact(t, "control", required["control"], manifest.ControlActions)
	assertExact(t, "session", required["session"], manifest.SessionActions)
	assertExact(t, "runtime", required["runtime"], manifest.RuntimeTypes)
	assertExact(t, "status", required["status"], manifest.SessionStatuses)
	if manifest.ConnectorPath != "/ws/v1/connector" || manifest.DefaultPort != 30009 {
		t.Fatalf("connector path or port drifted: %+v", manifest)
	}
}

func assertExact(t *testing.T, name string, want, got []string) {
	t.Helper()
	if !slices.Equal(want, got) {
		t.Fatalf("%s contract mismatch\nwant: %v\ngot:  %v", name, want, got)
	}
}
