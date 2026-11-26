import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { api, type BotApprovalMode, type BotProfile } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import {
  availableEffortsForModel,
  effortLabelOf,
  findModelGroup,
  groupModelChoices,
  normalizeEffortValue,
  parseAntigravityModelId,
  resolveGroupVariant,
  type ModelChoice,
  type ModelChoiceGroup,
} from '../chat/antigravityModels'

export const BOT_APPROVAL_MODE_OPTIONS: ReadonlyArray<{
  value: BotApprovalMode
  label: string
  shortLabel: string
  description: string
}> = [
  {
    value: 'full_access',
    label: '完全访问（默认）',
    shortLabel: '完全访问',
    description: '自动执行命令与文件读写，派单后无需人工逐次确认',
  },
  {
    value: 'auto',
    label: '自动审批',
    shortLabel: '自动审批',
    description: '常规操作自动放行，仅对高风险操作请求确认',
  },
  {
    value: 'manual',
    label: '每次询问',
    shortLabel: '每次询问',
    description: '执行命令与修改文件前始终请求人工批准',
  },
]

export function normalizeBotApprovalMode(mode?: string | null): BotApprovalMode {
  if (mode === 'auto' || mode === 'manual' || mode === 'full_access') {
    return mode
  }
  return 'full_access'
}

export function approvalModeShortLabel(mode?: string | null): string {
  const norm = normalizeBotApprovalMode(mode)
  return BOT_APPROVAL_MODE_OPTIONS.find((item) => item.value === norm)?.shortLabel || '完全访问'
}

export function approvalModeDescription(mode?: string | null): string {
  const norm = normalizeBotApprovalMode(mode)
  return BOT_APPROVAL_MODE_OPTIONS.find((item) => item.value === norm)?.description || ''
}

export function isAntigravityAgentKind(kind?: string | null): boolean {
  const lower = (kind || '').trim().toLowerCase()
  return lower === 'antigravity' || lower === 'antigravity-ssh'
}

export function useBotModelCatalog(
  agentId: string,
  agent: Agent | undefined,
  sessions: Session[],
  enabled = true,
) {
  const isAgy = isAntigravityAgentKind(agent?.kind)
  const preferredSession = useMemo(() => {
    if (!agentId) return null
    const conn = agent?.connection_id || 'local'
    const matching = sessions.filter((s) => s.agent_id === agentId)
    return (
      matching.find((s) => (s.connection_id || 'local') === conn) ||
      matching[0] ||
      null
    )
  }, [agentId, agent?.connection_id, sessions])

  const query = useQuery({
    queryKey: ['astrorder', 'bot-model-catalog', agentId, preferredSession?.id || ''],
    queryFn: async (): Promise<ModelChoice[]> => {
      let probeId = preferredSession?.id || ''
      if (!probeId) {
        const res = await api.getSessions(agentId)
        const conn = agent?.connection_id || 'local'
        const candidate =
          res.items.find((item) => (item.connection_id || 'local') === conn) ||
          res.items[0]
        probeId = candidate?.id || ''
      }
      if (!probeId) return []
      const res = await api.getSessionModels(probeId, agentId)
      return res.items || []
    },
    enabled: enabled && Boolean(agentId),
    staleTime: 60_000,
    retry: false,
  })

  const groups = useMemo(
    () => groupModelChoices(query.data || [], { treatAllAsAntigravity: isAgy }),
    [query.data, isAgy],
  )

  return {
    items: query.data || [],
    groups,
    isLoading: query.isLoading,
    isError: query.isError,
    isAntigravity: isAgy,
  }
}

export function splitBotModelSelection(
  groups: ModelChoiceGroup[],
  isAntigravity: boolean,
  savedProvider?: string | null,
  savedModel?: string | null,
  savedEffort?: string | null,
): { groupKey: string; effort: string } {
  const model = (savedModel || '').trim()
  const provider = (savedProvider || '').trim()
  const rawEffort = (savedEffort || '').trim()

  if (!model) {
    return { groupKey: '', effort: normalizeEffortValue(rawEffort) || rawEffort }
  }

  // Exact match via provider + model, then fallback to model-only match if provider wasn't stored previously.
  const matched =
    findModelGroup(groups, provider, model) ||
    groups.find(
      (g) =>
        g.baseModel === model ||
        g.defaultVariant.model === model ||
        g.efforts.some((e) => e.choice.model === model),
    ) ||
    null

  if (matched) {
    if (isAntigravity) {
      const fromVariant = matched.efforts.find((e) => e.choice.model === model)?.effort
      const fromId = parseAntigravityModelId(model).effort
      const resolvedEffort =
        fromVariant ||
        normalizeEffortValue(rawEffort) ||
        fromId ||
        (matched.isGrouped ? parseAntigravityModelId(matched.defaultVariant.model).effort || '' : '')
      return { groupKey: matched.key, effort: resolvedEffort }
    }
    return {
      groupKey: matched.key,
      effort: normalizeEffortValue(rawEffort) || rawEffort,
    }
  }

  const fallbackEffort =
    normalizeEffortValue(rawEffort) ||
    (isAntigravity ? parseAntigravityModelId(model).effort || '' : rawEffort)
  return {
    groupKey: JSON.stringify([provider, model]),
    effort: fallbackEffort,
  }
}

export function resolveBotModelSelection(
  groups: ModelChoiceGroup[],
  isAntigravity: boolean,
  groupKey: string,
  effort: string,
): { default_provider: string; default_model: string; default_effort: string } {
  const cleanEffort = effort.trim()
  if (!groupKey) {
    return {
      default_provider: '',
      default_model: '',
      default_effort: cleanEffort,
    }
  }
  const group = groups.find((g) => g.key === groupKey)
  if (group) {
    if (isAntigravity) {
      if (group.isGrouped) {
        const variant = resolveGroupVariant(group, cleanEffort || null)
        const variantEffort = parseAntigravityModelId(variant.model).effort || cleanEffort
        return {
          default_provider: variant.provider,
          default_model: variant.model,
          default_effort: variantEffort,
        }
      }
      return {
        default_provider: group.defaultVariant.provider,
        default_model: group.defaultVariant.model,
        default_effort: group.efforts.length === 1 ? group.efforts[0].effort : '',
      }
    }
    return {
      default_provider: group.defaultVariant.provider,
      default_model: group.defaultVariant.model,
      default_effort: cleanEffort,
    }
  }

  // Fallback for a saved model that isn't present in the currently loaded catalog.
  try {
    const parsed = JSON.parse(groupKey) as [string, string]
    if (Array.isArray(parsed) && parsed.length === 2) {
      return {
        default_provider: String(parsed[0] || ''),
        default_model: String(parsed[1] || ''),
        default_effort: cleanEffort,
      }
    }
  } catch {
    // ignore
  }
  return {
    default_provider: '',
    default_model: groupKey,
    default_effort: cleanEffort,
  }
}

export function effortOptionsForBotSelection(
  groups: ModelChoiceGroup[],
  isAntigravity: boolean,
  groupKey: string,
): {
  options: ReadonlyArray<{ value: string; label: string }>
  disabled: boolean
  placeholder: string
} {
  const group = groups.find((g) => g.key === groupKey) || null
  if (isAntigravity && group) {
    if (group.isGrouped && group.efforts.length > 1) {
      return {
        options: availableEffortsForModel(group),
        disabled: false,
        placeholder: '随模型默认',
      }
    }
    if (group.efforts.length === 1) {
      return {
        options: group.efforts.map((e) => ({ value: e.effort, label: e.label })),
        disabled: true,
        placeholder: group.efforts[0].label,
      }
    }
    return {
      options: [],
      disabled: true,
      placeholder: '该模型固定档位',
    }
  }
  return {
    options: availableEffortsForModel(null),
    disabled: false,
    placeholder: '沿用默认',
  }
}

export function formatBotModelSummary(
  bot: Pick<BotProfile, 'default_provider' | 'default_model' | 'default_effort'>,
  groups: ModelChoiceGroup[],
  isAntigravity: boolean,
): string {
  const model = (bot.default_model || '').trim()
  const effort = (bot.default_effort || '').trim()
  if (!model) {
    const effLabel = effortLabelOf(effort)
    return effLabel ? `默认模型 · 思考${effLabel}` : '沿用 Agent 默认模型'
  }
  const group =
    findModelGroup(groups, bot.default_provider, model) ||
    groups.find(
      (g) =>
        g.baseModel === model ||
        g.defaultVariant.model === model ||
        g.efforts.some((e) => e.choice.model === model),
    ) ||
    null

  if (group) {
    if (isAntigravity && group.isGrouped) {
      const parsedEff = parseAntigravityModelId(model).effort || normalizeEffortValue(effort)
      const effLabel = effortLabelOf(parsedEff)
      return effLabel ? `${group.label} · ${effLabel}` : group.label
    }
    const effLabel = !isAntigravity ? effortLabelOf(effort) : null
    return effLabel ? `${group.label} · ${effLabel}` : group.label
  }

  if (isAntigravity) {
    const parsed = parseAntigravityModelId(model)
    const effLabel = effortLabelOf(parsed.effort || effort)
    return effLabel ? `${parsed.baseModel} · ${effLabel}` : model
  }
  const effLabel = effortLabelOf(effort)
  return effLabel ? `${model} · ${effLabel}` : model
}
