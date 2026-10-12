package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRejectsPartialEntrypointOutsideParent(t *testing.T) {
	root, raw, entries := partialMatrixFixture(t)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	feature := document["migration_items"].([]any)[0].(map[string]any)
	partial := feature["partial_implementations"].([]any)[0].(map[string]any)
	partial["entrypoints"] = []string{"daemon:other-feature"}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BaselineHash = EntryHash(entries)
	result := Verify(root, manifest, entries, false)
	if result.OK || !hasCode(result, "invalid_partial") {
		t.Fatalf("partial claimed another feature's entrypoint: %+v", result)
	}
}

func TestVerifyRejectsInvalidPartialMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"missing ID", func(_, p map[string]any) { delete(p, "id") }},
		{"duplicate ID", func(f, p map[string]any) { f["partial_implementations"] = []any{p, p} }},
		{"unknown stage", func(_, p map[string]any) { p["stage"] = "P99" }},
		{"unknown status", func(_, p map[string]any) { p["status"] = "PASSED" }},
		{"partial cannot complete feature", func(_, p map[string]any) { p["status"] = "DONE" }},

		{"empty entrypoints", func(_, p map[string]any) { p["entrypoints"] = []string{} }},
		{"duplicate entrypoint", func(_, p map[string]any) { p["entrypoints"] = []string{"daemon:operation", "daemon:operation"} }},
		{"missing owner", func(_, p map[string]any) { delete(p, "data_owner") }},
		{"invalid acceptance", func(_, p map[string]any) { p["acceptance_ids"] = []string{"T99"} }},
		{"missing legacy disposition", func(_, p map[string]any) { delete(p, "legacy_removed") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, raw, entries := partialMatrixFixture(t)
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			feature := document["migration_items"].([]any)[0].(map[string]any)
			partial := feature["partial_implementations"].([]any)[0].(map[string]any)
			test.mutate(feature, partial)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := decodeManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			manifest.BaselineHash = EntryHash(entries)
			result := Verify(root, manifest, entries, false)
			if result.OK || !hasCode(result, "invalid_partial") {
				t.Fatalf("invalid partial metadata accepted: %+v", result)
			}
		})
	}
}

func TestVerifyBindsPartialReportsWithoutPromotingParent(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing review", func(p map[string]any) { delete(p, "review") }},
		{"missing report hash", func(p map[string]any) { delete(p["review"].(map[string]any), "sha256") }},
		{"report tampered", func(p map[string]any) { p["review"].(map[string]any)["sha256"] = strings.Repeat("c", 64) }},
		{"report escape", func(p map[string]any) { p["review"].(map[string]any)["path"] = "../review.json" }},
		{"failed report", func(p map[string]any) {
			p["review"].(map[string]any)["path"] = "docs/failed.json"
			p["review"].(map[string]any)["sha256"] = "FAILED_HASH"
		}},
		{"prior review cannot claim current REVIEWED", func(p map[string]any) { p["review"].(map[string]any)["status"] = "PASS_SCOPED_PREVIOUS_HASHES" }},
		{"isolated reports cannot certify production", func(p map[string]any) { p["status"] = "PRODUCTION_VERIFIED" }},
		{"missing isolated record", func(p map[string]any) {
			p["status"] = "ISOLATED_VERIFIED"
			delete(p, "evidence_path")
			delete(p, "evidence_sha256")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, raw, entries := partialMatrixFixture(t)
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			feature := document["migration_items"].([]any)[0].(map[string]any)
			partial := feature["partial_implementations"].([]any)[0].(map[string]any)
			partial["status"] = "REVIEWED"
			partial["review"] = map[string]any{"status": "PASS", "path": "docs/review.json", "sha256": fixtureFileHash(t, filepath.Join(root, "docs/review.json"))}
			partial["evidence_path"] = "docs/progress.md"
			partial["evidence_sha256"] = fixtureFileHash(t, filepath.Join(root, "docs/progress.md"))
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := decodeManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			manifest.BaselineHash = EntryHash(entries)
			if result := Verify(root, manifest, entries, false); !result.OK {
				t.Fatalf("valid bounded report rejected: %+v", result)
			}
			if result := Verify(root, manifest, entries, true); result.OK || !hasCode(result, "unfinished_item") {
				t.Fatalf("partial promoted unfinished parent: %+v", result)
			}
			test.mutate(partial)
			if review, ok := partial["review"].(map[string]any); ok && review["sha256"] == "FAILED_HASH" {
				review["sha256"] = fixtureFileHash(t, filepath.Join(root, "docs/failed.json"))
			}
			raw, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = decodeManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			manifest.BaselineHash = EntryHash(entries)
			if result := Verify(root, manifest, entries, false); result.OK || !hasCode(result, "invalid_partial") {
				t.Fatalf("unbound partial report accepted: %+v", result)
			}
		})
	}
}

func TestVerifyBindsPartialExecutionSummary(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing result", func(e map[string]any) { delete(e, "process_result") }},
		{"missing result hash", func(e map[string]any) { delete(e, "process_result_sha256") }},
		{"result tampered", func(e map[string]any) { e["process_result_sha256"] = strings.Repeat("c", 64) }},
		{"result escape", func(e map[string]any) { e["process_result"] = "../result.json" }},
		{"candidate mismatch", func(e map[string]any) { e["candidate_binary_sha256"] = strings.Repeat("c", 64) }},
		{"bad revision", func(e map[string]any) { e["candidate_revision"] = "abc" }},
		{"missing freshness limit", func(e map[string]any) { delete(e, "source_freshness_limited") }},
		{"unmatched exits", func(e map[string]any) { e["exit_codes"] = []int{} }},
		{"failed command", func(e map[string]any) { e["exit_codes"] = []int{1} }},
		{"failed result", func(e map[string]any) {
			e["process_result"] = "docs/result-failed.json"
			e["process_result_sha256"] = "FAILED_HASH"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, raw, entries := partialMatrixFixture(t)
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			feature := document["migration_items"].([]any)[0].(map[string]any)
			partial := feature["partial_implementations"].([]any)[0].(map[string]any)
			execution := map[string]any{
				"environment": "unit-fixture-not-real-execution", "candidate_revision": strings.Repeat("a", 40),
				"candidate_binary_sha256": strings.Repeat("b", 64), "source_freshness_limited": true,
				"process_result": "docs/result.json", "process_result_sha256": fixtureFileHash(t, filepath.Join(root, "docs/result.json")),
				"fixture_limits": "Synthetic fixture, not a production claim.",
			}
			partial["execution"] = execution
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := decodeManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			manifest.BaselineHash = EntryHash(entries)
			if result := Verify(root, manifest, entries, false); !result.OK {
				t.Fatalf("valid execution summary rejected: %+v", result)
			}
			test.mutate(execution)
			if execution["process_result_sha256"] == "FAILED_HASH" {
				execution["process_result_sha256"] = fixtureFileHash(t, filepath.Join(root, "docs/result-failed.json"))
			}
			raw, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = decodeManifest(raw)
			if err != nil {
				if test.name == "unmatched exits" || test.name == "failed command" {
					return // Redundant exit claims are now rejected during strict decoding.
				}
				t.Fatal(err)
			}
			manifest.BaselineHash = EntryHash(entries)
			if result := Verify(root, manifest, entries, false); result.OK || !hasCode(result, "invalid_partial") {
				t.Fatalf("unbound execution summary accepted: %+v", result)
			}
		})
	}
}

func partialMatrixFixture(t *testing.T) (string, []byte, []Entry) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"docs/review.json":        `{"passed":true,"scope":"unit-fixture-not-real-review"}`,
		"docs/failed.json":        `{"passed":false,"scope":"unit-fixture-not-real-review"}`,
		"docs/progress.md":        "Unit fixture, not real execution evidence.\n",
		"docs/result.json":        `{"schema_version":1,"stage":"P2-partial","fixture_receipt":"unit fixture, not execution evidence","commands":["unit command, never run"],"assertions":["unit fixture only"],"cleanup":"unit fixture has no processes","passed":true,"daemon_sha256":"` + strings.Repeat("b", 64) + `","full_migration_complete":false,"production_changed":false}`,
		"docs/result-failed.json": `{"schema_version":1,"stage":"P2-partial","fixture_receipt":"unit fixture, not execution evidence","commands":["unit command, never run"],"assertions":["unit fixture only"],"cleanup":"unit fixture has no processes","passed":false,"daemon_sha256":"` + strings.Repeat("b", 64) + `","full_migration_complete":false,"production_changed":false}`,
	} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries := []Entry{
		{ID: "daemon:operation", Name: "daemon:operation", FeatureID: "F03"},
		{ID: "daemon:status", Name: "daemon:status", FeatureID: "F03"},
	}
	document := map[string]any{
		"schema_version": 1, "source_revision": strings.Repeat("a", 40),
		"candidate_revision": strings.Repeat("a", 40), "inventory_sha256": strings.Repeat("b", 64),
		"migration_items": []any{map[string]any{
			"id": "migration:f03", "feature_id": "F03", "stage": "P2",
			"entrypoints": []string{"daemon:operation", "daemon:status"}, "status": "IMPLEMENTED",
			"partial_implementations": []any{map[string]any{
				"id": "operation-query", "stage": "P2", "status": "IMPLEMENTED",
				"entrypoints": []string{"daemon:operation"}, "data_owner": "daemon ledger read-only",
				"acceptance_ids": []string{"T05"}, "legacy_removed": false,
			}},
		}},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return root, raw, entries
}
