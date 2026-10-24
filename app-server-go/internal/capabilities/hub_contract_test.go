package capabilities

import (
	"context"
	"encoding/json"
	"testing"
)

// This exercises the public HTTP contract used by the real capability center.
func TestHubOfficialSourceAndRegisteredAgents(t *testing.T) {
	env := newEnv("local")
	env.Files[env.P.Codex+"/skills/astrorder-browser/SKILL.md"] = []byte("---\nname: astrorder-browser\ndescription: user-authored namesake\n---\nUser content\n")
	h := NewHandler(serviceFor(env), nil)
	for _, agent := range []string{"all", "codex", "hermes", "grok"} {
		t.Run(agent, func(t *testing.T) {
			status, body := call(t, h, "GET", "/api/v1/capabilities/summary?target_id=local&agent="+agent, nil)
			if status != 200 {
				t.Fatalf("summary status=%d", status)
			}
			raw, _ := json.Marshal(body)
			var summary struct {
				Skills []struct {
					Name, Scope, Source, Category, Content string
					Agents                                 []string
				}
				SkillAgents []struct{ ID, Label string } `json:"skill_agents"`
			}
			if err := json.Unmarshal(raw, &summary); err != nil {
				t.Fatal(err)
			}
			official := map[string]bool{}
			namesake := false
			for _, skill := range summary.Skills {
				if skill.Scope == "builtin" && skill.Source == "astrorder" {
					if skill.Category != "astrorder" || skill.Content == "" || len(skill.Agents) != 0 {
						t.Fatalf("invalid builtin source %q", skill.Name)
					}
					official[skill.Name] = true
				} else if skill.Name == "astrorder-browser" && skill.Scope != "builtin" {
					namesake = true
					if skill.Source == "astrorder" || skill.Category == "astrorder" {
						t.Fatal("user namesake classified as official")
					}
				}
			}
			if len(official) != 6 || !official["astrorder-sessions"] || !official["astrorder-browser"] || !official["astrorder-blackboard"] || !official["astrorder-swarm"] || !official["astrorder-workspace"] || !official["astrorder-monitor"] {
				t.Fatalf("official sources=%v", official)
			}
			registered := map[string]bool{}
			for _, item := range summary.SkillAgents {
				if item.Label == "" {
					t.Fatal("missing Agent label")
				}
				registered[item.ID] = true
			}
			for _, id := range []string{"codex", "hermes", "grok"} {
				if !registered[id] {
					t.Fatalf("missing registered skill Agent %q", id)
				}
			}
			if (agent == "all" || agent == "codex") && !namesake {
				t.Fatal("installed user namesake lost")
			}
		})
	}
}

func TestHubOfficialBrowserDispatchUsesBuiltinSource(t *testing.T) {
	env := newEnv("local")
	env.Info.Kind = "local"
	provisionLegacyCLI(t, env, env.P.Hermes, "hermes")
	provisionLegacyCLI(t, env, env.P.Grok, "grok")
	env.Files[env.P.Codex+"/skills/astrorder-browser/SKILL.md"] = []byte("---\nname: astrorder-browser\n---\nuser-authored namesake\n")
	svc := serviceFor(env)
	result, err := svc.Dispatch(context.Background(), "skill", "local", "astrorder", "astrorder-browser", []DispatchTarget{{TargetID: "local", Agent: "hermes"}, {TargetID: "local", Agent: "grok"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 2 {
		t.Fatalf("dispatch reports=%+v", result.Reports)
	}
	for _, report := range result.Reports {
		content, err := env.Read(context.Background(), report.InstalledPath+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if string(content) == "" || string(content) == "---\nname: astrorder-browser\n---\nuser-authored namesake\n" {
			t.Fatal("did not read back official source")
		}
	}
	if string(env.Files[env.P.Codex+"/skills/astrorder-browser/SKILL.md"]) != "---\nname: astrorder-browser\n---\nuser-authored namesake\n" {
		t.Fatal("source Agent's user file changed")
	}
}
