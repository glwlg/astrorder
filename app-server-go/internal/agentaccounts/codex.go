package agentaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/envtarget"
)

type codexAuthDocument struct {
	AuthMode     string `json:"auth_mode"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       *struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

// CodexIdentity validates a ~/.codex/auth.json document and computes a stable
// opaque identity that survives OAuth token refreshes.
func CodexIdentity(data []byte) (string, AccountMeta, bool) {
	if len(data) == 0 || len(data) > MaxCredentialBytes {
		return "", AccountMeta{}, false
	}
	var doc codexAuthDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", AccountMeta{}, false
	}

	var email, accountID, refreshToken, accessToken string
	if doc.Tokens != nil {
		accountID = strings.TrimSpace(doc.Tokens.AccountID)
		refreshToken = strings.TrimSpace(doc.Tokens.RefreshToken)
		accessToken = strings.TrimSpace(doc.Tokens.AccessToken)
		for _, tok := range []string{doc.Tokens.IDToken, doc.Tokens.AccessToken} {
			claims := decodeJWTPayload(tok)
			if claims == nil {
				continue
			}
			if email == "" {
				email = strings.ToLower(stringValue(claims, "email"))
			}
			if authObj, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
				if accountID == "" {
					accountID = stringValue(authObj, "chatgpt_account_id", "chatgpt_user_id")
				}
			}
			if accountID == "" {
				accountID = stringValue(claims, "sub")
			}
		}
	}

	apiKey := strings.TrimSpace(doc.OpenAIAPIKey)
	hasTokens := doc.Tokens != nil && (doc.Tokens.IDToken != "" || accessToken != "" || refreshToken != "")
	authMode := strings.ToLower(strings.TrimSpace(doc.AuthMode))
	if authMode == "" {
		if hasTokens {
			authMode = "chatgpt"
		} else if apiKey != "" {
			authMode = "apikey"
		}
	}

	meta := AccountMeta{Email: email, AuthMode: authMode}
	switch {
	case email != "" && accountID != "":
		return sha256Hex("codex:chatgpt", email, accountID), meta, true
	case accountID != "":
		return sha256Hex("codex:account", accountID), meta, true
	case email != "":
		return sha256Hex("codex:email", email), meta, true
	case refreshToken != "":
		return sha256Hex("codex:refresh", refreshToken), meta, true
	case accessToken != "":
		return sha256Hex("codex:access", accessToken), meta, true
	case apiKey != "":
		return sha256Hex("codex:apikey", apiKey), meta, true
	default:
		return "", AccountMeta{}, false
	}
}

func isCodexAPIKeyOnly(data []byte) bool {
	var doc codexAuthDocument
	if json.Unmarshal(data, &doc) != nil {
		return false
	}
	hasTokens := doc.Tokens != nil && (strings.TrimSpace(doc.Tokens.IDToken) != "" ||
		strings.TrimSpace(doc.Tokens.AccessToken) != "" ||
		strings.TrimSpace(doc.Tokens.RefreshToken) != "")
	return !hasTokens && strings.TrimSpace(doc.OpenAIAPIKey) != ""
}

// QueryCodexQuota runs an isolated `CODEX_HOME=<tmp> codex app-server` probe to
// fetch the account's subscription plan and 5h/weekly rate-limit windows
// without mutating the target machine's active ~/.codex/auth.json.
func QueryCodexQuota(ctx context.Context, env envtarget.Env, executable string, authJSON []byte) (QuotaResult, error) {
	_, meta, ok := CodexIdentity(authJSON)
	if !ok {
		return QuotaResult{}, ErrInvalidCredential
	}
	now := time.Now().UTC()
	if isCodexAPIKeyOnly(authJSON) {
		return QuotaResult{
			Plan:         "API Key",
			AccountLabel: meta.Email,
			Detail:       "当前为 API Key 模式，无订阅周期额度",
			Groups:       []QuotaGroup{},
			AsOf:         now.Format(time.RFC3339),
		}, nil
	}

	responses, stderr, err := runIsolatedStdioRPC(ctx, env, isolatedRPCRequest{
		Prefix:      "codex",
		BinaryName:  "codex",
		Executable:  executable,
		Subcommand:  []string{"app-server"},
		HomeEnvVar:  "CODEX_HOME",
		AuthFile:    "auth.json",
		AuthJSON:    authJSON,
		InitRequest: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"astrorder","version":"1.0.0"}}}`,
		FollowUps: []string{
			`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
			`{"jsonrpc":"2.0","id":2,"method":"account/read","params":{}}`,
			`{"jsonrpc":"2.0","id":3,"method":"account/rateLimits/read","params":{}}`,
		},
		RequiredIDs: []int{2, 3},
		Timeout:     25 * time.Second,
	})
	if err != nil {
		return QuotaResult{}, err
	}
	return ParseCodexQuotaResponses(responses[2], responses[3], meta, stderr, now)
}

type codexAccountReadResult struct {
	Account *struct {
		Type          string `json:"type"`
		Email         string `json:"email"`
		PlanType      string `json:"planType"`
		WorkspaceName string `json:"workspaceName"`
	} `json:"account"`
}

type codexRateLimitWindow struct {
	UsedPercent        float64  `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *float64 `json:"resetsAt"`
}

type codexCreditsSnapshot struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type codexRateLimitSnapshot struct {
	LimitID   string                `json:"limitId"`
	LimitName string                `json:"limitName"`
	Primary   *codexRateLimitWindow `json:"primary"`
	Secondary *codexRateLimitWindow `json:"secondary"`
	Credits   *codexCreditsSnapshot `json:"credits"`
	PlanType  string                `json:"planType"`
}

type codexRateLimitsReadResult struct {
	RateLimits          *codexRateLimitSnapshot           `json:"rateLimits"`
	RateLimitsByLimitID map[string]codexRateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// ParseCodexQuotaResponses converts Codex app-server `account/read` (id=2) and
// `account/rateLimits/read` (id=3) JSON-RPC responses into a QuotaResult.
func ParseCodexQuotaResponses(accountEnv, rateLimitsEnv rpcEnvelope, meta AccountMeta, stderr string, now time.Time) (QuotaResult, error) {
	if accountEnv.ID == nil && rateLimitsEnv.ID == nil {
		if stderr != "" {
			return QuotaResult{}, fmt.Errorf("无法从 Codex 读取额度: %s", firstLine(stderr))
		}
		return QuotaResult{}, errors.New("Codex 未返回账号或额度响应")
	}

	var accountRes codexAccountReadResult
	if len(accountEnv.Result) > 0 {
		_ = json.Unmarshal(accountEnv.Result, &accountRes)
	}

	planRaw := ""
	accountLabel := meta.Email
	if accountRes.Account != nil {
		planRaw = accountRes.Account.PlanType
		if accountRes.Account.Email != "" {
			accountLabel = accountRes.Account.Email
		}
		if accountRes.Account.WorkspaceName != "" {
			if accountLabel != "" {
				accountLabel += " · " + accountRes.Account.WorkspaceName
			} else {
				accountLabel = accountRes.Account.WorkspaceName
			}
		}
		if strings.EqualFold(accountRes.Account.Type, "apiKey") {
			return QuotaResult{
				Plan:         "API Key",
				AccountLabel: accountLabel,
				Detail:       "当前为 API Key 模式，无订阅周期额度",
				Groups:       []QuotaGroup{},
				AsOf:         now.Format(time.RFC3339),
			}, nil
		}
	}

	if rateLimitsEnv.Error != nil {
		msg := strings.TrimSpace(rateLimitsEnv.Error.Message)
		if msg == "" {
			msg = "额度查询失败"
		}
		return QuotaResult{}, fmt.Errorf("Codex 额度读取失败: %s", msg)
	}
	if len(rateLimitsEnv.Result) == 0 {
		if accountEnv.Error != nil && strings.TrimSpace(accountEnv.Error.Message) != "" {
			return QuotaResult{}, fmt.Errorf("Codex 账号状态读取失败: %s", strings.TrimSpace(accountEnv.Error.Message))
		}
		return QuotaResult{}, errors.New("Codex 未返回额度数据")
	}

	var rlRes codexRateLimitsReadResult
	if err := json.Unmarshal(rateLimitsEnv.Result, &rlRes); err != nil {
		return QuotaResult{}, fmt.Errorf("Codex 额度响应格式无效: %w", err)
	}

	snapshots := make([]codexRateLimitSnapshot, 0, len(rlRes.RateLimitsByLimitID)+1)
	if len(rlRes.RateLimitsByLimitID) > 0 {
		keys := make([]string, 0, len(rlRes.RateLimitsByLimitID))
		for k := range rlRes.RateLimitsByLimitID {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i] == "codex" {
				return true
			}
			if keys[j] == "codex" {
				return false
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			snap := rlRes.RateLimitsByLimitID[k]
			if snap.LimitID == "" {
				snap.LimitID = k
			}
			snapshots = append(snapshots, snap)
		}
	} else if rlRes.RateLimits != nil {
		snapshots = append(snapshots, *rlRes.RateLimits)
	}

	groups := make([]QuotaGroup, 0, len(snapshots))
	detailParts := make([]string, 0, 2)
	for _, snap := range snapshots {
		if planRaw == "" && snap.PlanType != "" {
			planRaw = snap.PlanType
		}
		buckets := make([]QuotaBucket, 0, 2)
		if snap.Primary != nil {
			buckets = append(buckets, codexWindowToBucket("primary", snap.Primary))
		}
		if snap.Secondary != nil {
			buckets = append(buckets, codexWindowToBucket("secondary", snap.Secondary))
		}
		if len(buckets) > 0 {
			groupName := codexGroupLabel(snap.LimitID, snap.LimitName)
			groups = append(groups, QuotaGroup{
				Name:    groupName,
				Buckets: buckets,
			})
		}
		if snap.Credits != nil && snap.Credits.HasCredits {
			if snap.Credits.Unlimited {
				detailParts = append(detailParts, "Credits 无限额度")
			} else if bal := strings.TrimSpace(snap.Credits.Balance); bal != "" && bal != "0" {
				detailParts = append(detailParts, "Credits 余额 "+bal)
			}
		}
	}

	return QuotaResult{
		Plan:         formatCodexPlan(planRaw),
		TierID:       strings.TrimSpace(planRaw),
		AccountLabel: accountLabel,
		Detail:       strings.Join(detailParts, " · "),
		Groups:       groups,
		AsOf:         now.Format(time.RFC3339),
	}, nil
}

func codexWindowToBucket(fallbackID string, win *codexRateLimitWindow) QuotaBucket {
	windowID := fallbackID
	name := fallbackID
	if win.WindowDurationMins != nil {
		mins := *win.WindowDurationMins
		switch {
		case mins == 300:
			windowID, name = "5h", "5 小时"
		case mins == 10080:
			windowID, name = "weekly", "每周"
		case mins > 0 && mins%1440 == 0:
			days := mins / 1440
			windowID, name = fmt.Sprintf("%dd", days), fmt.Sprintf("%d 天", days)
		case mins > 0 && mins%60 == 0:
			hours := mins / 60
			windowID, name = fmt.Sprintf("%dh", hours), fmt.Sprintf("%d 小时", hours)
		case mins > 0:
			windowID, name = fmt.Sprintf("%dm", mins), fmt.Sprintf("%d 分钟", mins)
		}
	}
	var resetAt int64
	if win.ResetsAt != nil {
		resetAt = unixToMillis(*win.ResetsAt)
	}
	return QuotaBucket{
		ID:                windowID,
		Name:              name,
		Window:            windowID,
		RemainingFraction: clampFraction(1 - win.UsedPercent/100.0),
		ResetAt:           resetAt,
	}
}

func codexGroupLabel(limitID, limitName string) string {
	if trimmed := strings.TrimSpace(limitName); trimmed != "" {
		return trimmed
	}
	switch strings.ToLower(strings.TrimSpace(limitID)) {
	case "", "codex":
		return "Codex"
	default:
		return limitID
	}
}

func formatCodexPlan(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "plus":
		return "ChatGPT Plus"
	case "pro":
		return "ChatGPT Pro"
	case "team":
		return "ChatGPT Team"
	case "enterprise":
		return "ChatGPT Enterprise"
	case "edu":
		return "ChatGPT Edu"
	case "go":
		return "ChatGPT Go"
	case "free":
		return "Free"
	default:
		return strings.TrimSpace(raw)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if len(s) > 220 {
		return s[:220] + "…"
	}
	return s
}
