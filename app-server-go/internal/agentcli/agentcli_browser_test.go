package agentcli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// windowsCommandLineLimit is the CreateProcess command-line ceiling. Payloads
// above it cannot be passed as a single argument, so file transport must carry
// them byte for byte.
const windowsCommandLineLimit = 32767

func writeFixture(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func TestInvokeInputFileTransportsLargeComplexJSON(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	// Mixed quoting, backticks, comment markers, Unicode, and a value above
	// float64's exact integer range.
	complexText := "多行 \"双引号\" 与 '单引号' 与 `反引号` 与 // 注释 与 \\ 反斜杠 — ✓ 😀"
	padding := strings.Repeat(complexText, (windowsCommandLineLimit/len([]byte(complexText)))+8)
	if len([]byte(padding)) <= windowsCommandLineLimit {
		t.Fatalf("fixture must exceed the Windows command-line limit, got %d bytes", len([]byte(padding)))
	}
	document := map[string]any{
		"big":     json.Number("9007199254740993"),
		"padding": padding,
		"nested":  map[string]any{"文字": "值", "items": []any{json.Number("1"), "two"}},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := writeFixture(t, "payload.json", raw)

	env := baseEnv(server.URL)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"invoke", "swarm.lock.acquire", "--input-file", path}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 || len(recorded) != 1 {
		t.Fatalf("requests = %d, recorded = %d", requests.Load(), len(recorded))
	}
	got := recorded[0]
	if got.capability != "swarm.lock.acquire" {
		t.Fatalf("capability = %q", got.capability)
	}
	if got.input["padding"] != padding {
		t.Fatalf("padding round-trip mismatch: got %d bytes, want %d", len(got.input["padding"].(string)), len(padding))
	}
	if got.input["big"] != json.Number("9007199254740993") {
		t.Fatalf("big = %#v, want json.Number(9007199254740993)", got.input["big"])
	}
	nested, ok := got.input["nested"].(map[string]any)
	if !ok || nested["文字"] != "值" {
		t.Fatalf("nested = %#v", got.input["nested"])
	}
}

func TestInvokeInputFileMutualExclusionAndFileErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()

	good := writeFixture(t, "good.json", []byte(`{"a":1}`))
	notObject := writeFixture(t, "array.json", []byte(`[1,2]`))
	badJSON := writeFixture(t, "bad.json", []byte(`{"a":`))
	invalidUTF8 := writeFixture(t, "utf8.json", []byte{0x7b, 0x22, 0xff, 0xfe, 0x22, 0x7d})
	oversized := writeFixture(t, "big.json", []byte(`{"a":"`+strings.Repeat("x", 64)+`"}`))
	missing := filepath.Join(t.TempDir(), "absent.json")

	tests := []struct {
		name   string
		args   []string
		config Config
		want   string
	}{
		{
			name: "both flags",
			args: []string{"invoke", "catalog.list", "--input", `{"a":1}`, "--input-file", good},
			want: "cannot provide both --input and --input-file\n",
		},
		{
			name: "missing file",
			args: []string{"invoke", "catalog.list", "--input-file", missing},
			want: "read --input-file " + missing,
		},
		{
			name: "non object",
			args: []string{"invoke", "catalog.list", "--input-file", notObject},
			want: "--input-file must contain a JSON object\n",
		},
		{
			name: "invalid JSON",
			args: []string{"invoke", "catalog.list", "--input-file", badJSON},
			want: "invalid --input-file JSON:",
		},
		{
			name: "invalid UTF-8",
			args: []string{"invoke", "catalog.list", "--input-file", invalidUTF8},
			want: "--input-file " + invalidUTF8 + " contains invalid UTF-8\n",
		},
		{
			name:   "oversized",
			args:   []string{"invoke", "catalog.list", "--input-file", oversized},
			config: Config{MaxBodyBytes: 16},
			want:   "--input-file " + oversized + " exceeds maximum size of 16 bytes\n",
		},
	}
	env := mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "secret", "ASTRORDER_AGENT_URL": server.URL})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := RunWithConfig(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, env, test.config)
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), test.want) || !strings.HasSuffix(stderr.String(), "\n") {
				t.Fatalf("stderr = %q, want prefix %q", stderr.String(), test.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestInvokeInputFileAndInputKeepCommandSchema(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	path := writeFixture(t, "payload.json", []byte(`{"文字":"值","ttl":9007199254740993}`))
	env := mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "secret", "ASTRORDER_AGENT_URL": server.URL})

	cases := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"default empty", []string{"invoke", "catalog.list"}, map[string]any{}},
		{"inline", []string{"invoke", "catalog.list", "--input", `{"n":1}`}, map[string]any{"n": json.Number("1")}},
		{"file", []string{"invoke", "catalog.list", "--input-file", path}, map[string]any{"文字": "值", "ttl": json.Number("9007199254740993")}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, env); code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			got := recorded[len(recorded)-1]
			if got.capability != "catalog.list" || !reflect.DeepEqual(got.input, test.want) {
				t.Fatalf("request = %#v, want %#v", got, test.want)
			}
		})
	}

	var invokeRegistration *Registration
	for _, registration := range Registrations() {
		if registration.Command == "invoke" {
			copy := registration
			invokeRegistration = &copy
		}
	}
	if invokeRegistration == nil {
		t.Fatal("invoke registration missing")
	}
	var inputFile *OptionRegistration
	for index := range invokeRegistration.Options {
		if invokeRegistration.Options[index].Name == "input_file" {
			inputFile = &invokeRegistration.Options[index]
		}
	}
	if inputFile == nil || inputFile.Required || inputFile.Type != "string" || inputFile.Metavar != "INPUT_FILE" || !reflect.DeepEqual(inputFile.Flags, []string{"--input-file"}) {
		t.Fatalf("input_file metadata = %#v", inputFile)
	}
}

func TestBrowserEvalTransportsComplexScriptExactly(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	var script strings.Builder
	script.WriteString("(async () => {\n")
	script.WriteString("  // 注释：中文 \"双引号\" '单引号' `反引号` \\\\ 反斜杠 — ✓ 😀\n")
	script.WriteString("  const 标签 = \"值\";\n")
	script.WriteString("  const 模式 = /[\\u4e00-\\u9fff]+/gu;\n")
	script.WriteString("  const 模板 = `多行\n模板 ${标签}`;\n")
	script.WriteString("  return { 标签, 模式: 模式.source, 模板 };\n")
	script.WriteString("})()\n")
	// Pad past the Windows command-line ceiling with valid JS comments.
	line := "// " + strings.Repeat("填充 中文 `'\"", 40) + "\n"
	for script.Len() <= windowsCommandLineLimit {
		script.WriteString(line)
	}
	want := script.String()
	if len([]byte(want)) <= windowsCommandLineLimit {
		t.Fatalf("script must exceed the Windows command-line limit, got %d bytes", len([]byte(want)))
	}
	path := writeFixture(t, "script.js", []byte(want))

	env := baseEnv(server.URL)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"browser", "eval", "--script-file", path}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 || len(recorded) != 1 {
		t.Fatalf("requests = %d, recorded = %d", requests.Load(), len(recorded))
	}
	got := recorded[0]
	if got.capability != "browser.cdp" {
		t.Fatalf("capability = %q, want browser.cdp", got.capability)
	}
	if got.input["method"] != "Runtime.evaluate" {
		t.Fatalf("method = %#v", got.input["method"])
	}
	params, ok := got.input["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v", got.input["params"])
	}
	if params["expression"] != want {
		t.Fatalf("expression mismatch: got %d bytes, want %d", len(params["expression"].(string)), len(want))
	}
	if params["awaitPromise"] != true || params["returnByValue"] != true {
		t.Fatalf("params flags = %#v", params)
	}
	if got.input["session_key"] != "hermes::hermes-session-42" {
		t.Fatalf("session_key = %#v, want caller identity", got.input["session_key"])
	}
	caller, ok := got.input["caller"].(map[string]any)
	if !ok || caller["agent_id"] != "hermes" || caller["session_id"] != "hermes-session-42" {
		t.Fatalf("caller = %#v", got.input["caller"])
	}
}

func TestBrowserEvalExplicitSessionKeyWinsAndFileErrors(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()
	env := mapLookup(baseEnv(server.URL))

	t.Run("explicit session key", func(t *testing.T) {
		path := writeFixture(t, "script.js", []byte("1 + 1"))
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), []string{"browser", "eval", "--script-file", path, "--session-key", "codex::s9"}, strings.NewReader(""), &stdout, &stderr, env); code != 0 {
			t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
		}
		got := recorded[len(recorded)-1]
		if got.input["session_key"] != "codex::s9" {
			t.Fatalf("session_key = %#v", got.input["session_key"])
		}
	})

	missing := filepath.Join(t.TempDir(), "absent.js")
	invalidUTF8 := writeFixture(t, "bad.js", []byte{0x61, 0xff, 0x62})
	oversized := writeFixture(t, "big.js", []byte(strings.Repeat("x", 64)))
	tests := []struct {
		name   string
		args   []string
		config Config
		want   string
	}{
		{"missing file", []string{"browser", "eval", "--script-file", missing}, Config{}, "read --script-file " + missing},
		{"invalid UTF-8", []string{"browser", "eval", "--script-file", invalidUTF8}, Config{}, "--script-file " + invalidUTF8 + " contains invalid UTF-8\n"},
		{"oversized", []string{"browser", "eval", "--script-file", oversized}, Config{MaxBodyBytes: 16}, "--script-file " + oversized + " exceeds maximum size of 16 bytes\n"},
		{"script file required", []string{"browser", "eval"}, Config{}, "astrorder-agent browser eval: error: the following arguments are required: --script-file\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := requests.Load()
			var stdout, stderr bytes.Buffer
			code := RunWithConfig(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, env, test.config)
			wantCode := 1
			if test.name == "script file required" {
				wantCode = 2
			}
			if code != wantCode || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) || !strings.HasSuffix(stderr.String(), "\n") {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			if requests.Load() != before {
				t.Fatalf("unexpected upstream request for %s", test.name)
			}
		})
	}
}

func TestBrowserRequestsRequireCallerIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	path := writeFixture(t, "script.js", []byte("1"))
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"browser", "eval", "--script-file", path}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": "secret",
		"ASTRORDER_AGENT_URL":   server.URL,
	}))
	if code != 1 || calls.Load() != 0 || !strings.Contains(stderr.String(), "caller resolution failed") {
		t.Fatalf("exit = %d, calls = %d, stderr = %q", code, calls.Load(), stderr.String())
	}
}

func TestBrowserScreenshotOutputFileAvoidsBase64(t *testing.T) {
	image := []byte("PNG-bytes-\x00\x01")
	encoded := base64.StdEncoding.EncodeToString(image)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, `{"url":"https://example.test","title":"示例","mime_type":"image/png","screenshot":"`+encoded+`","session_key":"hermes::hermes-session-42"}`)
	}))
	defer server.Close()

	outputPath := filepath.Join(t.TempDir(), "shot.png")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"browser", "screenshot", "--output", outputPath}, strings.NewReader(""), &stdout, &stderr, mapLookup(baseEnv(server.URL)))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	saved, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(saved, image) {
		t.Fatalf("saved = %q, err = %v", saved, err)
	}
	if strings.Contains(stdout.String(), encoded) {
		t.Fatalf("stdout still carries base64: %q", stdout.String())
	}
	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("stdout JSON: %v (%q)", err, stdout.String())
	}
	if body["saved_to"] != outputPath || body["size_bytes"] != float64(len(image)) {
		t.Fatalf("body = %#v", body)
	}
	if _, ok := body["screenshot"]; ok {
		t.Fatalf("screenshot field should be removed: %#v", body)
	}
}

func TestBrowserScreenshotOutputFileFailsWhenScreenshotMissing(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"missing field", `{"url":"https://example.test"}`, "screenshot response missing screenshot data\n"},
		{"empty field", `{"url":"https://example.test","screenshot":""}`, "screenshot response missing screenshot data\n"},
		{"invalid base64", `{"screenshot":"%%%not-base64%%%"}`, "decode screenshot base64:"},
		{"invalid JSON", `not-json`, "Astrorder agent API returned invalid JSON:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			outputPath := filepath.Join(t.TempDir(), "shot.png")
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"browser", "screenshot", "--output", outputPath}, strings.NewReader(""), &stdout, &stderr, mapLookup(baseEnv(server.URL)))
			if code != 1 || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if !strings.HasPrefix(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want prefix %q", stderr.String(), test.want)
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("screenshot file must not exist on failure, stat err = %v", err)
			}
		})
	}
}

func TestBrowserScreenshotWithoutOutputKeepsRawResponse(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("img"))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, `{"screenshot":"`+encoded+`","session_key":"hermes::hermes-session-42"}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"browser", "screenshot"}, strings.NewReader(""), &stdout, &stderr, mapLookup(baseEnv(server.URL))); code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), encoded) {
		t.Fatalf("raw response changed: %q", stdout.String())
	}
}
