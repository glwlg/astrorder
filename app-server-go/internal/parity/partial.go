package parity

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PartialImplementation records a bounded slice of an unfinished feature. It
// never supplies final Evidence, product registrations, or parent completion.
type PartialImplementation struct {
	ID              string            `json:"id"`
	Stage           string            `json:"stage"`
	Status          string            `json:"status"`
	Entrypoints     []string          `json:"entrypoints"`
	DataOwner       string            `json:"data_owner"`
	BackgroundOwner string            `json:"background_owner"`
	RPC             string            `json:"rpc"`
	Services        []string          `json:"services"`
	TargetPaths     []string          `json:"target_paths"`
	Remaining       []string          `json:"remaining"`
	AcceptanceIDs   []string          `json:"acceptance_ids"`
	LegacyRemoved   *bool             `json:"legacy_removed"`
	Review          PartialReview     `json:"review"`
	EvidencePath    string            `json:"evidence_path"`
	EvidenceSHA256  string            `json:"evidence_sha256"`
	Execution       *PartialExecution `json:"execution,omitempty"`
}

type PartialReview struct {
	Status         string `json:"status"`
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	Limits         string `json:"limits"`
	PriorFailPath  string `json:"prior_fail_path"`
	SourceIdentity string `json:"source_identity"`
}

// PartialExecution binds a historical process result. It does not satisfy the
// final execution-artifact schema or prove the current working-tree candidate.
type PartialExecution struct {
	Environment            string `json:"environment"`
	CandidateRevision      string `json:"candidate_revision"`
	CandidateBinarySHA256  string `json:"candidate_binary_sha256"`
	SourceFreshnessLimited *bool  `json:"source_freshness_limited"`
	ProcessResult          string `json:"process_result"`
	ProcessResultSHA256    string `json:"process_result_sha256"`
	FixtureLimits          string `json:"fixture_limits"`
}

// PartialProcessResult preserves the complete original isolation report schema.
// Its hash-bound claims remain documentary; verification never executes them.
type PartialProcessResult struct {
	Version               int      `json:"schema_version"`
	Passed                bool     `json:"passed"`
	Stage                 string   `json:"stage"`
	FixtureReceipt        string   `json:"fixture_receipt"`
	DaemonSHA256          string   `json:"daemon_sha256"`
	Commands              []string `json:"commands"`
	Assertions            []string `json:"assertions"`
	Cleanup               string   `json:"cleanup"`
	FullMigrationComplete *bool    `json:"full_migration_complete"`
	ProductionChanged     *bool    `json:"production_changed"`
}

// PartialGroup preserves the original feature boundary before entry expansion.
type PartialGroup struct {
	ParentID    string
	Entrypoints []string
	Partials    []PartialImplementation
}

func validatePartialGroup(root string, group PartialGroup) []string {
	var issues []string
	seenIDs := make(map[string]bool, len(group.Partials))
	parentEntries := make(map[string]bool, len(group.Entrypoints))
	claimedEntries := make(map[string]string)
	for _, id := range group.Entrypoints {
		parentEntries[id] = true
	}
	for _, partial := range group.Partials {
		if strings.TrimSpace(partial.ID) == "" || seenIDs[partial.ID] {
			issues = append(issues, "missing or duplicate partial ID: "+partial.ID)
		}
		seenIDs[partial.ID] = true
		if !regexp.MustCompile(`^P([0-9]|1[01])$`).MatchString(partial.Stage) {
			issues = append(issues, partial.ID+": invalid stage")
		}
		if !statuses[partial.Status] || partial.Status == "DONE" || partial.Status == "PRODUCTION_VERIFIED" {
			issues = append(issues, partial.ID+": bounded partial cannot claim production or final feature completion")
		}
		if len(partial.Entrypoints) == 0 || strings.TrimSpace(partial.DataOwner) == "" || len(partial.AcceptanceIDs) == 0 || partial.LegacyRemoved == nil {
			issues = append(issues, partial.ID+": entrypoints, owner, acceptance and explicit legacy disposition required")
		}
		for _, acceptance := range partial.AcceptanceIDs {
			if !acceptancePattern.MatchString(acceptance) {
				issues = append(issues, partial.ID+": invalid acceptance: "+acceptance)
			}
		}
		seenEntries := make(map[string]bool, len(partial.Entrypoints))
		for _, entry := range partial.Entrypoints {
			if old, exists := claimedEntries[entry]; exists && old != partial.ID {
				issues = append(issues, partial.ID+": entrypoint already claimed by "+old+": "+entry)
			}
			claimedEntries[entry] = partial.ID
			if seenEntries[entry] {
				issues = append(issues, partial.ID+": duplicate entrypoint: "+entry)
			}
			seenEntries[entry] = true
			if !parentEntries[entry] {
				issues = append(issues, partial.ID+": entrypoint outside parent: "+entry)
			}
		}
		if partial.Review.Path != "" || partial.Review.Status != "" || partial.Status == "REVIEWED" || partial.Status == "PRODUCTION_VERIFIED" {
			if partial.Review.Status != "PASS" && (partial.Review.Status != "PASS_SCOPED_PREVIOUS_HASHES" || strings.TrimSpace(partial.Review.Limits) == "") {
				issues = append(issues, partial.ID+": independent PASS review or explicit prior-hash limit required")
			}
			if (partial.Status == "REVIEWED" || partial.Status == "PRODUCTION_VERIFIED") && partial.Review.Status != "PASS" {
				issues = append(issues, partial.ID+": prior-hash review cannot certify current reviewed status")
			}
			if err := validateArtifactFile(root, partial.Review.Path, partial.Review.SHA256); err != nil {
				issues = append(issues, partial.ID+": review report: "+err.Error())
			} else {
				raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(partial.Review.Path)))
				var verdict struct {
					Passed bool `json:"passed"`
				}
				if err != nil || json.Unmarshal(raw, &verdict) != nil || !verdict.Passed {
					issues = append(issues, partial.ID+": independent report does not contain passed=true")
				}
			}
		}
		if partial.EvidencePath != "" || partial.EvidenceSHA256 != "" || partial.Status == "ISOLATED_VERIFIED" || partial.Status == "REVIEWED" || partial.Status == "PRODUCTION_VERIFIED" {
			if err := validateArtifactFile(root, partial.EvidencePath, partial.EvidenceSHA256); err != nil {
				issues = append(issues, partial.ID+": bounded execution record: "+err.Error())
			}
		}
		if partial.Execution != nil {
			for _, detail := range validatePartialExecution(root, *partial.Execution) {
				issues = append(issues, partial.ID+": "+detail)
			}
		}
	}
	return issues
}

func validatePartialExecution(root string, execution PartialExecution) []string {
	var issues []string
	if execution.Environment == "" || !revisionPattern.MatchString(execution.CandidateRevision) || !hashPattern.MatchString(execution.CandidateBinarySHA256) || execution.SourceFreshnessLimited == nil || !*execution.SourceFreshnessLimited || execution.FixtureLimits == "" {
		issues = append(issues, "historical execution requires environment, revision, binary hash and explicit freshness/fixture limits")
	}

	if err := validateArtifactFile(root, execution.ProcessResult, execution.ProcessResultSHA256); err != nil {
		return append(issues, "process result: "+err.Error())
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(execution.ProcessResult)))
	var result PartialProcessResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return append(issues, "process result must contain one strictly typed historical JSON object")
	}
	if result.Version != 1 || result.Stage != "P2-partial" || strings.TrimSpace(result.FixtureReceipt) == "" || strings.TrimSpace(result.Cleanup) == "" || len(result.Commands) == 0 || len(result.Assertions) == 0 {
		issues = append(issues, "process result requires schema v1, P2-partial, fixture scope, cleanup, commands and assertions")
	}
	for _, values := range [][]string{result.Commands, result.Assertions} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				issues = append(issues, "process result has an empty command or assertion")
			}
		}
	}
	if !result.Passed || result.DaemonSHA256 != execution.CandidateBinarySHA256 || result.FullMigrationComplete == nil || *result.FullMigrationComplete || result.ProductionChanged == nil || *result.ProductionChanged {
		issues = append(issues, "process result must match candidate hash and report passed bounded isolation without production changes")
	}
	return issues
}
