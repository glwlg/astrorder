package modelgateway_test

import (
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAntigravitySyncPreviewAndOldDaemonRejection(t *testing.T) {
	for _, supported := range []bool{true, false} {
		current := map[sessiondv1.ManagedFile]string{}
		if supported {
			current[sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG] = `{"keep":true,"gateway":{"apiKey":"old-fixture-secret"},"models":{"list":[{"modelId":"removed"}]}}`
		}
		handler, cleanup := magpiePreviewEnv(t, current)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/preview", bytes.NewBufferString(`{"targets":[{"target_id":"local","agents":["antigravity"]}]}`))
		req.Header.Set("Authorization", "Bearer browser-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		cleanup()
		if !supported {
			if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "小内核") {
				t.Fatal("old daemon did not fail explicitly")
			}
			continue
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("preview status %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "old-fixture-secret") {
			t.Fatal("preview disclosed previous credentials")
		}
		var preview map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		change := findChange(t, preview, "antigravity_config")
		if change["changed"] != true {
			t.Fatal("missing config change")
		}
		diff := preview["targets"].([]any)[0].(map[string]any)["model_diff"].(map[string]any)
		if diff["removed"].([]any)[0] != "removed" {
			t.Fatal("stale gateway model not removed")
		}
	}
}
