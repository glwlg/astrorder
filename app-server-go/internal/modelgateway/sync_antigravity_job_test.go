package modelgateway_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/modelgateway"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestAntigravitySyncJobRequiresExactReadback(t *testing.T) {
	for _, confirm := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "mismatch"}[confirm], func(t *testing.T) {
			content := []byte(`{}`)
			applied := false
			unexpectedEnvironment := false
			mock := &mockModelConfigServer{
				planFunc: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
					raw := content
					if applied && !confirm {
						raw = []byte(`{"mismatch":true}`)
					}
					sum := sha256.Sum256(raw)
					return &sessiondv1.ModelConfigPlanResponse{CurrentFiles: []*sessiondv1.ModelConfigFileState{{File: sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG, Exists: true, Content: raw, Sha256: hex.EncodeToString(sum[:])}}}, nil
				},
				applyFunc: func(ctx context.Context, req *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error) {
					unexpectedEnvironment = len(req.EnvironmentValues) > 0
					if len(req.Files) == 1 && req.Files[0].File == sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG {
						content = req.Files[0].Content
						applied = true
					}
					return &sessiondv1.ModelConfigApplyResponse{OperationId: req.Meta.OperationId, OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED, Changed: []sessiondv1.ManagedFile{sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG}}, nil
				},
			}
			st, dc, authz, cleanup := setupTestEnvironment(t, mock)
			defer cleanup()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "group/luozhi"}}})
			}))
			defer upstream.Close()
			if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{"gateway_type": "magpie", "inference_url": upstream.URL + "/v1", "api_key": "fixture-secret"}); err != nil {
				t.Fatal(err)
			}
			handler, err := modelgateway.NewHandler(modelgateway.Options{Store: st, Client: dc, Authorizer: authz, Publisher: &mockPublisher{}, HTTPClient: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/jobs", strings.NewReader(`{"targets":[{"target_id":"local","agents":["antigravity"]}]}`))
			req.Header.Set("Authorization", "Bearer browser-secret")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("start status %d", rec.Code)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				pref, err := st.GetPreference(ctx, "model_sync:jobs")
				if err != nil { t.Fatal(err) }
				raw, _ := json.Marshal(pref.Value)
				if strings.Contains(string(raw), `"status":"success"`) || strings.Contains(string(raw), `"status":"failed"`) { break }
				if ctx.Err() != nil { t.Fatal("sync job did not finish") }
				time.Sleep(10*time.Millisecond)
			}
			if err = handler.(interface{ Close(context.Context) error }).Close(ctx); err != nil {
				t.Fatal(err)
			}
			req = httptest.NewRequest(http.MethodGet, "/api/v1/model-sync/jobs", nil)
			req.Header.Set("Authorization", "Bearer browser-secret")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			var reply struct {
				Items []struct {
					Status string `json:"status"`
				} `json:"items"`
			}
			if err = json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if !applied || unexpectedEnvironment || len(reply.Items) != 1 {
				t.Fatal("agy-only apply contract failed")
			}
			expected := "failed"
			if confirm {
				expected = "success"
			}
			if reply.Items[0].Status != expected {
				t.Fatalf("readback status=%s want=%s", reply.Items[0].Status, expected)
			}
		})
	}
}
