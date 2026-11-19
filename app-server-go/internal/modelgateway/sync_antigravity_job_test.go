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

func TestRemoteSSHAntigravitySyncTargetsPreviewAndApply(t *testing.T) {
	content := []byte(`{"keep":true}`)
	var plannedSSH, appliedSSH bool
	var unexpectedEnv bool
	mock := &mockModelConfigServer{
		planFunc: func(ctx context.Context, req *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
			if sshTarget := req.GetTarget().GetSsh(); sshTarget != nil && sshTarget.ConnectionId == "wsl-ssh" && sshTarget.Config.GetHost() == "127.0.0.1" {
				plannedSSH = true
			}
			sum := sha256.Sum256(content)
			return &sessiondv1.ModelConfigPlanResponse{
				CurrentFiles: []*sessiondv1.ModelConfigFileState{{
					File:    sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG,
					Exists:  true,
					Content: content,
					Sha256:  hex.EncodeToString(sum[:]),
				}},
			}, nil
		},
		applyFunc: func(ctx context.Context, req *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error) {
			unexpectedEnv = len(req.EnvironmentValues) > 0
			if sshTarget := req.GetTarget().GetSsh(); sshTarget != nil && sshTarget.ConnectionId == "wsl-ssh" {
				appliedSSH = true
			}
			if len(req.Files) == 1 && req.Files[0].File == sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG {
				content = req.Files[0].Content
			}
			return &sessiondv1.ModelConfigApplyResponse{
				OperationId:    req.Meta.OperationId,
				OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
				Changed:        []sessiondv1.ManagedFile{sessiondv1.ManagedFile_MANAGED_FILE_ANTIGRAVITY_CONFIG},
			}, nil
		},
	}
	st, dc, authz, cleanup := setupTestEnvironment(t, mock)
	defer cleanup()
	if _, err := st.SaveSSHConnection(context.Background(), map[string]any{
		"id":           "wsl-ssh",
		"display_name": "WSL SSH",
		"host":         "127.0.0.1",
		"port":         22,
		"user":         "luwei",
		"state":        "connected",
	}); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "antigravity/gemini-3.8-flash"}}})
	}))
	defer upstream.Close()
	if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{
		"gateway_type":  "magpie",
		"inference_url": upstream.URL + "/v1",
		"api_key":       "fixture-secret",
	}); err != nil {
		t.Fatal(err)
	}
	handler, err := modelgateway.NewHandler(modelgateway.Options{Store: st, Client: dc, Authorizer: authz, Publisher: &mockPublisher{}, HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Target listing exposes antigravity on SSH target.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/model-sync/targets", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"ssh:wsl-ssh"`) {
		t.Fatalf("targets status=%d body=%s", rec.Code, rec.Body.String())
	}
	var targetsResp struct {
		Items []struct {
			ID     string   `json:"id"`
			Agents []string `json:"agents"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &targetsResp); err != nil {
		t.Fatal(err)
	}
	foundAgyOnSSH := false
	for _, item := range targetsResp.Items {
		if item.ID == "ssh:wsl-ssh" {
			for _, ag := range item.Agents {
				if ag == "antigravity" {
					foundAgyOnSSH = true
				}
			}
		}
	}
	if !foundAgyOnSSH {
		t.Fatalf("ssh target did not advertise antigravity: %+v", targetsResp.Items)
	}

	// 2. Preview succeeds for ssh:wsl-ssh with antigravity.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/preview", strings.NewReader(`{"targets":[{"target_id":"ssh:wsl-ssh","agents":["antigravity"]}]}`))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !plannedSSH {
		t.Fatalf("remote preview failed: code=%d plannedSSH=%v body=%s", rec.Code, plannedSSH, rec.Body.String())
	}

	// 3. Apply job succeeds with exact readback on ssh:wsl-ssh.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/model-sync/jobs", strings.NewReader(`{"targets":[{"target_id":"ssh:wsl-ssh","agents":["antigravity"]}]}`))
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote sync job start failed: %d %s", rec.Code, rec.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		pref, err := st.GetPreference(ctx, "model_sync:jobs")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(pref.Value)
		if strings.Contains(string(raw), `"status":"success"`) || strings.Contains(string(raw), `"status":"failed"`) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("remote sync job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := handler.(interface{ Close(context.Context) error }).Close(ctx); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/model-sync/jobs", nil)
	req.Header.Set("Authorization", "Bearer browser-secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !appliedSSH || unexpectedEnv || !strings.Contains(rec.Body.String(), `"status":"success"`) {
		t.Fatalf("remote antigravity sync job failed: appliedSSH=%v unexpectedEnv=%v body=%s", appliedSSH, unexpectedEnv, rec.Body.String())
	}
}
