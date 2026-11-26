package agentaccounts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
)

func makeJWT(claims map[string]any) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	raw, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return hdr + "." + payload + ".sig"
}

func TestCodexIdentityStableAcrossTokenRefresh(t *testing.T) {
	jwt1 := makeJWT(map[string]any{
		"email": "Alice@Example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-123",
		},
	})
	jwt2 := makeJWT(map[string]any{
		"email": "alice@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-123",
		},
	})
	cred1, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      jwt1,
			"access_token":  "access-old",
			"refresh_token": "refresh-old",
			"account_id":    "acct-123",
		},
	})
	cred2, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      jwt2,
			"access_token":  "access-new",
			"refresh_token": "refresh-new",
			"account_id":    "acct-123",
		},
	})

	id1, meta1, ok1 := CodexIdentity(cred1)
	id2, meta2, ok2 := CodexIdentity(cred2)
	if !ok1 || !ok2 {
		t.Fatal("expected valid Codex credentials")
	}
	if id1 != id2 {
		t.Fatalf("expected stable identity across token refresh: %s != %s", id1, id2)
	}
	if meta1.Email != "alice@example.com" || meta2.AuthMode != "chatgpt" {
		t.Fatalf("unexpected meta: %+v / %+v", meta1, meta2)
	}

	// API Key mode works too and gets distinct identity.
	apiCred := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test-12345"}`)
	idAPI, metaAPI, okAPI := CodexIdentity(apiCred)
	if !okAPI || idAPI == id1 || metaAPI.AuthMode != "apikey" {
		t.Fatalf("unexpected API key identity: ok=%v mode=%s", okAPI, metaAPI.AuthMode)
	}

	// Empty JSON rejected.
	if _, _, ok := CodexIdentity([]byte(`{}`)); ok {
		t.Fatal("empty object should be invalid")
	}
}

func TestGrokIdentityFormats(t *testing.T) {
	// Native Grok OAuth scoped store with JWT token.
	jwt := makeJWT(map[string]any{"email": "dev@x.ai", "sub": "user-9", "team_id": "team-1"})
	cred1, _ := json.Marshal(map[string]any{
		"https://accounts.x.ai/sign-in": map[string]any{
			"key":           jwt,
			"refresh_token": "rt-1",
		},
	})
	cred2, _ := json.Marshal(map[string]any{
		"https://accounts.x.ai/sign-in": map[string]any{
			"key":           jwt,
			"refresh_token": "rt-2",
		},
	})
	id1, meta1, ok1 := GrokIdentity(cred1)
	id2, _, ok2 := GrokIdentity(cred2)
	if !ok1 || !ok2 || id1 != id2 {
		t.Fatalf("expected stable Grok JWT identity: ok1=%v ok2=%v id1=%s id2=%s", ok1, ok2, id1, id2)
	}
	if meta1.Email != "dev@x.ai" || meta1.AuthMode != "oauth" {
		t.Fatalf("unexpected Grok meta: %+v", meta1)
	}

	// Native Grok API key scope.
	apiCred := []byte(`{"xai::api_key":{"key":"xai-test-key"}}`)
	idAPI, metaAPI, okAPI := GrokIdentity(apiCred)
	if !okAPI || idAPI == id1 || metaAPI.AuthMode != "apikey" {
		t.Fatalf("unexpected Grok API key identity: ok=%v meta=%+v", okAPI, metaAPI)
	}

	if _, _, ok := GrokIdentity([]byte(`{"empty":{}}`)); ok {
		t.Fatal("empty scoped object should be invalid")
	}
}

func TestVaultSaveDeduplicateListAndDelete(t *testing.T) {
	vault := NewVault(filepath.Join(t.TempDir(), "codex-vault"), "Codex", CodexIdentity)
	credA := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-alpha"}`)
	credB := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-beta"}`)

	idA, err := vault.Save("工作 Key", credA)
	if err != nil {
		t.Fatalf("save A: %v", err)
	}
	// Saving same credential with new name updates in place without duplicating.
	idA2, err := vault.Save("主力 Key", credA)
	if err != nil {
		t.Fatalf("resave A: %v", err)
	}
	if idA != idA2 {
		t.Fatalf("expected deduplicated ID %s, got %s", idA, idA2)
	}
	idB, err := vault.Save("备用 Key", credB)
	if err != nil {
		t.Fatalf("save B: %v", err)
	}

	items, err := vault.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(items))
	}

	got, err := vault.Credential(idA)
	if err != nil || string(got) != string(credA) {
		t.Fatalf("credential mismatch: %v %s", err, string(got))
	}

	if err := vault.Delete(idB); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	if err := vault.Delete(idB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
	items, _ = vault.List()
	if len(items) != 1 || items[0].ID != idA || items[0].Name != "主力 Key" {
		t.Fatalf("unexpected items after delete: %+v", items)
	}
}

func TestParseCodexQuotaResponses(t *testing.T) {
	id2, id3 := 2, 3
	accResp := rpcEnvelope{
		ID:     &id2,
		Result: json.RawMessage(`{"account":{"type":"chatgpt","email":"user@example.com","planType":"plus"}}`),
	}
	rlResp := rpcEnvelope{
		ID: &id3,
		Result: json.RawMessage(`{
			"rateLimits":{
				"limitId":"codex",
				"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1795675485},
				"secondary":{"usedPercent":35,"windowDurationMins":10080,"resetsAt":1796179148},
				"credits":{"hasCredits":true,"unlimited":false,"balance":"14.5"},
				"planType":"plus"
			},
			"rateLimitsByLimitId":{
				"codex":{
					"limitId":"codex",
					"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":1795675485},
					"secondary":{"usedPercent":35,"windowDurationMins":10080,"resetsAt":1796179148},
					"credits":{"hasCredits":true,"unlimited":false,"balance":"14.5"},
					"planType":"plus"
				}
			}
		}`),
	}
	now := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)
	res, err := ParseCodexQuotaResponses(accResp, rlResp, AccountMeta{}, "", now)
	if err != nil {
		t.Fatalf("parse codex quota: %v", err)
	}
	if res.Plan != "ChatGPT Plus" || res.AccountLabel != "user@example.com" {
		t.Fatalf("unexpected plan/label: %+v", res)
	}
	if len(res.Groups) != 1 || len(res.Groups[0].Buckets) != 2 {
		t.Fatalf("unexpected groups: %+v", res.Groups)
	}
	b0, b1 := res.Groups[0].Buckets[0], res.Groups[0].Buckets[1]
	if b0.Window != "5h" || b0.RemainingFraction != 0.88 || b0.ResetAt != 1795675485000 {
		t.Fatalf("unexpected 5h bucket: %+v", b0)
	}
	if b1.Window != "weekly" || b1.RemainingFraction != 0.65 || b1.ResetAt != 1796179148000 {
		t.Fatalf("unexpected weekly bucket: %+v", b1)
	}
	if res.Detail != "Credits 余额 14.5" {
		t.Fatalf("unexpected detail: %q", res.Detail)
	}
}

func TestParseGrokQuotaResponses(t *testing.T) {
	id2, id3, id4 := 2, 3, 4
	authEnv := rpcEnvelope{
		ID:     &id2,
		Result: json.RawMessage(`{"methodId":"grok-web","email":"grok@example.com","teamName":"xAI Studio"}`),
	}
	subEnv := rpcEnvelope{
		ID:     &id3,
		Result: json.RawMessage(`{"authenticated":true,"meta":{"planType":"supergrok"}}`),
	}
	billingEnv := rpcEnvelope{
		ID:     &id4,
		Result: json.RawMessage(`{"credits":{"remaining":80,"limit":100,"balance":80}}`),
	}
	now := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)
	res, err := ParseGrokQuotaResponses(authEnv, subEnv, billingEnv, AccountMeta{AuthMode: "oauth"}, "", now)
	if err != nil {
		t.Fatalf("parse grok quota: %v", err)
	}
	if res.Plan != "SuperGrok" {
		t.Fatalf("expected SuperGrok plan, got %q", res.Plan)
	}
	if len(res.Groups) != 1 || len(res.Groups[0].Buckets) != 1 || res.Groups[0].Buckets[0].RemainingFraction != 0.8 {
		t.Fatalf("unexpected grok buckets: %+v", res.Groups)
	}
}

func TestRemoteStdioRPCScriptWithPosixSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh test runs on Unix")
	}
	mockBin := filepath.Join(t.TempDir(), "mock-codex")
	script := `#!/bin/sh
if [ ! -f "$CODEX_HOME/auth.json" ]; then
  echo "missing auth.json" >&2
  exit 2
fi
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{"userAgent":"mock"}}\n'
      ;;
    *'"method":"account/read"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"chatgpt","email":"mock@example.com","planType":"pro"}}}\n'
      ;;
    *'"method":"account/rateLimits/read"'*)
      printf '{"jsonrpc":"2.0","id":3,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":25,"windowDurationMins":300,"resetsAt":1795675485},"planType":"pro"}}}\n'
      ;;
  esac
done
`
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	env := envtargettest.New("ssh:dev")
	env.Shell_ = func(shScript string, stdin []byte, args []string, _ *envtargettest.Env) (envtarget.Result, error) {
		cmdArgs := append([]string{"-c", shScript, "astrorder"}, args...)
		cmd := exec.CommandContext(context.Background(), "/bin/sh", cmdArgs...)
		cmd.Stdin = nil
		if len(stdin) > 0 {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.Output()
		if err != nil {
			return envtarget.Result{Code: 1}, err
		}
		return envtarget.Result{Stdout: string(out), Code: 0}, nil
	}

	cred := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"tok","refresh_token":"ref"}}`)
	quota, err := QueryCodexQuota(context.Background(), env, mockBin, cred)
	if err != nil {
		t.Fatalf("QueryCodexQuota via remote script failed: %v", err)
	}
	if quota.Plan != "ChatGPT Pro" || quota.AccountLabel != "mock@example.com" {
		t.Fatalf("unexpected quota: %+v", quota)
	}
	if len(quota.Groups) != 1 || quota.Groups[0].Buckets[0].RemainingFraction != 0.75 {
		t.Fatalf("unexpected buckets: %+v", quota.Groups)
	}
}
