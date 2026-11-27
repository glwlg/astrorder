package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/lifecycle"
	"astrorder.dev/app-server/internal/llmcompat"
	"astrorder.dev/app-server/internal/modelgateway"
)

// OnboardingCapabilitiesInjector installs Astrorder CLI and official skills into a
// configured Agent during first-launch onboarding.
type OnboardingCapabilitiesInjector interface {
	InjectForOnboarding(ctx context.Context, envID, agentID, agentKind string) (cliReady bool, installedSkills []string, warnings []string)
}

type OnboardingConfigureRequest struct {
	EnvironmentID      string `json:"environment_id"`
	AgentKind          string `json:"agent_kind"`
	AgentID            string `json:"agent_id"`
	APIKey             string `json:"api_key"`
	BaseURL            string `json:"base_url"`
	ProviderName       string `json:"provider_name"`
	Protocol           string `json:"protocol"`
	Model              string `json:"model"`
	SkipKey            bool   `json:"skip_key"`
	InjectCapabilities bool   `json:"inject_capabilities"`
	SyncUniversalLLM   *bool  `json:"sync_universal_llm"`
	Workspace          string `json:"workspace"`
}

type OnboardingConfigureResult struct {
	OK                    bool     `json:"ok"`
	EnvironmentID         string   `json:"environment_id"`
	AgentID               string   `json:"agent_id"`
	AgentKind             string   `json:"agent_kind"`
	Workspace             string   `json:"workspace"`
	CredentialsWritten    bool     `json:"credentials_written"`
	UniversalLLMMode      string   `json:"universal_llm_mode,omitempty"` // "api_key" | "agent" | ""
	CLIReady              bool     `json:"cli_ready"`
	InjectedSkills        []string `json:"injected_skills"`
	Warnings              []string `json:"warnings,omitempty"`
	DaemonReloaded        bool     `json:"daemon_reloaded,omitempty"`
	RequiresDaemonRestart bool     `json:"requires_daemon_restart"`
}

func (s *Service) ConfigureOnboarding(ctx context.Context, req OnboardingConfigureRequest) (OnboardingConfigureResult, error) {
	envID := strings.TrimSpace(req.EnvironmentID)
	if envID == "" {
		envID = "local"
	}
	spec, ok := LookupAgentEngine(req.AgentKind)
	if !ok {
		return OnboardingConfigureResult{}, envtarget.Errorf(422, "不支持的 Agent 类型：%s", req.AgentKind)
	}

	env, err := s.resolver.Open(ctx, envID)
	if err != nil {
		return OnboardingConfigureResult{}, err
	}

	agentID := strings.TrimSpace(req.AgentID)
	agentName := spec.Name
	if agentID == "" && s.store != nil {
		if agents, listErr := s.store.ListAgents(ctx); listErr == nil {
			for _, row := range agents {
				rowKind, _ := row["kind"].(string)
				rowConn, _ := row["connection_id"].(string)
				if envID == "local" && rowConn != "" && rowConn != "local" {
					continue
				}
				if matchedSpec, ok := LookupAgentEngine(rowKind); ok && matchedSpec.Kind == spec.Kind {
					if id, _ := row["id"].(string); id != "" {
						agentID = id
						if n, _ := row["name"].(string); strings.TrimSpace(n) != "" {
							agentName = strings.TrimSpace(n)
						}
						break
					}
				}
			}
		}
	}

	// Ensure a clean personal workspace exists so novice users never have to hand-type paths.
	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(s.defaultWorkspace)
	}
	if workspace == "" && env.Target().Kind == "local" {
		if home, hErr := os.UserHomeDir(); hErr == nil && strings.TrimSpace(home) != "" {
			workspace = filepath.Join(home, "AstrorderWorkspace")
		}
	}
	if workspace != "" && env.Target().Kind == "local" {
		_ = os.MkdirAll(workspace, 0o755)
	}

	result := OnboardingConfigureResult{
		OK:             true,
		EnvironmentID:  envID,
		AgentID:        agentID,
		AgentKind:      spec.Kind,
		Workspace:      workspace,
		InjectedSkills: []string{},
	}

	apiKey := strings.TrimSpace(req.APIKey)
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	model := strings.TrimSpace(req.Model)
	proto := llmcompat.NormalizeProtocol(req.Protocol)
	if proto == llmcompat.ProtocolAgent {
		proto = llmcompat.ProtocolOpenAI
	}

	// 1. Optionally write Agent credentials (never overwrite when skip_key is true or api_key is empty).
	if !req.SkipKey && apiKey != "" {
		if err := writeOnboardingAgentCredentials(ctx, env, spec, apiKey, baseURL, model, strings.TrimSpace(req.ProviderName)); err != nil {
			result.Warnings = append(result.Warnings, "写入引擎凭据时出现提示："+err.Error())
		} else {
			result.CredentialsWritten = true
		}
	}

	// 2. Sync Astrorder's default universal LLM (AI Provider):
	//    - If API key was provided -> save as standard HTTP provider & set default.
	//    - Otherwise (no key / existing login) -> wire default universal LLM directly to the chosen Agent + model!
	syncUniversal := req.SyncUniversalLLM == nil || *req.SyncUniversalLLM
	if syncUniversal && s.store != nil {
		if !req.SkipKey && apiKey != "" {
			effBase := baseURL
			if effBase == "" {
				if proto == llmcompat.ProtocolGemini {
					effBase = "https://generativelanguage.googleapis.com/v1beta"
				} else {
					effBase = "https://api.openai.com/v1"
				}
			}
			var models []string
			if model != "" {
				models = []string{model}
			}
			keyCopy := apiKey
			enabled := true
			if _, _, err := aiprovider.UpsertProvider(ctx, s.store, aiprovider.ProviderPatch{
				Name:         strings.TrimSpace(req.ProviderName),
				Protocol:     proto,
				BaseURL:      effBase,
				APIKey:       &keyCopy,
				Models:       models,
				Enabled:      &enabled,
				SetAsDefault: true,
				DefaultModel: model,
			}); err != nil {
				result.Warnings = append(result.Warnings, "同步星序通用模型失败："+err.Error())
			} else {
				result.UniversalLLMMode = "api_key"
			}
		} else if agentID != "" {
			var models []string
			if model != "" {
				models = []string{model}
			}
			enabled := true
			if _, _, err := aiprovider.UpsertProvider(ctx, s.store, aiprovider.ProviderPatch{
				Name:         fmt.Sprintf("%s（免 Key 直连）", agentName),
				Protocol:     llmcompat.ProtocolAgent,
				AgentID:      agentID,
				AgentKind:    spec.Kind,
				Models:       models,
				Enabled:      &enabled,
				SetAsDefault: true,
				DefaultModel: model,
			}); err != nil {
				result.Warnings = append(result.Warnings, "同步免 Key 通用模型失败："+err.Error())
			} else {
				result.UniversalLLMMode = "agent"
			}
		}
	}

	// 3. Inject Astrorder CLI and core official skills into the selected Agent.
	if req.InjectCapabilities && s.onboardingInjector != nil && agentID != "" {
		cliReady, skills, warns := s.onboardingInjector.InjectForOnboarding(ctx, envID, agentID, spec.Kind)
		result.CLIReady = cliReady
		if skills != nil {
			result.InjectedSkills = skills
		}
		result.Warnings = append(result.Warnings, warns...)
	}

	// 4. Sync local runtime config & trigger non-fatal Session Daemon hot reload.
	// Against older daemons (Unimplemented) or temporarily disconnected daemons,
	// degrade gracefully with an actionable warning instead of blocking onboarding.
	if env.Target().Kind == "local" {
		if s.stateDir != "" {
			if toolSpec, ok := LookupSpec(spec.Kind); ok {
				if found, located := s.locateBinary(ctx, env, toolSpec); located {
					_, _ = lifecycle.UpsertLocalRuntime(s.stateDir, s.defaultWorkspace, spec.Kind, found)
				}
			}
		}
		daemonRegistered := s.readDaemonRegistered(ctx, true)
		needsReload := !daemonRegistered[spec.Kind] || result.CredentialsWritten
		if needsReload && (s.getDaemonClient() != nil || s.daemonReloader != nil) {
			reloadRes := s.tryReloadDaemonConfig(ctx)
			if reloadRes.Reloaded {
				result.DaemonReloaded = true
				daemonRegistered = s.readDaemonRegistered(ctx, true)
				if agentID == "" && s.store != nil {
					if agents, listErr := s.store.ListAgents(ctx); listErr == nil {
						for _, row := range agents {
							rowKind, _ := row["kind"].(string)
							rowConn, _ := row["connection_id"].(string)
							if rowConn != "" && rowConn != "local" {
								continue
							}
							if matchedSpec, ok := LookupAgentEngine(rowKind); ok && matchedSpec.Kind == spec.Kind {
								if id, _ := row["id"].(string); id != "" {
									result.AgentID = id
									agentID = id
									break
								}
							}
						}
					}
				}
			} else if reloadRes.Detail != "" && (!daemonRegistered[spec.Kind] || reloadRes.Unsupported) {
				result.Warnings = append(result.Warnings, reloadRes.Detail)
			}
		}
		if agentID == "" && !daemonRegistered[spec.Kind] && !result.DaemonReloaded {
			result.RequiresDaemonRestart = true
		}
	}

	return result, nil
}

func writeOnboardingAgentCredentials(
	ctx context.Context,
	env envtarget.Env,
	spec AgentEngineSpec,
	apiKey string,
	baseURL string,
	model string,
	providerName string,
) error {
	paths, err := env.Paths(ctx)
	if err != nil {
		return err
	}
	home := agentHomeForEngine(paths, spec.HomeField)
	if home == "" {
		return fmt.Errorf("无法确定 %s 的配置目录", spec.Name)
	}

	switch spec.Kind {
	case "codex":
		// 1. Merge OPENAI_API_KEY into ~/.codex/auth.json
		authPath := path.Join(home, "auth.json")
		authDoc := map[string]any{}
		if raw, rErr := env.Read(ctx, authPath); rErr == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &authDoc)
		}
		authDoc["OPENAI_API_KEY"] = apiKey
		encoded, mErr := json.MarshalIndent(authDoc, "", "  ")
		if mErr != nil {
			return mErr
		}
		if err := env.Write(ctx, authPath, append(encoded, '\n'), 0o600); err != nil {
			return err
		}

		// 2. Update ~/.codex/config.toml when custom BaseURL or default model is provided
		isCustomBase := baseURL != "" && !strings.EqualFold(strings.TrimRight(baseURL, "/"), "https://api.openai.com/v1")
		if isCustomBase || model != "" {
			cfgPath := path.Join(home, "config.toml")
			existing := ""
			if raw, rErr := env.Read(ctx, cfgPath); rErr == nil {
				existing = string(raw)
			}
			next := existing
			if isCustomBase {
				pName := providerName
				if pName == "" {
					pName = "Custom OpenAI Provider"
				}
				next = modelgateway.RenderCodexConfig(next, baseURL, "", "astrorder_custom", pName, "OPENAI_API_KEY", nil)
			}
			if model != "" {
				next = upsertTOMLTopKey(next, "model", model)
			}
			if err := env.Write(ctx, cfgPath, []byte(next), 0o600); err != nil {
				return err
			}
		}
		return nil

	case "hermes":
		envPath := path.Join(home, ".env")
		existing := ""
		if raw, rErr := env.Read(ctx, envPath); rErr == nil {
			existing = string(raw)
		}
		next := upsertDotEnvKey(existing, "OPENAI_API_KEY", apiKey)
		if baseURL != "" {
			next = upsertDotEnvKey(next, "OPENAI_BASE_URL", baseURL)
		}
		if model != "" {
			next = upsertDotEnvKey(next, "LLM_MODEL", model)
		}
		return env.Write(ctx, envPath, []byte(next), 0o600)

	case "grok":
		authPath := path.Join(home, "auth.json")
		authDoc := map[string]any{}
		if raw, rErr := env.Read(ctx, authPath); rErr == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &authDoc)
		}
		authDoc["XAI_API_KEY"] = apiKey
		encoded, mErr := json.MarshalIndent(authDoc, "", "  ")
		if mErr != nil {
			return mErr
		}
		return env.Write(ctx, authPath, append(encoded, '\n'), 0o600)

	default:
		return errors.New("该引擎使用官方账号登录，无需手动写入 API Key")
	}
}

func upsertTOMLTopKey(content, key, value string) string {
	quoted, _ := json.Marshal(value)
	replacement := fmt.Sprintf("%s = %s", key, string(quoted))
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	inHeader := true
	firstSectionIdx := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			if inHeader {
				firstSectionIdx = i
			}
			inHeader = false
			continue
		}
		if !inHeader {
			continue
		}
		if strings.HasPrefix(trimmed, key) {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
			if strings.HasPrefix(rest, "=") {
				lines[i] = replacement
				return strings.Join(lines, "\n")
			}
		}
	}
	header := append([]string{}, lines[:firstSectionIdx]...)
	tail := append([]string{}, lines[firstSectionIdx:]...)
	if len(header) == 1 && strings.TrimSpace(header[0]) == "" {
		header = []string{replacement}
	} else {
		header = append(header, replacement)
	}
	out := append(header, tail...)
	res := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	return res
}

func upsertDotEnvKey(content, key, value string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	prefix := key + "="
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			lines[i] = prefix + value
			found = true
			break
		}
	}
	if !found {
		if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
			lines = []string{prefix + value}
		} else {
			lines = append(lines, prefix+value)
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
