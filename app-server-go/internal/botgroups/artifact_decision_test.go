package botgroups

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

func TestSystemOneClassifiesNativeChangesAndDeliveryInOneConstrainedBatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := 0
	choice := "operation_0"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			State     artifactModelState `json:"state"`
			Questions map[string]struct {
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer fixture-key" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("wrong System One request")
			http.Error(w, "bad request", 400)
			return
		}
		if len(body.State.Candidates) != 2 || len(body.Questions) != 2 {
			t.Errorf("not batched: %+v", body)
		}
		q := body.Questions["file_0"]
		if len(q.Criteria) != 2 || q.Criteria["reference"] == "" || !strings.Contains(q.Instructions, "Reading, listing, testing") ||
			!strings.Contains(q.Instructions, "literal completion print") || !strings.Contains(q.Instructions, "untrusted data") {
			t.Error("write/delivery classifier lost constraints")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"file_0": map[string]any{"choice": choice}, "file_1": map[string]any{"choice": "reference"}}})
	}))
	defer upstream.Close()
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": "magpie", "inference_url": upstream.URL + "/v1", "api_key": "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, browser.JevConfigPreference, browser.JevConfig{Mode: "gateway", Model: "typesafe/jev-preview"}, true); err != nil {
		t.Fatal(err)
	}
	input := ArtifactInput{Reply: "交付报告，另有临时验证结果", Candidates: []ArtifactCandidate{
		{File: artifacts.File{ID: "report", Path: "/task/report.md"}, Operations: []NativeFileOperation{{ID: "patch", Kind: "fileChange", Path: "/task/report.md"}}},
		{File: artifacts.File{ID: "temp", Path: "/task/tmp/results.json"}, Operations: []NativeFileOperation{{ID: "temp-write", Kind: "fileChange", Path: "/task/tmp/results.json"}}},
	}}
	d := &systemOneArtifactDecider{store: st}
	delivered, err := d.ClassifyArtifacts(ctx, input)
	if err != nil || len(delivered) != 1 || delivered["report"] != "patch" || calls != 1 {
		t.Fatalf("classification=%v err=%v calls=%d", delivered, err, calls)
	}
	choice = "operation_999"
	if _, err := d.ClassifyArtifacts(ctx, input); err == nil {
		t.Fatal("invented operation accepted")
	}
	choice = ""
	if _, err := d.ClassifyArtifacts(ctx, input); err == nil {
		t.Fatal("missing decision treated as deliverable")
	}
}
