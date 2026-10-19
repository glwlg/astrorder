package modelconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAntigravityPlanApplyAndDigestGuard(t *testing.T) {
	home := t.TempDir()
	svc, err := New(Config{AllowedRoots: []string{home}})
	if err != nil {
		t.Fatal(err)
	}
	text := `{"gateway":{"enabled":true,"url":"http://127.0.0.1:3425/v1","wireProtocol":"openai","apiKey":"fixture"},"models":{"list":[{"modelId":"group/luozhi","displayName":"group/luozhi"}]}}`
	request := map[string]any{"files": map[string]any{FileAntigravityConfig: text}, "expected_digests": map[string]any{FileAntigravityConfig: EmptySHA256}}
	plan, err := svc.Execute(context.Background(), "model_config.plan", request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ManagedFiles[FileAntigravityConfig])
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preview wrote file")
	}
	if plan["dryrun"] != true {
		t.Fatal("missing preview receipt")
	}
	if _, err = svc.Execute(context.Background(), "model_config.apply", request); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != text {
		t.Fatal("apply readback failed")
	}
	plan, err = svc.Execute(context.Background(), "model_config.plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := plan[FileAntigravityConfig].(map[string]any)
	if state["valid"] != true || state["sha256"] != sha256Hex(raw) {
		t.Fatal("invalid native config readback")
	}
	request["files"] = map[string]any{FileAntigravityConfig: `{}`}
	if _, err = svc.Execute(context.Background(), "model_config.apply", request); err == nil {
		t.Fatal("stale digest accepted")
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != text {
		t.Fatal("digest conflict changed file")
	}
}

func TestAntigravityRejectsMalformedConfiguration(t *testing.T) {
	for _, text := range []string{`null`, `[]`, `{"gateway":true}`, `{"gateway":{"enabled":true,"url":"file:///tmp/x","wireProtocol":"openai"}}`, `{"models":{"list":[{"modelId":"a"},{"modelId":"a"}]}}`, `{"models":{"list":[{}]}}`} {
		if validateManagedContent(FileAntigravityConfig, []byte(text)) == nil {
			t.Fatalf("accepted malformed config: %s", text)
		}
	}
}
