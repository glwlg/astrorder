export type ManagedAgentKind = 'antigravity' | 'codex' | 'hermes' | 'grok'

export interface RelatedPluginLink {
  id: string
  label: string
}

/**
 * Single declarative frontend registry for native Agent engines.
 * Adding a new Agent engine starts by adding one entry here (see docs/ADDING_AN_AGENT.md).
 */
export interface AgentEnginePluginDescriptor {
  id: string
  kind: ManagedAgentKind
  aliases: string[]
  title: string
  shortLabel: string
  description: string
  searchKeywords: string[]
  relatedPlugins?: RelatedPluginLink[]
}

export const AGENT_ANTIGRAVITY_PLUGIN_ID = 'agent-antigravity'
export const AGENT_CODEX_PLUGIN_ID = 'agent-codex'
export const AGENT_HERMES_PLUGIN_ID = 'agent-hermes'
export const AGENT_GROK_PLUGIN_ID = 'agent-grok'

export const AGENT_ENGINE_REGISTRY: AgentEnginePluginDescriptor[] = [
  {
    id: AGENT_ANTIGRAVITY_PLUGIN_ID,
    kind: 'antigravity',
    aliases: ['antigravity-ssh', 'agy'],
    title: 'Google Antigravity (agy)',
    shortLabel: 'Antigravity',
    description: '接入 Google Antigravity 原生 CLI Agent，支持多账号配额池、原生 Swarm 观测与深度思考模式',
    searchKeywords: ['antigravity', 'agy', 'google', 'gemini', 'agent', 'swarm'],
    relatedPlugins: [
      { id: 'workspace-agent-accounts', label: 'Agent 多账号与配额池' },
    ],
  },
  {
    id: AGENT_CODEX_PLUGIN_ID,
    kind: 'codex',
    aliases: ['codex-ssh'],
    title: 'OpenAI Codex',
    shortLabel: 'Codex',
    description: '接入 OpenAI Codex CLI Agent，支持工作区审批、原生桌面观测与 Git Worktree 分叉协作',
    searchKeywords: ['codex', 'openai', 'gpt', 'agent', 'npm'],
  },
  {
    id: AGENT_HERMES_PLUGIN_ID,
    kind: 'hermes',
    aliases: ['ssh-hermes', 'hermes-ssh'],
    title: 'Hermes Agent',
    shortLabel: 'Hermes',
    description: '接入 NousResearch Hermes Agent，支持本机、WSL 与多台远程 SSH 主机协同',
    searchKeywords: ['hermes', 'nous', 'agent', 'ssh', 'uv', 'python'],
  },
  {
    id: AGENT_GROK_PLUGIN_ID,
    kind: 'grok',
    aliases: ['grok-ssh'],
    title: 'xAI Grok',
    shortLabel: 'Grok',
    description: '接入 xAI Grok 原生命令行 Agent，支持本机与远程 SSH 运行环境',
    searchKeywords: ['grok', 'xai', 'agent'],
  },
]

export const BUILTIN_AGENT_PLUGINS = AGENT_ENGINE_REGISTRY

export function lookupAgentEngine(kind?: string | null): AgentEnginePluginDescriptor | undefined {
  const normalized = (kind || '').trim().toLowerCase()
  if (!normalized) return undefined
  return AGENT_ENGINE_REGISTRY.find(
    (entry) => entry.kind === normalized || entry.aliases.includes(normalized),
  )
}

export function canonicalAgentKind(kind?: string | null): string {
  return lookupAgentEngine(kind)?.kind || (kind || '').trim().toLowerCase()
}

export function agentPluginIdForKind(kind?: string | null): string | null {
  return lookupAgentEngine(kind)?.id || null
}

export function isAgentKindEnabled(
  kind: string | null | undefined,
  enabledPlugins: Record<string, boolean>,
): boolean {
  const pluginId = agentPluginIdForKind(kind)
  if (!pluginId) return true
  return enabledPlugins[pluginId] !== false
}
