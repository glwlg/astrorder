package modelgateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type syncTargetSelection struct {
	TargetID string   `json:"target_id"`
	Agents   []string `json:"agents"`
}

type targetInfo struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Agents       []string `json:"agents"`
	Distro       string   `json:"distro,omitempty"`
	ConnectionID string   `json:"connection_id,omitempty"`
	State        string   `json:"state,omitempty"`
}

func (h *Handler) listTargets(ctx context.Context) ([]targetInfo, error) {
	targets := []targetInfo{
		{ID: "local", Kind: "local", Name: "本机", Agents: []string{"codex", "grok", "hermes"}},
	}

	sshConnections, err := h.store.ListSSHConnections(ctx)
	if err != nil {
		return nil, err
	}

	hasWslSSH := false
	for _, conn := range sshConnections {
		settings, _ := conn["settings"].(map[string]any)
		host, _ := settings["host"].(string)
		host = strings.ToLower(strings.TrimSpace(host))
		displayName, _ := conn["display_name"].(string)
		profileName, _ := conn["profile_name"].(string)
		if host == "127.0.0.1" || host == "localhost" || strings.Contains(strings.ToLower(displayName), "wsl") || strings.Contains(strings.ToLower(profileName), "wsl") {
			hasWslSSH = true
			break
		}
	}

	if os.Getenv("OS") == "Windows_NT" && !hasWslSSH {
		cmd := exec.CommandContext(ctx, "wsl.exe", "-l", "-q")
		if out, err := cmd.Output(); err == nil {
			text := string(out)
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(strings.ReplaceAll(line, "\x00", ""))
				if line != "" {
					targets = append(targets, targetInfo{
						ID:     "wsl:" + line,
						Kind:   "wsl",
						Distro: line,
						Name:   "WSL · " + line,
						Agents: []string{"codex", "grok", "hermes"},
					})
				}
			}
		}
	}

	for _, conn := range sshConnections {
		cid, _ := conn["id"].(string)
		displayName, _ := conn["display_name"].(string)
		if displayName == "" {
			displayName, _ = conn["profile_name"].(string)
		}
		if displayName == "" {
			displayName = cid
		}
		state, _ := conn["state"].(string)
		targets = append(targets, targetInfo{
			ID:           "ssh:" + cid,
			Kind:         "ssh",
			ConnectionID: cid,
			Name:         displayName,
			State:        state,
			Agents:       []string{"codex", "grok", "hermes"},
		})
	}

	return targets, nil
}

func (h *Handler) resolveTargetProto(ctx context.Context, targetID string) (*sessiondv1.ModelConfigTarget, error) {
	targets, err := h.listTargets(ctx)
	if err != nil {
		return nil, err
	}
	var found *targetInfo
	for _, t := range targets {
		if t.ID == targetID {
			found = &t
			break
		}
	}
	if found == nil {
		return nil, errors.New("模型同步目标不存在")
	}

	switch found.Kind {
	case "local":
		return &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Local{
				Local: &sessiondv1.LocalTarget{},
			},
		}, nil
	case "wsl":
		return &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Wsl{
				Wsl: &sessiondv1.WslTarget{
					Distro: found.Distro,
				},
			},
		}, nil
	case "ssh":
		row, err := h.store.GetSSHConnection(ctx, found.ConnectionID)
		if err != nil || row == nil {
			return nil, errors.New("SSH 连接不存在")
		}
		settings, _ := row["settings"].(map[string]any)
		host, _ := settings["host"].(string)
		port := int64(22)
		if pVal, ok := settings["port"]; ok {
			switch p := pVal.(type) {
			case int:
				port = int64(p)
			case float64:
				port = int64(p)
			case int64:
				port = p
			}
		}
		var user *string
		if u, ok := settings["user"].(string); ok && u != "" {
			user = &u
		}
		var alias *string
		if a, ok := settings["ssh_config_alias"].(string); ok && a != "" {
			alias = &a
		}
		var identity *string
		if id, ok := settings["identity_file"].(string); ok && id != "" {
			identity = &id
		}
		var sshExec *string
		if se, ok := settings["ssh_executable"].(string); ok && se != "" {
			sshExec = &se
		}

		return &sessiondv1.ModelConfigTarget{
			Target: &sessiondv1.ModelConfigTarget_Ssh{
				Ssh: &sessiondv1.SshTarget{
					ConnectionId: found.ConnectionID,
					Config: &sessiondv1.SshConnectConfig{
						Host:           host,
						Port:           uint32(port),
						User:           user,
						SshConfigAlias: alias,
						IdentityFile:   identity,
						SshExecutable:  sshExec,
					},
				},
			},
		}, nil
	default:
		return nil, errors.New("未知目标类型")
	}
}

func (h *Handler) validateSelections(ctx context.Context, raw any) ([]syncTargetSelection, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 || len(items) > 64 {
		return nil, errors.New("请选择同步目标")
	}
	var res []syncTargetSelection
	seen := make(map[string]bool)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, errors.New("同步目标格式无效")
		}
		tid, _ := m["target_id"].(string)
		if tid == "" || seen[tid] {
			return nil, errors.New("同步目标重复或无效")
		}
		rawAgents, ok := m["agents"].([]any)
		if !ok || len(rawAgents) == 0 {
			return nil, errors.New("Agent 选择无效")
		}
		agentSet := make(map[string]bool)
		for _, a := range rawAgents {
			as, ok := a.(string)
			if !ok || (as != "codex" && as != "grok" && as != "hermes") {
				return nil, errors.New("Agent 选择无效")
			}
			agentSet[as] = true
		}
		agents := []string{}
		for _, order := range []string{"codex", "grok", "hermes"} {
			if agentSet[order] {
				agents = append(agents, order)
			}
		}

		if _, err := h.resolveTargetProto(ctx, tid); err != nil {
			return nil, err
		}

		seen[tid] = true
		res = append(res, syncTargetSelection{
			TargetID: tid,
			Agents:   agents,
		})
	}
	return res, nil
}

type targetPlanResult struct {
	TargetID           string                        `json:"target_id"`
	Agents             []string                      `json:"agents"`
	Files              map[string]string             `json:"files"`
	ModelDiff          map[string]any                `json:"model_diff"`
	Changes            []map[string]any              `json:"changes"`
	CatalogFingerprint string                        `json:"catalog_fingerprint"`
	ReloadPending      bool                          `json:"reload_pending"`
	Hermes             map[string]any                `json:"hermes"`
	TargetProto        *sessiondv1.ModelConfigTarget `json:"-"`
}

func (h *Handler) buildTargetPlan(ctx context.Context, catalog *Catalog, sel syncTargetSelection, config GatewayConfig) (*targetPlanResult, error) {
	if h.client == nil {
		return nil, errors.New("小内核未启用")
	}
	targetProto, err := h.resolveTargetProto(ctx, sel.TargetID)
	if err != nil {
		return nil, err
	}

	planResp, err := h.client.Model.Plan(ctx, &sessiondv1.ModelConfigPlanRequest{
		Target: targetProto,
	})
	if err != nil {
		return nil, fmt.Errorf("model plan rpc: %w", err)
	}

	home := ""
	if planResp.Home != nil {
		home = *planResp.Home
	}
	if home == "" {
		home = "~"
	}
	home = strings.ReplaceAll(home, "\\", "/")

	inferenceURL := config.InferenceURL
	if ov, ok := config.TargetOverrides[sel.TargetID]; ok && ov != "" {
		inferenceURL = ov
	}

	files := make(map[string]string)
	var oldCatalogModels []any
	hermesState := map[string]any{"dynamic": true, "status": "未选择"}
	for _, a := range sel.Agents {
		if a == "hermes" {
			hermesState = map[string]any{"dynamic": true, "status": "待验证"}
		}
	}

	currentFileMap := make(map[string]*sessiondv1.ModelConfigFileState)
	for _, f := range planResp.CurrentFiles {
		switch f.File {
		case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG:
			currentFileMap["codex_config"] = f
		case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CATALOG:
			currentFileMap["codex_catalog"] = f
		case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_MAGPIE_CATALOG:
			currentFileMap["codex_magpie_catalog"] = f
		case sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG:
			currentFileMap["grok_config"] = f
		case sessiondv1.ManagedFile_MANAGED_FILE_HERMES_CONFIG:
			currentFileMap["hermes_config"] = f
		}
	}

	for _, a := range sel.Agents {
		switch a {
		case "codex":
			// The catalog file follows the active gateway: magpie mode writes
			// and references .codex/magpie-catalog.json, opencodex mode keeps
			// .codex/opencodex-catalog.json. The previous content of that same
			// file feeds the merge and the model diff.
			catalogFileKey := "codex_catalog"
			catalogPath := fmt.Sprintf("%s/.codex/opencodex-catalog.json", home)
			gwName := "OpenCodeX"
			providerKey := "opencodex"
			providerName := "OpenCodeX Proxy"
			if config.GatewayType == "magpie" {
				catalogFileKey = "codex_magpie_catalog"
				catalogPath = fmt.Sprintf("%s/.codex/magpie-catalog.json", home)
				gwName = "Magpie"
				providerKey = "magpie"
				providerName = "Magpie Proxy"
			}
			if curCat := currentFileMap[catalogFileKey]; curCat != nil && curCat.Content != nil {
				var parsed any
				if err := json.Unmarshal(curCat.Content, &parsed); err == nil {
					if m, ok := parsed.(map[string]any); ok {
						if mdls, ok := m["models"].([]any); ok {
							oldCatalogModels = mdls
						}
					}
				}
			}
			files[catalogFileKey] = RenderCodexCatalog(catalog, oldCatalogModels, gwName)

			var existingCodexCfg string
			if curCfg := currentFileMap["codex_config"]; curCfg != nil && curCfg.Content != nil {
				existingCodexCfg = string(curCfg.Content)
			}

			var allProviders []ConfiguredProvider
			for k, p := range config.GatewayProfiles {
				pURL := p.TargetOverrides[sel.TargetID]
				if pURL == "" {
					pURL = p.InferenceURL
				}
				if pURL != "" {
					pName := "OpenCodeX Proxy"
					if k == "magpie" {
						pName = "Magpie Proxy"
					}
					allProviders = append(allProviders, ConfiguredProvider{
						Key:     k,
						Name:    pName,
						BaseURL: pURL,
						EnvKey:  gatewayEnvKeys[k],
					})
				}
			}

			files["codex_config"] = RenderCodexConfig(
				existingCodexCfg, inferenceURL, catalogPath, providerKey, providerName, gatewayEnvKeys[providerKey], allProviders,
			)

		case "grok":
			var existingGrokCfg string
			if curCfg := currentFileMap["grok_config"]; curCfg != nil && curCfg.Content != nil {
				existingGrokCfg = string(curCfg.Content)
			}
			files["grok_config"] = RenderGrokConfig(existingGrokCfg, catalog, inferenceURL, config.APIKey)

		case "hermes":
			var existingHermesCfg string
			if curCfg := currentFileMap["hermes_config"]; curCfg != nil && curCfg.Content != nil {
				existingHermesCfg = string(curCfg.Content)
			}
			if strings.TrimSpace(existingHermesCfg) == "" {
				hermesState = map[string]any{"dynamic": true, "status": "未找到配置"}
			} else {
				bindings := make(map[string]map[string]string)
				hermesProviderKey := "opencodex"
				if config.GatewayType == "magpie" {
					hermesProviderKey = "magpie"
				}
				bindings[hermesProviderKey] = map[string]string{
					"base_url": inferenceURL,
					"api_key":  config.APIKey,
				}
				rendered := RenderHermesConfig(existingHermesCfg, bindings)
				if rendered != "" {
					files["hermes_config"] = rendered
				}
			}
		}
	}

	newSlugs := make(map[string]bool)
	for _, m := range catalog.Models {
		newSlugs[m.Slug] = true
	}
	oldSlugs := make(map[string]bool)
	for _, m := range oldCatalogModels {
		if mm, ok := m.(map[string]any); ok {
			s, _ := mm["slug"].(string)
			if s == "" {
				s, _ = mm["id"].(string)
			}
			if s != "" {
				oldSlugs[s] = true
			}
		}
	}

	added := []string{}
	removed := []string{}
	keptCount := 0
	for s := range newSlugs {
		if !oldSlugs[s] {
			added = append(added, s)
		} else {
			keptCount++
		}
	}
	for s := range oldSlugs {
		if !newSlugs[s] {
			removed = append(removed, s)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	changes := []map[string]any{}
	for name, text := range files {
		expectedSHA := sha256Text(text)
		currentSHA := ""
		currentContent := ""
		if cur := currentFileMap[name]; cur != nil {
			currentSHA = cur.Sha256
			currentContent = string(cur.Content)
		}
		changes = append(changes, map[string]any{
			"file":            name,
			"changed":         expectedSHA != currentSHA,
			"current_sha256":  currentSHA,
			"expected_sha256": expectedSHA,
			"diff":            unifiedDiffText(currentContent, text, name),
		})
	}

	reloadPending := false
	if sel.TargetID == "local" {
		sessions, _ := h.store.ListSessions(ctx, "")
		for _, s := range sessions {
			status, _ := s["status"].(string)
			if status == "running" || status == "waiting_approval" {
				reloadPending = true
				break
			}
		}
	}

	return &targetPlanResult{
		TargetID:           sel.TargetID,
		Agents:             sel.Agents,
		Files:              files,
		ModelDiff:          map[string]any{"added": added, "removed": removed, "kept_count": keptCount},
		Changes:            changes,
		CatalogFingerprint: catalog.Fingerprint,
		ReloadPending:      reloadPending,
		Hermes:             hermesState,
		TargetProto:        targetProto,
	}, nil
}

func (h *Handler) handleGetModelSyncTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := h.listTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": targets})
}

func (h *Handler) handlePreviewModelSync(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Targets []any `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_body", "无效的请求格式")
		return
	}

	selections, err := h.validateSelections(r.Context(), payload.Targets)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_selection", err.Error())
		return
	}

	config, err := GetGatewayConfig(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}

	catalog, err := fetchCatalog(r.Context(), h.httpClient, config)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_catalog_failed", fmt.Sprintf("模型同步预览失败：%v", err))
		return
	}

	targetsOut := []map[string]any{}
	for _, sel := range selections {
		plan, err := h.buildTargetPlan(r.Context(), catalog, sel, config)
		if err != nil {
			writeError(w, http.StatusBadGateway, "plan_failed", fmt.Sprintf("模型同步预览失败：%v", err))
			return
		}
		targetsOut = append(targetsOut, map[string]any{
			"target_id":           plan.TargetID,
			"agents":              plan.Agents,
			"model_diff":          plan.ModelDiff,
			"changes":             plan.Changes,
			"catalog_fingerprint": plan.CatalogFingerprint,
			"reload_pending":      plan.ReloadPending,
			"hermes":              plan.Hermes,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"catalog_fingerprint": catalog.Fingerprint,
		"model_count":         len(catalog.Models),
		"targets":             targetsOut,
	})
}

func (h *Handler) handleStartModelSync(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Targets []any `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_body", "无效的请求格式")
		return
	}

	selections, err := h.validateSelections(r.Context(), payload.Targets)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_selection", err.Error())
		return
	}

	if h.client == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable", "小内核未启用")
		return
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	opID := "model-sync-" + hex.EncodeToString(b)

	job := syncJob{
		ID:        opID,
		Status:    "running",
		CreatedAt: nowISO(),
		UpdatedAt: nowISO(),
		Targets:   []map[string]any{},
	}
	if err := saveJob(r.Context(), h.store, job); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", "failed to record durable sync job: "+err.Error())
		return
	}

	ctx, cancel := context.WithCancel(h.jobs.ctx)
	h.jobs.register(opID, cancel)
	h.jobs.wg.Add(1)

	go func() {
		defer h.jobs.wg.Done()
		h.runSyncJob(ctx, opID, selections, job)
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     opID,
		"status": "running",
	})
}

func (h *Handler) runSyncJob(ctx context.Context, opID string, selections []syncTargetSelection, job syncJob) {
	defer h.jobs.finish(opID)

	config, err := GetGatewayConfig(ctx, h.store)
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		job.UpdatedAt = nowISO()
		_ = saveJob(context.Background(), h.store, job)
		return
	}

	catalog, err := fetchCatalog(ctx, h.httpClient, config)
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		job.UpdatedAt = nowISO()
		_ = saveJob(context.Background(), h.store, job)
		return
	}

	job.CatalogFingerprint = catalog.Fingerprint

	var plans []*targetPlanResult
	for _, sel := range selections {
		p, err := h.buildTargetPlan(ctx, catalog, sel, config)
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
			job.UpdatedAt = nowISO()
			_ = saveJob(context.Background(), h.store, job)
			return
		}
		plans = append(plans, p)
	}

	if ctx.Err() != nil {
		job.Status = "cancelled"
		job.UpdatedAt = nowISO()
		_ = saveJob(context.Background(), h.store, job)
		return
	}

	results := []map[string]any{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range plans {
		wg.Add(1)
		go func(plan *targetPlanResult) {
			defer wg.Done()
			if ctx.Err() != nil {
				mu.Lock()
				results = append(results, map[string]any{
					"target_id": plan.TargetID,
					"status":    "cancelled",
				})
				mu.Unlock()
				return
			}

			// Reconcile pending/prior operation to never rerun uncertain apply
			targetOpID := fmt.Sprintf("%s-%s-apply", opID, plan.TargetID)
			opKey := &sessiondv1.OperationKey{
				OperationId:      targetOpID,
				LegacyAction:     "model_config.apply",
				Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
			}

			// Check if this operation already exists in daemon ledger
			existingReceipt, getErr := h.client.Operation.GetOperation(ctx, &sessiondv1.GetOperationRequest{
				Key: opKey,
			})
			if getErr == nil && existingReceipt != nil {
				if existingReceipt.State == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN {
					mu.Lock()
					results = append(results, map[string]any{
						"target_id": plan.TargetID,
						"status":    "failed",
						"error":     "previous operation outcome uncertain; cannot retry apply",
					})
					mu.Unlock()
					return
				}
			}

			var fileInputs []*sessiondv1.ModelConfigFileInput
			for name, content := range plan.Files {
				var mf sessiondv1.ManagedFile
				switch name {
				case "codex_config":
					mf = sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG
				case "codex_catalog":
					mf = sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CATALOG
				case "codex_magpie_catalog":
					mf = sessiondv1.ManagedFile_MANAGED_FILE_CODEX_MAGPIE_CATALOG
				case "grok_config":
					mf = sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG
				case "hermes_config":
					mf = sessiondv1.ManagedFile_MANAGED_FILE_HERMES_CONFIG
				}

				var expSha *string
				for _, chg := range plan.Changes {
					if chg["file"] == name {
						if curSha, ok := chg["current_sha256"].(string); ok && curSha != "" {
							sCopy := curSha
							expSha = &sCopy
						}
						break
					}
				}

				fileInputs = append(fileInputs, &sessiondv1.ModelConfigFileInput{
					File:           mf,
					Content:        []byte(content),
					ExpectedSha256: expSha,
				})
			}

			if len(fileInputs) == 0 {
				mu.Lock()
				results = append(results, map[string]any{
					"target_id":      plan.TargetID,
					"status":         "success",
					"changed":        []string{},
					"reload_pending": false,
					"reloaded":       []string{},
					"hermes":         plan.Hermes,
				})
				mu.Unlock()
				return
			}

			var envValues []*sessiondv1.SensitiveEnvironmentValue
			if config.APIKey != "" {
				envValues = append(envValues, &sessiondv1.SensitiveEnvironmentValue{
					Name:  "OPENCODEX_API_AUTH_TOKEN",
					Value: []byte(config.APIKey),
				})
			}
			for gt, prof := range config.GatewayProfiles {
				if prof.APIKey != "" {
					if envK, ok := gatewayEnvKeys[gt]; ok {
						envValues = append(envValues, &sessiondv1.SensitiveEnvironmentValue{
							Name:  envK,
							Value: []byte(prof.APIKey),
						})
					}
				}
			}

			applyReq := &sessiondv1.ModelConfigApplyRequest{
				Meta: &sessiondv1.CommandMeta{
					OperationId:      targetOpID,
					Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
				},
				Target:            plan.TargetProto,
				Files:             fileInputs,
				EnvironmentValues: envValues,
			}

			applyResp, err := h.client.Model.Apply(ctx, applyReq)
			if err != nil {
				mu.Lock()
				results = append(results, map[string]any{
					"target_id": plan.TargetID,
					"status":    "failed",
					"error":     err.Error(),
				})
				mu.Unlock()
				return
			}

			if applyResp.Error != nil {
				mu.Lock()
				results = append(results, map[string]any{
					"target_id": plan.TargetID,
					"status":    "failed",
					"error":     applyResp.Error.Message,
				})
				mu.Unlock()
				return
			}

			if applyResp.OperationState == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN {
				mu.Lock()
				results = append(results, map[string]any{
					"target_id": plan.TargetID,
					"status":    "failed",
					"error":     "model configuration outcome uncertain; reconcile target before retry",
				})
				mu.Unlock()
				return
			}

			changed := []string{}
			for _, ch := range applyResp.Changed {
				changed = append(changed, ch.String())
			}

			// Reload if local and changed
			reloadPending := false
			reloaded := []string{}
			var reloadErr error
			if plan.TargetID == "local" && len(changed) > 0 {
				var reloadAgents []sessiondv1.ReloadAgent
				for _, a := range plan.Agents {
					switch a {
					case "codex":
						reloadAgents = append(reloadAgents, sessiondv1.ReloadAgent_RELOAD_AGENT_CODEX)
					case "grok":
						reloadAgents = append(reloadAgents, sessiondv1.ReloadAgent_RELOAD_AGENT_GROK)
					}
				}
				if len(reloadAgents) > 0 {
					reloadOpID := fmt.Sprintf("%s-%s-reload", opID, plan.TargetID)
					reloadResp, rErr := h.client.Model.Reload(ctx, &sessiondv1.ModelConfigReloadRequest{
						Meta: &sessiondv1.CommandMeta{
							OperationId:      reloadOpID,
							Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
						},
						Agents: reloadAgents,
					})
					if rErr != nil {
						reloadErr = rErr
					} else if reloadResp.Error != nil {
						reloadErr = errors.New(reloadResp.Error.Message)
					} else {
						reloadPending = len(reloadResp.Pending) > 0 || reloadResp.Deferred
						for _, rag := range reloadResp.Reloaded {
							reloaded = append(reloaded, rag.String())
						}
					}
				}
			}

			mu.Lock()
			res := map[string]any{
				"target_id":      plan.TargetID,
				"changed":        changed,
				"reload_pending": reloadPending,
				"reloaded":       reloaded,
				"hermes":         plan.Hermes,
			}
			if reloadErr != nil {
				res["status"] = "failed"
				res["error"] = "reload failed: " + reloadErr.Error()
			} else {
				res["status"] = "success"
			}
			results = append(results, res)
			mu.Unlock()
		}(p)
	}

	wg.Wait()

	hasFailures := false
	hasCancelled := false
	for _, r := range results {
		if r["status"] == "failed" {
			hasFailures = true
		}
		if r["status"] == "cancelled" {
			hasCancelled = true
		}
	}

	job.Targets = results
	job.UpdatedAt = nowISO()
	if hasFailures {
		job.Status = "failed"
		job.Error = "部分或全部目标同步失败"
	} else if hasCancelled {
		job.Status = "cancelled"
	} else {
		job.Status = "success"
	}

	_ = saveJob(context.Background(), h.store, job)
}

func (h *Handler) handleGetModelSyncJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := listJobs(r.Context(), h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	for i := range jobs {
		if jobs[i].Status == "running" && !h.jobs.isActive(jobs[i].ID) {
			jobs[i].Status = "failed"
			jobs[i].UpdatedAt = nowISO()
			jobs[i].Error = "大内核重启，同步任务已中断"
			_ = saveJob(r.Context(), h.store, jobs[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

func (h *Handler) handleCancelModelSync(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "invalid_path", "operation_id required")
		return
	}
	opID := parts[3]
	cancelled := h.jobs.cancelJob(opID)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled})
}
