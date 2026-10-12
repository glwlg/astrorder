package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFrozenInventoryValidatesContentHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	document := map[string]any{"schema_version": 1, "source_revision": strings.Repeat("a", 40), "inventory": map[string]any{"http_routes": []any{map[string]any{"id": "http:health", "methods": []string{"GET"}, "path": "/health", "feature_id": "F01"}}}}
	hash, err := ContentHash(document)
	if err != nil {
		t.Fatal(err)
	}
	document["content_sha256"] = hash
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Hash != hash || len(baseline.Entries) != 1 || baseline.Entries[0].Name != "http:health" {
		t.Fatalf("wrong frozen inventory: %+v", baseline)
	}
	document["source_revision"] = strings.Repeat("b", 40)
	raw, _ = json.Marshal(document)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadBaseline(path); err == nil {
		t.Fatal("tampered inventory accepted")
	}
}

func TestLoadFeatureMatrixPreservesPerEntrypointCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.json")
	body := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f01","feature_id":"F01","source":["backend/src/astrorder/main.py"],"entrypoints":["http:health","http:auth"],"contract":"preserve health/auth","target_paths":["app-server-go/internal/httpapi/"],"current_owner":{"data":"Python App","background":"Python lifespan"},"tests":["backend/tests/test_bootstrap.py",{"path":"app-server-go/internal/httpapi/httpapi_test.go","name":"TestStaticSPAAndReserved404","command_id":"go-package-test","command":"cd app-server-go && go test ./internal/httpapi -count=1"}],"status":"NOT_STARTED","evidence":[]}]}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	matrix, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix.Items) != 2 || matrix.Items[0].ID == matrix.Items[1].ID {
		t.Fatalf("per-entry mapping lost: %+v", matrix)
	}
	if matrix.FrozenHash != strings.Repeat("b", 64) || matrix.Items[0].DataOwner["F01"] != "Python App" {
		t.Fatalf("hash/owner lost: %+v", matrix)
	}
	if matrix.CandidateRevision != strings.Repeat("c", 40) || len(matrix.Items[0].Tests) != 2 || matrix.Items[0].Tests[1].CommandID != "go-package-test" {
		t.Fatalf("candidate revision or command ID lost: %+v", matrix)
	}
}

func TestContentHashPreservesNativeIntegersAndUnicode(t *testing.T) {
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"文字":"<页面>","seq":9007199254740993}`))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	hash, err := ContentHash(document)
	if err != nil {
		t.Fatal(err)
	}
	other := map[string]any{"文字": "<页面>", "seq": json.Number("9007199254740993")}
	expected, err := ContentHash(other)
	if err != nil {
		t.Fatal(err)
	}
	if hash != expected {
		t.Fatal("hash changes integers or Unicode")
	}
}
