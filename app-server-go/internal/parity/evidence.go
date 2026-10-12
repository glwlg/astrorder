package parity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

const ExecutionArtifactVersion = 2

// ExecutionArtifact v2 binds captured output and the exact tested binary to a
// manifest candidate revision and one code-owned test command. Hashes prove
// byte integrity only; they do not prove that an author ran the command. The
// trusted runner and independent review remain separate gates.
type ExecutionArtifact struct {
	Version           int      `json:"schema_version"`
	CandidateRevision string   `json:"candidate_revision"`
	BinaryPath        string   `json:"binary_path"`
	BinaryHash        string   `json:"binary_hash"`
	Environment       string   `json:"environment"`
	CommandID         string   `json:"command_id"`
	TestPath          string   `json:"test_path"`
	TestName          string   `json:"test_name"`
	Command           string   `json:"command"`
	ExitCode          int      `json:"exit_code"`
	Assertions        []string `json:"assertions"`
	OutputPath        string   `json:"output_path"`
	OutputSHA256      string   `json:"output_sha256"`
	RunnerVersion     string   `json:"runner_version"`
}

func validateExecutionArtifact(root string, evidence Evidence) error {
	if !hashPattern.MatchString(evidence.SHA256) || !regularInside(root, evidence.Path) {
		return fmt.Errorf("execution artifact path and SHA256 are required")
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(evidence.Path)))
	if err != nil {
		return fmt.Errorf("read execution artifact: %w", err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != evidence.SHA256 {
		return fmt.Errorf("execution artifact SHA256 mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var artifact ExecutionArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return fmt.Errorf("invalid execution artifact: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("execution artifact must contain exactly one JSON object")
	}
	if artifact.Version != ExecutionArtifactVersion {
		return fmt.Errorf("execution artifact schema_version must be %d", ExecutionArtifactVersion)
	}
	if artifact.CandidateRevision != evidence.Revision || artifact.BinaryHash != evidence.BinaryHash ||
		artifact.Environment != evidence.Environment || artifact.CommandID != evidence.CommandID ||
		artifact.TestPath != evidence.TestPath || artifact.TestName != evidence.TestName ||
		artifact.Command != evidence.Command || artifact.ExitCode != evidence.ExitCode ||
		!reflect.DeepEqual(artifact.Assertions, evidence.Assertions) {
		return fmt.Errorf("execution artifact does not match declared result")
	}
	if artifact.RunnerVersion == "" {
		return fmt.Errorf("execution artifact runner_version is required")
	}
	for _, assertion := range artifact.Assertions {
		if assertion == "" {
			return fmt.Errorf("execution artifact has an empty assertion")
		}
	}
	if err := validateArtifactFile(root, artifact.BinaryPath, artifact.BinaryHash); err != nil {
		return fmt.Errorf("tested binary: %w", err)
	}
	if err := validateArtifactFile(root, artifact.OutputPath, artifact.OutputSHA256); err != nil {
		return fmt.Errorf("captured output: %w", err)
	}
	return nil
}

func validateArtifactFile(root, path, expected string) error {
	if !hashPattern.MatchString(expected) || !regularInside(root, path) {
		return fmt.Errorf("repository-relative file and SHA256 are required")
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("file SHA256 mismatch")
	}
	return nil
}
