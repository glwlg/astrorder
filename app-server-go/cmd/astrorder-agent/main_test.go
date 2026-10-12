package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltCandidateExactArgsEnvironmentAndExitOutputs(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := candidateBinary(t, moduleRoot)

	requestBody := make(chan []byte, 1)
	requestAuth := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/agent/invoke" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		requestBody <- raw
		requestAuth <- request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(response, `{"ok":true,"seq":9007199254740993,"message":"真实子进程"}`)
	}))
	defer server.Close()

	command := exec.Command(binaryPath, "invoke", "custom.precision", "--input", `{"seq":9007199254740993,"message":"你好"}`)
	command.Env = append(filteredEnvironment(os.Environ()),
		"ASTRORDER_AGENT_TOKEN=subprocess-fixture-token",
		"ASTRORDER_BROWSER_SECRET=must-not-win",
		"ASTRORDER_CONNECTOR_SECRET=must-not-win",
		"ASTRORDER_TOKEN_FILE=must-not-be-read.token",
		"ASTRORDER_AGENT_URL="+server.URL+"/",
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("candidate exit: %v, stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	wantOutput := "{\n  \"ok\": true,\n  \"seq\": 9007199254740993,\n  \"message\": \"真实子进程\"\n}\n"
	if stdout.String() != wantOutput {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantOutput)
	}

	var envelope struct {
		Capability string         `json:"capability"`
		Input      map[string]any `json:"input"`
	}
	decoder := json.NewDecoder(bytes.NewReader(<-requestBody))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Capability != "custom.precision" || envelope.Input["seq"] != json.Number("9007199254740993") || envelope.Input["message"] != "你好" {
		t.Fatalf("request envelope = %#v", envelope)
	}
	if authorization := <-requestAuth; authorization != "Bearer subprocess-fixture-token" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestBuiltCandidateParseFailureExitTwo(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := candidateBinary(t, moduleRoot)
	command := exec.Command(binaryPath, "bogus")
	command.Env = filteredEnvironment(os.Environ())
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	var exitError *exec.ExitError
	if !strings.Contains(stderr.String(), "invalid choice") || stdout.Len() != 0 || !errorAs(err, &exitError) || exitError.ExitCode() != 2 {
		t.Fatalf("error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func candidateBinary(t *testing.T, moduleRoot string) string {
	t.Helper()
	if configured := os.Getenv("ASTRORDER_AGENT_CANDIDATE"); configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			t.Fatalf("resolve configured candidate: %v", err)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			t.Fatalf("stat configured candidate: %v", err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("configured candidate is not a regular file: %s", absolute)
		}
		return absolute
	}
	binaryName := "astrorder-agent-candidate"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/astrorder-agent")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build candidate: %v\n%s", err, output)
	}
	return binaryPath
}

func filteredEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"ASTRORDER_AGENT_TOKEN":      {},
		"ASTRORDER_BROWSER_SECRET":   {},
		"ASTRORDER_CONNECTOR_SECRET": {},
		"ASTRORDER_TOKEN_FILE":       {},
		"ASTRORDER_AGENT_URL":        {},
	}
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		if _, skip := blocked[strings.ToUpper(key)]; !skip {
			result = append(result, item)
		}
	}
	return result
}

func errorAs(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	exitError, ok := err.(*exec.ExitError)
	if ok {
		*target = exitError
	}
	return ok
}
