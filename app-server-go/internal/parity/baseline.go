package parity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type Baseline struct {
	Revision string
	Hash     string
	Entries  []Entry
	Document json.RawMessage
}

// ContentHash uses the frozen scanner's canonical JSON representation. Unknown
// native integers stay json.Number; HTML and non-ASCII text are not rewritten.
func ContentHash(document map[string]any) (string, error) {
	copyDocument := make(map[string]any, len(document))
	for key, value := range document {
		if key != "content_sha256" {
			copyDocument[key] = value
		}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(copyDocument); err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
	return hex.EncodeToString(hash[:]), nil
}

func LoadBaseline(path string) (Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err = decoder.Decode(&document); err != nil {
		return Baseline{}, err
	}
	expected, _ := document["content_sha256"].(string)
	actual, err := ContentHash(document)
	if err != nil {
		return Baseline{}, err
	}
	if !hashPattern.MatchString(expected) || expected != actual {
		return Baseline{}, fmt.Errorf("frozen inventory SHA256 mismatch")
	}
	revision, _ := document["source_revision"].(string)
	if !revisionPattern.MatchString(revision) {
		return Baseline{}, fmt.Errorf("invalid source revision")
	}
	if document["schema_version"] != json.Number("1") {
		return Baseline{}, fmt.Errorf("unsupported frozen inventory version")
	}
	inventory, ok := document["inventory"].(map[string]any)
	if !ok {
		return Baseline{}, fmt.Errorf("frozen inventory missing")
	}
	result := Baseline{Revision: revision, Hash: expected, Entries: []Entry{}, Document: raw}
	seen := map[string]bool{}
	for kind, value := range inventory {
		entries, ok := value.([]any)
		if !ok {
			return Baseline{}, fmt.Errorf("invalid inventory category %s", kind)
		}
		for _, value := range entries {
			item, ok := value.(map[string]any)
			if !ok {
				return Baseline{}, fmt.Errorf("invalid entry in %s", kind)
			}
			id, _ := item["id"].(string)
			feature, _ := item["feature_id"].(string)
			if id == "" || seen[id] || !featurePattern.MatchString(feature) {
				return Baseline{}, fmt.Errorf("invalid or duplicate frozen identity %s", id)
			}
			seen[id] = true
			result.Entries = append(result.Entries, Entry{ID: id, Kind: kind, Name: id, FeatureID: feature})
		}
	}
	if len(result.Entries) == 0 {
		return Baseline{}, fmt.Errorf("empty frozen inventory")
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].ID < result.Entries[j].ID })
	return result, nil
}

// featureMatrix translates the P0 per-feature ledger into per-entry coverage.
// It is an on-read view of the same ledger, not a second mutable matrix.
type featureMatrix struct {
	Version           int           `json:"schema_version"`
	Revision          string        `json:"source_revision"`
	CandidateRevision string        `json:"candidate_revision"`
	InventoryHash     string        `json:"inventory_sha256"`
	AllowedStatuses   []string      `json:"allowed_statuses,omitempty"`
	Items             []featureItem `json:"migration_items"`
}

type featureItem struct {
	ID                 string          `json:"id"`
	FeatureID          string          `json:"feature_id"`
	Title              string          `json:"title,omitempty"`
	Stage              string          `json:"stage"`
	Source             []string        `json:"source"`
	Entrypoints        []string        `json:"entrypoints"`
	Contract           json.RawMessage `json:"contract"`
	TargetPaths        []string        `json:"target_paths"`
	BaselineDifference []string        `json:"baseline_difference,omitempty"`
	Owner              struct {
		Data       string `json:"data"`
		Background string `json:"background"`
	} `json:"current_owner"`
	DataOwner       map[string]string       `json:"data_owner"`
	BackgroundOwner map[string]string       `json:"background_owner"`
	Tests           []json.RawMessage       `json:"tests"`
	AcceptanceIDs   []string                `json:"acceptance_ids"`
	Status          string                  `json:"status"`
	Evidence        []Evidence              `json:"evidence"`
	Blockers        []string                `json:"blockers"`
	Review          Review                  `json:"review"`
	LegacyRemoved   bool                    `json:"legacy_removed"`
	Partials        []PartialImplementation `json:"partial_implementations"`
}

func decodeManifest(raw []byte) (Manifest, error) {
	var shape map[string]json.RawMessage
	if err := decodeStrictObject(raw, &shape); err != nil {
		return Manifest{}, err
	}
	if _, present := shape["migration_items"]; !present {
		var result Manifest
		if err := decodeStrictObject(raw, &result); err != nil {
			return Manifest{}, err
		}
		return result, nil
	}
	var source featureMatrix
	if err := decodeStrictObject(raw, &source); err != nil {
		return Manifest{}, err
	}
	result := Manifest{Version: source.Version, BaselineRevision: source.Revision, CandidateRevision: source.CandidateRevision, FrozenHash: source.InventoryHash, Items: []Item{}}
	seenParentIDs := make(map[string]bool, len(source.Items))
	seenFeatureIDs := make(map[string]bool, len(source.Items))
	for _, feature := range source.Items {
		trimmedID := strings.TrimSpace(feature.ID)
		if trimmedID == "" {
			return Manifest{}, fmt.Errorf("empty parent item ID")
		}
		if seenParentIDs[feature.ID] {
			return Manifest{}, fmt.Errorf("duplicate parent item ID: %s", feature.ID)
		}
		seenParentIDs[feature.ID] = true
		if !featurePattern.MatchString(feature.FeatureID) {
			return Manifest{}, fmt.Errorf("invalid feature ID %s on item %s", feature.FeatureID, feature.ID)
		}
		if seenFeatureIDs[feature.FeatureID] {
			return Manifest{}, fmt.Errorf("duplicate feature ID: %s", feature.FeatureID)
		}
		seenFeatureIDs[feature.FeatureID] = true

		if len(feature.Partials) > 0 {
			result.PartialGroups = append(result.PartialGroups, PartialGroup{
				ParentID: feature.ID, Entrypoints: feature.Entrypoints, Partials: feature.Partials,
			})
		}
		tests := []TestRef{}
		for _, rawTest := range feature.Tests {
			var path string
			if err := json.Unmarshal(rawTest, &path); err == nil {
				tests = append(tests, TestRef{Path: path})
				continue
			}
			var test TestRef
			if err := decodeStrictObject(rawTest, &test); err != nil {
				return Manifest{}, fmt.Errorf("invalid test mapping for %s: %w", feature.ID, err)
			}
			tests = append(tests, test)
		}
		dataOwner := feature.DataOwner
		if len(dataOwner) == 0 {
			dataOwner = map[string]string{feature.FeatureID: feature.Owner.Data}
		}
		backgroundOwner := feature.BackgroundOwner
		if len(backgroundOwner) == 0 {
			backgroundOwner = map[string]string{feature.FeatureID: feature.Owner.Background}
		}
		for _, entryID := range feature.Entrypoints {
			result.Items = append(result.Items, Item{ID: entryID, FeatureID: feature.FeatureID, Stage: feature.Stage, SourcePaths: feature.Source, Entrypoints: []string{entryID}, Contract: feature.Contract, TargetPaths: feature.TargetPaths, DataOwner: dataOwner, BackgroundOwner: backgroundOwner, AcceptanceIDs: feature.AcceptanceIDs, Tests: tests, Status: feature.Status, Evidence: feature.Evidence, Blockers: feature.Blockers, Review: feature.Review, LegacyRemoved: feature.LegacyRemoved})
		}
	}
	return result, nil
}
