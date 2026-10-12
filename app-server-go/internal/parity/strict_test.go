package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTestMatrixRaw() string {
	return `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03","feature_id":"F03","source":["backend/src/astrorder/main.py"],"entrypoints":["http:f03_a","http:f03_b"],"contract":"preserve f03","target_paths":["app-server-go/internal/f03/"],"current_owner":{"data":"Python App","background":"Python lifespan"},"tests":["backend/tests/test_f03.py"],"status":"NOT_STARTED","evidence":[]}]}`
}

func TestStrictDecodeRejectsMisspelledPartialImplementation(t *testing.T) {
	// Misspelled partial_implementation vs partial_implementations
	raw := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03","feature_id":"F03","source":["backend/src/astrorder/main.py"],"entrypoints":["http:f03"],"contract":"preserve f03","target_paths":["app-server-go/internal/f03/"],"current_owner":{"data":"Python App","background":"Python lifespan"},"tests":["backend/tests/test_f03.py"],"status":"NOT_STARTED","evidence":[],"partial_implementation":[{"id":"p1"}]}]}`
	path := filepath.Join(t.TempDir(), "matrix.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected decodeManifest to reject misspelled partial_implementation field")
	}
}

func TestStrictDecodeRejectsMisspelledExecuton(t *testing.T) {
	// Misspelled executon vs execution inside partial_implementations
	raw := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03","feature_id":"F03","source":["backend/src/astrorder/main.py"],"entrypoints":["http:f03"],"contract":"preserve f03","target_paths":["app-server-go/internal/f03/"],"current_owner":{"data":"Python App","background":"Python lifespan"},"tests":["backend/tests/test_f03.py"],"status":"NOT_STARTED","evidence":[],"partial_implementations":[{"id":"p1","stage":"P2","status":"ISOLATED_VERIFIED","entrypoints":["http:f03"],"data_owner":"Go","acceptance_ids":["T01"],"legacy_removed":false,"executon":{"environment":"docker"}}]}]}`
	path := filepath.Join(t.TempDir(), "matrix.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected decodeManifest to reject misspelled executon field")
	}
}

func TestStrictDecodeRejectsUnknownRootAndTestFields(t *testing.T) {
	// Unknown root field
	rawUnknownRoot := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","unknown_field":"foo","migration_items":[]}`
	path1 := filepath.Join(t.TempDir(), "matrix_root.json")
	if err := os.WriteFile(path1, []byte(rawUnknownRoot), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path1); err == nil {
		t.Fatal("expected decodeManifest to reject unknown root field")
	}

	// Unknown test object field
	rawUnknownTest := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03","feature_id":"F03","source":["backend/src/astrorder/main.py"],"entrypoints":["http:f03"],"contract":"preserve f03","target_paths":["app-server-go/internal/f03/"],"current_owner":{"data":"Python App","background":"Python lifespan"},"tests":[{"path":"foo.go","unknown_test_attr":123}],"status":"NOT_STARTED","evidence":[]}]}`
	path2 := filepath.Join(t.TempDir(), "matrix_test.json")
	if err := os.WriteFile(path2, []byte(rawUnknownTest), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path2); err == nil {
		t.Fatal("expected decodeManifest to reject unknown test field")
	}
}

func TestStrictDecodeRejectsTrailingGarbage(t *testing.T) {
	rawTrailing := validTestMatrixRaw() + ` {"extra":"garbage"}`
	path := filepath.Join(t.TempDir(), "matrix_trailing.json")
	if err := os.WriteFile(path, []byte(rawTrailing), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected decodeManifest to reject trailing content after JSON root object")
	}
}

func TestDecodeManifestRequiresValidNonEmptyParentAndFeatureID(t *testing.T) {
	// Blank parent group ID
	rawBlankParentID := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"  ","feature_id":"F03","source":["s"],"entrypoints":["e"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]}]}`
	path1 := filepath.Join(t.TempDir(), "matrix_blank_parent.json")
	_ = os.WriteFile(path1, []byte(rawBlankParentID), 0600)
	if _, err := Load(path1); err == nil {
		t.Fatal("expected error for blank parent group ID")
	}

	// Duplicate parent group ID
	rawDupParentID := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03","feature_id":"F03","source":["s"],"entrypoints":["e1"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]},{"id":"migration:f03","feature_id":"F04","source":["s"],"entrypoints":["e2"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]}]}`
	path2 := filepath.Join(t.TempDir(), "matrix_dup_parent.json")
	_ = os.WriteFile(path2, []byte(rawDupParentID), 0600)
	if _, err := Load(path2); err == nil {
		t.Fatal("expected error for duplicate parent group ID")
	}

	// Duplicate feature_id
	rawDupFeatureID := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f03a","feature_id":"F03","source":["s"],"entrypoints":["e1"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]},{"id":"migration:f03b","feature_id":"F03","source":["s"],"entrypoints":["e2"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]}]}`
	path3 := filepath.Join(t.TempDir(), "matrix_dup_feature.json")
	_ = os.WriteFile(path3, []byte(rawDupFeatureID), 0600)
	if _, err := Load(path3); err == nil {
		t.Fatal("expected error for duplicate feature_id")
	}

	// Invalid feature_id pattern (e.g. wrong format)
	rawInvalidFeatureID := `{"schema_version":1,"source_revision":"` + strings.Repeat("a", 40) + `","candidate_revision":"` + strings.Repeat("c", 40) + `","inventory_sha256":"` + strings.Repeat("b", 64) + `","migration_items":[{"id":"migration:f99","feature_id":"F99","source":["s"],"entrypoints":["e1"],"contract":"c","target_paths":["t"],"current_owner":{"data":"d","background":"b"},"tests":[],"status":"NOT_STARTED","evidence":[]}]}`
	path4 := filepath.Join(t.TempDir(), "matrix_invalid_feature.json")
	_ = os.WriteFile(path4, []byte(rawInvalidFeatureID), 0600)
	if _, err := Load(path4); err == nil {
		t.Fatal("expected error for invalid feature_id pattern")
	}
}
