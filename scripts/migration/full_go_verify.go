// Command full-go-verify validates the frozen full-Go migration contract.
// It intentionally has no Python dependency and remains usable after the
// legacy App source tree is removed.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var requiredCategories = []string{
	"http_routes",
	"websocket_routes",
	"daemon_actions",
	"mcp_capabilities",
	"cli_commands",
	"database_objects",
	"preference_namespaces",
	"background_tasks",
	"remote_python_dependencies",
	"script_entries",
	"build_entries",
	"desktop_startup_entries",
	"configuration_keys",
}

func main() {
	root := flag.String("root", ".", "repository root")
	baseline := flag.String("baseline", "", "frozen baseline JSON")
	flag.Parse()
	if *baseline == "" {
		*baseline = filepath.Join(*root, "docs", "reports", "full-go-baseline.json")
	}
	if err := verify(*baseline); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read frozen inventory: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err = decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode frozen inventory: %w", err)
	}
	stored, ok := document["content_sha256"].(string)
	if !ok || stored == "" {
		return fmt.Errorf("frozen inventory has no content_sha256")
	}
	delete(document, "content_sha256")
	canonical, err := marshalCanonical(document)
	if err != nil {
		return fmt.Errorf("canonicalize frozen inventory: %w", err)
	}
	digest := sha256.Sum256(canonical)
	calculated := hex.EncodeToString(digest[:])
	if calculated != stored {
		return fmt.Errorf("frozen inventory SHA256 mismatch: stored=%s calculated=%s", stored, calculated)
	}
	if document["schema_version"] != json.Number("1") {
		return fmt.Errorf("unsupported frozen inventory schema_version %v", document["schema_version"])
	}
	if revision, _ := document["source_revision"].(string); len(revision) != 40 {
		return fmt.Errorf("source_revision is not a full Git revision")
	}
	schemas, ok := document["schemas"].(map[string]any)
	if !ok {
		return fmt.Errorf("frozen inventory has no schemas object")
	}
	components, ok := schemas["components"].(map[string]any)
	if !ok || len(components) == 0 {
		return fmt.Errorf("frozen inventory has no OpenAPI component schemas")
	}
	validation, ok := schemas["validation_error"].(map[string]any)
	if !ok || validation["status"] != json.Number("422") {
		return fmt.Errorf("frozen inventory has no normalized 422 validation contract")
	}
	inventory, ok := document["inventory"].(map[string]any)
	if !ok {
		return fmt.Errorf("frozen inventory has no inventory object")
	}
	seen := map[string]string{}
	counts := map[string]int{}
	for _, category := range requiredCategories {
		rawEntries, exists := inventory[category]
		if !exists {
			return fmt.Errorf("frozen inventory category %s is missing", category)
		}
		entries, ok := rawEntries.([]any)
		if !ok || len(entries) == 0 {
			return fmt.Errorf("frozen inventory category %s is empty or invalid", category)
		}
		counts[category] = len(entries)
		for index, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return fmt.Errorf("%s[%d] is not an object", category, index)
			}
			id, _ := entry["id"].(string)
			if id == "" {
				return fmt.Errorf("%s[%d] has no stable id", category, index)
			}
			if other, exists := seen[id]; exists {
				return fmt.Errorf("duplicate inventory id %s in %s and %s", id, other, category)
			}
			seen[id] = category
			source, ok := entry["source"].(map[string]any)
			if !ok {
				return fmt.Errorf("%s has no source object", id)
			}
			if sourcePath, _ := source["path"].(string); sourcePath == "" || filepath.IsAbs(sourcePath) {
				return fmt.Errorf("%s has invalid repository-relative source path", id)
			}
			if sourceHash, _ := source["sha256"].(string); len(sourceHash) != 64 {
				return fmt.Errorf("%s has invalid source SHA256", id)
			}
			if category == "daemon_actions" {
				support, _ := entry["support"].(string)
				if support != "SUPPORTED" && support != "UNSUPPORTED_BASELINE" {
					return fmt.Errorf("%s has invalid support classification", id)
				}
				dispatch, dispatchOK := entry["dispatch_sources"].([]any)
				_, callersOK := entry["app_callers"].([]any)
				if !dispatchOK || !callersOK {
					return fmt.Errorf("%s has incomplete daemon source/caller evidence", id)
				}
				if support == "SUPPORTED" && len(dispatch) == 0 {
					return fmt.Errorf("%s claims support without Go dispatch evidence", id)
				}
			}
			if feature, _ := entry["feature_id"].(string); len(feature) != 3 || feature[0] != 'F' {
				return fmt.Errorf("%s has invalid F01-F16 mapping", id)
			}
		}
	}
	categories := append([]string(nil), requiredCategories...)
	sort.Strings(categories)
	fmt.Printf("verified frozen inventory %s", stored)
	for _, category := range categories {
		fmt.Printf(" %s=%d", category, counts[category])
	}
	fmt.Println()
	return nil
}

func marshalCanonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
