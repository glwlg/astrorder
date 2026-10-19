package grpcapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/modelconfig"
)

func TestAntigravityTypedApplyAndReadback(t *testing.T) {
	f := startModelConfigFixture(t, nil)
	text := []byte(`{"gateway":{"enabled":true,"url":"http://127.0.0.1:3425/v1","wireProtocol":"openai","apiKey":"fixture"},"models":{"list":[{"modelId":"group/luozhi","displayName":"group/luozhi"}]}}`)
	req := &sessiondv1.ModelConfigApplyRequest{Meta: &sessiondv1.CommandMeta{OperationId: "agy-apply", Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1}, Files: []*sessiondv1.ModelConfigFileInput{{File: sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG, Content: text}}}
	reply, err := f.client.Apply(f.ctx, req)
	if err != nil || reply.Error != nil || len(reply.Changed) != 1 {
		t.Fatalf("typed apply failed: %v", err)
	}
	plan, err := f.client.Plan(f.ctx, &sessiondv1.ModelConfigPlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, state := range plan.CurrentFiles {
		if state.File == sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG {
			found = state.Exists && string(state.Content) == string(text) && state.Sha256 != ""
		}
	}
	if !found {
		t.Fatal("typed readback missing applied agy configuration")
	}
	retry, err := f.client.Apply(f.ctx, req)
	if err != nil || retry.Error != nil || len(retry.Changed) != 1 {
		t.Fatal("idempotent typed apply failed")
	}
}

func TestAntigravityTypedApplyNativeProtocol(t *testing.T) {
	executable := os.Getenv("ASTRORDER_AGY_GATEWAY_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit native executable required")
	}
	requests := make(chan bool, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests <- r.URL.Path == "/v1/chat/completions" && body["model"] == "group/luozhi" && r.Header.Get("Authorization") == "Bearer fixture-only"
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"AGY_TYPED_GATEWAY_OK\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	f := startModelConfigFixture(t, nil)
	doc := map[string]any{"gateway": map[string]any{"enabled": true, "url": upstream.URL + "/v1", "wireProtocol": "openai", "apiKey": "fixture-only"}, "models": map[string]any{"list": []any{map[string]any{"modelId": "group/luozhi", "displayName": "Gateway fixture"}}}}
	raw, _ := json.Marshal(doc)
	reply, err := f.client.Apply(f.ctx, &sessiondv1.ModelConfigApplyRequest{Meta: &sessiondv1.CommandMeta{OperationId: "agy-native-apply", Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1}, Files: []*sessiondv1.ModelConfigFileInput{{File: sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG, Content: raw}}})
	if err != nil || reply.Error != nil {
		t.Fatalf("apply failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.home, modelconfig.ManagedFiles[modelconfig.FileAntigravityConfig]))
	if err != nil || string(data) != string(raw) {
		t.Fatal("file readback mismatch")
	}
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ASTRORDER_") && !strings.HasPrefix(value, "AGY_") && !strings.HasPrefix(value, "ANTIGRAVITY_") && !strings.HasPrefix(value, "GEMINI_") && !strings.HasPrefix(value, "GOOGLE_") && !strings.HasPrefix(value, "HOME=") && !strings.HasPrefix(value, "USERPROFILE=") {
			env = append(env, value)
		}
	}
	env = append(env, "HOME="+f.home, "USERPROFILE="+f.home, "AGY_CLI_DISABLE_AUTO_UPDATE=1")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "models")
	cmd.Env = env
	cmd.Dir = f.home
	output, err := cmd.Output()
	if err != nil || !strings.Contains(string(output), "group/luozhi\tGateway fixture") {
		t.Fatal("native models did not read typed gateway configuration")
	}
	cmd = exec.CommandContext(ctx, executable, "--gemini_dir="+filepath.Join(f.home, ".gemini"), "--input-format", "stream-json", "--output-format", "stream-json", "--model", "group/luozhi")
	cmd.Env = env
	cmd.Dir = f.home
	cmd.Stdin = strings.NewReader("{\"event\":\"user\",\"message\":{\"content\":\"Reply OK. Do not use tools.\"}}\n")
	output, err = cmd.Output()
	if err != nil || !strings.Contains(string(output), "AGY_TYPED_GATEWAY_OK") || !strings.Contains(string(output), `"status":"SUCCESS"`) {
		t.Fatal("native gateway turn failed")
	}
	select {
	case valid := <-requests:
		if !valid {
			t.Fatal("native request identity or auth mismatch")
		}
	default:
		t.Fatal("native made no gateway request")
	}
	t.Log("typed apply, file readback, native model discovery and streaming request confirmed")
}
