package capabilities

import (
	"astrorder.dev/app-server/internal/agentskill"
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

type multipleDispatchAgents struct{ agents map[string]map[string]any }

func (s multipleDispatchAgents) GetAgent(_ context.Context, id string) (map[string]any, error) {
	return s.agents[id], nil
}

func (s multipleDispatchAgents) ListAgents(context.Context) ([]map[string]any, error) {
	agents := make([]map[string]any, 0, len(s.agents))
	for _, agent := range s.agents {
		agents = append(agents, agent)
	}
	return agents, nil
}

func TestDispatchUsesExistingBindingWhenAgentIsAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller string
		ok     bool
	}{
		{"bound agent", `{"agent_id":"codex-main","connection_id":"local","profile_name":"default","runtime":"codex"}`, true},
		{"missing binding", "", false},
		{"foreign binding", `{"agent_id":"other","connection_id":"local","profile_name":"default","runtime":"codex"}`, false},
		{"different profile", `{"agent_id":"codex-main","connection_id":"local","profile_name":"other","runtime":"codex"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := localEnv("local")
			env.Run_ = currentCLI
			svc := serviceFor(env)
			svc.Agents = multipleDispatchAgents{map[string]map[string]any{
				"codex-main":  {"id": "codex-main", "kind": "codex", "connection_id": "local"},
				"codex-other": {"id": "codex-other", "kind": "codex", "connection_id": "local"},
			}}
			svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
				return "astrorder-agent", []byte("fixture executable"), nil
			}
			for _, module := range []string{"astrorder-sessions", "astrorder-browser"} {
				p := env.P.Codex + "/skills/" + module + "/scripts/astrorder-agent"
				env.Bins[p] = p
			}
			callerPath := env.P.Codex + "/" + agentskill.CallerFile
			if tc.caller != "" {
				env.Files[callerPath] = []byte(tc.caller)
			}
			result, err := svc.Dispatch(context.Background(), "skill", "local", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "codex"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := result.SuccessCount == 1; got != tc.ok {
				t.Fatalf("success = %v, reports = %+v", got, result.Reports)
			}
			if tc.caller != "" && string(env.Files[callerPath]) != tc.caller {
				t.Fatal("existing binding changed")
			}
			if !tc.ok && len(env.Writes) != 0 {
				t.Fatalf("ambiguous or invalid binding wrote files: %v", env.Writes)
			}
		})
	}
}
