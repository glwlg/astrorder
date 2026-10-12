package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Entry struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	FeatureID string `json:"feature_id"`
}
type Manifest struct {
	Version           int               `json:"version"`
	BaselineRevision  string            `json:"baseline_revision"`
	CandidateRevision string            `json:"candidate_revision"`
	BaselineHash      string            `json:"baseline_hash"`
	FrozenHash        string            `json:"inventory_sha256,omitempty"`
	Items             []Item            `json:"items"`
	Exceptions        []json.RawMessage `json:"exceptions"`
	PartialGroups     []PartialGroup    `json:"-"`
}
type Item struct {
	ID                 string            `json:"id"`
	FeatureID          string            `json:"feature_id"`
	Stage              string            `json:"stage"`
	SourcePaths        []string          `json:"source_paths"`
	Entrypoints        []string          `json:"entrypoints"`
	Contract           json.RawMessage   `json:"contract"`
	TargetPaths        []string          `json:"target_paths"`
	DataOwner          map[string]string `json:"data_owner"`
	BackgroundOwner    map[string]string `json:"background_owner"`
	AcceptanceIDs      []string          `json:"acceptance_ids"`
	Tests              []TestRef         `json:"tests"`
	Status             string            `json:"status"`
	Evidence           []Evidence        `json:"evidence"`
	Blockers           []string          `json:"blockers"`
	BaselineDifference []string          `json:"baseline_difference"`
	Review             Review            `json:"review"`
	LegacyRemoved      bool              `json:"legacy_removed"`
}
type TestRef struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	CommandID string `json:"command_id"`
	Command   string `json:"command"`
}
type Evidence struct {
	Revision    string   `json:"candidate_revision"`
	BinaryHash  string   `json:"binary_hash"`
	Environment string   `json:"environment"`
	CommandID   string   `json:"command_id"`
	TestPath    string   `json:"test_path"`
	TestName    string   `json:"test_name"`
	Command     string   `json:"command"`
	ExitCode    int      `json:"exit_code"`
	Assertions  []string `json:"assertions"`
	Path        string   `json:"path"`
	SHA256      string   `json:"sha256"`
}
type Review struct {
	Status   string `json:"status"`
	Reviewer string `json:"reviewer"`
	Path     string `json:"path"`
}
type Issue struct {
	ItemID string `json:"item_id,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}
type Result struct {
	OK         bool    `json:"ok"`
	Final      bool    `json:"final"`
	ItemCount  int     `json:"item_count"`
	EntryCount int     `json:"entry_count"`
	Issues     []Issue `json:"issues"`
}

func Load(path string) (Manifest, error) {
	var result Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	return decodeManifest(raw)
}
func LoadEntries(path string) ([]Entry, error) {
	var result []Entry
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}
func EntryHash(entries []Entry) string {
	copyEntries := append([]Entry(nil), entries...)
	sort.Slice(copyEntries, func(i, j int) bool { return copyEntries[i].ID < copyEntries[j].ID })
	raw, _ := json.Marshal(copyEntries)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var featurePattern = regexp.MustCompile(`^F(0[1-9]|1[0-6])$`)
var acceptancePattern = regexp.MustCompile(`^T(0[1-9]|1[0-5])$`)
var statuses = map[string]bool{"NOT_STARTED": true, "IMPLEMENTED": true, "ISOLATED_VERIFIED": true, "REVIEWED": true, "PRODUCTION_VERIFIED": true, "DONE": true, "BLOCKED": true}

// Verify consumes the frozen baseline without importing or executing legacy code.
// It never runs evidence commands supplied in the manifest.
func Verify(root string, manifest Manifest, entries []Entry, final bool) Result {
	result := Result{OK: true, Final: final, ItemCount: len(manifest.Items), EntryCount: len(entries), Issues: []Issue{}}
	issue := func(id, code, detail string) {
		result.OK = false
		result.Issues = append(result.Issues, Issue{id, code, detail})
	}
	if manifest.Version != 1 {
		issue("", "invalid_version", "migration manifest version must be 1")
	}
	if !revisionPattern.MatchString(manifest.BaselineRevision) {
		issue("", "invalid_revision", "baseline revision must be an exact Git SHA")
	}
	if final && !revisionPattern.MatchString(manifest.CandidateRevision) {
		issue("", "invalid_candidate_revision", "candidate revision must be a full lowercase Git SHA")
	}
	if len(entries) == 0 {
		issue("", "empty_baseline", "frozen baseline must not be empty")
	}
	if manifest.BaselineHash != EntryHash(entries) {
		issue("", "baseline_hash_mismatch", "frozen entrypoint hash does not match manifest")
	}
	for _, group := range manifest.PartialGroups {
		for _, detail := range validatePartialGroup(root, group) {
			issue(group.ParentID, "invalid_partial", detail)
		}
	}
	byID := map[string]Item{}
	owners := map[string]string{}
	for _, item := range manifest.Items {
		if item.ID == "" {
			issue("", "missing_id", "item ID is required")
			continue
		}
		if _, exists := byID[item.ID]; exists {
			issue(item.ID, "duplicate_id", "item ID is duplicated")
		}
		byID[item.ID] = item
		if !featurePattern.MatchString(item.FeatureID) {
			issue(item.ID, "invalid_feature", "feature_id must be F01-F16")
		}
		if !statuses[item.Status] {
			issue(item.ID, "invalid_status", "unknown migration status")
		}
		for kind, domains := range map[string]map[string]string{"data": item.DataOwner, "background": item.BackgroundOwner} {
			for domain, owner := range domains {
				key := kind + ":" + domain
				if old, exists := owners[key]; exists && old != owner {
					issue(item.ID, "conflicting_writer", key+" has owners "+old+" and "+owner)
				}
				owners[key] = owner
			}
		}
		if !final {
			continue
		}
		if item.Status != "DONE" {
			issue(item.ID, "unfinished_item", "required item is "+item.Status)
		}
		if len(item.Blockers) > 0 {
			issue(item.ID, "blocked_item", "required item has blockers")
		}
		if len(item.SourcePaths) == 0 || len(item.Entrypoints) == 0 || len(item.Contract) == 0 {
			issue(item.ID, "missing_contract", "source, entrypoints and contract are required")
		}
		if len(item.TargetPaths) == 0 {
			issue(item.ID, "missing_target", "target implementation is required")
		}
		for _, path := range item.TargetPaths {
			if !regularInside(root, path) {
				issue(item.ID, "missing_target", path)
			}
		}
		if len(item.AcceptanceIDs) == 0 {
			issue(item.ID, "missing_acceptance", "T01-T15 mapping is required")
		}
		for _, id := range item.AcceptanceIDs {
			if !acceptancePattern.MatchString(id) {
				issue(item.ID, "invalid_acceptance", id)
			}
		}
		if len(item.Tests) == 0 {
			issue(item.ID, "missing_tests", "executable behavior test mapping is required")
		}
		approvedTests := make(map[string]CommandSpec, len(item.Tests))
		for _, test := range item.Tests {
			if test.CommandID == "" || test.Command == "" || test.Name == "" || !regularInside(root, test.Path) {
				issue(item.ID, "invalid_test", "test path, name, command_id and command are required")
				continue
			}
			spec, err := ResolveTestCommand(test)
			if err != nil {
				issue(item.ID, "invalid_test", err.Error())
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(test.Path)))
			if err != nil || !regexp.MustCompile(`\b`+regexp.QuoteMeta(test.Name)+`\b`).Match(raw) {
				issue(item.ID, "invalid_test", "test symbol absent: "+test.Name)
				continue
			}
			approvedTests[testIdentity(test)] = spec
		}
		if len(item.Evidence) == 0 {
			issue(item.ID, "missing_evidence", "actual command and readback evidence is required")
		}
		for _, e := range item.Evidence {
			if !revisionPattern.MatchString(e.Revision) || !hashPattern.MatchString(e.BinaryHash) || e.Environment == "" || e.CommandID == "" || e.TestPath == "" || e.TestName == "" || e.Command == "" || e.ExitCode != 0 || len(e.Assertions) == 0 || !regularInside(root, e.Path) {
				issue(item.ID, "invalid_evidence", "evidence requires exact revision/hash, command/test binding, successful exit, assertions and artifact")
				continue
			}
			if e.Revision != manifest.CandidateRevision {
				issue(item.ID, "evidence_revision_mismatch", "evidence candidate revision does not match manifest candidate_revision")
				continue
			}
			spec, mapped := approvedTests[evidenceTestIdentity(e)]
			if !mapped || spec.ID != e.CommandID || spec.Display != e.Command {
				issue(item.ID, "invalid_evidence", "evidence is not bound to an approved mapped TestRef and exact resolved command")
				continue
			}
			if !spec.FinalEligible {
				issue(item.ID, "ineligible_final_evidence", "transitional Python baseline evidence cannot complete final Go verification")
				continue
			}
			if err := validateExecutionArtifact(root, e); err != nil {
				issue(item.ID, "invalid_evidence", err.Error())
			}
		}
		if item.Review.Status != "PASS" || item.Review.Reviewer == "" || !regularInside(root, item.Review.Path) {
			issue(item.ID, "missing_review", "independent PASS review artifact required")
		}
		if !item.LegacyRemoved {
			issue(item.ID, "legacy_retained", "legacy implementation not cleared")
		}
	}
	seenEntries := map[string]bool{}
	for _, entry := range entries {
		if seenEntries[entry.ID] {
			issue(entry.ID, "duplicate_baseline_id", "frozen ID duplicated")
		}
		seenEntries[entry.ID] = true
		item, exists := byID[entry.ID]
		if !exists {
			issue(entry.ID, "unmapped_entrypoint", entry.Name)
			continue
		}
		if item.FeatureID != entry.FeatureID {
			issue(entry.ID, "feature_changed", entry.Name)
		}
		found := false
		for _, name := range item.Entrypoints {
			if name == entry.Name {
				found = true
				break
			}
		}
		if !found {
			issue(entry.ID, "entrypoint_missing", entry.Name)
		}
	}
	sort.Slice(result.Issues, func(i, j int) bool {
		a, b := result.Issues[i], result.Issues[j]
		return fmt.Sprint(a.ItemID, a.Code, a.Detail) < fmt.Sprint(b.ItemID, b.Code, b.Detail)
	})
	return result
}

func testIdentity(test TestRef) string {
	return test.CommandID + "\x00" + test.Path + "\x00" + test.Name + "\x00" + test.Command
}

func evidenceTestIdentity(evidence Evidence) string {
	return evidence.CommandID + "\x00" + evidence.TestPath + "\x00" + evidence.TestName + "\x00" + evidence.Command
}

func regularInside(root, name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, `\:`) || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") {
		return false
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err = filepath.Rel(absolute, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	stat, err := os.Stat(resolved)
	return err == nil && stat.Mode().IsRegular()
}
