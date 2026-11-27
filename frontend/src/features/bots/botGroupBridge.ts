import type { BotSummary } from '../../api/client'
import type { Agent } from '../../domain/types'

export interface BotGroupMemberSeed {
  key: string
  id: string
  agentId: string
  machineId: string
  name: string
  alias: string
  role: string
  workspace: string
  model: string
  effort: string
  approval: string
  sourceSessionId: string
  forkSessionId: string
  locked: boolean
  forkUncertain: boolean
  sourceBotId: string
}

function isAntigravityKind(kind?: string | null): boolean {
  return kind === 'antigravity' || kind === 'antigravity-ssh'
}

export function sanitizeGroupAlias(raw: string): string {
  const cleaned = raw.trim().replace(/[\s@]+/g, '-').replace(/^-+|-+$/g, '')
  if (!cleaned) return 'Bot'
  const lower = cleaned.toLowerCase()
  if (lower === 'all' || lower === '所有人') return `${cleaned}-bot`
  return cleaned
}

export function uniqueBotAlias(preferred: string, existingAliases: Iterable<string>): string {
  const base = sanitizeGroupAlias(preferred)
  const used = new Set(Array.from(existingAliases, (a) => a.trim().toLowerCase()).filter(Boolean))
  if (!used.has(base.toLowerCase())) return base
  let suffix = 2
  while (used.has(`${base}-${suffix}`.toLowerCase())) {
    suffix += 1
  }
  return `${base}-${suffix}`
}

export function buildBotRolePrompt(
  bot: Pick<BotSummary, 'description' | 'agents_md_excerpt'>,
): string {
  const desc = (bot.description || '').trim()
  const excerpt = (bot.agents_md_excerpt || '').trim()
  if (desc && excerpt) {
    if (excerpt.includes(desc)) return excerpt
    if (desc.includes(excerpt)) return desc
    return `${desc}；${excerpt}`
  }
  return desc || excerpt
}

export function resolveAgentForBot(
  bot: Pick<BotSummary, 'default_agent_id' | 'connection_id'>,
  agents: Record<string, Agent>,
): Agent | undefined {
  if (bot.default_agent_id && agents[bot.default_agent_id]) {
    return agents[bot.default_agent_id]
  }
  const targetConn = (bot.connection_id || '').trim() || 'local'
  const list = Object.values(agents)
  const byConnReady = list.find(
    (a) => a.status === 'ready' && ((a.connection_id || '').trim() || 'local') === targetConn,
  )
  if (byConnReady) return byConnReady
  return list.find((a) => a.status === 'ready') || list[0]
}

export function createMemberSeedFromBot(
  bot: BotSummary,
  agents: Record<string, Agent>,
  existingAliases: Iterable<string>,
  id: string,
): BotGroupMemberSeed {
  const resolvedAgent = resolveAgentForBot(bot, agents)
  const agentId = resolvedAgent?.id || bot.default_agent_id
  const machineId =
    (bot.connection_id || '').trim() ||
    (resolvedAgent?.connection_id || '').trim() ||
    'local'
  const agyMember = isAntigravityKind(resolvedAgent?.kind)
  const rawApproval = bot.default_approval_mode || 'manual'
  const approval = agyMember && rawApproval === 'auto' ? 'manual' : rawApproval
  const model =
    bot.default_provider && bot.default_model
      ? JSON.stringify([bot.default_provider, bot.default_model])
      : ''
  const effort = agyMember ? '' : (bot.default_effort || '')

  return {
    key: id,
    id,
    agentId,
    machineId,
    name: bot.name || bot.slug || agentId,
    alias: uniqueBotAlias(bot.name || bot.slug || 'Bot', existingAliases),
    role: buildBotRolePrompt(bot),
    workspace: (bot.workspace || '').trim(),
    model,
    effort,
    approval,
    sourceSessionId: '',
    forkSessionId: '',
    locked: false,
    forkUncertain: false,
    sourceBotId: bot.id,
  }
}
