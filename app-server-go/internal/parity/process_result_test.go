package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPartialProcessResultRejectsInvalidHistoricalClaims(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong schema type", func(d map[string]any) { d["schema_version"] = "1" }},
		{"unsupported schema", func(d map[string]any) { d["schema_version"] = 2 }},
		{"wrong stage", func(d map[string]any) { d["stage"] = "P2-complete" }},
		{"missing receipt scope", func(d map[string]any) { delete(d, "fixture_receipt") }},
		{"empty cleanup", func(d map[string]any) { d["cleanup"] = " " }},
		{"wrong commands type", func(d map[string]any) { d["commands"] = "success" }},
		{"empty commands", func(d map[string]any) { d["commands"] = []string{} }},
		{"empty assertion", func(d map[string]any) { d["assertions"] = []string{" "} }},
		{"wrong assertion type", func(d map[string]any) { d["assertions"] = map[string]string{"claim": "success"} }},
		{"misspelled safety field", func(d map[string]any) { d["production_chagned"] = false }},
		{"missing explicit production", func(d map[string]any) { delete(d, "production_changed") }},
		{"missing explicit completion", func(d map[string]any) { delete(d, "full_migration_complete") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			body := map[string]any{
				"schema_version": 1, "passed": true, "stage": "P2-partial",
				"fixture_receipt": "Synthetic unit fixture, not real evidence.",
				"daemon_sha256":   strings.Repeat("b", 64),
				"commands":        []string{"unit fixture command, never executed"},
				"assertions":      []string{"unit fixture only"}, "cleanup": "unit fixture has no child processes",
				"production_changed": false, "full_migration_complete": false,
			}
			resultPath := filepath.Join(root, "result.json")
			write := func() {
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(resultPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write()
			limited := true
			execution := PartialExecution{
				Environment: "unit fixture, no processes", CandidateRevision: strings.Repeat("a", 40),
				CandidateBinarySHA256: strings.Repeat("b", 64), SourceFreshnessLimited: &limited,
				ProcessResult: "result.json", ProcessResultSHA256: fixtureFileHash(t, resultPath),
				FixtureLimits: "Synthetic unit fixture.",
			}
			if issues := validatePartialExecution(root, execution); len(issues) != 0 {
				t.Fatalf("valid fixture rejected: %v", issues)
			}
			test.mutate(body)
			write()
			execution.ProcessResultSHA256 = fixtureFileHash(t, resultPath)
			if issues := validatePartialExecution(root, execution); len(issues) == 0 {
				t.Fatal("invalid rehashed historical claim accepted")
			}
		})
	}
}
