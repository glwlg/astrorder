package connections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"astrorder.dev/app-server/internal/agyaccounts"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	agyAccountsPath      = "/api/v1/agy-accounts"
	agyStateReadCacheTTL = 15 * time.Second
)

type agyReadCache struct {
	mu       sync.Mutex
	states   map[string]cachedAgyState
	inFlight map[string]*agyReadFlight
}

type cachedAgyState struct {
	out       *pb.AgyAccountResponse
	expiresAt time.Time
}

type agyReadFlight struct {
	done chan struct{}
	out  *pb.AgyAccountResponse
	err  error
}

func (c *agyReadCache) invalidate(envID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if envID == "" {
		c.states = nil
		return
	}
	delete(c.states, envID)
}

func (c *agyReadCache) run(
	ctx context.Context,
	key string,
	cacheEnv string,
	fn func(context.Context) (*pb.AgyAccountResponse, error),
) (*pb.AgyAccountResponse, error) {
	c.mu.Lock()
	if cacheEnv != "" {
		if entry, ok := c.states[cacheEnv]; ok && time.Now().Before(entry.expiresAt) {
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
		c.inFlight = make(map[string]*agyReadFlight)
	}
	flight := &agyReadFlight{done: make(chan struct{})}
	c.inFlight[key] = flight
	c.mu.Unlock()

	// Detach cancellation of one waiting HTTP caller from the shared upstream probe,
	// while keeping the bounded 95s handler deadline.
	callCtx, cancel := context.WithTimeout(context.Background(), 95*time.Second)
	defer cancel()
	out, err := fn(callCtx)

	c.mu.Lock()
	delete(c.inFlight, key)
	if err == nil && cacheEnv != "" {
		if c.states == nil {
			c.states = make(map[string]cachedAgyState)
		}
		c.states[cacheEnv] = cachedAgyState{
			out:       out,
			expiresAt: time.Now().Add(agyStateReadCacheTTL),
		}
	}
	flight.out = out
	flight.err = err
	close(flight.done)
	c.mu.Unlock()
	return out, err
}

type agyAccountTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *Handler) agyAccountTargets(ctx context.Context) ([]agyAccountTarget, error) {
	agents, err := h.store.ListCurrentAgents(ctx)
	if err != nil {
		return nil, err
	}
	items := []agyAccountTarget{}
	seen := map[string]bool{}
	for _, agent := range agents {
		if agent["kind"] != "antigravity" && agent["kind"] != "antigravity-ssh" {
			continue
		}
		id, _ := agent["connection_id"].(string)
		if id == "" {
			id = "local"
		}
		if seen[id] {
			continue
		}
		enabled, present, err := h.store.GetAgentConnectionChoice(ctx, id+":antigravity")
		if err != nil {
			return nil, err
		}
		if present && !enabled {
			continue
		}
		name, _ := agent["name"].(string)
		if id == "local" {
			name = "AGY · 本机"
		} else {
			row, err := h.store.GetSSHConnection(ctx, id)
			if err != nil {
				return nil, err
			}
			if row == nil {
				continue
			}
			if label, _ := row["display_name"].(string); label != "" {
				name = "AGY · " + label
			}
		}
		seen[id] = true
		items = append(items, agyAccountTarget{ID: id, Name: name})
	}
	return items, nil
}

func matchesAgyAccounts(path string) bool {
	return path == agyAccountsPath || path == agyAccountsPath+"/targets" || path == agyAccountsPath+"/import"
}

func decodeAgyBody(w http.ResponseWriter, r *http.Request, maxBytes int64, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	decoder.DisallowUnknownFields()
	return decoder.Decode(body) == nil && decoder.Decode(new(any)) == io.EOF
}

func (h *Handler) storeAgyCredential(w http.ResponseWriter, name string, credential []byte) {
	name = strings.TrimSpace(name)
	if !validAgyName(name) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_name", "请填写 1–64 字的账号名称")
		return
	}
	id, err := h.agyVault.Save(name, credential)
	if errors.Is(err, agyaccounts.ErrInvalidCredential) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_credential", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "vault_error", "无法保存 AGY 账号")
		return
	}
	h.agyReadCache.invalidate("")
	writeJSON(w, http.StatusOK, map[string]any{"account_id": id})
}

func validAgyName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 64
}

func (h *Handler) handleAgyAccounts(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 95*time.Second)
	defer cancel()

	switch path {
	case agyAccountsPath + "/import":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
			return
		}
		var body struct {
			Name       string `json:"name"`
			Credential string `json:"credential"`
		}
		// JSON string escaping can expand the raw credential by up to 6x.
		if !decodeAgyBody(w, r, 6*agyaccounts.MaxImportBytes+2048, &body) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameters", agyaccounts.ErrInvalidCredential.Error())
			return
		}
		credential, err := agyaccounts.ImportCredential([]byte(body.Credential))
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_credential", err.Error())
			return
		}
		h.storeAgyCredential(w, body.Name, credential)
		return
	case agyAccountsPath + "/targets":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
			return
		}
		items, err := h.agyAccountTargets(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", "无法读取 AGY 环境")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	envID := r.URL.Query().Get("environment_id")
	accountID := r.URL.Query().Get("account_id")
	if r.Method == http.MethodGet && envID == "" && accountID == "" {
		items, err := h.agyVault.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "vault_error", "无法读取已保存的 AGY 账号")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	req := &pb.AgyAccountRequest{Action: pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE}
	var name string
	switch r.Method {
	case http.MethodGet:
		if accountID != "" {
			req.Action, req.AccountId = pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA, accountID
		}
	case http.MethodPost:
		var body struct {
			Action        string `json:"action"`
			Name          string `json:"name"`
			AccountID     string `json:"account_id"`
			ConfirmActive bool   `json:"confirm_active"`
		}
		if !decodeAgyBody(w, r, 2048, &body) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_parameters", "账号操作参数无效")
			return
		}
		switch body.Action {
		case "save":
			name = strings.TrimSpace(body.Name)
			if !validAgyName(name) {
				writeError(w, http.StatusUnprocessableEntity, "invalid_name", "请填写 1–64 字的账号名称")
				return
			}
			req.Action = pb.AgyAccountAction_AGY_ACCOUNT_ACTION_CAPTURE_CREDENTIAL
		case "switch":
			if body.AccountID == "" || body.AccountID == "current" {
				writeError(w, http.StatusUnprocessableEntity, "invalid_account", "请选择已保存的 AGY 账号")
				return
			}
			req.Action, req.AccountId, req.ConfirmActive = pb.AgyAccountAction_AGY_ACCOUNT_ACTION_APPLY_CREDENTIAL, body.AccountID, body.ConfirmActive
		default:
			writeError(w, http.StatusUnprocessableEntity, "invalid_action", "不支持的账号操作")
			return
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "invalid_method", "请求方式无效")
		return
	}

	if h.client == nil || h.client.Runtime == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "小内核连接不可用")
		return
	}
	items, err := h.agyAccountTargets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", "无法读取 AGY 环境")
		return
	}
	if !slices.ContainsFunc(items, func(item agyAccountTarget) bool { return item.ID == envID }) {
		writeError(w, http.StatusNotFound, "agent_not_found", "所选 AGY 环境未接入")
		return
	}
	var sshConn map[string]any
	if envID != "local" {
		sshConn, err = h.store.GetSSHConnection(ctx, envID)
		if err != nil || sshConn == nil {
			writeError(w, http.StatusNotFound, "connection_not_found", "找不到所选 AGY 环境")
			return
		}
		executable := RuntimeExecutable(ctx, h.executables, "antigravity", sshConn)
		if executable == nil {
			writeError(w, http.StatusBadGateway, "executable_unresolved", "未找到目标环境的 AGY 命令")
			return
		}
		sshConn = withResolvedExecutable(sshConn, "antigravity", *executable)
	}
	if req.AccountId != "" && req.AccountId != "current" {
		req.Credential, err = h.agyVault.Credential(req.AccountId)
		if errors.Is(err, agyaccounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account_not_found", err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "vault_error", "无法读取 AGY 账号凭证")
			return
		}
	}
	req.Target = runtimeTarget("antigravity", &envID, sshConn)
	var out *pb.AgyAccountResponse
	switch req.Action {
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE:
		out, err = h.agyReadCache.run(ctx, "state:"+envID, envID, func(callCtx context.Context) (*pb.AgyAccountResponse, error) {
			return h.client.Runtime.ManageAgyAccount(callCtx, req)
		})
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA:
		out, err = h.agyReadCache.run(ctx, "quota:"+envID+":"+req.AccountId, "", func(callCtx context.Context) (*pb.AgyAccountResponse, error) {
			return h.client.Runtime.ManageAgyAccount(callCtx, req)
		})
	default:
		out, err = h.client.Runtime.ManageAgyAccount(ctx, req)
	}
	if err != nil {
		message := status.Convert(err).Message()
		code := http.StatusConflict
		switch status.Code(err) {
		case codes.Unimplemented:
			code, message = http.StatusNotImplemented, "小内核或所选 SSH 连接方式暂不支持 AGY 账号管理"
		case codes.InvalidArgument:
			code, message = http.StatusUnprocessableEntity, "请更新小内核后重试 AGY 账号操作，或检查目标环境设置"
		case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable:
			code, message = http.StatusBadGateway, "AGY 操作结果未确认，请刷新账号状态"
		}
		writeError(w, code, "agy_account_operation", message)
		return
	}
	if req.Action == pb.AgyAccountAction_AGY_ACCOUNT_ACTION_CAPTURE_CREDENTIAL {
		h.storeAgyCredential(w, name, out.GetCredential())
		return
	}
	result := out.GetResult()
	if result.GetSchema() != "astrorder.agy-account-vault-result.v1" || !json.Valid(result.GetUtf8Json()) {
		writeError(w, http.StatusBadGateway, "agy_account_response", "AGY 操作响应无效")
		return
	}
	switch req.Action {
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE:
		var state struct {
			Identity       string `json:"identity"`
			QuotaSupported bool   `json:"quota_supported"`
		}
		if json.Unmarshal(result.Utf8Json, &state) != nil {
			writeError(w, http.StatusBadGateway, "agy_account_response", "AGY 账号状态无效")
			return
		}
		currentID := ""
		if state.Identity != "" {
			currentID = "current"
			entries, err := h.agyVault.List()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "vault_error", "无法读取已保存的 AGY 账号")
				return
			}
			for _, entry := range entries {
				if entry.Identity == state.Identity {
					currentID = entry.ID
					break
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"current_account_id": currentID, "quota_supported": state.QuotaSupported})
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_APPLY_CREDENTIAL:
		var applied struct {
			AccountID string `json:"account_id"`
		}
		if json.Unmarshal(result.Utf8Json, &applied) != nil || applied.AccountID != req.AccountId {
			writeError(w, http.StatusBadGateway, "agy_account_response", "AGY 切换结果未确认")
			return
		}
		h.agyReadCache.invalidate(envID)
		attachErr := h.changeEnvironmentAgent(ctx, envID, "antigravity", "connect", false)
		writeJSON(w, http.StatusOK, map[string]any{"account_id": applied.AccountID, "restarted": attachErr == nil})
	case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA:
		writeJSON(w, http.StatusOK, json.RawMessage(result.Utf8Json))
	}
}
