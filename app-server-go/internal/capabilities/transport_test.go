package capabilities

import (
	"astrorder.dev/app-server/internal/envtarget"
	"context"
	"testing"
)

type dispatchAgentStore struct{ cliAgentStore }

func (s dispatchAgentStore) ListAgents(context.Context) ([]map[string]any, error) {
	return []map[string]any{s.agent}, nil
}

func TestDispatchAutomaticallyBootstrapsCLI(t *testing.T) {
	env := localEnv("local")
	env.Run_ = currentCLI
	svc := serviceFor(env)
	svc.Agents = dispatchAgentStore{cliAgentStore{map[string]any{"id": "codex-main", "kind": "codex", "connection_id": "local"}}}
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", []byte("fixture executable"), nil
	}
	svc.CLITransport = TransportProvisioner("30001", "", "fixture")
	for _, module := range []string{"astrorder-sessions", "astrorder-browser"} {
		p := env.P.Codex + "/skills/" + module + "/scripts/astrorder-agent"
		env.Bins[p] = p
	}
	result, err := svc.Dispatch(context.Background(), "skill", "local", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 1 {
		t.Fatalf("dispatch failed: %+v", result.Reports)
	}
	if len(env.Files[env.P.Codex+"/astrorder-transport.json"]) == 0 {
		t.Fatal("missing transport")
	}
	if len(env.Files[env.P.Codex+"/skills/astrorder-browser/scripts/astrorder-agent"]) == 0 {
		t.Fatal("missing browser CLI")
	}
}

func TestFailedProbeRollsBackTransport(t *testing.T) {
	env := localEnv("local")
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"kind": "codex"}}
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", []byte("bad executable"), nil
	}
	svc.CLITransport = TransportProvisioner("30001", "", "fixture")
	if _, err := svc.SessionCLI(context.Background(), "codex-main", true); err == nil {
		t.Fatal("bad executable accepted")
	}
	if len(env.Files) != 0 {
		t.Fatal("failed setup left files")
	}
}
