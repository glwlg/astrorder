package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFinalVerificationRejectsUnboundEvidenceDocument(t *testing.T) {
	root, manifest, entries := validFixture(t)
	if err := os.WriteFile(filepath.Join(root, "evidence/result.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	result := Verify(root, manifest, entries, true)
	if result.OK || !hasCode(result, "invalid_evidence") {
		t.Fatalf("empty evidence accepted as execution: %+v", result)
	}
}

func TestFinalVerificationRejectsVersionOneArtifact(t *testing.T) {
	root, manifest, entries := validFixture(t)
	evidence := &manifest.Items[0].Evidence[0]
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "revision": evidence.Revision,
		"binary_path": "evidence/candidate.bin", "binary_hash": evidence.BinaryHash,
		"environment": evidence.Environment, "command": evidence.Command,
		"exit_code": 0, "assertions": evidence.Assertions,
		"output_path": "evidence/output.log", "output_sha256": fixtureFileHash(t, filepath.Join(root, "evidence", "output.log")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evidence", "result.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	evidence.SHA256 = hex.EncodeToString(digest[:])
	result := Verify(root, manifest, entries, true)
	if result.OK || !hasCode(result, "invalid_evidence") {
		t.Fatalf("artifact v1 accepted for final verification: %+v", result)
	}
}

func TestFinalVerificationRejectsChangedExecutionFiles(t *testing.T) {
	for _, name := range []string{"evidence/candidate.bin", "evidence/output.log"} {
		t.Run(name, func(t *testing.T) {
			root, manifest, entries := validFixture(t)
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("changed after verification"), 0600); err != nil {
				t.Fatal(err)
			}
			result := Verify(root, manifest, entries, true)
			if result.OK || !hasCode(result, "invalid_evidence") {
				t.Fatalf("modified execution file accepted: %+v", result)
			}
		})
	}
}
