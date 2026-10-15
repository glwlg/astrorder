package contracts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestCheckedBufTemplateUsesPortablePluginNames(t *testing.T) {
	raw, err := os.ReadFile("buf.gen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := filepath.ToSlash(string(raw))
	if strings.Contains(text, ":/Users/") || strings.Contains(strings.ToLower(text), "hermes/cache/scratch") {
		t.Fatalf("buf.gen.yaml contains a workstation-specific plugin path:\n%s", text)
	}
	for _, plugin := range []string{"local: protoc-gen-go\n", "local: protoc-gen-go-grpc\n"} {
		if !strings.Contains(text, plugin) {
			t.Errorf("buf.gen.yaml does not use portable plugin name %q", strings.TrimSpace(plugin))
		}
	}
}

func TestRecordedBaselineRawHashAndActionSetMatchActualFile(t *testing.T) {
	mapping := loadMap(t)
	if mapping.Baseline.Path == "" || mapping.Baseline.SHA256 == "" {
		t.Fatalf("action map baseline provenance is incomplete: %+v", mapping.Baseline)
	}
	baselinePath := filepath.Join("..", filepath.FromSlash(mapping.Baseline.Path))
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read recorded baseline %s: %v", baselinePath, err)
	}
	sum := sha256.Sum256(raw)
	actualHash := hex.EncodeToString(sum[:])
	if mapping.Baseline.SHA256 != actualHash {
		t.Errorf("recorded baseline raw SHA-256 is stale: map=%s actual=%s", mapping.Baseline.SHA256, actualHash)
	}
	var baseline struct {
		Inventory struct {
			DaemonActions []struct {
				Name string `json:"name"`
			} `json:"daemon_actions"`
		} `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("decode baseline: %v", err)
	}
	baselineActions := make([]string, 0, len(baseline.Inventory.DaemonActions))
	for _, action := range baseline.Inventory.DaemonActions {
		if action.Name == "" {
			t.Fatal("baseline contains an unnamed daemon action")
		}
		baselineActions = append(baselineActions, action.Name)
	}
	mapped := make(map[string]bool, len(mapping.RequestActions)+len(mapping.LegacyAliases))
	for _, action := range mapping.RequestActions {
		if action.PostBaseline {
			// Additions must not rewrite the frozen migration inventory.
			if action.LegacyAction != "session.goal.set" && action.LegacyAction != "session.goal.clear" {
				t.Fatalf("unreviewed post-baseline action: %s", action.LegacyAction)
			}
			continue
		}
		mapped[action.LegacyAction] = true
	}
	for _, alias := range mapping.LegacyAliases {
		if alias.Namespace == "daemon-request-action" {
			mapped[alias.LegacyName] = true
		}
	}
	mappedActions := make([]string, 0, len(mapped))
	for action := range mapped {
		mappedActions = append(mappedActions, action)
	}
	sort.Strings(baselineActions)
	sort.Strings(mappedActions)
	if strings.Join(mappedActions, "\n") != strings.Join(baselineActions, "\n") {
		t.Fatalf("action map/baseline union drift\n mapped: %v\nbaseline: %v", mappedActions, baselineActions)
	}
	aliases := make(map[string]aliasEntry, len(mapping.LegacyAliases))
	for _, alias := range mapping.LegacyAliases {
		aliases[alias.Namespace+"\x00"+alias.LegacyName] = alias
	}
	for _, action := range mapping.RequestActions {
		if action.AliasOf == "" {
			continue
		}
		alias, ok := aliases["daemon-request-action\x00"+action.LegacyAction]
		if !ok || alias.AliasOf != action.AliasOf || alias.RPC != action.RPC {
			t.Fatalf("daemon alias %q is not explicitly reconciled: request=%+v alias=%+v", action.LegacyAction, action, alias)
		}
	}
}

func TestBuildScriptsDeclareRetainedCandidateArtifacts(t *testing.T) {
	for _, script := range []string{"../scripts/build-go.sh", "../scripts/build-go.ps1"} {
		raw, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		text := filepath.ToSlash(string(raw))
		if strings.Contains(strings.ToLower(text), ":/users/") {
			t.Errorf("%s contains a username-specific default", script)
		}
		for _, artifact := range []string{
			"astrorder-sessiond.exe",
			"astrorder-app.exe",
			"astrorderctl.exe",
			"astrorder-agent.exe",
			"astrorder-helper.exe",
			"SHA256SUMS",
		} {
			if !strings.Contains(text, artifact) {
				t.Errorf("%s does not retain %s", script, artifact)
			}
		}
		if strings.Contains(text, "gen/python/astrorder/sessiond/v1/sessiond_pb2.py") {
			t.Errorf("%s imports or addresses the transitional Python binding directly", script)
		}
	}
}
