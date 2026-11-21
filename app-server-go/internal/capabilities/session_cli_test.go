package capabilities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/agentskill"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
)

type cliAgentStore struct{ agent map[string]any }

func (s cliAgentStore) GetAgent(context.Context, string) (map[string]any, error) { return s.agent, nil }

func TestSessionCLISetupRollsBackFailedExecutableProbe(t *testing.T) {
	env := localEnv("local")
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"kind": "codex"}}
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", []byte("bad executable"), nil
	}
	if _, err := svc.SessionCLI(context.Background(), "codex-main", true); err == nil {
		t.Fatal("bad executable accepted")
	}
	if len(env.Files) != 0 {
		t.Fatalf("failed setup left files: %v", env.Files)
	}
}

func TestSessionCLISetupRefusesAnotherAgentBinding(t *testing.T) {
	env := localEnv("local")
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"kind": "codex"}}
	raw := []byte(`{"agent_id":"other","connection_id":"local","profile_name":"default","runtime":"codex"}`)
	env.Files[env.P.Codex+"/"+agentskill.CallerFile] = raw
	if _, err := svc.SessionCLI(context.Background(), "codex-main", true); err == nil {
		t.Fatal("foreign binding overwritten")
	}
	if len(env.Writes) != 0 {
		t.Fatal("conflicting setup wrote files")
	}
}
func TestSessionCLISetupEnablesFirstOfficialDispatch(t *testing.T) {
	env := localEnv("local")
	env.Run_ = currentCLI
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"id": "codex-main", "kind": "codex", "connection_id": "local", "profile_name": "default"}}
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", []byte("#!/bin/sh\nexit 0\n"), nil
	}
	for _, name := range []string{"astrorder-sessions", "astrorder-browser"} {
		binaryPath := env.P.Codex + "/skills/" + name + "/scripts/astrorder-agent"
		env.Bins[binaryPath] = binaryPath
	}
	handler := NewHandler(svc, nil)
	route := "/api/v1/capabilities/session-cli"
	if !Matches(http.MethodGet, route) || !Matches(http.MethodPost, route) {
		t.Fatal("setup routes are not registered")
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, route+"?agent_id=codex-main", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"ready":false`) {
		t.Fatalf("missing status: %d %s", get.Code, get.Body.String())
	}
	post := httptest.NewRecorder()
	handler.ServeHTTP(post, httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"agent_id":"codex-main"}`)))
	if post.Code != 200 || !strings.Contains(post.Body.String(), `"ready":true`) {
		t.Fatalf("setup: %d %s", post.Code, post.Body.String())
	}
	var caller map[string]any
	if err := json.Unmarshal(env.Files[env.P.Codex+"/"+agentskill.CallerFile], &caller); err != nil {
		t.Fatal(err)
	}
	if len(caller) != 4 || caller["agent_id"] != "codex-main" || caller["connection_id"] != "local" {
		t.Fatalf("caller = %#v", caller)
	}
	result, err := svc.Dispatch(context.Background(), "skill", "local", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if result.SuccessCount != 1 || len(result.Reports) != 1 || !result.Reports[0].OK {
		t.Fatalf("dispatch after setup: %s", raw)
	}
	original := append([]byte(nil), env.Files[env.P.Codex+"/"+agentskill.CallerFile]...)
	env.Files[env.P.Codex+"/skills/astrorder-sessions/SKILL.md"] = []byte("user edited")
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{"agent_id":"codex-main"}`)))
	if refused.Code != 409 {
		t.Fatalf("modified skill accepted: %d %s", refused.Code, refused.Body.String())
	}
	if string(original) != string(env.Files[env.P.Codex+"/"+agentskill.CallerFile]) {
		t.Fatal("binding changed on failure")
	}
}

func TestSessionCLIUpgradeRefreshesExistingOfficialModules(t *testing.T) {
	ctx := context.Background()
	env := localEnv("local")
	env.Run_ = currentCLI
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"id": "codex-main", "kind": "codex", "connection_id": "local", "profile_name": "default"}}
	binary := []byte("old-client")
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", binary, nil
	}
	for _, module := range []string{"astrorder-sessions", "astrorder-blackboard"} {
		file := env.P.Codex + "/skills/" + module + "/scripts/astrorder-agent"
		env.Bins[file] = file
	}
	if _, err := svc.SessionCLI(ctx, "codex-main", true); err != nil {
		t.Fatal(err)
	}
	deliver, err := svc.officialSkillDelivery("astrorder-blackboard")
	if err != nil {
		t.Fatal(err)
	}
	folder, _, err := deliver(ctx, env, "codex")
	if err != nil {
		t.Fatal(err)
	}
	originalDoc := append([]byte(nil), env.Files[folder+"/SKILL.md"]...)
	binary = []byte("new-client")
	if _, err := svc.SessionCLI(ctx, "codex-main", true); err != nil {
		t.Fatal(err)
	}
	if string(env.Files[folder+"/scripts/astrorder-agent"]) != "new-client" || string(env.Files[folder+"/SKILL.md"]) != string(originalDoc) {
		t.Fatal("official blackboard client was not upgraded without changing its instructions")
	}
	var manifest officialManifest
	if err := json.Unmarshal(env.Files[folder+"/"+officialManifestFile], &manifest); err != nil || manifest.Files["scripts/astrorder-agent"] != payloadHash(binary) {
		t.Fatal("updated client manifest did not match")
	}

	env.Files[folder+"/SKILL.md"] = []byte("user edit")
	binary = []byte("later-client")
	if _, err := svc.SessionCLI(ctx, "codex-main", true); err == nil {
		t.Fatal("upgrade overwrote a user-edited official module")
	}
	if string(env.Files[folder+"/SKILL.md"]) != "user edit" || string(env.Files[env.P.Codex+"/skills/astrorder-sessions/scripts/astrorder-agent"]) != "new-client" {
		t.Fatal("refused upgrade changed installed files")
	}
}

func TestSessionCLISetupSupportsRemoteAntigravity(t *testing.T) {
	env := newEnv("wsl-main")
	env.Info.ConnectionID = "wsl-main"
	env.Run_ = currentCLI
	env.Shell_ = func(_ string, _ []byte, _ []string, _ *envtargettest.Env) (envtarget.Result, error) {
		return envtarget.Result{Code: 0}, nil
	}
	svc := serviceFor(env)
	svc.Agents = cliAgentStore{map[string]any{"id": "agy-wsl", "kind": "antigravity", "connection_id": "wsl-main", "profile_name": "default"}}
	svc.CLIBinary = func(context.Context, envtarget.Env) (string, []byte, error) {
		return "astrorder-agent", []byte("#!/bin/sh\nexit 0\n"), nil
	}
	agyHome := env.P.Antigravity
	for _, module := range []string{"astrorder-sessions", "astrorder-browser"} {
		file := agyHome + "/skills/" + module + "/scripts/astrorder-agent"
		env.Bins[file] = file
	}
	status, err := svc.SessionCLI(context.Background(), "agy-wsl", true)
	if err != nil || !status.Ready || status.Home != agyHome {
		t.Fatalf("remote antigravity SessionCLI: status=%+v err=%v", status, err)
	}
	result, err := svc.Dispatch(context.Background(), "skill", "wsl-main", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "wsl-main", Agent: "antigravity"}})
	if err != nil || result.SuccessCount != 1 {
		t.Fatalf("official dispatch to remote antigravity: result=%+v err=%v", result, err)
	}
}
