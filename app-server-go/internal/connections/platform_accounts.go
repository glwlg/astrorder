package connections

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/agentaccounts"
	"astrorder.dev/app-server/internal/envtarget"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

const (
	platformAccountsPrefix    = "/api/v1/agent-accounts/"
	platformStateReadCacheTTL = 15 * time.Second
	platformQuotaReadCacheTTL = 30 * time.Second
)

// AccountEnvOpener opens a target machine environment (local or SSH) for
// credential file inspection, switching, and isolated quota queries.
type AccountEnvOpener interface {
	Open(ctx context.Context, id string) (envtarget.Env, error)
}

// PlatformQuotaQuerier runs an isolated quota query for a platform account.
type PlatformQuotaQuerier func(ctx context.Context, platform string, env envtarget.Env, executable string, credential []byte) (agentaccounts.QuotaResult, error)

type platformReadCache struct {
	mu       sync.Mutex
	entries  map[string]cachedPlatformRead
	inFlight map[string]*platformReadFlight
}

type cachedPlatformRead struct {
	platform  string
	envID     string
	out       any
	expiresAt time.Time
}

type platformReadFlight struct {
	done chan struct{}
	out  any
	err  error
}

func (c *platformReadCache) invalidate(platform, envID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if platform == "" {
		c.entries = nil
		return
	}
	for k, entry := range c.entries {
		if entry.platform == platform && (envID == "" || entry.envID == envID) {
			delete(c.entries, k)
		}
	}
}

func (c *platformReadCache) run(
	ctx context.Context,
	key string,
	platform string,
	cacheEnv string,
	ttl time.Duration,
	bypassCache bool,
	fn func(context.Context) (any, error),
) (any, error) {
	c.mu.Lock()
	if !bypassCache && ttl > 0 && cacheEnv != "" {
		if entry, ok := c.entries[key]; ok && time.Now().Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.out, nil
		}
	}
	if flight, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.out, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.inFlight == nil {
		c.inFlight = make(map[string]*platformReadFlight)
	}
	flight := &platformReadFlight{done: make(chan struct{})}
	c.inFlight[key] = flight
	c.mu.Unlock()

	callCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := fn(callCtx)

	c.mu.Lock()
	delete(c.inFlight, key)
	if err == nil && ttl > 0 && cacheEnv != "" {
		if c.entries == nil {
			c.entries = make(map[string]cachedPlatformRead)
		}
		c.entries[key] = cachedPlatformRead{
			platform:  platform,
			envID:     cacheEnv,
			out:       out,
			expiresAt: time.Now().Add(ttl),
		}
	}
	flight.out = out
	flight.err = err
	close(flight.done)
	c.mu.Unlock()
	return out, err
}

type platformAccountSpec struct {
	platform string
	label    string
	authHint string
	identify agentaccounts.IdentityFunc
}

var platformAccountSpecs = map[string]platformAccountSpec{
	"codex": {
		platform: "codex",
		label:    "Codex",
		authHint: "~/.codex/auth.json",
		identify: agentaccounts.CodexIdentity,
	},
	"grok": {
		platform: "grok",
		label:    "Grok",
		authHint: "~/.grok/auth.json",
		identify: agentaccounts.GrokIdentity,
	},
}

func parsePlatformAccountsRoute(path string) (platform string, sub string, ok bool) {
	if !strings.HasPrefix(path, platformAccountsPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, platformAccountsPrefix)
	if rest == "" {
		return "", "", false
	}
	platform, sub, hasSlash := strings.Cut(rest, "/")
	if _, supported := platformAccountSpecs[platform]; !supported {
		return "", "", false
	}
	if !hasSlash {
		return platform, "", true
	}
	if sub == "targets" || sub == "import" {
		return platform, sub, true
	}
	return "", "", false
}

func matchesPlatformAccounts(path string) bool {
	_, _, ok := parsePlatformAccountsRoute(path)
	return ok
}

func defaultPlatformQuotaQuery(ctx context.Context, platform string, env envtarget.Env, executable string, credential []byte) (agentaccounts.QuotaResult, error) {
	switch platform {
	case "codex":
		return agentaccounts.QueryCodexQuota(ctx, env, executable, credential)
	case "grok":
		return agentaccounts.QueryGrokQuota(ctx, env, executable, credential)
	default:
		return agentaccounts.QuotaResult{}, fmt.Errorf("不支持的平台：%s", platform)
	}
}

func (h *Handler) platformVault(platform string) *agentaccounts.Vault {
	switch platform {
	case "codex":
		return h.codexVault
	case "grok":
		return h.grokVault
	default:
		return nil
	}
}

func (h *Handler) platformAccountTargets(ctx context.Context, spec platformAccountSpec) ([]agyAccountTarget, error) {
	agents, err := h.store.ListCurrentAgents(ctx)
	if err != nil {
		return nil, err
	}
	items := []agyAccountTarget{}
	seen := map[string]bool{}
	remoteKind := spec.platform + "-ssh"
	for _, agent := range agents {
		kind, _ := agent["kind"].(string)
		if kind != spec.platform && kind != remoteKind {
			continue
		}
		id, _ := agent["connection_id"].(string)
		if id == "" {
			id = "local"
		}
		if seen[id] {
			continue
		}
		enabled, present, err := h.store.GetAgentConnectionChoice(ctx, id+":"+spec.platform)
		if err != nil {
			return nil, err
		}
		if present && !enabled {
			continue
		}
		name, _ := agent["name"].(string)
		if id == "local" {
			name = spec.label + " · 本机"
		} else {
			row, err := h.store.GetSSHConnection(ctx, id)
			if err != nil {
				return nil, err
			}
			if row == nil {
				continue
			}
			if label, _ := row["display_name"].(string); label != "" {
				name = spec.label + " · " + label
			} else if label, _ := row["profile_name"].(string); label != "" {
				name = spec.label + " · " + label
			}
		}
		seen[id] = true
		items = append(items, agyAccountTarget{ID: id, Name: name})
	}
	return items, nil
}

func platformAuthFilePath(paths envtarget.Paths, platform string) (string, error) {
	switch platform {
	case "codex":
		if strings.TrimSpace(paths.Codex) == "" {
			return "", errors.New("无法确定 Codex 配置目录")
		}
		return envtarget.Join(paths.Codex, "auth.json"), nil
	case "grok":
		if strings.TrimSpace(paths.Grok) == "" {
			return "", errors.New("无法确定 Grok 配置目录")
		}
		return envtarget.Join(paths.Grok, "auth.json"), nil
	default:
		return "", fmt.Errorf("不支持的平台：%s", platform)
	}
}

func (h *Handler) openAccountEnv(ctx context.Context, envID string) (envtarget.Env, error) {
	if h.envResolver == nil {
		return nil, envtarget.Errorf(http.StatusServiceUnavailable, "目标环境解析器不可用")
	}
	return h.envResolver.Open(ctx, envID)
}

func (h *Handler) readTargetPlatformCredential(ctx context.Context, env envtarget.Env, platform string) ([]byte, error) {
	paths, err := env.Paths(ctx)
	if err != nil {
		return nil, err
	}
	authPath, err := platformAuthFilePath(paths, platform)
	if err != nil {
		return nil, err
	}
	return env.Read(ctx, authPath)
}

func (h *Handler) storePlatformCredential(w http.ResponseWriter, spec platformAccountSpec, name string, credential []byte) {
	name = strings.TrimSpace(name)
	if !validAgyName(name) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_name", "请填写 1–64 字的账号名称")
		return
	}
	vault := h.platformVault(spec.platform)
	id, err := vault.Save(name, credential)
	if errors.Is(err, agentaccounts.ErrInvalidCredential) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_credential", fmt.Sprintf("请选择有效的 %s 凭证 JSON 文件（%s，最大 512 KB）", spec.label, spec.authHint))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "vault_error", fmt.Sprintf("无法保存 %s 账号", spec.label))
		return
	}
	h.platformReadCache.invalidate(spec.platform, "")
	writeJSON(w, http.StatusOK, map[string]any{"account_id": id})
}

func (h *Handler) handlePlatformAccounts(w http.ResponseWriter, r *http.Request, path string) {
	platform, sub, ok := parsePlatformAccountsRoute(path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "不支持的账号管理平台")
		return
	}
	spec := platformAccountSpecs[platform]
	vault := h.platformVault(platform)
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	switch sub {
	case "import":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
			return
		}
		var body struct {
			Name       string `json:"name"`
			Credential string `json:"credential"`
		}
		if !decodeAgyBody(w, r, 6*agentaccounts.MaxImportBytes+2048, &body) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameters", fmt.Sprintf("请输入有效的 %s 凭证 JSON（最大 512 KB）", spec.label))
			return
		}
		credential, err := agentaccounts.ImportCredential([]byte(body.Credential), spec.identify)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_credential", fmt.Sprintf("请选择有效的 %s 凭证 JSON 文件（%s，最大 512 KB）", spec.label, spec.authHint))
			return
		}
		h.storePlatformCredential(w, spec, body.Name, credential)
		return

	case "targets":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
			return
		}
		items, err := h.platformAccountTargets(ctx, spec)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", fmt.Sprintf("无法读取 %s 环境", spec.label))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	envID := strings.TrimSpace(r.URL.Query().Get("environment_id"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if r.Method == http.MethodGet && envID == "" && accountID == "" {
		items, err := vault.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "vault_error", fmt.Sprintf("无法读取已保存的 %s 账号", spec.label))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	var postBody struct {
		Action        string `json:"action"`
		Name          string `json:"name"`
		AccountID     string `json:"account_id"`
		ConfirmActive bool   `json:"confirm_active"`
	}
	if r.Method == http.MethodPost {
		if !decodeAgyBody(w, r, 2048, &postBody) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameters", "账号操作参数无效")
			return
		}
		if postBody.Action == "delete" {
			if postBody.AccountID == "" || postBody.AccountID == "current" {
				writeError(w, http.StatusUnprocessableEntity, "invalid_account", fmt.Sprintf("请选择要删除的已保存 %s 账号", spec.label))
				return
			}
			if err := vault.Delete(postBody.AccountID); errors.Is(err, agentaccounts.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account_not_found", fmt.Sprintf("找不到所选 %s 账号", spec.label))
				return
			} else if err != nil {
				writeError(w, http.StatusInternalServerError, "vault_error", fmt.Sprintf("无法删除 %s 账号", spec.label))
				return
			}
			h.platformReadCache.invalidate(platform, "")
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "account_id": postBody.AccountID})
			return
		}
	} else if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
		return
	}

	items, err := h.platformAccountTargets(ctx, spec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", fmt.Sprintf("无法读取 %s 环境", spec.label))
		return
	}
	if !slices.ContainsFunc(items, func(item agyAccountTarget) bool { return item.ID == envID }) {
		writeError(w, http.StatusNotFound, "agent_not_found", fmt.Sprintf("所选 %s 环境未接入", spec.label))
		return
	}

	env, err := h.openAccountEnv(ctx, envID)
	if err != nil {
		writeEnvTargetError(w, err, fmt.Sprintf("无法连接到所选 %s 环境", spec.label))
		return
	}

	freshRead := r.URL.Query().Get("fresh") == "1" || r.URL.Query().Get("refresh") == "1"

	if r.Method == http.MethodGet {
		if accountID == "" {
			// Read current active account state on target.
			out, err := h.platformReadCache.run(ctx, "state:"+platform+":"+envID, platform, envID, platformStateReadCacheTTL, freshRead, func(callCtx context.Context) (any, error) {
				raw, readErr := h.readTargetPlatformCredential(callCtx, env, platform)
				if errors.Is(readErr, envtarget.ErrNotExist) {
					return map[string]any{"current_account_id": "", "quota_supported": true}, nil
				}
				if readErr != nil {
					return nil, readErr
				}
				identity, _, valid := spec.identify(raw)
				if !valid {
					return map[string]any{"current_account_id": "", "quota_supported": true}, nil
				}
				currentID := "current"
				entries, listErr := vault.List()
				if listErr != nil {
					return nil, listErr
				}
				for _, entry := range entries {
					if entry.Identity == identity {
						currentID = entry.ID
						break
					}
				}
				return map[string]any{"current_account_id": currentID, "quota_supported": true}, nil
			})
			if err != nil {
				writeEnvTargetError(w, err, fmt.Sprintf("无法读取目标环境的 %s 账号状态", spec.label))
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}

		// Isolated quota read for either "current" or a saved account ID.
		var cred []byte
		if accountID == "current" {
			cred, err = h.readTargetPlatformCredential(ctx, env, platform)
			if errors.Is(err, envtarget.ErrNotExist) {
				writeError(w, http.StatusNotFound, "account_not_found", fmt.Sprintf("目标环境尚未登录 %s", spec.label))
				return
			}
			if err != nil {
				writeEnvTargetError(w, err, fmt.Sprintf("无法读取目标环境的 %s 凭证", spec.label))
				return
			}
		} else {
			cred, err = vault.Credential(accountID)
			if errors.Is(err, agentaccounts.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account_not_found", fmt.Sprintf("找不到所选 %s 账号", spec.label))
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "vault_error", fmt.Sprintf("无法读取 %s 账号凭证", spec.label))
				return
			}
		}

		executable := h.resolvePlatformExecutable(ctx, platform, envID)
		querier := h.platformQuota
		if querier == nil {
			querier = defaultPlatformQuotaQuery
		}
		out, err := h.platformReadCache.run(ctx, "quota:"+platform+":"+envID+":"+accountID, platform, envID, platformQuotaReadCacheTTL, freshRead, func(callCtx context.Context) (any, error) {
			return querier(callCtx, platform, env, executable, cred)
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, "platform_quota_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// POST actions scoped to environment_id ("save" | "switch")
	switch postBody.Action {
	case "save":
		name := strings.TrimSpace(postBody.Name)
		if !validAgyName(name) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_name", "请填写 1–64 字的账号名称")
			return
		}
		cred, err := h.readTargetPlatformCredential(ctx, env, platform)
		if errors.Is(err, envtarget.ErrNotExist) {
			writeError(w, http.StatusConflict, "not_logged_in", fmt.Sprintf("目标环境尚未登录 %s（未找到 %s）", spec.label, spec.authHint))
			return
		}
		if err != nil {
			writeEnvTargetError(w, err, fmt.Sprintf("无法读取目标环境的 %s 凭证", spec.label))
			return
		}
		h.storePlatformCredential(w, spec, name, cred)
		return

	case "switch":
		if postBody.AccountID == "" || postBody.AccountID == "current" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_account", fmt.Sprintf("请选择已保存的 %s 账号", spec.label))
			return
		}
		cred, err := vault.Credential(postBody.AccountID)
		if errors.Is(err, agentaccounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", fmt.Sprintf("找不到所选 %s 账号", spec.label))
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "vault_error", fmt.Sprintf("无法读取 %s 账号凭证", spec.label))
			return
		}

		paths, err := env.Paths(ctx)
		if err != nil {
			writeEnvTargetError(w, err, fmt.Sprintf("无法解析目标环境的 %s 路径", spec.label))
			return
		}
		authPath, err := platformAuthFilePath(paths, platform)
		if err != nil {
			writeError(w, http.StatusBadGateway, "platform_path_error", err.Error())
			return
		}

		// Cleanly disconnect the active runtime for this platform/target before
		// replacing auth.json so active sessions are respected unless forced.
		if h.client != nil && h.client.Runtime != nil {
			var connPtr *string
			var sshConn map[string]any
			if envID != "local" {
				connPtr = &envID
				sshConn, err = h.store.GetSSHConnection(ctx, envID)
				if err != nil || sshConn == nil {
					writeError(w, http.StatusNotFound, "connection_not_found", fmt.Sprintf("找不到所选 %s 环境", spec.label))
					return
				}
			}
			opID := fmt.Sprintf("acct-switch-disconnect-%s-%s-%d", platform, envID, time.Now().UnixNano())
			resp, discErr := h.client.Runtime.DisconnectRuntime(ctx, &sessiondv1.DisconnectRuntimeRequest{
				Meta:          canonicalMeta(opID),
				Target:        runtimeTarget(platform, connPtr, sshConn),
				ConfirmActive: postBody.ConfirmActive,
			})
			if err := h.disconnectReceipt(resp, discErr); err != nil {
				writeError(w, http.StatusConflict, "disconnect_refused", err.Error())
				return
			}
		}

		if err := env.Write(ctx, authPath, cred, 0o600); err != nil {
			writeEnvTargetError(w, err, fmt.Sprintf("写入目标环境 %s 凭证失败", spec.label))
			return
		}
		h.platformReadCache.invalidate(platform, envID)

		restarted := true
		if h.client != nil && h.client.Session != nil {
			attachErr := h.changeEnvironmentAgent(ctx, envID, platform, "connect", false)
			restarted = attachErr == nil
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"account_id": postBody.AccountID,
			"restarted":  restarted,
		})
		return

	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid_action", "不支持的账号操作")
		return
	}
}

func (h *Handler) resolvePlatformExecutable(ctx context.Context, platform, envID string) string {
	if envID == "" || envID == "local" {
		return ""
	}
	sshConn, err := h.store.GetSSHConnection(ctx, envID)
	if err != nil || sshConn == nil {
		return ""
	}
	if exe := RuntimeExecutable(ctx, h.executables, platform, sshConn); exe != nil {
		return *exe
	}
	return ""
}

func writeEnvTargetError(w http.ResponseWriter, err error, fallback string) {
	var envErr *envtarget.Error
	if errors.As(err, &envErr) && envErr.Status >= 400 && envErr.Status < 600 {
		writeError(w, envErr.Status, "environment_error", envErr.Detail)
		return
	}
	msg := fallback
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = fallback + ": " + err.Error()
	}
	writeError(w, http.StatusBadGateway, "environment_error", msg)
}
