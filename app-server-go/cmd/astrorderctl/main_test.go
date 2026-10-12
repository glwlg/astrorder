package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/parity"
)

func TestMigrationVerifyExitsNonzeroOnIncompleteInventory(t *testing.T) {
	root := t.TempDir()
	reports := filepath.Join(root, "docs", "reports")
	if err := os.MkdirAll(reports, 0700); err != nil {
		t.Fatal(err)
	}
	writeFrozen(t, reports)
	matrix := `{"schema_version":1,"source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","inventory_sha256":"mismatch","migration_items":[]}`
	if err := os.WriteFile(filepath.Join(reports, "full-go-migration-matrix.json"), []byte(matrix), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := run([]string{"migration", "verify", "--root", root}, &output)
	if code == 0 {
		t.Fatalf("unfinished migration passed: %s", output.String())
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["ok"] != false {
		t.Fatalf("wrong result: %s", output.String())
	}
}

func TestVerifyRequiresExplicitIsolatedInputs(t *testing.T) {
	for _, args := range [][]string{
		{"verify"},
		{"verify", "--root", t.TempDir()},
		{"verify", "--isolated", "--root", t.TempDir()},
	} {
		var output bytes.Buffer
		if code := run(args, &output); code == 0 || !strings.Contains(output.String(), `"ok":false`) {
			t.Fatalf("unsafe verify invocation accepted: args=%v output=%s", args, output.String())
		}
	}
}

func TestInventoryDoesNotStartServicesOrImportPython(t *testing.T) {
	root := t.TempDir()
	reports := filepath.Join(root, "docs", "reports")
	if err := os.MkdirAll(reports, 0700); err != nil {
		t.Fatal(err)
	}
	writeFrozen(t, reports)
	var output bytes.Buffer
	code := run([]string{"migration", "inventory", "--root", root}, &output)
	if code != 0 {
		t.Fatalf("frozen inventory failed without Python: %s", output.String())
	}
	var result struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("inventory lost entries: %s", output.String())
	}
}

func TestFinalVerifyRejectsDocumentOnlyCandidate(t *testing.T) {
	root := t.TempDir()
	reports := filepath.Join(root, "docs", "reports")
	if err := os.MkdirAll(reports, 0700); err != nil {
		t.Fatal(err)
	}
	writeFrozen(t, reports)
	baseline, err := parity.LoadBaseline(filepath.Join(reports, "full-go-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"fake.go":       "package fake\n",
		"fake_test.go":  "package fake\nimport \"testing\"\nfunc TestFake(t *testing.T) {}\n",
		"evidence.json": "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := parity.Manifest{Version: 1, BaselineRevision: baseline.Revision, FrozenHash: baseline.Hash, Items: []parity.Item{{
		ID: baseline.Entries[0].ID, FeatureID: "F04", Stage: "P7", SourcePaths: []string{"removed.py"}, Entrypoints: []string{baseline.Entries[0].Name},
		Contract: json.RawMessage(`{"preserve":true}`), TargetPaths: []string{"fake.go"}, DataOwner: map[string]string{"sessions": "go-app"},
		AcceptanceIDs: []string{"T10"}, Tests: []parity.TestRef{{Path: "fake_test.go", Name: "TestFake", Command: "DO_NOT_EXECUTE"}}, Status: "DONE",
		Evidence: []parity.Evidence{{Revision: baseline.Revision, BinaryHash: strings.Repeat("b", 64), Environment: "isolated", Command: "DO_NOT_EXECUTE", Assertions: []string{"document only"}, Path: "evidence.json"}},
		Review:   parity.Review{Status: "PASS", Reviewer: "claimed-reviewer", Path: "evidence.json"}, LegacyRemoved: true,
	}}}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reports, "full-go-migration-matrix.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := run([]string{"migration", "verify", "--root", root}, &output); code == 0 || !strings.Contains(output.String(), "unregistered_entrypoint") {
		t.Fatalf("document-only candidate accepted or wrong rejection: %s", output.String())
	}
}

func TestFinalVerifyReadsExecutableProductRegistry(t *testing.T) {
	root := t.TempDir()
	reports := filepath.Join(root, "docs", "reports")
	if err := os.MkdirAll(reports, 0700); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"schema_version": 1, "source_revision": strings.Repeat("a", 40),
		"inventory": map[string]any{"http_routes": []any{map[string]any{
			"id": "http:get-health:74fac65f35", "methods": []string{"GET"}, "path": "/health", "feature_id": "F01",
		}}},
	}
	hash, err := parity.ContentHash(document)
	if err != nil {
		t.Fatal(err)
	}
	document["content_sha256"] = hash
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reports, "full-go-baseline.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	matrix := map[string]any{"schema_version": 1, "source_revision": strings.Repeat("a", 40), "inventory_sha256": hash, "migration_items": []any{}}
	body, err = json.Marshal(matrix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reports, "full-go-migration-matrix.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := run([]string{"migration", "verify", "--root", root}, &output); code == 0 {
		t.Fatal("incomplete matrix passed final verification")
	}
	var result parity.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, issue := range result.Issues {
		if issue.ItemID == "http:get-health:74fac65f35" && issue.Code == "unregistered_entrypoint" {
			t.Fatalf("executable Go health registry was not read: %+v", issue)
		}
	}
}

func writeFrozen(t *testing.T, reports string) {
	t.Helper()
	document := map[string]any{"schema_version": 1, "source_revision": strings.Repeat("a", 40), "inventory": map[string]any{"http_routes": []any{map[string]any{"id": "HTTP-001", "methods": []string{"GET"}, "path": "/api/v1/sessions", "feature_id": "F04"}}}}
	hash, err := parity.ContentHash(document)
	if err != nil {
		t.Fatal(err)
	}
	document["content_sha256"] = hash
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(reports, "full-go-baseline.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
