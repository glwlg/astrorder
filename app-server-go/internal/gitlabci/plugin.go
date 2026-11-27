package gitlabci

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/pluginhost"
)

//go:embed SKILL.md
var gitlabSkillMD string

const (
	// PluginID is the canonical extension ID for the builtin GitLab CI plugin.
	PluginID = "gitlab-ci-viewer"

	// SignalKindWorkspaceFailures is the standard signal kind for workspace CI/build failure feeds.
	SignalKindWorkspaceFailures = "workspace.failure_signals"
)

var (
	_ pluginhost.BuiltinPlugin        = (*Service)(nil)
	_ pluginhost.BuiltinSkillProvider = (*Service)(nil)
)

// Skills returns the GitLab CI plugin's self-contained SKILL.md entry.
func (s *Service) Skills() []pluginhost.PluginSkillEntry {
	return []pluginhost.PluginSkillEntry{
		{
			PluginID:    PluginID,
			Name:        "astrorder-gitlab-ci",
			DisplayName: "GitLab CI 流水线查询",
			Description: "通过星序查询自建或云端 GitLab CI 流水线、阶段作业与失败日志（自动匹配当前工作区仓库）",
			Content:     gitlabSkillMD,
		},
	}
}

// Manifest returns the declarative extension manifest for the builtin GitLab CI plugin.
func (s *Service) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{
		SchemaVersion: 1,
		ID:            PluginID,
		Name:          "GitLab CI 流水线",
		Version:       "1.0.0",
		Category:      "development",
		Description:   "直连自建或云端 GitLab，在会话底栏与右侧面板实时追踪 CI 流水线、阶段作业与日志，并开放给 Agent 排查失败任务",
		Author:        "Astrorder",
		Icon:          "gitlab",
		Permissions: []string{
			pluginhost.PermNetworkOutbound,
			pluginhost.PermWorkspaceGitRead,
			pluginhost.PermStorageSecrets,
			pluginhost.PermAgentCapabilities,
			pluginhost.PermUISlots,
			pluginhost.PermSignalsProvide,
		},
		Backend: pluginhost.BackendSpec{
			Type: pluginhost.BackendTypeBuiltin,
		},
		Settings: pluginhost.SettingsSpec{
			Mode: pluginhost.SettingsModeCustomUI,
		},
		Contributes: pluginhost.ContributesSpec{
			UISlots: []pluginhost.UISlotContribution{
				{
					Slot:              "chat.composer.status_bar",
					ID:                "pipeline-pill",
					Renderer:          "declarative_pill",
					PollEndpoint:      "/status-pill",
					PollIntervalMS:    15000,
					RequiresWorkspace: true,
					OnClick: &pluginhost.SlotClickAction{
						OpenPanel: "ci-panel",
					},
				},
				{
					Slot:              "chat.git_menu.items",
					ID:                "open-ci",
					Renderer:          "menu_item",
					Label:             "查看 GitLab CI 流水线...",
					Icon:              "gitlab",
					RequiresWorkspace: true,
					OnClick: &pluginhost.SlotClickAction{
						OpenPanel: "ci-panel",
					},
				},
				{
					Slot:              "sidecar.welcome_menu",
					ID:                "ci-entry",
					Renderer:          "welcome_item",
					Label:             "GitLab CI 流水线",
					Badge:             "CI / CD",
					Icon:              "gitlab",
					RequiresWorkspace: true,
					OnClick: &pluginhost.SlotClickAction{
						OpenPanel: "ci-panel",
					},
				},
			},
			SidecarPanels: []pluginhost.SidecarPanelContribution{
				{
					ID:         "ci-panel",
					Title:      "GitLab CI",
					BadgeLabel: "[GitLab CI]",
					TabColor:   "#e24329",
					MediaType:  "application/x-gitlab-ci",
					Icon:       "gitlab",
				},
			},
			AgentCapabilities: []pluginhost.AgentCapabilityContribution{
				{
					ID:       "pipelines",
					CLIAlias: []string{"gitlab", "pipelines"},
					Summary:  "List recent GitLab CI pipelines for the current workspace repository (auto-detected from .git/config and HEAD) or an explicit GitLab project.",
					InputSchema: map[string]any{
						"workspace":     "optional workspace directory (defaults to caller session workspace)",
						"connection_id": "optional connection id (defaults to caller session connection)",
						"instance_id":   "optional configured GitLab instance id or name (auto-matched by git remote host when omitted)",
						"project":       "optional GitLab project path override (e.g. 'group/repo')",
						"ref":           "optional branch or tag filter",
						"sha":           "optional commit SHA filter",
						"limit":         "optional maximum pipelines to return (default 10, max 30)",
					},
					RequiresCaller: true,
				},
				{
					ID:       "pipeline",
					CLIAlias: []string{"gitlab", "status"},
					Summary:  "Inspect a GitLab CI pipeline's detailed stages and jobs (defaults to the latest pipeline matching the current workspace HEAD/branch when pipeline_id is omitted).",
					InputSchema: map[string]any{
						"pipeline_id":   "optional positive integer pipeline ID (omit to inspect the latest pipeline)",
						"workspace":     "optional workspace directory (defaults to caller session workspace)",
						"connection_id": "optional connection id (defaults to caller session connection)",
						"instance_id":   "optional configured GitLab instance id or name",
						"project":       "optional GitLab project path override",
						"ref":           "optional branch or tag filter when picking latest pipeline",
					},
					RequiresCaller: true,
				},
				{
					ID:       "job_log",
					CLIAlias: []string{"gitlab", "log"},
					Summary:  "Read the ANSI-cleaned tail of a GitLab CI job's execution log to diagnose build or test failures.",
					InputSchema: map[string]any{
						"job_id":        "required positive integer GitLab CI job ID",
						"tail":          "optional number of trailing log lines to return (default 200, max 1000)",
						"workspace":     "optional workspace directory (defaults to caller session workspace)",
						"connection_id": "optional connection id (defaults to caller session connection)",
						"instance_id":   "optional configured GitLab instance id or name",
						"project":       "optional GitLab project path override",
					},
					RequiresCaller: true,
				},
			},
			SignalProviders: []pluginhost.SignalProviderContribution{
				{
					Kind: SignalKindWorkspaceFailures,
				},
			},
		},
	}
}

// ServeExtension handles authenticated extension proxy requests routed under /api/v1/ext/gitlab-ci-viewer/*.
func (s *Service) ServeExtension(w http.ResponseWriter, r *http.Request, subpath string) {
	trimmed := strings.Trim(strings.TrimSpace(subpath), "/")
	if trimmed == "" {
		writeJSONError(w, http.StatusNotFound, "Not Found")
		return
	}
	cleanSub := "/" + trimmed
	if cleanSub == "/status-pill" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		s.handleStatusPill(w, r)
		return
	}

	targetPath := BasePath + cleanSub
	reqCopy := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = targetPath
	reqCopy.URL = &urlCopy
	s.serveRoutes(w, reqCopy, targetPath)
}

func (s *Service) handleStatusPill(w http.ResponseWriter, r *http.Request) {
	target := queryTargetFromRequest(r)
	if target.Limit <= 0 {
		target.Limit = 6
	}
	if strings.TrimSpace(target.Workspace) == "" && strings.TrimSpace(target.ProjectPath) == "" {
		writeJSON(w, http.StatusOK, pluginhost.DeclarativePillPayload{Visible: false})
		return
	}
	resp, err := s.QueryPipelines(r.Context(), target)
	if err != nil || resp == nil || !resp.Matched {
		writeJSON(w, http.StatusOK, pluginhost.DeclarativePillPayload{Visible: false})
		return
	}
	projectName := "GitLab"
	if resp.Repo != nil && strings.TrimSpace(resp.Repo.ProjectPath) != "" {
		projectName = resp.Repo.ProjectPath
	}
	if resp.LatestPipeline == nil {
		writeJSON(w, http.StatusOK, pluginhost.DeclarativePillPayload{
			Visible:  true,
			Label:    "CI",
			Sublabel: "暂无记录",
			Tone:     "muted",
			DotColor: "#868e96",
			Tooltip:  fmt.Sprintf("%s — 点击查看 GitLab CI 流水线", projectName),
			Icon:     "gitlab",
		})
		return
	}
	p := resp.LatestPipeline
	meta := statusPillMeta(p.Status)
	tooltip := fmt.Sprintf("%s · #%d (%s @ %s) — 点击查看 CI 流水线与日志", projectName, p.ID, p.Ref, p.ShortSHA)
	writeJSON(w, http.StatusOK, pluginhost.DeclarativePillPayload{
		Visible:     true,
		Label:       "CI " + meta.label,
		Sublabel:    fmt.Sprintf("#%d", p.ID),
		Tone:        meta.tone,
		DotColor:    meta.dotColor,
		Pulsing:     meta.pulsing,
		Tooltip:     tooltip,
		Icon:        "gitlab",
		PanelParams: map[string]any{"pipeline_id": p.ID},
	})
}

type pillVisualMeta struct {
	label    string
	tone     string
	dotColor string
	pulsing  bool
}

func statusPillMeta(status string) pillVisualMeta {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return pillVisualMeta{label: "通过", tone: "success", dotColor: "#2f9e44", pulsing: false}
	case "running":
		return pillVisualMeta{label: "运行中", tone: "running", dotColor: "#228be6", pulsing: true}
	case "pending", "created", "preparing", "waiting_for_resource", "scheduled":
		return pillVisualMeta{label: "排队中", tone: "pending", dotColor: "#d97706", pulsing: true}
	case "failed":
		return pillVisualMeta{label: "失败", tone: "failed", dotColor: "#e03131", pulsing: false}
	case "canceled", "canceling":
		return pillVisualMeta{label: "已取消", tone: "muted", dotColor: "#868e96", pulsing: false}
	case "manual":
		return pillVisualMeta{label: "待手动", tone: "manual", dotColor: "#5c7cfa", pulsing: false}
	case "skipped":
		return pillVisualMeta{label: "已跳过", tone: "muted", dotColor: "#adb5bd", pulsing: false}
	default:
		return pillVisualMeta{label: nonEmpty(status, "未知"), tone: "muted", dotColor: "#868e96", pulsing: false}
	}
}

// InvokeCapability dispatches agent capabilities declared by the GitLab CI plugin.
func (s *Service) InvokeCapability(ctx context.Context, capID string, payload map[string]any) (any, error) {
	switch strings.TrimSpace(capID) {
	case "pipelines":
		return s.QueryPipelines(ctx, buildQueryTargetFromPayload(payload))
	case "pipeline":
		pid := payloadInt64(payload, "pipeline_id")
		return s.QueryPipelineDetail(ctx, buildQueryTargetFromPayload(payload), pid)
	case "job_log":
		jid := payloadInt64(payload, "job_id")
		tail := int(payloadInt64(payload, "tail"))
		if tail <= 0 {
			tail = int(payloadInt64(payload, "tail_lines"))
		}
		return s.QueryJobTrace(ctx, buildQueryTargetFromPayload(payload), jid, tail, true)
	default:
		return nil, fmt.Errorf("%w: unknown gitlab-ci-viewer capability %q", ErrNotFound, capID)
	}
}

// CollectSignals produces workspace failure signals from failed GitLab CI pipelines.
func (s *Service) CollectSignals(ctx context.Context, kind string, q pluginhost.SignalQuery) ([]pluginhost.SignalItem, error) {
	if strings.TrimSpace(kind) != SignalKindWorkspaceFailures {
		return nil, nil
	}
	if strings.TrimSpace(q.Workspace) == "" {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 6
	}
	resp, err := s.QueryPipelines(ctx, QueryTarget{
		Workspace:    q.Workspace,
		ConnectionID: q.ConnectionID,
		SessionID:    q.SessionID,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.Matched {
		return nil, nil
	}
	out := make([]pluginhost.SignalItem, 0, len(resp.Pipelines))
	for _, p := range resp.Pipelines {
		if p.Status != "failed" {
			continue
		}
		out = append(out, pluginhost.SignalItem{
			SourcePlugin: PluginID,
			Kind:         "gitlab_ci",
			Title:        fmt.Sprintf("GitLab CI Pipeline #%d 失败 (%s)", p.ID, p.Ref),
			Detail:       fmt.Sprintf("SHA %s · %s", p.ShortSHA, p.WebURL),
			URL:          p.WebURL,
			Severity:     "error",
			Timestamp:    nonEmpty(p.UpdatedAt, p.CreatedAt),
			Meta: map[string]any{
				"pipeline_id": p.ID,
				"ref":         p.Ref,
				"sha":         p.SHA,
				"short_sha":   p.ShortSHA,
			},
		})
	}
	return out, nil
}

func buildQueryTargetFromPayload(payload map[string]any) QueryTarget {
	if payload == nil {
		return QueryTarget{}
	}
	project := payloadString(payload, "project")
	if project == "" {
		project = payloadString(payload, "project_path")
	}
	instanceID := payloadString(payload, "instance_id")
	if instanceID == "" {
		instanceID = payloadString(payload, "instance")
	}
	return QueryTarget{
		Workspace:    payloadString(payload, "workspace"),
		ConnectionID: payloadString(payload, "connection_id"),
		SessionID:    payloadString(payload, "session_id"),
		InstanceID:   instanceID,
		ProjectPath:  project,
		Ref:          payloadString(payload, "ref"),
		SHA:          payloadString(payload, "sha"),
		Limit:        int(payloadInt64(payload, "limit")),
	}
}

func payloadString(payload map[string]any, key string) string {
	v, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func payloadInt64(payload map[string]any, key string) int64 {
	raw, exists := payload[key]
	if !exists || raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return 0
}
