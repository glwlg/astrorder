package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPartialEntriesCannotOverlapAcrossSlices(t *testing.T) {
	root, raw, entries := partialMatrixFixture(t)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	feature := document["migration_items"].([]any)[0].(map[string]any)
	partials := feature["partial_implementations"].([]any)
	first := partials[0].(map[string]any)
	second := make(map[string]any)
	for key, value := range first {
		second[key] = value
	}
	second["id"] = "another-slice"
	feature["partial_implementations"] = append(partials, second)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BaselineHash = EntryHash(entries)
	if result := Verify(root, manifest, entries, false); result.OK || !hasCode(result, "invalid_partial") {
		t.Fatalf("overlapping partial entries accepted: %+v", result)
	}
}

func TestArtifactPathsMustUseCanonicalSlashIdentity(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "docs", "result.json")
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("unit fixture only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hash := fixtureFileHash(t, full)
	if err := validateArtifactFile(root, "docs/result.json", hash); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"docs/../docs/result.json", "docs/./result.json", "docs//result.json", `docs\result.json`} {
		t.Run(alias, func(t *testing.T) {
			if err := validateArtifactFile(root, alias, hash); err == nil {
				t.Fatalf("noncanonical artifact alias accepted: %s", alias)
			}
		})
	}
}
