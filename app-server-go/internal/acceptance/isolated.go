// Package acceptance runs a bounded, private App process acceptance slice.
package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/lifecycle"
	"astrorder.dev/app-server/internal/parity"
)

const (
	RunnerVersion          = "go-app-process-isolated/v1"
	GoAppProcessIsolatedV1 = RunnerVersion
	maxOutputBytes         = 4 << 20
	processTimeout         = 45 * time.Second
)

var revisionPattern = regexpMustCompile(`^[a-f0-9]{40}$`)

type regexpMatcher interface{ MatchString(string) bool }

func regexpMustCompile(pattern string) regexpMatcher {
	return regexpWrapper{pattern: pattern}
}

type regexpWrapper struct{ pattern string }

func (wrapper regexpWrapper) MatchString(value string) bool {
	if wrapper.pattern != `^[a-f0-9]{40}$` || len(value) != 40 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'f') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// Options are explicit and have no production defaults.
type Options struct {
	Root              string
	AppExecutable     string
	OutputDir         string
	CandidateRevision string
	ControlExecutable string
}

type Result struct {
	OK                     bool     `json:"ok"`
	Scope                  string   `json:"scope"`
	CompleteMigration      bool     `json:"complete_migration"`
	CandidateRevision      string   `json:"candidate_revision"`
	CommandID              string   `json:"command_id"`
	TestPath               string   `json:"test_path"`
	TestName               string   `json:"test_name"`
	Command                string   `json:"command"`
	ExitCode               int      `json:"exit_code"`
	Assertions             []string `json:"assertions"`
	BinaryPath             string   `json:"binary_path"`
	BinarySHA256           string   `json:"binary_sha256"`
	OutputPath             string   `json:"output_path"`
	OutputSHA256           string   `json:"output_sha256"`
	ArtifactPath           string   `json:"artifact_path"`
	ArtifactSHA256         string   `json:"artifact_sha256"`
	ReadyAddress           string   `json:"ready_address"`
	ReadyPID               int      `json:"ready_pid"`
	CandidateVCSRevision   string   `json:"candidate_vcs_revision"`
	CandidateVCSModified   bool     `json:"candidate_vcs_modified"`
	CandidatePackage       string   `json:"candidate_package"`
	SourceFreshnessLimited bool     `json:"source_freshness_limited"`
}

type readyMessage struct {
	Ready   bool   `json:"ready"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
}

type capture struct {
	mu        sync.Mutex
	file      *os.File
	hash      hashWriter
	remaining int64
	truncated bool
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func Run(parent context.Context, options Options) (result Result, runErr error) {
	validated, err := validateOptions(options)
	if err != nil {
		return result, err
	}
	options = validated
	result = Result{
		Scope: "go-app-foundation-isolated", CompleteMigration: false,
		CandidateRevision: options.CandidateRevision, CommandID: parity.CommandGoAppIsolated,
		TestPath: parity.GoAppIsolatedTestPath, TestName: parity.GoAppIsolatedTestName,
		ExitCode: -1, SourceFreshnessLimited: true,
	}
	commandArgv := []string{options.ControlExecutable, "verify", "--isolated", "--root", options.Root, "--app-executable", options.AppExecutable, "--output-dir", options.OutputDir, "--candidate-revision", options.CandidateRevision}
	encodedCommand, _ := json.Marshal(commandArgv)
	result.Command = string(encodedCommand)
	if _, err := parity.ResolveTestCommand(parity.TestRef{Path: result.TestPath, Name: result.TestName, CommandID: result.CommandID, Command: result.Command}); err != nil {
		return result, fmt.Errorf("resolve isolated command: %w", err)
	}

	candidateBuild, err := inspectCandidate(options.AppExecutable, options.CandidateRevision)
	if err != nil {
		return result, err
	}
	result.CandidateVCSRevision = candidateBuild.revision
	result.CandidateVCSModified = candidateBuild.modified
	result.CandidatePackage = candidateBuild.packagePath

	if err := os.Mkdir(options.OutputDir, 0700); err != nil {
		return result, fmt.Errorf("create fresh output directory: %w", err)
	}
	ownedRoot, err := os.MkdirTemp(options.OutputDir, "isolated-state-")
	if err != nil {
		return result, fmt.Errorf("create isolated state root: %w", err)
	}
	staticDir := filepath.Join(ownedRoot, "frontend")
	if err := os.MkdirAll(staticDir, 0700); err != nil {
		return result, err
	}
	const spaFixture = "<!doctype html><title>Astrorder isolated acceptance</title><main>isolated-spa-fixture</main>"
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(spaFixture), 0600); err != nil {
		return result, err
	}
	credential, err := randomCredential()
	if err != nil {
		return result, err
	}

	candidatePath := filepath.Join(options.OutputDir, executableName("astrorder-app-candidate"))
	if err := copyExclusive(options.AppExecutable, candidatePath); err != nil {
		return result, fmt.Errorf("preserve candidate binary: %w", err)
	}
	binaryHash, err := hashFile(candidatePath)
	if err != nil {
		return result, err
	}
	result.BinaryPath, result.BinarySHA256 = candidatePath, binaryHash
	outputPath := filepath.Join(options.OutputDir, "go-app-process-isolated.log")
	captureFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, fmt.Errorf("create captured output: %w", err)
	}
	output := newCapture(captureFile, maxOutputBytes)
	result.OutputPath = outputPath
	defer func() {
		if err := output.close(); runErr == nil && err != nil {
			runErr = err
		}
		result.OutputSHA256 = output.sum()
	}()

	ctx, cancel := context.WithTimeout(parent, processTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, candidatePath, "--listen", "127.0.0.1:0", "--static-dir", staticDir)
	command.Dir = ownedRoot
	command.Env = isolatedEnvironment(credential)
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return result, err
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return result, err
	}
	configureHiddenProcess(command)
	if err := command.Start(); err != nil {
		return result, fmt.Errorf("start supplied App candidate: %w", err)
	}
	startedPID := command.Process.Pid
	waited := false
	defer func() {
		if waited {
			return
		}
		if identity, err := lifecycle.InspectProcess(startedPID); err == nil && identity.PID == startedPID {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	stderrDone := make(chan error, 1)
	go func() { _, err := io.Copy(output, stderrPipe); stderrDone <- err }()

	ready, stdoutDone, err := readReady(stdoutPipe, output)
	if err != nil {
		return result, err
	}
	result.ReadyAddress, result.ReadyPID = ready.Address, ready.PID
	if ready.PID != startedPID {
		return result, errors.New("candidate ready PID did not match the exact spawned process")
	}
	port, err := validateReadyAddress(ready.Address)
	if err != nil {
		return result, err
	}
	identity, err := lifecycle.InspectProcess(startedPID)
	if err != nil || !samePath(identity.ExecutablePath, candidatePath) {
		return result, errors.New("candidate process identity did not match the preserved binary")
	}
	owners, err := lifecycle.ListeningPIDs(port)
	if err != nil || len(owners) != 1 || owners[0] != startedPID {
		return result, fmt.Errorf("candidate did not exclusively own its ready listener: owners=%v err=%v", owners, err)
	}

	assertions, err := runHTTPSlice(ctx, ready.Address, credential, spaFixture)
	if err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, assertions...)
	shutdownRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ready.Address+"/_desktop/shutdown", nil)
	shutdownRequest.Header.Set("Authorization", "Bearer "+credential)
	shutdownResponse, err := isolatedHTTPClient().Do(shutdownRequest)
	if err != nil {
		return result, fmt.Errorf("authenticated App shutdown: %w", err)
	}
	shutdownBody, readErr := io.ReadAll(io.LimitReader(shutdownResponse.Body, 4096))
	shutdownResponse.Body.Close()
	if readErr != nil || shutdownResponse.StatusCode != http.StatusOK || !bytes.Contains(shutdownBody, []byte(`"stopping":true`)) {
		return result, fmt.Errorf("authenticated App shutdown response was not exact: status=%d", shutdownResponse.StatusCode)
	}
	result.Assertions = append(result.Assertions, "authenticated App shutdown returned stopping=true")

	waitErr := command.Wait()
	waited = true
	if stdoutErr := <-stdoutDone; stdoutErr != nil {
		return result, fmt.Errorf("capture candidate stdout: %w", stdoutErr)
	}
	if stderrErr := <-stderrDone; stderrErr != nil {
		return result, fmt.Errorf("capture candidate stderr: %w", stderrErr)
	}
	result.ExitCode = processExitCode(waitErr)
	if result.ExitCode != 0 {
		return result, fmt.Errorf("candidate exit code was %d", result.ExitCode)
	}
	if _, err := lifecycle.InspectProcess(startedPID); !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("candidate process did not exit exactly: %v", err)
	}
	owners, err = lifecycle.ListeningPIDs(port)
	if err != nil || len(owners) != 0 {
		return result, fmt.Errorf("candidate listener was not released: owners=%v err=%v", owners, err)
	}
	result.Assertions = append(result.Assertions, "exact candidate process exited with code 0", "random loopback listener released")

	if err := output.close(); err != nil {
		return result, err
	}
	result.OutputSHA256 = output.sum()
	artifact := parity.ExecutionArtifact{
		Version: parity.ExecutionArtifactVersion, CandidateRevision: options.CandidateRevision,
		BinaryPath: relativeSlash(options.Root, candidatePath), BinaryHash: result.BinarySHA256,
		Environment: "isolated-private-app-process", CommandID: result.CommandID,
		TestPath: result.TestPath, TestName: result.TestName, Command: result.Command,
		ExitCode: result.ExitCode, Assertions: result.Assertions,
		OutputPath: relativeSlash(options.Root, outputPath), OutputSHA256: result.OutputSHA256,
		RunnerVersion: RunnerVersion,
	}
	artifactPath := filepath.Join(options.OutputDir, "go-app-process-isolated-artifact.json")
	artifactBody, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return result, err
	}
	artifactBody = append(artifactBody, '\n')
	if err := os.WriteFile(artifactPath, artifactBody, 0600); err != nil {
		return result, fmt.Errorf("write execution artifact after cleanup: %w", err)
	}
	result.ArtifactPath = artifactPath
	artifactDigest := sha256.Sum256(artifactBody)
	result.ArtifactSHA256 = hex.EncodeToString(artifactDigest[:])
	result.OK = true
	return result, nil
}

type candidateBuildInfo struct {
	revision    string
	modified    bool
	packagePath string
}

func inspectCandidate(filename, expectedRevision string) (candidateBuildInfo, error) {
	info, err := buildinfo.ReadFile(filename)
	if err != nil {
		return candidateBuildInfo{}, fmt.Errorf("read candidate Go build info: %w", err)
	}
	result := candidateBuildInfo{packagePath: info.Path}
	if info.Path != "astrorder.dev/app-server/cmd/astrorder-app" {
		return result, fmt.Errorf("candidate package path %q is not the Astrorder App command", info.Path)
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.revision = setting.Value
		case "vcs.modified":
			result.modified = setting.Value == "true"
		}
	}
	if result.revision != expectedRevision {
		return result, fmt.Errorf("candidate vcs.revision %q does not match expected candidate revision", result.revision)
	}
	return result, nil
}

func Validate(options Options) error {
	_, err := validateOptions(options)
	return err
}

func validateOptions(options Options) (Options, error) {
	if runtime.GOOS != "windows" {
		return options, errors.New("isolated process ownership acceptance currently requires Windows")
	}
	if !revisionPattern.MatchString(options.CandidateRevision) {
		return options, errors.New("candidate revision must be a full lowercase Git SHA")
	}
	for label, value := range map[string]string{"root": options.Root, "app executable": options.AppExecutable, "output directory": options.OutputDir, "control executable": options.ControlExecutable} {
		if !filepath.IsAbs(value) {
			return options, fmt.Errorf("%s must be an explicit clean absolute path", label)
		}
	}
	root, err := filepath.EvalSymlinks(options.Root)
	if err != nil {
		return options, fmt.Errorf("resolve root: %w", err)
	}
	options.Root = root
	for label, value := range map[string]string{"app executable": options.AppExecutable, "control executable": options.ControlExecutable} {
		resolved, err := filepath.EvalSymlinks(value)
		if err != nil {
			return options, fmt.Errorf("resolve %s: %w", label, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return options, fmt.Errorf("%s must be an existing regular file", label)
		}
		if label == "app executable" {
			options.AppExecutable = resolved
		} else {
			options.ControlExecutable = resolved
		}
	}
	parent := filepath.Dir(options.OutputDir)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return options, fmt.Errorf("output directory parent must exist: %w", err)
	}
	options.OutputDir = filepath.Join(resolvedParent, filepath.Base(options.OutputDir))
	if pathWithin(options.Root, options.OutputDir) == false {
		return options, errors.New("output directory must be a fresh child of --root")
	}
	if _, err := os.Stat(options.OutputDir); !errors.Is(err, os.ErrNotExist) {
		return options, errors.New("output directory must not already exist")
	}
	return options, nil
}

func readReady(stdout io.Reader, output io.Writer) (readyMessage, <-chan error, error) {
	reader := bufio.NewReader(io.LimitReader(stdout, 64<<10))
	line, err := reader.ReadBytes('\n')
	if len(line) > 0 {
		_, _ = output.Write(line)
	}
	if err != nil {
		return readyMessage{}, nil, fmt.Errorf("read candidate ready JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var ready readyMessage
	if err := decoder.Decode(&ready); err != nil || !ready.Ready || ready.PID <= 0 || ready.Address == "" {
		return readyMessage{}, nil, errors.New("candidate emitted invalid ready JSON")
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(output, reader); done <- err }()
	return ready, done, nil
}

func validateReadyAddress(address string) (int, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return 0, errors.New("candidate ready address must include a loopback port")
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() || ip.To4() == nil {
		return 0, errors.New("candidate ready address must be random IPv4 loopback")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("candidate ready port is invalid")
	}
	return port, nil
}

func runHTTPSlice(ctx context.Context, address, credential, spaFixture string) ([]string, error) {
	baseURL := "http://" + address
	client := isolatedHTTPClient()
	request := func(method, requestPath string, body io.Reader, headers map[string]string) (int, http.Header, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, baseURL+requestPath, body)
		if err != nil {
			return 0, nil, nil, err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response, err := client.Do(req)
		if err != nil {
			return 0, nil, nil, err
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return response.StatusCode, response.Header.Clone(), payload, err
	}
	status, _, body, err := request(http.MethodGet, "/health", nil, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(`"service":"astrorder"`)) {
		return nil, fmt.Errorf("public health assertion failed: status=%d err=%v", status, err)
	}
	status, _, body, err = request(http.MethodGet, "/api/v1/auth/session", nil, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(`"authenticated":false`)) {
		return nil, fmt.Errorf("unauthenticated status assertion failed: status=%d err=%v", status, err)
	}
	wrongBody := strings.NewReader(`{"token":"wrong-isolated-token"}`)
	status, _, _, err = request(http.MethodPost, "/api/v1/auth/session", wrongBody, map[string]string{"Content-Type": "application/json", "Origin": baseURL})
	if err != nil || status != http.StatusUnauthorized {
		return nil, fmt.Errorf("wrong credential assertion failed: status=%d err=%v", status, err)
	}
	loginBody, _ := json.Marshal(map[string]string{"token": credential})
	status, _, body, err = request(http.MethodPost, "/api/v1/auth/session", bytes.NewReader(loginBody), map[string]string{"Content-Type": "application/json", "Origin": baseURL})
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(`"authenticated":true`)) {
		return nil, fmt.Errorf("login assertion failed: status=%d err=%v", status, err)
	}
	status, _, body, err = request(http.MethodGet, "/api/v1/auth/session", nil, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(`"authenticated":true`)) {
		return nil, fmt.Errorf("authenticated status assertion failed: status=%d err=%v", status, err)
	}
	status, _, body, err = request(http.MethodGet, "/chat/isolated-fixture", nil, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(spaFixture)) {
		return nil, fmt.Errorf("static SPA assertion failed: status=%d err=%v", status, err)
	}
	status, _, body, err = request(http.MethodGet, "/api/v1/strict-unregistered", nil, nil)
	if err != nil || status != http.StatusNotFound || !bytes.Contains(body, []byte(`"detail":"Not Found"`)) {
		return nil, fmt.Errorf("strict unregistered API assertion failed: status=%d err=%v", status, err)
	}
	return []string{
		"public health returned Astrorder protocol v1",
		"unauthenticated browser session reported false",
		"wrong browser credential returned 401",
		"generated browser credential established and restored an authenticated session",
		"known temporary frontend fixture served SPA fallback",
		"unregistered reserved API path returned strict JSON 404",
	}, nil
}

func isolatedHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout: 5 * time.Second, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext},
	}
}

func isolatedEnvironment(credential string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "ASTRORDER_") {
			result = append(result, entry)
		}
	}
	result = append(result, "ASTRORDER_BROWSER_SECRET="+credential, "ASTRORDER_CONNECTOR_SECRET=isolated-connector-not-used")
	return result
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func randomCredential() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func copyExclusive(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	return errors.Join(copyErr, closeErr)
}

func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func samePath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbsolute), filepath.Clean(rightAbsolute))
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func relativeSlash(root, filename string) string {
	relative, err := filepath.Rel(root, filename)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filename)
	}
	return filepath.ToSlash(relative)
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func newCapture(file *os.File, limit int64) *capture {
	return &capture{file: file, hash: sha256.New(), remaining: limit}
}

func (writer *capture) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.file == nil {
		return len(payload), nil
	}
	original := len(payload)
	if int64(len(payload)) > writer.remaining {
		payload = payload[:writer.remaining]
		writer.truncated = true
	}
	if len(payload) > 0 {
		if _, err := writer.hash.Write(payload); err != nil {
			return 0, err
		}
		if _, err := writer.file.Write(payload); err != nil {
			return 0, err
		}
		writer.remaining -= int64(len(payload))
	}
	return original, nil
}

func (writer *capture) close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.file == nil {
		return nil
	}
	file := writer.file
	writer.file = nil
	if writer.truncated {
		return errors.Join(file.Close(), errors.New("candidate output exceeded bounded capture limit"))
	}
	return file.Close()
}

func (writer *capture) sum() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return hex.EncodeToString(writer.hash.Sum(nil))
}
