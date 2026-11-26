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

// GrokIdentity validates a ~/.grok/auth.json document and computes a stable
// opaque identity. It supports Grok CLI's native scoped credential map
// (`{"https://accounts.x.ai/sign-in": {...}}`, `{"xai::api_key": {"key": "..."}}`)
// as well as flat token JSON documents.
func GrokIdentity(data []byte) (string, AccountMeta, bool) {
	if len(data) == 0 || len(data) > MaxCredentialBytes {
		return "", AccountMeta{}, false
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil || len(root) == 0 {
		return "", AccountMeta{}, false
	}

	var (
		email        string
		subject      string
		teamID       string
		refreshToken string
		secretToken  string
		scopes       []string
		hasOAuth     bool
		hasAPIKey    bool
	)

	inspectToken := func(tok string) {
		claims := decodeJWTPayload(tok)
		if claims == nil {
			return
		}
		if email == "" {
			email = strings.ToLower(stringValue(claims, "email"))
		}
		if subject == "" {
			subject = stringValue(claims, "sub", "user_id", "principal_id")
		}
		if teamID == "" {
			teamID = stringValue(claims, "team_id", "org_id", "organization_id")
		}
	}

	inspectObject := func(scope string, obj map[string]any) bool {
		key := stringValue(obj, "key", "api_key", "apiKey", "access_token", "accessToken", "token")
		refresh := stringValue(obj, "refresh_token", "refreshToken")
		idTok := stringValue(obj, "id_token", "idToken")
		if key == "" && refresh == "" && idTok == "" {
			return false
		}
		if e := strings.ToLower(stringValue(obj, "email")); e != "" && email == "" {
			email = e
		}
		if t := stringValue(obj, "team_id", "teamId"); t != "" && teamID == "" {
			teamID = t
		}
		inspectToken(idTok)
		inspectToken(key)
		if refreshToken == "" && refresh != "" {
			refreshToken = refresh
		}
		if secretToken == "" && key != "" {
			secretToken = key
		}
		lowerScope := strings.ToLower(scope)
		if strings.Contains(lowerScope, "api_key") || strings.HasPrefix(key, "xai-") {
			hasAPIKey = true
		} else {
			hasOAuth = true
		}
		return true
	}

	// Format 1: Flat credential JSON.
	if inspectObject("flat", root) {
		scopes = append(scopes, "flat")
	}

	// Format 2: Scoped map (`{"https://accounts.x.ai/sign-in": {...}}`, `{"xai::api_key": {...}}`).
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, scope := range keys {
		obj, ok := root[scope].(map[string]any)
		if !ok {
			continue
		}
		if inspectObject(scope, obj) {
			scopes = append(scopes, scope)
		}
	}

	if len(scopes) == 0 {
		return "", AccountMeta{}, false
	}

	authMode := "oauth"
	if hasAPIKey && !hasOAuth {
		authMode = "apikey"
	}
	meta := AccountMeta{Email: email, AuthMode: authMode}
	scopeFingerprint := strings.Join(scopes, ",")

	switch {
	case email != "" && teamID != "":
		return sha256Hex("grok:account", email, teamID), meta, true
	case email != "" && subject != "":
		return sha256Hex("grok:user", email, subject), meta, true
	case email != "":
		return sha256Hex("grok:email", email), meta, true
	case subject != "":
		return sha256Hex("grok:sub", subject, teamID), meta, true
	case refreshToken != "":
		return sha256Hex("grok:refresh", scopeFingerprint, refreshToken), meta, true
	case secretToken != "":
		return sha256Hex("grok:token", scopeFingerprint, secretToken), meta, true
	default:
		return "", AccountMeta{}, false
	}
}

// QueryGrokQuota runs an isolated `GROK_HOME=<tmp> grok agent stdio` probe
// calling ACP `_x.ai/auth/info`, `_x.ai/auth/check_subscription`, and
// `_x.ai/billing` without mutating the target machine's active ~/.grok/auth.json.
func QueryGrokQuota(ctx context.Context, env envtarget.Env, executable string, authJSON []byte) (QuotaResult, error) {
	_, meta, ok := GrokIdentity(authJSON)
	if !ok {
		return QuotaResult{}, ErrInvalidCredential
	}
	now := time.Now().UTC()

	responses, stderr, err := runIsolatedStdioRPC(ctx, env, isolatedRPCRequest{
		Prefix:      "grok",
		BinaryName:  "grok",
		Executable:  executable,
		Subcommand:  []string{"agent", "stdio"},
		HomeEnvVar:  "GROK_HOME",
		AuthFile:    "auth.json",
		AuthJSON:    authJSON,
		InitRequest: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
		FollowUps: []string{
			`{"jsonrpc":"2.0","id":2,"method":"_x.ai/auth/info","params":{}}`,
			`{"jsonrpc":"2.0","id":3,"method":"_x.ai/auth/check_subscription","params":{}}`,
			`{"jsonrpc":"2.0","id":4,"method":"_x.ai/billing","params":{}}`,
		},
		RequiredIDs: []int{2, 3, 4},
		Timeout:     25 * time.Second,
	})
	if err != nil {
		return QuotaResult{}, err
	}
	return ParseGrokQuotaResponses(responses[2], responses[3], responses[4], meta, stderr, now)
}

// ParseGrokQuotaResponses converts Grok ACP responses (`_x.ai/auth/info`,
// `_x.ai/auth/check_subscription`, `_x.ai/billing`) into a unified QuotaResult.
func ParseGrokQuotaResponses(authInfoEnv, subEnv, billingEnv rpcEnvelope, meta AccountMeta, stderr string, now time.Time) (QuotaResult, error) {
	if authInfoEnv.ID == nil && subEnv.ID == nil && billingEnv.ID == nil {
		if stderr != "" {
			return QuotaResult{}, fmt.Errorf("无法从 Grok 读取账号状态: %s", firstLine(stderr))
		}
		return QuotaResult{}, errors.New("Grok 未返回账号或订阅响应")
	}

	var authInfo, subInfo, billingInfo map[string]any
	if len(authInfoEnv.Result) > 0 {
		_ = json.Unmarshal(authInfoEnv.Result, &authInfo)
	}
	if len(subEnv.Result) > 0 {
		_ = json.Unmarshal(subEnv.Result, &subInfo)
	}
	if len(billingEnv.Result) > 0 {
		_ = json.Unmarshal(billingEnv.Result, &billingInfo)
	}

	authenticated, hasAuthFlag := boolValue(subInfo, "authenticated")
	methodID := stringValue(authInfo, "methodId", "method_id")
	email := firstNonEmpty(stringValue(authInfo, "email"), meta.Email)
	teamName := stringValue(authInfo, "teamName", "team_name", "organizationName", "organization_name")

	if authInfoEnv.Error != nil && subEnv.Error != nil && billingEnv.Error != nil {
		return QuotaResult{}, fmt.Errorf("Grok 账号查询失败: %s", firstNonEmpty(authInfoEnv.Error.Message, billingEnv.Error.Message))
	}
	if methodID == "" && hasAuthFlag && !authenticated && billingInfo == nil {
		return QuotaResult{}, errors.New("Grok 凭证未通过认证或已过期")
	}

	subMeta, _ := subInfo["meta"].(map[string]any)
	planRaw := firstNonEmpty(
		extractGrokPlan(subMeta),
		extractGrokPlan(subInfo),
		extractGrokPlan(billingInfo),
	)
	if planRaw == "" {
		switch {
		case strings.Contains(strings.ToLower(methodID), "api") || meta.AuthMode == "apikey":
			planRaw = "xAI API Key"
		case teamName != "":
			planRaw = teamName
		case authenticated || methodID != "":
			planRaw = "Grok"
		}
	}

	groups := make([]QuotaGroup, 0, 2)
	detailParts := make([]string, 0, 3)

	// Extract any structured quota/rate-limit buckets exposed by subscription meta or billing.
	for _, candidate := range []map[string]any{billingInfo, subMeta, subInfo} {
		if candidate == nil {
			continue
		}
		if parsed := extractGrokBuckets(candidate); len(parsed) > 0 {
			groups = append(groups, parsed...)
			break
		}
	}

	// Extract human-readable billing / credit summary if present.
	if billingInfo != nil {
		if creditSummary := extractGrokCreditDetail(billingInfo); creditSummary != "" {
			detailParts = append(detailParts, creditSummary)
		}
	} else if billingEnv.Error != nil && meta.AuthMode == "apikey" {
		detailParts = append(detailParts, "当前为 xAI API Key 模式")
	}

	accountLabel := email
	if teamName != "" && !strings.EqualFold(teamName, planRaw) {
		if accountLabel != "" {
			accountLabel += " · " + teamName
		} else {
			accountLabel = teamName
		}
	}
	if len(groups) == 0 && len(detailParts) == 0 {
		if accountLabel != "" {
			detailParts = append(detailParts, "已认证（"+accountLabel+"）")
		} else {
			detailParts = append(detailParts, "已认证（Grok CLI 暂未返回周期用量百分比）")
		}
	}

	return QuotaResult{
		Plan:         formatGrokPlan(planRaw),
		TierID:       strings.TrimSpace(planRaw),
		AccountLabel: accountLabel,
		Detail:       strings.Join(detailParts, " · "),
		Groups:       groups,
		AsOf:         now.Format(time.RFC3339),
	}, nil
}

func extractGrokPlan(m map[string]any) string {
	if m == nil {
		return ""
	}
	if direct := stringValue(m, "planName", "plan_name", "planType", "plan_type", "tierName", "tier_name", "subscriptionTier", "subscription_tier", "plan", "tier", "subscription"); direct != "" {
		return direct
	}
	for _, nestedKey := range []string{"subscription", "plan", "billing", "account"} {
		if nested, ok := m[nestedKey].(map[string]any); ok {
			if found := stringValue(nested, "name", "displayName", "display_name", "planName", "plan_name", "tier", "plan", "type"); found != "" {
				return found
			}
		}
	}
	return ""
}

func formatGrokPlan(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "supergrok":
		return "SuperGrok"
	case "supergrok_heavy", "supergrok-heavy", "heavy":
		return "SuperGrok Heavy"
	case "enterprise":
		return "xAI Enterprise"
	case "team":
		return "xAI Team"
	case "free":
		return "Free"
	default:
		return strings.TrimSpace(raw)
	}
}

func extractGrokBuckets(m map[string]any) []QuotaGroup {
	if m == nil {
		return nil
	}
	buckets := make([]QuotaBucket, 0, 4)

	// Shape 1: rateLimits / windows / quotas arrays.
	for _, listKey := range []string{"rateLimits", "rate_limits", "windows", "quotas", "buckets"} {
		arr, ok := m[listKey].([]any)
		if !ok {
			continue
		}
		for idx, item := range arr {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if b, ok := grokObjectToBucket(fmt.Sprintf("bucket-%d", idx), obj); ok {
				buckets = append(buckets, b)
			}
		}
	}

	// Shape 2: primary / secondary windows (similar to Codex/AGY).
	for _, winKey := range []string{"primary", "secondary"} {
		if obj, ok := m[winKey].(map[string]any); ok {
			if b, ok := grokObjectToBucket(winKey, obj); ok {
				buckets = append(buckets, b)
			}
		}
	}

	// Shape 3: credits / budget remaining vs limit.
	if len(buckets) == 0 {
		if creditsObj, ok := m["credits"].(map[string]any); ok {
			if b, ok := grokCreditsToBucket(creditsObj); ok {
				buckets = append(buckets, b)
			}
		} else if b, ok := grokCreditsToBucket(m); ok {
			buckets = append(buckets, b)
		}
	}

	if len(buckets) == 0 {
		return nil
	}
	return []QuotaGroup{{Name: "Grok", Buckets: buckets}}
}

func grokObjectToBucket(fallbackID string, obj map[string]any) (QuotaBucket, bool) {
	var remaining float64
	found := false
	if frac, ok := floatValue(obj, "remainingFraction", "remaining_fraction"); ok {
		remaining, found = clampFraction(frac), true
	} else if pct, ok := floatValue(obj, "remainingPercent", "remaining_percent"); ok {
		remaining, found = clampFraction(pct/100.0), true
	} else if usedPct, ok := floatValue(obj, "usedPercent", "used_percent"); ok {
		remaining, found = clampFraction(1-usedPct/100.0), true
	} else if limit, okLimit := floatValue(obj, "limit", "total", "max"); okLimit && limit > 0 {
		if rem, okRem := floatValue(obj, "remaining", "available"); okRem {
			remaining, found = clampFraction(rem/limit), true
		} else if used, okUsed := floatValue(obj, "used", "consumed"); okUsed {
			remaining, found = clampFraction(1-used/limit), true
		}
	}
	if !found {
		return QuotaBucket{}, false
	}

	window := firstNonEmpty(stringValue(obj, "window", "period", "id"), fallbackID)
	name := firstNonEmpty(stringValue(obj, "name", "label"), window)
	var resetAt int64
	if ts, ok := floatValue(obj, "resetAt", "reset_at", "resetsAt", "resets_at"); ok {
		resetAt = unixToMillis(ts)
	} else if iso := stringValue(obj, "resetAt", "reset_at", "resetsAt", "resets_at"); iso != "" {
		if parsed, err := time.Parse(time.RFC3339, iso); err == nil {
			resetAt = parsed.UnixMilli()
		}
	}

	return QuotaBucket{
		ID:                window,
		Name:              name,
		Window:            window,
		RemainingFraction: remaining,
		ResetAt:           resetAt,
	}, true
}

func grokCreditsToBucket(obj map[string]any) (QuotaBucket, bool) {
	limit, okLimit := floatValue(obj, "monthlyLimit", "monthly_limit", "creditLimit", "credit_limit", "spendLimit", "spend_limit", "limit")
	if !okLimit || limit <= 0 {
		return QuotaBucket{}, false
	}
	if rem, ok := floatValue(obj, "remaining", "remainingCredits", "remaining_credits", "balance"); ok {
		return QuotaBucket{
			ID:                "credits",
			Name:              "额度余额",
			Window:            "credits",
			RemainingFraction: clampFraction(rem / limit),
		}, true
	}
	if used, ok := floatValue(obj, "used", "spent", "currentSpend", "current_spend"); ok {
		return QuotaBucket{
			ID:                "credits",
			Name:              "额度余额",
			Window:            "credits",
			RemainingFraction: clampFraction(1 - used/limit),
		}, true
	}
	return QuotaBucket{}, false
}

func extractGrokCreditDetail(m map[string]any) string {
	for _, container := range []map[string]any{m} {
		if bal, ok := floatValue(container, "balance", "remainingCredits", "remaining_credits", "creditBalance", "credit_balance"); ok {
			if currency := stringValue(container, "currency"); currency != "" {
				return fmt.Sprintf("余额 %.2f %s", bal, currency)
			}
			return fmt.Sprintf("余额 %.2f", bal)
		}
		if credits, ok := container["credits"].(map[string]any); ok {
			if bal, ok := floatValue(credits, "balance", "remaining"); ok {
				return fmt.Sprintf("余额 %.2f", bal)
			}
		}
	}
	return ""
}
