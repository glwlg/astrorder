import { useQuery } from '@tanstack/react-query'
import { api, type SessionCheckpoint } from '../api/client'
import type { Session } from '../domain/types'
import { CODE_CHECKPOINTS_PLUGIN_ID, DEFAULT_MAX_AUTO_CHECKPOINTS } from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'

export function checkpointsQueryKey(agentId: string, sessionId: string) {
  return ['astrorder', 'checkpoints', agentId, sessionId] as const
}

export function useSessionCheckpoints(session: Session | null | undefined, enabled = true) {
  const agentId = session?.agent_id ?? ''
  const sessionId = session?.id ?? ''
  const hasWorkspace = Boolean(session?.workspace?.trim())
  const pluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(CODE_CHECKPOINTS_PLUGIN_ID))
  const maxAutoSnapshots = usePluginSettingsStore((s) => {
    const raw = s.getPluginConfig<unknown>(CODE_CHECKPOINTS_PLUGIN_ID, 'maxAutoSnapshots', DEFAULT_MAX_AUTO_CHECKPOINTS)
    const parsed = typeof raw === 'number' ? raw : Number(raw)
    if (!Number.isFinite(parsed) || parsed < 1) return DEFAULT_MAX_AUTO_CHECKPOINTS
    return Math.min(200, Math.round(parsed))
  })
  const query = useQuery<{ items: SessionCheckpoint[] }>({
    queryKey: checkpointsQueryKey(agentId, sessionId),
    queryFn: () => api.listCheckpoints(sessionId, agentId),
    enabled: Boolean(enabled && pluginEnabled && agentId && sessionId && hasWorkspace),
    staleTime: 15_000,
    retry: false,
  })
  return {
    ...query,
    items: pluginEnabled ? (query.data?.items ?? []) : [],
    hasWorkspace,
    pluginEnabled,
    maxAutoSnapshots,
  }
}
