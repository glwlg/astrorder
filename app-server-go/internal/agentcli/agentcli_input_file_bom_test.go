package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Windows PowerShell 5.1 writes UTF-8 with a BOM. --input-file must strip it
// without losing JSON number precision or UTF-8 text.
func TestInputFileStripsUTF8BOMAndPreservesBigIntegers(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "input.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"ttl":9007199254740993,"文字":"值"}`)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"invoke", "swarm.lock.list", "--input-file", path}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": "secret",
		"ASTRORDER_AGENT_URL":   server.URL,
	}))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 || len(recorded) != 1 {
		t.Fatalf("requests = %d, recorded = %d", requests.Load(), len(recorded))
	}
	got := recorded[0]
	if got.capability != "swarm.lock.list" {
		t.Fatalf("capability = %q", got.capability)
	}
	if number, ok := got.input["ttl"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("ttl lost precision: %#v", got.input["ttl"])
	}
	if got.input["文字"] != "值" {
		t.Fatalf("UTF-8 text = %#v", got.input["文字"])
	}
	if !strings.Contains(stdout.String(), `"status": "ok"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// A BOM-only file is an empty document, not a valid JSON object.
func TestInputFileBOMOnlyIsRejected(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, []byte{0xEF, 0xBB, 0xBF}, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"invoke", "swarm.lock.list", "--input-file", path}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": "secret",
		"ASTRORDER_AGENT_URL":   server.URL,
	}))
	if code != 1 || requests.Load() != 0 {
		t.Fatalf("exit = %d, requests = %d, stderr = %q", code, requests.Load(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid --input-file JSON") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
