package capabilities

import (
	"context"
	"testing"
)

func TestSummaryIncludesAndRemovesPluginSkillsDynamically(t *testing.T) {
	env := newEnv("a")
	svc := serviceFor(env)

	enabled := true
	svc.SetPluginSkillProvider(PluginSkillProviderFunc(func(ctx context.Context) []PluginSkill {
		if !enabled {
			return nil
		}
		return []PluginSkill{
			{
				PluginID:    "com.acme.hello",
				Name:        "hello-ext",
				DisplayName: "Hello Extension Skill",
				Description: "Teach agents how to call hello-ext",
				Content:     "---\nname: hello-ext\n---\n# Hello\n",
			},
		}
	}))

	sum, err := svc.Summary(context.Background(), "a", "all")
	if err != nil {
		t.Fatalf("summary with enabled plugin skill: %v", err)
	}
	var found *Skill
	for i := range sum.Skills {
		if sum.Skills[i].Name == "hello-ext" {
			found = &sum.Skills[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected plugin skill hello-ext in summary, got %+v", sum.Skills)
	}
	if found.Source != "plugin" || found.Scope != "plugin" || found.PluginID != "com.acme.hello" || found.DisplayName != "Hello Extension Skill" {
		t.Fatalf("unexpected plugin skill metadata: %+v", found)
	}

	// Disabling the plugin removes the skill immediately
	enabled = false
	sumAfter, err := svc.Summary(context.Background(), "a", "all")
	if err != nil {
		t.Fatalf("summary after disable: %v", err)
	}
	for _, sk := range sumAfter.Skills {
		if sk.Name == "hello-ext" {
			t.Fatalf("disabled plugin skill should disappear immediately, found: %+v", sk)
		}
	}
}
