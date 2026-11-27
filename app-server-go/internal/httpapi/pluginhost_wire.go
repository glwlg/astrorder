package httpapi

import (
	"context"
	"net"
	"strings"

	"astrorder.dev/app-server/internal/agentapi"
	"astrorder.dev/app-server/internal/capabilities"
	"astrorder.dev/app-server/internal/learning"
	"astrorder.dev/app-server/internal/pluginhost"
)

type pluginHostAgentAdapter struct {
	mgr *pluginhost.Manager
}

func (a *pluginHostAgentAdapter) ListAgentCapabilities(ctx context.Context) []agentapi.ExtensionCapability {
	if a == nil || a.mgr == nil {
		return nil
	}
	raw := a.mgr.ListAgentCapabilities(ctx)
	out := make([]agentapi.ExtensionCapability, 0, len(raw))
	for _, c := range raw {
		out = append(out, agentapi.ExtensionCapability{
			PluginID:       c.PluginID,
			PluginName:     c.PluginName,
			CapabilityID:   c.CapabilityID,
			FullID:         c.FullID,
			CLIAlias:       append([]string(nil), c.CLIAlias...),
			Summary:        c.Summary,
			InputSchema:    c.InputSchema,
			RequiresCaller: c.RequiresCaller,
		})
	}
	return out
}

func (a *pluginHostAgentAdapter) InvokeAgentCapability(ctx context.Context, pluginID, capID string, payload map[string]any) (any, error) {
	if a == nil || a.mgr == nil {
		return nil, agentapi.NewAgentApiError("unavailable", "Extension host is not configured")
	}
	return a.mgr.InvokeAgentCapability(ctx, pluginID, capID, payload)
}

func (a *pluginHostAgentAdapter) ResolveCLIAlias(ctx context.Context, alias []string) (string, string, bool) {
	if a == nil || a.mgr == nil {
		return "", "", false
	}
	pid, cid, _, ok := a.mgr.ResolveCLIAlias(ctx, alias)
	return pid, cid, ok
}

type pluginHostFailureSignalsAdapter struct {
	mgr *pluginhost.Manager
}

func (a *pluginHostFailureSignalsAdapter) CollectFailureSignals(ctx context.Context, workspace, connectionID, sessionID string, limit int) []learning.ExternalFailureSignal {
	if a == nil || a.mgr == nil {
		return nil
	}
	items := a.mgr.CollectSignals(ctx, "workspace.failure_signals", pluginhost.SignalQuery{
		Workspace:    workspace,
		ConnectionID: connectionID,
		SessionID:    sessionID,
		Limit:        limit,
	})
	out := make([]learning.ExternalFailureSignal, 0, len(items))
	for _, item := range items {
		source := strings.TrimSpace(item.Kind)
		if source == "" {
			source = strings.TrimSpace(item.SourcePlugin)
		}
		if source == "" {
			source = "extension"
		}
		out = append(out, learning.ExternalFailureSignal{
			Source:    source,
			Title:     item.Title,
			Detail:    item.Detail,
			Timestamp: item.Timestamp,
		})
	}
	return out
}

func bindPluginHost(ctx context.Context, mgr *pluginhost.Manager, agentHandler any, capabilitiesHandler any) {
	if mgr == nil {
		return
	}
	if binder, ok := agentHandler.(interface {
		SetExtensions(agentapi.ExtensionDispatcher)
	}); ok {
		binder.SetExtensions(&pluginHostAgentAdapter{mgr: mgr})
	}
	if skillBinder, ok := capabilitiesHandler.(interface {
		SetPluginSkillProvider(capabilities.PluginSkillProvider)
	}); ok {
		skillBinder.SetPluginSkillProvider(capabilities.PluginSkillProviderFunc(func(ctx context.Context) []capabilities.PluginSkill {
			entries := mgr.ListEnabledSkills(ctx)
			out := make([]capabilities.PluginSkill, 0, len(entries))
			for _, e := range entries {
				out = append(out, capabilities.PluginSkill{
					PluginID:    e.PluginID,
					Name:        e.Name,
					DisplayName: e.DisplayName,
					Description: e.Description,
					Content:     e.Content,
				})
			}
			return out
		}))
	}
	if syncer, ok := capabilitiesHandler.(interface {
		TriggerAutoSync(context.Context)
	}); ok {
		syncCtx := ctx
		if syncCtx == nil {
			syncCtx = context.Background()
		}
		mgr.SetOnSkillsChanged(func() {
			syncer.TriggerAutoSync(syncCtx)
		})
	}
}

func normalizeLoopbackHostBaseURL(rawBaseURL, listenAddr string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	if trimmed != "" {
		return trimmed
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil || port == "" {
		return ""
	}
	switch strings.Trim(host, "[]") {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
