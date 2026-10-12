package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRejectsUncoveredFrozenEntrypoint(t *testing.T) {
	root := t.TempDir()
	baseline := []Entry{{ID: "HTTP-001", Kind: "http", Name: "GET /api/v1/sessions", FeatureID: "F04"}}
	manifest := Manifest{Version: 1, BaselineRevision: strings.Repeat("a", 40), BaselineHash: EntryHash(baseline)}
	result := Verify(root, manifest, baseline, false)
	if result.OK || !hasCode(result, "unmapped_entrypoint") {
		t.Fatalf("uncovered route accepted: %+v", result)
	}
}

func TestVerifyDoesNotAcceptImplementedAsFinal(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	manifest.Items[0].Status = "IMPLEMENTED"
	result := Verify(root, manifest, baseline, true)
	if result.OK || !hasCode(result, "unfinished_item") {
		t.Fatalf("partial implementation passed: %+v", result)
	}
}

func TestFinalVerifyRejectsMissingOrMalformedCandidateRevision(t *testing.T) {
	for _, revision := range []string{"", "abc", strings.Repeat("A", 40), strings.Repeat("a", 39), strings.Repeat("g", 40)} {
		t.Run(revision, func(t *testing.T) {
			root, manifest, baseline := validFixture(t)
			manifest.CandidateRevision = revision
			result := Verify(root, manifest, baseline, true)
			if result.OK || !hasCode(result, "invalid_candidate_revision") {
				t.Fatalf("candidate revision %q passed final verification: %+v", revision, result)
			}
		})
	}
}

func TestFinalVerifyRejectsEvidenceFromDifferentCandidateRevision(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	manifest.CandidateRevision = strings.Repeat("c", 40)
	result := Verify(root, manifest, baseline, true)
	if result.OK || !hasCode(result, "evidence_revision_mismatch") {
		t.Fatalf("stale candidate evidence passed final verification: %+v", result)
	}
}

func TestFinalVerifyRejectsUnsafeOrUnboundTestMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Item)
		code   string
	}{
		{name: "unknown command ID", code: "invalid_test", mutate: func(item *Item) { item.Tests[0].CommandID = "operator-shell" }},
		{name: "shell payload", code: "invalid_test", mutate: func(item *Item) { item.Tests[0].Command += " && del important" }},
		{name: "path traversal", code: "invalid_test", mutate: func(item *Item) { item.Tests[0].Path = "../example_test.go" }},
		{name: "template mismatch", code: "invalid_test", mutate: func(item *Item) { item.Tests[0].Command = "cd app-server-go && go test ./internal/other -count=1" }},
		{name: "mapped test absent", code: "invalid_test", mutate: func(item *Item) { item.Tests[0].Name = "TestDoesNotExist" }},
		{name: "evidence command ID mismatch", code: "invalid_evidence", mutate: func(item *Item) { item.Evidence[0].CommandID = "python-app-baseline-test" }},
		{name: "evidence test path mismatch", code: "invalid_evidence", mutate: func(item *Item) { item.Evidence[0].TestPath = "app-server-go/internal/other_test.go" }},
		{name: "evidence test name mismatch", code: "invalid_evidence", mutate: func(item *Item) { item.Evidence[0].TestName = "TestOther" }},
		{name: "evidence command mismatch", code: "invalid_evidence", mutate: func(item *Item) { item.Evidence[0].Command += " -v" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, manifest, baseline := validFixture(t)
			test.mutate(&manifest.Items[0])
			result := Verify(root, manifest, baseline, true)
			if result.OK || !hasCode(result, test.code) {
				t.Fatalf("unsafe or unbound metadata passed: %+v", result)
			}
		})
	}
}

func TestFinalVerifyRejectsPythonBaselineAsGoCompletionEvidence(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	test := &manifest.Items[0].Tests[0]
	test.Path = "backend/tests/test_example.py"
	test.Name = "test_example"
	test.CommandID = CommandPythonAppBaseline
	test.Command = "backend/.venv/Scripts/python.exe -m pytest backend/tests/test_example.py -q"
	if err := os.MkdirAll(filepath.Join(root, "backend", "tests"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(test.Path)), []byte("def test_example():\n    pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	evidence := &manifest.Items[0].Evidence[0]
	evidence.CommandID, evidence.TestPath, evidence.TestName, evidence.Command = test.CommandID, test.Path, test.Name, test.Command
	rewriteFixtureArtifact(t, root, evidence)
	result := Verify(root, manifest, baseline, true)
	if result.OK || !hasCode(result, "ineligible_final_evidence") {
		t.Fatalf("Python baseline evidence completed final Go verification: %+v", result)
	}
}

func TestVerifyRejectsBaselineTampering(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	baseline[0].Name = "GET /api/v1/other"
	result := Verify(root, manifest, baseline, false)
	if result.OK || !hasCode(result, "baseline_hash_mismatch") {
		t.Fatalf("mutated baseline passed: %+v", result)
	}
}

func TestVerifyRequiresExecutableTestAndEvidence(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	manifest.Items[0].Tests = nil
	manifest.Items[0].Evidence = nil
	result := Verify(root, manifest, baseline, true)
	if result.OK || !hasCode(result, "missing_tests") || !hasCode(result, "missing_evidence") {
		t.Fatalf("unverified work passed: %+v", result)
	}
}

func TestVerifyFailsOnConflictingWriter(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	manifest.Items = append(manifest.Items, Item{ID: "TASK-002", FeatureID: "F03", Status: "NOT_STARTED", DataOwner: map[string]string{"sessions": "python-app"}})
	result := Verify(root, manifest, baseline, false)
	if result.OK || !hasCode(result, "conflicting_writer") {
		t.Fatalf("dual writer accepted: %+v", result)
	}
}

func TestVerifyFrozenInventorySurvivesSourceRemoval(t *testing.T) {
	root, manifest, baseline := validFixture(t)
	result := Verify(root, manifest, baseline, true)
	if !result.OK {
		t.Fatalf("frozen inventory incorrectly requires removed Python source: %+v", result)
	}
}

func TestLoadPreservesContractLargeIntegers(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "matrix.json")
	body := `{"version":1,"baseline_revision":"` + strings.Repeat("a", 40) + `","items":[{"id":"HTTP-001","contract":{"native_id":9007199254740993}}]}`
	if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	matrix, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(matrix.Items[0].Contract), "9007199254740993") {
		t.Fatal("native integer lost precision")
	}
}

func hasCode(result Result, code string) bool {
	for _, issue := range result.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func validFixture(t *testing.T) (string, Manifest, []Entry) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{"app-server-go/internal/example/example.go": "package example\n", "app-server-go/internal/example/example_test.go": "package example\nimport \"testing\"\nfunc TestExample(t *testing.T) {}\n", "evidence/result.json": "{\"exit_code\":0,\"assertions\":[\"exact readback\"]}\n"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline := []Entry{{ID: "HTTP-001", Kind: "http", Name: "GET /api/v1/example", FeatureID: "F01"}}
	revision := strings.Repeat("a", 40)
	test := TestRef{Path: "app-server-go/internal/example/example_test.go", Name: "TestExample", CommandID: CommandGoPackageTest, Command: "cd app-server-go && go test ./internal/example -count=1"}
	manifest := Manifest{Version: 1, BaselineRevision: revision, CandidateRevision: revision, BaselineHash: EntryHash(baseline), Items: []Item{{ID: "HTTP-001", FeatureID: "F01", Stage: "P4", SourcePaths: []string{"backend/src/astrorder/removed.py"}, Entrypoints: []string{"GET /api/v1/example"}, Contract: json.RawMessage(`{"authentication":"browser"}`), TargetPaths: []string{"app-server-go/internal/example/example.go"}, DataOwner: map[string]string{"sessions": "go-app"}, AcceptanceIDs: []string{"T01"}, Tests: []TestRef{test}, Status: "DONE", Evidence: []Evidence{{Revision: revision, BinaryHash: strings.Repeat("b", 64), Environment: "isolated-unit-fixture-not-real-run", CommandID: test.CommandID, TestPath: test.Path, TestName: test.Name, Command: test.Command, ExitCode: 0, Assertions: []string{"unit fixture exact readback; not a real execution"}, Path: "evidence/result.json"}}, LegacyRemoved: true, Review: Review{Status: "PASS", Reviewer: "independent", Path: "evidence/result.json"}}}}
	// This unit fixture checks schema binding and file integrity only. It is
	// explicitly not evidence that a command or candidate binary was executed.
	for name, body := range map[string][]byte{"evidence/candidate.bin": []byte("unit-fixture-binary"), "evidence/output.log": []byte("unit-fixture captured output; not a real run\n")} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	evidence := &manifest.Items[0].Evidence[0]
	evidence.BinaryHash = fixtureFileHash(t, filepath.Join(root, "evidence", "candidate.bin"))
	rewriteFixtureArtifact(t, root, evidence)
	return root, manifest, baseline
}

func rewriteFixtureArtifact(t *testing.T, root string, evidence *Evidence) {
	t.Helper()
	artifact := ExecutionArtifact{
		Version: ExecutionArtifactVersion, CandidateRevision: evidence.Revision,
		BinaryPath: "evidence/candidate.bin", BinaryHash: evidence.BinaryHash,
		Environment: evidence.Environment, CommandID: evidence.CommandID,
		TestPath: evidence.TestPath, TestName: evidence.TestName, Command: evidence.Command,
		ExitCode: evidence.ExitCode, Assertions: evidence.Assertions,
		OutputPath: "evidence/output.log", OutputSHA256: fixtureFileHash(t, filepath.Join(root, "evidence", "output.log")),
		RunnerVersion: "unit-fixture-not-real-run",
	}
	body, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evidence", "result.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	evidence.SHA256 = hex.EncodeToString(digest[:])
}

func fixtureFileHash(t *testing.T, filename string) string {
	t.Helper()
	body, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
