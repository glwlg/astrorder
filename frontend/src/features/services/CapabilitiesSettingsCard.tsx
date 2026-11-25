import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Drawer,
  Group,
  JsonInput,
  Modal,
  Paper,
  Progress,
  ScrollArea,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconChecklist,
  IconCode,
  IconDeviceDesktop,
  IconDownload,
  IconEdit,
  IconLayersLinked,
  IconListDetails,
  IconNetwork,
  IconPlug,
  IconPlus,
  IconRefresh,
  IconRotateClockwise,
  IconSearch,
  IconSend,
  IconServer,
  IconShieldLock,
  IconShoppingCart,
  IconSparkles,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import { api, type CapabilityDispatchJob } from '../../api/client'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import './SkillsHub.css'

export interface SkillItem {
  name: string
  display_name?: string
  path?: string
  scope?: string
  agents?: string[]
  installed_agents?: string[]
  source?: string
  sourceUrl?: string
  description?: string
  agent?: string
  category?: string
  content?: string
}

export const OFFICIAL_SKILL_TITLES: Record<string, string> = {
  'astrorder-sessions': '会话管理',
  'astrorder-browser': '浏览器控制',
  'astrorder-blackboard': '共享黑板',
  'astrorder-swarm': 'Swarm 协同',
  'astrorder-workspace': '工作区拓扑',
  'astrorder-monitor': '监控墙编组',
}

export const isOfficialSkill = (s?: SkillItem | null) => {
  if (!s) return false
  return s.source === 'astrorder' && s.category === 'astrorder' && s.scope === 'builtin'
}

export const isUninstalledOfficialSkill = (s?: SkillItem | null) => {
  if (!s || !isOfficialSkill(s)) return false
  return getSkillInstalledAgents(s).length === 0
}

export const getSkillInstalledAgents = (s?: SkillItem | null): string[] => {
  if (!s) return []
  const raw = isOfficialSkill(s)
    ? (s.installed_agents || [])
    : (s.installed_agents && s.installed_agents.length > 0 ? s.installed_agents : (s.agents || []))
  const seen = new Set<string>()
  const out: string[] = []
  for (const agent of raw) {
    const clean = String(agent || '').trim().toLowerCase()
    if (clean && !seen.has(clean)) {
      seen.add(clean)
      out.push(clean)
    }
  }
  return out
}

export const isSkillInstalledForView = (s: SkillItem, selectedAgent: string): boolean => {
  const installed = getSkillInstalledAgents(s)
  if (selectedAgent === 'all') return installed.length > 0
  return installed.includes(selectedAgent.toLowerCase())
}

export const getSkillDisplayTitle = (s: SkillItem): string => {
  if (isOfficialSkill(s)) {
    return s.display_name || OFFICIAL_SKILL_TITLES[s.name] || s.name
  }
  return s.name
}

export const getSkillKey = (s: SkillItem) => `${s.scope || 'installed'}:${s.name}:${s.path || ''}`

const AGENT_SHORT_LABELS: Record<string, string> = {
  codex: 'Codex',
  hermes: 'Hermes',
  grok: 'Grok',
  antigravity: 'Antigravity',
}

function shortAgentLabel(id: string, fallback?: string): string {
  return AGENT_SHORT_LABELS[id.toLowerCase()] || fallback || id
}

interface CapabilityTargetSummary {
  target_id: string
  target_name: string
  kind: string
  counts: { skills: number; mcp: number; plugins: number; marketplaces: number }
  skill_agents?: Array<{ id: string; label: string }>
  skills: SkillItem[]
  mcp_servers: Array<{
    name: string
    agent: string
    enabled: boolean
    transport: string
    url?: string
    command?: string
    args?: string[]
    headers?: Record<string, string>
    env?: Record<string, string>
    tools?: string[]
    source?: string
  }>
  plugins: Array<{ id: string; name: string; full_name?: string; agent: string; marketplace?: string; enabled: boolean; source?: string; config?: Record<string, any> }>
  marketplaces: Array<{ id: string; name: string; agent?: string; source_type: string; source: string; last_updated?: string; description?: string }>
}

interface DispatchDraftItem {
  item_id: string
  source_agent: string
  label: string
  installedAgents?: string[]
  raw?: any
}

function normalizeDispatchJob(raw: CapabilityDispatchJob): CapabilityDispatchJob {
  return {
    ...raw,
    items: Array.isArray(raw?.items) ? raw.items : [],
    target_matrix: Array.isArray(raw?.target_matrix) ? raw.target_matrix : [],
    reports: Array.isArray(raw?.reports) ? raw.reports : [],
  }
}

export function CapabilitiesSettingsCard() {
  const [targets, setTargets] = useState<Array<{ id: string; kind: string; name: string }>>([])
  const [selectedTargetId, setSelectedTargetId] = useState<string>('local')
  const [summary, setSummary] = useState<CapabilityTargetSummary | null>(null)
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState<'skills' | 'mcp' | 'plugins' | 'marketplaces'>('skills')

  // 单一顶层 Agent 视点过滤器
  const [selectedAgent, setSelectedAgent] = useState<string>('all')

  // 技能来源与安装状态过滤
  const [skillCategory, setSkillCategory] = useState<'all' | 'astrorder' | 'proprietary' | 'community' | 'memory'>('all')
  const [installFilter, setInstallFilter] = useState<'all' | 'installed' | 'partial' | 'uninstalled'>('all')
  const [skillSearch, setSkillSearch] = useState('')
  const [selectedSkill, setSelectedSkill] = useState<SkillItem | null>(null)
  const [checkedSkillKeys, setCheckedSkillKeys] = useState<string[]>([])

  // 检查器右侧 Tab（保留真正有内容的两个视图）
  const [inspectorTab, setInspectorTab] = useState<'spec' | 'install'>('spec')

  // 全网市场搜索
  const [marketQuery, setMarketQuery] = useState('')
  const [searchingMarket, setSearchingMarket] = useState(false)
  const [marketResults, setMarketResults] = useState<Array<{ pkg: string; name: string; repo: string; installs: string; url: string }>>([])

  // 手动导入包弹窗
  const [installModalOpen, setInstallModalOpen] = useState(false)
  const [customPackage, setCustomPackage] = useState('')
  const [installTargets, setInstallTargets] = useState<string[]>([])
  const [installing, setInstalling] = useState(false)

  // MCP 新增/编辑 Modal
  const [mcpModalOpen, setMcpModalOpen] = useState(false)
  const [mcpEditing, setMcpEditing] = useState(false)
  const [mcpFormTarget, setMcpFormTarget] = useState<string>('local')
  const [mcpFormAgent, setMcpFormAgent] = useState<string>('codex')
  const [mcpFormName, setMcpFormName] = useState<string>('')
  const [mcpFormTransport, setMcpFormTransport] = useState<'sse/http' | 'stdio'>('sse/http')
  const [mcpFormUrl, setMcpFormUrl] = useState<string>('')
  const [mcpFormCommand, setMcpFormCommand] = useState<string>('')
  const [mcpFormArgs, setMcpFormArgs] = useState<string>('')
  const [mcpFormHeaders, setMcpFormHeaders] = useState<string>('{}')
  const [mcpFormEnv, setMcpFormEnv] = useState<string>('{}')
  const [mcpFormEnabled, setMcpFormEnabled] = useState<boolean>(true)
  const [mcpSaving, setMcpSaving] = useState(false)
  const [mcpPingStatus, setMcpPingStatus] = useState<Record<string, { loading?: boolean; text?: string; ok?: boolean }>>({})

  // Antigravity 插件安装/导入 Modal
  const [pluginInstallOpen, setPluginInstallOpen] = useState(false)
  const [pluginInstallMode, setPluginInstallMode] = useState<'install' | 'import'>('install')
  const [pluginInstallSource, setPluginInstallSource] = useState<string>('')
  const [pluginImportSource, setPluginImportSource] = useState<string>('claude')
  const [pluginInstalling, setPluginInstalling] = useState(false)

  // 市场源新增 Modal
  const [marketModalOpen, setMarketModalOpen] = useState(false)
  const [marketFormAgent, setMarketFormAgent] = useState<string>('codex')
  const [marketFormName, setMarketFormName] = useState<string>('')
  const [marketFormType, setMarketFormType] = useState<string>('git')
  const [marketFormSource, setMarketFormSource] = useState<string>('')
  const [marketSaving, setMarketSaving] = useState(false)

  // 跨端/跨 Agent 批量后台分发模态框 + 后台任务队列
  const [dispatchModalOpen, setDispatchModalOpen] = useState(false)
  const [dispatchType, setDispatchType] = useState<'skill' | 'mcp' | 'plugin'>('skill')
  const [dispatchItems, setDispatchItems] = useState<DispatchDraftItem[]>([])
  const [dispatchMatrix, setDispatchMatrix] = useState<Array<{ target_id: string; agent: string }>>([])
  const [submittingJob, setSubmittingJob] = useState(false)

  const [dispatchJobs, setDispatchJobs] = useState<CapabilityDispatchJob[]>([])
  const [jobsDrawerOpen, setJobsDrawerOpen] = useState(false)
  const prevJobStatusesRef = useRef<Map<string, string>>(new Map())

  const reqIdRef = useRef(0)

  const loadTargets = async () => {
    try {
      const res = await api.getCapabilitiesTargets()
      setTargets(res)
      if (res.length > 0 && !res.some((t) => t.id === selectedTargetId)) {
        setSelectedTargetId(res[0].id)
      }
    } catch (err: any) {
      notifications.show({ title: '加载目标环境失败', message: err?.message || '请检查后端服务', color: 'red' })
    }
  }

  const loadTargetCapabilities = useCallback(async (targetId: string) => {
    const curReqId = ++reqIdRef.current
    setLoading(true)
    setSummary((prev) => (prev?.target_id === targetId ? prev : null))
    try {
      const data = await api.getCapabilitiesSummary(targetId)
      if (curReqId !== reqIdRef.current) return
      setSummary(data)
    } catch (err: any) {
      if (curReqId !== reqIdRef.current) return
      notifications.show({ title: '加载能力中心数据失败', message: err?.message || '请检查后端响应', color: 'red' })
    } finally {
      if (curReqId === reqIdRef.current) {
        setLoading(false)
      }
    }
  }, [])

  const loadDispatchJobs = useCallback(async () => {
    if (typeof api.listCapabilityDispatchJobs !== 'function') return
    try {
      const res = await api.listCapabilityDispatchJobs()
      const nextJobs = (Array.isArray(res?.jobs) ? res.jobs : []).map(normalizeDispatchJob)
      setDispatchJobs(nextJobs)

      let anyJustFinished = false
      const prevMap = prevJobStatusesRef.current
      for (const job of nextJobs) {
        const prevStatus = prevMap.get(job.id)
        if (prevStatus === 'running' && job.status !== 'running') {
          anyJustFinished = true
          const summaryTitle = job.items.length > 1 ? `${job.items[0]?.label || job.items[0]?.item_id} 等 ${job.items.length} 项` : (job.items[0]?.label || job.items[0]?.item_id || '能力')
          notifications.show({
            title: job.status === 'completed' ? '后台分发已完成' : job.status === 'partial' ? '后台分发部分成功' : '后台分发失败',
            message: `${summaryTitle} · 成功 ${job.success_count}/${job.total_steps}`,
            color: job.status === 'completed' ? 'teal' : job.status === 'partial' ? 'yellow' : 'red',
          })
        }
        prevMap.set(job.id, job.status)
      }
      if (anyJustFinished && selectedTargetId) {
        void loadTargetCapabilities(selectedTargetId)
      }
    } catch {
      // Ignore transient poll errors; next tick will recover.
    }
  }, [loadTargetCapabilities, selectedTargetId])

  useEffect(() => {
    void loadTargets()
    void loadDispatchJobs()
  }, [])

  useEffect(() => {
    if (selectedTargetId) {
      setCheckedSkillKeys([])
      void loadTargetCapabilities(selectedTargetId)
    }
  }, [loadTargetCapabilities, selectedTargetId])

  const runningJobs = useMemo(
    () => dispatchJobs.filter((j) => j.status === 'running'),
    [dispatchJobs],
  )
  const hasRunningJobs = runningJobs.length > 0

  useEffect(() => {
    if (!hasRunningJobs) return
    const timer = window.setInterval(() => {
      void loadDispatchJobs()
    }, 1200)
    return () => window.clearInterval(timer)
  }, [hasRunningJobs, loadDispatchJobs])

  useEffect(() => {
    if (activeTab === 'marketplaces' && marketResults.length === 0 && !searchingMarket) {
      void handleMarketSearch(marketQuery || 'agent')
    }
  }, [activeTab])

  const handleMarketSearch = async (queryText?: string) => {
    const q = (queryText !== undefined ? queryText : marketQuery).trim()
    if (!q) {
      notifications.show({ title: '提示', message: '请输入要搜索的关键词', color: 'yellow' })
      return
    }
    setSearchingMarket(true)
    try {
      const res = await api.searchMarketCapabilities(q)
      setMarketResults(res.items || [])
      if (!res.items || res.items.length === 0) {
        notifications.show({ title: '无结果', message: `未在市场源中找到与 "${q}" 相关的内容`, color: 'blue' })
      }
    } catch (err: any) {
      notifications.show({ title: '市场搜索失败', message: err?.message || '网络连接超时', color: 'red' })
    } finally {
      setSearchingMarket(false)
    }
  }

  // ==========================================
  // MCP 业务处理
  // ==========================================
  const mcpKey = (srv: { agent: string; name: string }) => `${srv.agent}::${srv.name}`
  const isPluginManagedSource = (source?: string) => Boolean(source && source.startsWith('antigravity-plugin:'))
  const pluginSourceName = (source?: string) => (source && source.startsWith('antigravity-plugin:') ? source.slice('antigravity-plugin:'.length) : '')
  const agentBadgeColor = (agent: string) =>
    agent === 'codex' ? 'indigo' : agent === 'hermes' ? 'orange' : agent === 'antigravity' ? 'violet' : 'teal'

  const handleOpenAddMcp = () => {
    setMcpEditing(false)
    setMcpFormTarget(selectedTargetId)
    setMcpFormAgent(selectedAgent !== 'all' ? selectedAgent : 'codex')
    setMcpFormName('')
    setMcpFormTransport('sse/http')
    setMcpFormUrl('')
    setMcpFormCommand('')
    setMcpFormArgs('')
    setMcpFormHeaders('{}')
    setMcpFormEnv('{}')
    setMcpFormEnabled(true)
    setMcpModalOpen(true)
  }

  const handleOpenEditMcp = (srv: CapabilityTargetSummary['mcp_servers'][number]) => {
    setMcpEditing(true)
    setMcpFormTarget(selectedTargetId)
    setMcpFormAgent(srv.agent || 'codex')
    setMcpFormName(srv.name)
    setMcpFormTransport(srv.transport === 'stdio' ? 'stdio' : 'sse/http')
    setMcpFormUrl(srv.url || '')
    setMcpFormCommand(srv.command || '')
    setMcpFormArgs((srv.args || []).join(' '))
    setMcpFormHeaders(JSON.stringify(srv.headers || {}, null, 2))
    setMcpFormEnv(JSON.stringify(srv.env || {}, null, 2))
    setMcpFormEnabled(srv.enabled)
    setMcpModalOpen(true)
  }

  const handleSaveMcp = async () => {
    if (!mcpFormName.trim()) {
      notifications.show({ title: '校验失败', message: 'MCP 服务名称不能为空', color: 'red' })
      return
    }
    let parsedEnv: Record<string, string> = {}
    let parsedHeaders: Record<string, string> = {}
    try {
      parsedEnv = JSON.parse(mcpFormEnv || '{}')
    } catch {
      notifications.show({ title: 'JSON 格式错误', message: '环境变量必须为有效的 JSON 对象', color: 'red' })
      return
    }
    try {
      parsedHeaders = JSON.parse(mcpFormHeaders || '{}')
    } catch {
      notifications.show({ title: 'JSON 格式错误', message: 'HTTP Headers 必须为有效的 JSON 对象', color: 'red' })
      return
    }

    setMcpSaving(true)
    try {
      await api.updateMcpServer({
        target_id: mcpFormTarget,
        agent: mcpFormAgent,
        action: 'save',
        server: {
          name: mcpFormName.trim(),
          transport: mcpFormTransport,
          url: mcpFormTransport === 'sse/http' ? mcpFormUrl.trim() : undefined,
          command: mcpFormTransport === 'stdio' ? mcpFormCommand.trim() : undefined,
          args: mcpFormTransport === 'stdio' && mcpFormArgs ? mcpFormArgs.split(' ').filter(Boolean) : undefined,
          enabled: mcpFormEnabled,
          headers: mcpFormTransport === 'sse/http' ? parsedHeaders : undefined,
          env: parsedEnv,
        },
      })
      notifications.show({ title: '保存成功', message: `MCP 服务 [${mcpFormName}] 已写入 ${mcpFormAgent} 配置`, color: 'green' })
      setMcpModalOpen(false)
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '保存失败', message: err?.message || '配置更新被拒绝', color: 'red' })
    } finally {
      setMcpSaving(false)
    }
  }

  const handleToggleMcp = async (srv: CapabilityTargetSummary['mcp_servers'][number]) => {
    const nextState = !srv.enabled
    try {
      await api.updateMcpServer({
        target_id: selectedTargetId,
        agent: srv.agent || 'codex',
        action: 'toggle',
        server: { name: srv.name, enabled: nextState },
      })
      notifications.show({
        title: nextState ? 'MCP 服务已启用' : 'MCP 服务已停用',
        message: `[${srv.name}] 状态已更新到 ${srv.agent} 配置`,
        color: 'teal',
      })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '状态更新失败', message: err?.message || '切换 MCP 状态失败', color: 'red' })
    }
  }

  const handleDeleteMcp = async (server: any) => {
    if (!confirm(`确定要从 ${server.agent || 'Agent'} 配置文件中彻底删除 MCP 服务 [${server.name}] 吗？`)) return
    try {
      await api.updateMcpServer({
        target_id: selectedTargetId,
        agent: server.agent || 'codex',
        action: 'delete',
        server: { name: server.name },
      })
      notifications.show({ title: '删除成功', message: `MCP 服务 [${server.name}] 已安全移除`, color: 'green' })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '删除失败', message: err?.message || '操作异常', color: 'red' })
    }
  }

  const handlePingMcp = async (server: any) => {
    const key = mcpKey(server)
    setMcpPingStatus((prev) => ({ ...prev, [key]: { loading: true } }))
    try {
      const res = await api.pingMcpServer({ target_id: selectedTargetId, server })
      setMcpPingStatus((prev) => ({
        ...prev,
        [key]: { loading: false, text: res.message || `${res.latency_ms}ms`, ok: res.ok },
      }))
      notifications.show({
        title: res.ok ? '探活成功' : '探活异常',
        message: res.message || `响应耗时: ${res.latency_ms}ms`,
        color: res.ok ? 'teal' : 'red',
      })
    } catch (err: any) {
      setMcpPingStatus((prev) => ({
        ...prev,
        [key]: { loading: false, text: '连接失败', ok: false },
      }))
      notifications.show({ title: '探活失败', message: err?.message || '网络连接超时', color: 'red' })
    }
  }

  // ==========================================
  // 插件业务处理
  // ==========================================
  const handleInstallOrImportAgyPlugin = async () => {
    const source = pluginInstallMode === 'install'
      ? pluginInstallSource.trim()
      : (pluginImportSource === 'auto' ? '' : pluginImportSource)
    if (pluginInstallMode === 'install' && !source) {
      notifications.show({ title: '校验失败', message: '请输入插件目录、.zip、URL 或 plugin@marketplace', color: 'red' })
      return
    }
    setPluginInstalling(true)
    try {
      const res = await api.updatePluginState({
        target_id: selectedTargetId,
        agent: 'antigravity',
        action: pluginInstallMode,
        source,
      })
      notifications.show({
        title: pluginInstallMode === 'install' ? '插件安装完成' : '插件导入完成',
        message: res.output || `已完成 Antigravity 插件${pluginInstallMode === 'install' ? '安装' : '导入'}`,
        color: 'green',
      })
      setPluginInstallOpen(false)
      setPluginInstallSource('')
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '操作失败', message: err?.message || 'agy plugin 执行失败', color: 'red' })
    } finally {
      setPluginInstalling(false)
    }
  }

  const handleTogglePlugin = async (plg: any) => {
    const nextState = !plg.enabled
    try {
      await api.updatePluginState({
        target_id: selectedTargetId,
        agent: plg.agent || 'codex',
        action: 'toggle',
        plugin_name: plg.full_name || plg.name,
        enabled: nextState,
      })
      notifications.show({
        title: nextState ? '插件已启用' : '插件已停用',
        message: `[${plg.name}] 状态已写回 ${plg.agent} 配置文件`,
        color: 'teal',
      })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '状态更新失败', message: err?.message || '写入被拒绝', color: 'red' })
    }
  }

  const handleUninstallPlugin = async (plg: any) => {
    if (!confirm(`确定要从 ${plg.agent} 配置中彻底卸载插件 [${plg.name}] 吗？`)) return
    try {
      await api.updatePluginState({
        target_id: selectedTargetId,
        agent: plg.agent || 'codex',
        action: 'uninstall',
        plugin_name: plg.full_name || plg.name,
      })
      notifications.show({ title: '卸载成功', message: `插件 [${plg.name}] 已彻底移除`, color: 'green' })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '卸载失败', message: err?.message || '操作失败', color: 'red' })
    }
  }

  // ==========================================
  // 市场源业务处理
  // ==========================================
  const handleOpenAddMarket = () => {
    setMarketFormAgent(selectedAgent !== 'all' ? selectedAgent : 'codex')
    setMarketFormName('')
    setMarketFormType('git')
    setMarketFormSource('https://github.com/DietrichGebert/ponytail.git')
    setMarketModalOpen(true)
  }

  const handleSaveMarket = async () => {
    if (!marketFormName.trim() || !marketFormSource.trim()) {
      notifications.show({ title: '校验失败', message: '市场名称与 Git/URL 不能为空', color: 'red' })
      return
    }
    setMarketSaving(true)
    try {
      await api.updateMarketplace({
        target_id: selectedTargetId,
        agent: marketFormAgent,
        action: 'add',
        market: {
          name: marketFormName.trim(),
          source_type: marketFormType,
          source: marketFormSource.trim(),
        },
      })
      notifications.show({ title: '订阅成功', message: `市场源 [${marketFormName}] 已写入 ${marketFormAgent} 配置文件`, color: 'green' })
      setMarketModalOpen(false)
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '订阅失败', message: err?.message || '配置更新错误', color: 'red' })
    } finally {
      setMarketSaving(false)
    }
  }

  const handlePullMarket = async (m: any) => {
    notifications.show({ title: '正在拉取', message: `正在从 ${m.source} 同步最新清单与包索引...`, color: 'blue' })
    try {
      await api.updateMarketplace({
        target_id: selectedTargetId,
        agent: m.agent || 'codex',
        action: 'pull',
        market: { name: m.name },
      })
      notifications.show({ title: '同步完成', message: `市场源 [${m.name}] 索引已刷新至最新版本`, color: 'green' })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '同步失败', message: err?.message || '网络拉取超时', color: 'red' })
    }
  }

  const handleDeleteMarket = async (m: any) => {
    if (!confirm(`确定要从 ${m.agent} 中删除订阅源 [${m.name}] 吗？`)) return
    try {
      await api.updateMarketplace({
        target_id: selectedTargetId,
        agent: m.agent || 'codex',
        action: 'delete',
        market: { name: m.name },
      })
      notifications.show({ title: '删除成功', message: `市场源 [${m.name}] 已移除`, color: 'green' })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '删除失败', message: err?.message || '操作失败', color: 'red' })
    }
  }

  // ==========================================
  // 真实跨端/跨 Agent 批量后台分发
  // ==========================================
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const isAgentEnabled = usePluginSettingsStore((s) => s.isAgentEnabled)
  void enabledPlugins
  const availableSkillAgents = (summary?.skill_agents || []).filter((ag) => isAgentEnabled(ag.id))
  const allSkills = summary?.skills || []

  const openSkillDispatchModal = (skillsToDispatch: SkillItem[], preselectMissingOnly = false) => {
    const drafts: DispatchDraftItem[] = skillsToDispatch.map((sk) => ({
      item_id: sk.name,
      source_agent: isOfficialSkill(sk) ? 'astrorder' : (sk.agent || 'all'),
      label: getSkillDisplayTitle(sk),
      installedAgents: getSkillInstalledAgents(sk),
      raw: sk,
    }))
    setDispatchType('skill')
    setDispatchItems(drafts)
    if (preselectMissingOnly && drafts.length === 1) {
      const installedSet = new Set(drafts[0].installedAgents || [])
      const missingOnCurrent = availableSkillAgents
        .filter((ag) => !installedSet.has(ag.id.toLowerCase()))
        .map((ag) => ({ target_id: selectedTargetId, agent: ag.id }))
      setDispatchMatrix(missingOnCurrent)
    } else if (drafts.length > 1 && selectedAgent !== 'all') {
      setDispatchMatrix([{ target_id: selectedTargetId, agent: selectedAgent }])
    } else {
      setDispatchMatrix([])
    }
    setDispatchModalOpen(true)
  }

  const handleOpenSingleDispatchModal = (type: 'skill' | 'mcp' | 'plugin', item: any) => {
    if (type === 'skill') {
      openSkillDispatchModal([item], false)
      return
    }
    setDispatchType(type)
    setDispatchItems([
      {
        item_id: item.name || item.id,
        source_agent: item.agent || 'all',
        label: item.name || item.id,
        raw: item,
      },
    ])
    setDispatchMatrix([])
    setDispatchModalOpen(true)
  }

  const handleStartBackgroundDispatch = async () => {
    if (dispatchItems.length === 0 || dispatchMatrix.length === 0) {
      notifications.show({ title: '提示', message: '请至少保留一个待分发项并勾选一个目标节点与 Agent', color: 'yellow' })
      return
    }
    setSubmittingJob(true)
    try {
      if (typeof api.startCapabilityDispatchJob === 'function') {
        const rawJob = await api.startCapabilityDispatchJob({
          capability_type: dispatchType,
          source_target_id: selectedTargetId,
          items: dispatchItems.map(({ item_id, source_agent, label }) => ({
            item_id,
            source_agent,
            label,
          })),
          target_matrix: dispatchMatrix,
        })
        const job = normalizeDispatchJob(rawJob)
        prevJobStatusesRef.current.set(job.id, job.status)
        setDispatchJobs((prev) => [job, ...prev.filter((existing) => existing.id !== job.id)])
        setDispatchModalOpen(false)
        setCheckedSkillKeys([])
        notifications.show({
          title: '已提交后台分发任务',
          message: `正在后台分发 ${job.items.length} 项能力到 ${job.target_matrix.length} 个目标（共 ${job.total_steps} 步），可继续其他操作`,
          color: 'indigo',
        })
      } else {
        // Fallback for older test mocks that only stub synchronous dispatchCapability.
        let totalOk = 0
        let totalSteps = 0
        for (const draft of dispatchItems) {
          const res = await api.dispatchCapability({
            capability_type: dispatchType,
            source_target_id: selectedTargetId,
            source_agent: draft.source_agent,
            item_id: draft.item_id,
            target_matrix: dispatchMatrix,
            item_data: draft.raw,
          })
          totalOk += res.success_count
          totalSteps += res.total_targets
        }
        setDispatchModalOpen(false)
        setCheckedSkillKeys([])
        if (totalOk > 0) void loadTargetCapabilities(selectedTargetId)
        notifications.show({
          title: totalOk === totalSteps ? '分发完成' : '分发结果',
          message: `已完成 ${totalOk}/${totalSteps} 个目标`,
          color: totalOk === totalSteps ? 'teal' : 'yellow',
        })
      }
    } catch (err: any) {
      notifications.show({ title: '提交后台分发失败', message: err?.message || '通信异常', color: 'red' })
    } finally {
      setSubmittingJob(false)
    }
  }

  const handleRetryFailedJob = async (job: CapabilityDispatchJob) => {
    const failedReports = (job.reports || []).filter((r) => !r.ok)
    if (failedReports.length === 0) return
    const itemMap = new Map((job.items || []).map((it) => [it.item_id, it]))
    const retryItemsMap = new Map<string, { item_id: string; source_agent: string; label?: string }>()
    const retryTargetsMap = new Map<string, { target_id: string; agent: string }>()
    for (const r of failedReports) {
      const orig = itemMap.get(r.item_id)
      retryItemsMap.set(r.item_id, {
        item_id: r.item_id,
        source_agent: orig?.source_agent || 'all',
        label: orig?.label || r.item_label || r.item_id,
      })
      retryTargetsMap.set(`${r.target_id}::${r.agent}`, { target_id: r.target_id, agent: r.agent })
    }
    try {
      const rawNewJob = await api.startCapabilityDispatchJob({
        capability_type: (job.capability_type as 'skill' | 'mcp' | 'plugin') || 'skill',
        source_target_id: job.source_target_id || selectedTargetId,
        items: Array.from(retryItemsMap.values()),
        target_matrix: Array.from(retryTargetsMap.values()),
      })
      const newJob = normalizeDispatchJob(rawNewJob)
      prevJobStatusesRef.current.set(newJob.id, newJob.status)
      setDispatchJobs((prev) => [newJob, ...prev.filter((j) => j.id !== newJob.id)])
      notifications.show({
        title: '已重新提交失败项',
        message: `已将 ${newJob.items.length} 项能力放入后台重试`,
        color: 'indigo',
      })
    } catch (err: any) {
      notifications.show({ title: '重试提交失败', message: err?.message || '请稍后再试', color: 'red' })
    }
  }

  const handleClearFinishedJobs = async () => {
    if (typeof api.clearCapabilityDispatchJobs !== 'function') return
    try {
      const res = await api.clearCapabilityDispatchJobs()
      setDispatchJobs((Array.isArray(res?.jobs) ? res.jobs : []).map(normalizeDispatchJob))
    } catch (err: any) {
      notifications.show({ title: '清理失败', message: err?.message || '请稍后再试', color: 'red' })
    }
  }

  // 安装 / 卸载单个技能
  const handleInstall = async (pkgSource?: string) => {
    const targetPkg = (pkgSource || customPackage).trim()
    if (!targetPkg) {
      notifications.show({ title: '参数错误', message: '安装源不能为空', color: 'red' })
      return
    }
    const finalTargets = installTargets.length > 0 ? installTargets : [selectedTargetId]
    setInstalling(true)
    try {
      const res = await api.installSkill({ target_ids: finalTargets, package_source: targetPkg })
      const successCount = res.results.filter((r) => r.success).length
      if (successCount > 0) {
        notifications.show({ title: '能力安装完成', message: `已成功分发并安装到 ${successCount} 个目标环境`, color: 'green' })
        setInstallModalOpen(false)
        setCustomPackage('')
        void loadTargetCapabilities(selectedTargetId)
      } else {
        const errorMsg = res.results.map((r) => `${r.target_id}: ${r.error || r.output}`).join('\n')
        notifications.show({ title: '安装未成功', message: errorMsg || '安装进程报错', color: 'red' })
      }
    } catch (err: any) {
      notifications.show({ title: '安装失败', message: err?.message || '调用异常', color: 'red' })
    } finally {
      setInstalling(false)
    }
  }

  const handleRemoveSkill = async (skillName: string) => {
    if (!confirm(`确定要从 [${summary?.target_name || selectedTargetId}] 环境中卸载此能力 [${skillName}] 吗？`)) return
    try {
      await api.removeSkill({ target_id: selectedTargetId, skill_name: skillName })
      notifications.show({ title: '已卸载', message: `能力 [${skillName}] 已成功移除`, color: 'green' })
      void loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '卸载失败', message: err?.message || '操作异常', color: 'red' })
    }
  }

  // 技能分类
  const astrorderSkills = useMemo(() => allSkills.filter(isOfficialSkill), [allSkills])
  const proprietarySkills = useMemo(
    () =>
      allSkills.filter(
        (s) =>
          !isOfficialSkill(s) &&
          (s.source?.includes('native') ||
            s.name.includes('liuruyan') ||
            s.name.includes('photo') ||
            s.name.includes('image') ||
            s.name.includes('damo') ||
            s.name.includes('temu')),
      ),
    [allSkills],
  )
  const memorySkills = useMemo(
    () =>
      allSkills.filter(
        (s) => !isOfficialSkill(s) && (s.path?.includes('openviking') || s.name.includes('context') || s.name.includes('viking')),
      ),
    [allSkills],
  )
  const communitySkills = useMemo(
    () => allSkills.filter((s) => !isOfficialSkill(s) && !proprietarySkills.includes(s) && !memorySkills.includes(s)),
    [allSkills, memorySkills, proprietarySkills],
  )

  const categoryFilteredSkills = useMemo(() => {
    return allSkills.filter((sk) => {
      if (skillCategory === 'astrorder' && !isOfficialSkill(sk)) return false
      if (skillCategory === 'proprietary' && !proprietarySkills.includes(sk)) return false
      if (skillCategory === 'community' && !communitySkills.includes(sk)) return false
      if (skillCategory === 'memory' && !memorySkills.includes(sk)) return false
      return true
    })
  }, [allSkills, communitySkills, memorySkills, proprietarySkills, skillCategory])

  // 先按 Agent 视点收敛作用域：选中具体 Agent 时保留该 Agent 已装技能 + 星序官方技能（便于一眼看出官方技能是否已装）
  const agentScopedSkills = useMemo(() => {
    if (selectedAgent === 'all') return categoryFilteredSkills
    return categoryFilteredSkills.filter(
      (sk) => isOfficialSkill(sk) || isSkillInstalledForView(sk, selectedAgent),
    )
  }, [categoryFilteredSkills, selectedAgent])

  const isSkillPartiallyInstalled = useCallback(
    (sk: SkillItem) => {
      if (selectedAgent !== 'all' || availableSkillAgents.length <= 1) return false
      const installedCount = getSkillInstalledAgents(sk).length
      return installedCount > 0 && installedCount < availableSkillAgents.length
    },
    [availableSkillAgents.length, selectedAgent],
  )

  const isSkillNeedsInstallForView = useCallback(
    (sk: SkillItem) => {
      if (selectedAgent !== 'all') return !isSkillInstalledForView(sk, selectedAgent)
      const installedCount = getSkillInstalledAgents(sk).length
      if (installedCount === 0) return true
      return availableSkillAgents.length > 0 && installedCount < availableSkillAgents.length
    },
    [availableSkillAgents.length, selectedAgent],
  )

  // 在当前来源分类 + Agent 视点下，分别统计已安装、待补齐与未安装数量
  const installedCountForView = useMemo(
    () => agentScopedSkills.filter((sk) => isSkillInstalledForView(sk, selectedAgent)).length,
    [agentScopedSkills, selectedAgent],
  )
  const partialCountForView = useMemo(
    () => agentScopedSkills.filter(isSkillPartiallyInstalled).length,
    [agentScopedSkills, isSkillPartiallyInstalled],
  )
  const uninstalledCountForView = agentScopedSkills.length - installedCountForView

  const filteredSkills = useMemo(() => {
    return agentScopedSkills.filter((sk) => {
      const installedForView = isSkillInstalledForView(sk, selectedAgent)
      if (installFilter === 'installed' && !installedForView) return false
      if (installFilter === 'partial' && !isSkillPartiallyInstalled(sk)) return false
      if (installFilter === 'uninstalled' && installedForView) return false

      if (skillSearch) {
        const q = skillSearch.toLowerCase()
        const title = getSkillDisplayTitle(sk)
        const matchTitle = title.toLowerCase().includes(q)
        const matchName = sk.name.toLowerCase().includes(q)
        const matchDesc = sk.description && sk.description.toLowerCase().includes(q)
        if (!matchTitle && !matchName && !matchDesc) return false
      }
      return true
    })
  }, [agentScopedSkills, installFilter, isSkillPartiallyInstalled, selectedAgent, skillSearch])

  const selectedSkillKey = selectedSkill ? getSkillKey(selectedSkill) : null
  const activeSkill = filteredSkills.find((sk) => getSkillKey(sk) === selectedSkillKey) || filteredSkills[0] || null

  const checkedSkillSet = useMemo(() => new Set(checkedSkillKeys), [checkedSkillKeys])
  const checkedSkills = useMemo(
    () => allSkills.filter((sk) => checkedSkillSet.has(getSkillKey(sk))),
    [allSkills, checkedSkillSet],
  )

  const toggleSkillChecked = (key: string) => {
    setCheckedSkillKeys((prev) =>
      prev.includes(key) ? prev.filter((item) => item !== key) : [...prev, key],
    )
  }

  const filteredMcpServers = (summary?.mcp_servers || []).filter(
    (srv) => selectedAgent === 'all' || srv.agent.toLowerCase() === selectedAgent.toLowerCase(),
  )
  const filteredPlugins = (summary?.plugins || []).filter(
    (plg) => selectedAgent === 'all' || plg.agent.toLowerCase() === selectedAgent.toLowerCase(),
  )
  const filteredMarketplaces = (summary?.marketplaces || []).filter(
    (m) => selectedAgent === 'all' || !m.agent || m.agent === 'all' || m.agent.toLowerCase() === selectedAgent.toLowerCase(),
  )

  const activeAgentLabel =
    selectedAgent === 'all'
      ? '全部 Agent'
      : shortAgentLabel(
          selectedAgent,
          availableSkillAgents.find((a) => a.id === selectedAgent)?.label,
        )

  const primaryRunningJob = runningJobs[0] || null
  const latestJobStatus = primaryRunningJob
    ? 'running'
    : dispatchJobs[0]?.status || ''

  // 计算分发矩阵中的已安装提示（仅针对当前已加载的节点）
  const singleDraftInstalledSet = useMemo(() => {
    if (dispatchType !== 'skill' || dispatchItems.length !== 1) return null
    return new Set(dispatchItems[0].installedAgents || [])
  }, [dispatchItems, dispatchType])

  const totalDispatchSteps = dispatchItems.length * dispatchMatrix.length

  return (
    <div className="capabilities-hub-container">
      {/* 精简、清晰的双行顶栏（消除重复控件） */}
      <div className="capabilities-topbar">
        <div className="capabilities-topbar-row1">
          <Group gap="sm" wrap="nowrap">
            <Text fw={700} size="md" style={{ letterSpacing: '-0.02em', color: 'var(--cap-text-primary)' }}>
              能力分发中心
            </Text>
            <div className="cap-node-switcher" role="group" aria-label="托管节点切换">
              {targets.map((tgt) => {
                const isSelected = tgt.id === selectedTargetId
                return (
                  <button
                    key={tgt.id}
                    type="button"
                    className={`cap-node-btn${isSelected ? ' active' : ''}`}
                    onClick={() => setSelectedTargetId(tgt.id)}
                  >
                    {tgt.kind === 'local' ? <IconDeviceDesktop size={13} /> : <IconServer size={13} />}
                    <span>{tgt.name}</span>
                  </button>
                )
              })}
            </div>
          </Group>

          <Group gap="xs">
            <button
              type="button"
              className={`cap-jobs-trigger${hasRunningJobs ? ' is-running' : ''}`}
              onClick={() => setJobsDrawerOpen(true)}
            >
              <span className={`cap-jobs-dot${latestJobStatus ? ` is-${latestJobStatus}` : ''}`} aria-hidden="true" />
              <IconListDetails size={14} aria-hidden="true" />
              <span>
                {primaryRunningJob
                  ? `分发中 ${primaryRunningJob.completed_steps}/${primaryRunningJob.total_steps}`
                  : dispatchJobs.length > 0
                    ? `后台任务 (${dispatchJobs.length})`
                    : '后台任务'}
              </span>
            </button>

            <Button
              size="xs"
              variant="default"
              leftSection={<IconRefresh size={14} className={loading ? 'animate-spin' : ''} />}
              onClick={() => void loadTargetCapabilities(selectedTargetId)}
            >
              刷新
            </Button>

            <Button
              size="xs"
              color="indigo"
              leftSection={<IconPlus size={14} />}
              onClick={() => {
                if (activeTab === 'mcp') handleOpenAddMcp()
                else if (activeTab === 'plugins') {
                  setPluginInstallMode('install')
                  setPluginInstallOpen(true)
                } else if (activeTab === 'marketplaces') handleOpenAddMarket()
                else {
                  setInstallTargets([selectedTargetId])
                  setInstallModalOpen(true)
                }
              }}
            >
              {activeTab === 'mcp'
                ? '新建 MCP'
                : activeTab === 'plugins'
                  ? '安装插件'
                  : activeTab === 'marketplaces'
                    ? '新增市场源'
                    : '导入技能'}
            </Button>
          </Group>
        </div>

        <div className="capabilities-topbar-row2">
          <div className="capabilities-nav-tabs">
            <button
              type="button"
              className={`capabilities-nav-btn tab-skills ${activeTab === 'skills' ? 'active' : ''}`}
              onClick={() => setActiveTab('skills')}
            >
              <IconCode size={15} />
              <span>技能</span>
              <span className="cap-tab-count">{filteredSkills.length}</span>
            </button>

            <button
              type="button"
              className={`capabilities-nav-btn tab-mcp ${activeTab === 'mcp' ? 'active' : ''}`}
              onClick={() => setActiveTab('mcp')}
            >
              <IconLayersLinked size={15} />
              <span>MCP 服务器</span>
              <span className="cap-tab-count">{filteredMcpServers.length}</span>
            </button>

            <button
              type="button"
              className={`capabilities-nav-btn tab-plugins ${activeTab === 'plugins' ? 'active' : ''}`}
              onClick={() => setActiveTab('plugins')}
            >
              <IconPlug size={15} />
              <span>复合插件</span>
              <span className="cap-tab-count">{filteredPlugins.length}</span>
            </button>

            <button
              type="button"
              className={`capabilities-nav-btn tab-market ${activeTab === 'marketplaces' ? 'active' : ''}`}
              onClick={() => setActiveTab('marketplaces')}
            >
              <IconShoppingCart size={15} />
              <span>能力市场</span>
              <span className="cap-tab-count">{filteredMarketplaces.length}</span>
            </button>
          </div>

          <div className="cap-agent-view-pills" role="group" aria-label="Agent 架构视点">
            <Text size="xs" c="dimmed" fw={600} mr={2}>Agent 视点:</Text>
            {[{ id: 'all', label: '全部 Agent' }, ...availableSkillAgents].map((ag) => (
              <button
                key={ag.id}
                type="button"
                className={`cap-agent-pill${selectedAgent === ag.id ? ' active' : ''}`}
                onClick={() => {
                  setSelectedAgent(ag.id)
                  if (ag.id !== 'all' && installFilter === 'partial') {
                    setInstallFilter('all')
                  }
                }}
              >
                {ag.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 三栏工作台主体 */}
      <div className="capabilities-workbench">
        {/* ===================== TAB 1: 技能工作台 ===================== */}
        {activeTab === 'skills' && (
          <>
            {/* 左侧筛选栏：安装状态 + 能力来源（彻底移除重复的 Agent 过滤列表） */}
            <div className="capabilities-filter-rail">
              <div className="filter-section-title">安装状态 · {activeAgentLabel}</div>
              <button
                type="button"
                className={`filter-item-btn ${installFilter === 'all' ? 'active' : ''}`}
                onClick={() => setInstallFilter('all')}
              >
                <span>全部状态</span>
                <span className="cap-filter-count">{agentScopedSkills.length}</span>
              </button>
              <button
                type="button"
                className={`filter-item-btn ${installFilter === 'installed' ? 'active' : ''}`}
                onClick={() => setInstallFilter('installed')}
              >
                <Group gap={7}>
                  <span className="cap-status-dot is-installed" aria-hidden="true" />
                  <span>已安装</span>
                </Group>
                <span className="cap-filter-count is-installed">{installedCountForView}</span>
              </button>
              {selectedAgent === 'all' && partialCountForView > 0 && (
                <button
                  type="button"
                  className={`filter-item-btn ${installFilter === 'partial' ? 'active' : ''}`}
                  onClick={() => setInstallFilter('partial')}
                >
                  <Group gap={7}>
                    <span className="cap-status-dot is-partial" aria-hidden="true" />
                    <span>待补齐</span>
                  </Group>
                  <span className="cap-filter-count">{partialCountForView}</span>
                </button>
              )}
              <button
                type="button"
                className={`filter-item-btn ${installFilter === 'uninstalled' ? 'active' : ''}`}
                onClick={() => setInstallFilter('uninstalled')}
              >
                <Group gap={7}>
                  <span className="cap-status-dot is-uninstalled" aria-hidden="true" />
                  <span>未安装</span>
                </Group>
                <span className="cap-filter-count is-uninstalled">{uninstalledCountForView}</span>
              </button>

              <div className="filter-section-title" style={{ marginTop: 18 }}>能力来源</div>
              <button
                type="button"
                className={`filter-item-btn ${skillCategory === 'all' ? 'active' : ''}`}
                onClick={() => setSkillCategory('all')}
              >
                <span>全部技能</span>
                <span className="cap-filter-count">{allSkills.length}</span>
              </button>
              <button
                type="button"
                className={`filter-item-btn ${skillCategory === 'astrorder' ? 'active' : ''}`}
                onClick={() => setSkillCategory('astrorder')}
              >
                <Group gap={6}><IconCode size={14} /><span>星序技能</span></Group>
                <span className="cap-filter-count">{astrorderSkills.length}</span>
              </button>
              <button
                type="button"
                className={`filter-item-btn ${skillCategory === 'proprietary' ? 'active' : ''}`}
                onClick={() => setSkillCategory('proprietary')}
              >
                <Group gap={6}><IconShieldLock size={14} color="var(--cap-amber)" /><span>私有业务</span></Group>
                <span className="cap-filter-count">{proprietarySkills.length}</span>
              </button>
              <button
                type="button"
                className={`filter-item-btn ${skillCategory === 'community' ? 'active' : ''}`}
                onClick={() => setSkillCategory('community')}
              >
                <span>社区开源</span>
                <span className="cap-filter-count">{communitySkills.length}</span>
              </button>
              <button
                type="button"
                className={`filter-item-btn ${skillCategory === 'memory' ? 'active' : ''}`}
                onClick={() => setSkillCategory('memory')}
              >
                <Group gap={6}><IconSparkles size={14} color="var(--cap-cyan)" /><span>OpenViking</span></Group>
                <span className="cap-filter-count">{memorySkills.length}</span>
              </button>
            </div>

            {/* 中间栏：可多选技能列表 + 批量分发操作栏 */}
            <div className="capabilities-list-pane">
              <div className="cap-list-toolbar">
                <TextInput
                  placeholder={`在 ${filteredSkills.length} 个技能中检索名称或描述...`}
                  value={skillSearch}
                  onChange={(e) => setSkillSearch(e.target.value)}
                  leftSection={<IconSearch size={14} />}
                  size="xs"
                />

                <div className="cap-select-bar">
                  <Group gap={6}>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      leftSection={<IconChecklist size={13} />}
                      disabled={filteredSkills.length === 0}
                      onClick={() => setCheckedSkillKeys(filteredSkills.map(getSkillKey))}
                    >
                      全选当前 ({filteredSkills.length})
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      disabled={filteredSkills.every((sk) => !isSkillNeedsInstallForView(sk))}
                      onClick={() =>
                        setCheckedSkillKeys(
                          filteredSkills
                            .filter(isSkillNeedsInstallForView)
                            .map(getSkillKey),
                        )
                      }
                    >
                      只选未安装
                    </Button>
                  </Group>
                  {checkedSkills.length > 0 && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      onClick={() => setCheckedSkillKeys([])}
                    >
                      清空已选
                    </Button>
                  )}
                </div>

                {checkedSkills.length > 0 && (
                  <div className="cap-batch-bar">
                    <Text size="xs" fw={650}>
                      已勾选 {checkedSkills.length} 个技能
                    </Text>
                    <Button
                      size="compact-xs"
                      color="indigo"
                      leftSection={<IconSend size={13} />}
                      disabled={loading || availableSkillAgents.length === 0}
                      onClick={() => openSkillDispatchModal(checkedSkills, false)}
                    >
                      批量分发 ({checkedSkills.length})
                    </Button>
                  </div>
                )}
              </div>

              <ScrollArea style={{ flex: 1 }}>
                <div style={{ padding: '4px 0 12px' }}>
                  {filteredSkills.map((sk) => {
                    const key = getSkillKey(sk)
                    const isSelected = activeSkill && getSkillKey(activeSkill) === key
                    const isChecked = checkedSkillSet.has(key)
                    const installedAgents = getSkillInstalledAgents(sk)
                    const installedSet = new Set(installedAgents)
                    const totalAgentCount = availableSkillAgents.length
                    const installedForView = isSkillInstalledForView(sk, selectedAgent)
                    const displayTitle = getSkillDisplayTitle(sk)

                    let statusPillText = '未安装'
                    let statusPillTone: 'is-installed' | 'is-partial' | 'is-missing' = 'is-missing'
                    if (selectedAgent !== 'all') {
                      if (installedForView) {
                        statusPillText = `已安装 · ${activeAgentLabel}`
                        statusPillTone = 'is-installed'
                      } else {
                        statusPillText = `未安装 · ${activeAgentLabel}`
                        statusPillTone = 'is-missing'
                      }
                    } else if (installedAgents.length > 0) {
                      if (totalAgentCount > 0 && installedAgents.length >= totalAgentCount) {
                        statusPillText = `已全装 ${installedAgents.length}/${totalAgentCount}`
                        statusPillTone = 'is-installed'
                      } else {
                        statusPillText = totalAgentCount > 0
                          ? `已装 ${installedAgents.length}/${totalAgentCount}`
                          : `已装 ${installedAgents.length}`
                        statusPillTone = 'is-partial'
                      }
                    }

                    return (
                      <div
                        key={key}
                        onClick={() => setSelectedSkill(sk)}
                        className={`capability-card${isSelected ? ' selected' : ''}${isChecked ? ' is-checked' : ''}`}
                      >
                        <Group justify="space-between" wrap="nowrap" align="flex-start" gap={8}>
                          <Group gap={8} wrap="nowrap" align="flex-start" style={{ minWidth: 0, flex: 1 }}>
                            <Checkbox
                              size="xs"
                              mt={3}
                              checked={isChecked}
                              aria-label={`选择技能 ${displayTitle}`}
                              onClick={(e) => e.stopPropagation()}
                              onChange={() => toggleSkillChecked(key)}
                            />
                            <div style={{ minWidth: 0, flex: 1 }}>
                              <Group gap={6} wrap="nowrap">
                                <Text size="sm" fw={700} truncate style={{ color: 'var(--cap-text-primary)' }}>
                                  {displayTitle}
                                </Text>
                                {isOfficialSkill(sk) && displayTitle !== sk.name && (
                                  <Text size="xs" c="dimmed" truncate style={{ fontFamily: 'monospace' }}>
                                    {sk.name}
                                  </Text>
                                )}
                              </Group>
                              <Text size="xs" c="dimmed" lineClamp={2} mt={3} style={{ lineHeight: 1.45 }}>
                                {sk.description || '未提供描述'}
                              </Text>
                            </div>
                          </Group>

                          <span className={`cap-install-pill ${statusPillTone}`}>
                            {statusPillTone !== 'is-missing' && <IconCheck size={11} stroke={2.6} />}
                            <span>{statusPillText}</span>
                          </span>
                        </Group>

                        {/* 底部清晰展示每个 Agent 的安装状态 */}
                        <Group justify="space-between" align="center" mt={9} pt={7} style={{ borderTop: '1px solid var(--cap-border)' }} gap={6}>
                          <div className="cap-agent-chips">
                            {(availableSkillAgents.length > 0
                              ? availableSkillAgents
                              : installedAgents.map((id) => ({ id, label: shortAgentLabel(id) }))
                            ).map((ag) => {
                              const hasInstalled = installedSet.has(ag.id.toLowerCase())
                              return (
                                <span
                                  key={ag.id}
                                  className={`cap-agent-chip ${hasInstalled ? 'is-installed' : 'is-missing'}`}
                                  title={hasInstalled ? `已安装到 ${ag.label}` : `尚未安装到 ${ag.label}`}
                                >
                                  {hasInstalled ? '✓' : '○'} {shortAgentLabel(ag.id, ag.label)}
                                </span>
                              )
                            })}
                          </div>
                          <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
                            {isOfficialSkill(sk) ? '星序官方' : (sk.source || '本地')}
                          </Text>
                        </Group>
                      </div>
                    )
                  })}
                  {filteredSkills.length === 0 && (
                    <div style={{ padding: 40, textAlign: 'center', color: 'var(--cap-text-muted)', fontSize: 13 }}>
                      无匹配的技能资产
                    </div>
                  )}
                </div>
              </ScrollArea>
            </div>

            {/* 右侧栏：技能详情与本节点安装分布 */}
            <div className="capabilities-inspector-pane">
              {activeSkill ? (() => {
                const activeInstalled = getSkillInstalledAgents(activeSkill)
                const activeInstalledSet = new Set(activeInstalled)
                const missingAgents = availableSkillAgents.filter(
                  (ag) => !activeInstalledSet.has(ag.id.toLowerCase()),
                )
                return (
                  <>
                    <div className="inspector-top-meta">
                      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
                        <div style={{ minWidth: 0, flex: '1 1 240px' }}>
                          <Group gap="xs" wrap="wrap">
                            <h2 style={{ margin: 0, fontSize: 17, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                              {getSkillDisplayTitle(activeSkill)}
                            </h2>
                            <Badge size="xs" variant="light" color={isOfficialSkill(activeSkill) ? 'indigo' : 'gray'}>
                              {isOfficialSkill(activeSkill) ? '星序官方源' : activeSkill.source || '本机技能源'}
                            </Badge>
                          </Group>
                          <Text size="xs" c="dimmed" mt={4} style={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
                            {isOfficialSkill(activeSkill)
                              ? `模块标识: ${activeSkill.name}`
                              : `路径: ${activeSkill.path || '未提供路径'}`}
                          </Text>
                        </div>

                        <Group gap="xs">
                          {missingAgents.length > 0 && (
                            <Button
                              size="xs"
                              variant="filled"
                              color="indigo"
                              leftSection={<IconSend size={13} />}
                              disabled={loading || availableSkillAgents.length === 0}
                              onClick={() => openSkillDispatchModal([activeSkill], true)}
                            >
                              分发到未安装 ({missingAgents.length})
                            </Button>
                          )}
                          <Button
                            size="xs"
                            variant="default"
                            leftSection={<IconSend size={13} />}
                            disabled={loading || availableSkillAgents.length === 0}
                            onClick={() => handleOpenSingleDispatchModal('skill', activeSkill)}
                          >
                            分发到 Agent
                          </Button>
                          {isPluginManagedSource(activeSkill.source) ? (
                            <Button
                              size="xs"
                              color="orange"
                              variant="subtle"
                              leftSection={<IconPlug size={13} />}
                              onClick={() => setActiveTab('plugins')}
                            >
                              插件: {pluginSourceName(activeSkill.source)}
                            </Button>
                          ) : !isOfficialSkill(activeSkill) && (
                            <Button
                              size="xs"
                              color="red"
                              variant="subtle"
                              leftSection={<IconTrash size={13} />}
                              onClick={() => void handleRemoveSkill(activeSkill.name)}
                            >
                              卸载
                            </Button>
                          )}
                        </Group>
                      </Group>

                      {/* 当前节点安装矩阵总览条 */}
                      <div className="cap-inspector-matrix">
                        <Text size="xs" fw={650} c="dimmed">
                          {summary?.target_name || selectedTargetId} 安装状态:
                        </Text>
                        {availableSkillAgents.map((ag) => {
                          const ok = activeInstalledSet.has(ag.id.toLowerCase())
                          return (
                            <span
                              key={ag.id}
                              className={`cap-agent-chip ${ok ? 'is-installed' : 'is-missing'}`}
                            >
                              {ok ? '✓ 已安装' : '○ 未安装'} · {shortAgentLabel(ag.id, ag.label)}
                            </span>
                          )
                        })}
                      </div>
                    </div>

                    <div className="inspector-tabs-header">
                      <div
                        className={`inspector-tab-item ${inspectorTab === 'spec' ? 'active' : ''}`}
                        onClick={() => setInspectorTab('spec')}
                      >
                        SKILL.md 规范与定义
                      </div>
                      <div
                        className={`inspector-tab-item ${inspectorTab === 'install' ? 'active' : ''}`}
                        onClick={() => setInspectorTab('install')}
                      >
                        安装分布与来源
                      </div>
                    </div>

                    <ScrollArea style={{ flex: 1 }}>
                      <div style={{ padding: 18 }}>
                        {inspectorTab === 'spec' && (
                          <Stack gap="md">
                            {activeSkill.description && (
                              <Card withBorder radius="md" p="sm" style={{ background: 'var(--cap-card-bg)' }}>
                                <Text size="xs" fw={700} c="dimmed" mb={4}>技能描述</Text>
                                <Text size="sm" style={{ lineHeight: 1.65, color: 'var(--cap-text-primary)' }}>
                                  {activeSkill.description}
                                </Text>
                              </Card>
                            )}
                            <Card withBorder radius="md" p="sm" style={{ background: 'var(--cap-card-bg)' }}>
                              <Text size="xs" fw={700} c="dimmed" mb={6}>SKILL.md 原文</Text>
                              <div className="code-preview-block">
                                {activeSkill.content || '当前来源未提供 SKILL.md 原文。'}
                              </div>
                            </Card>
                          </Stack>
                        )}

                        {inspectorTab === 'install' && (
                          <Stack gap="sm">
                            <Card withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                              <Text size="xs" fw={700} c="dimmed" mb="xs">
                                节点「{summary?.target_name || selectedTargetId}」各 Agent 安装详情
                              </Text>
                              <Stack gap={6}>
                                {availableSkillAgents.map((ag) => {
                                  const ok = activeInstalledSet.has(ag.id.toLowerCase())
                                  return (
                                    <Group key={ag.id} justify="space-between" p="xs" style={{ borderRadius: 6, background: 'var(--cap-surface-elevated)' }}>
                                      <Text size="xs" fw={600}>{ag.label}</Text>
                                      <span className={`cap-install-pill ${ok ? 'is-installed' : 'is-missing'}`}>
                                        {ok ? '✓ 已安装' : '未安装'}
                                      </span>
                                    </Group>
                                  )
                                })}
                              </Stack>
                            </Card>
                            <Card withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                              <Text size="xs" fw={700} c="dimmed" mb={4}>来源与路径</Text>
                              <Text size="xs" style={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
                                {activeSkill.path || (isOfficialSkill(activeSkill) ? '星序内置官方技能目录（按目标 Agent 模板动态生成）' : '未提供路径')}
                              </Text>
                            </Card>
                          </Stack>
                        )}
                      </div>
                    </ScrollArea>
                  </>
                )
              })() : (
                <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--cap-text-muted)', fontSize: 13 }}>
                  请在左侧选择一个技能查看详情与安装状态
                </div>
              )}
            </div>
          </>
        )}

        {/* ===================== TAB 2: MCP 服务器控制台 ===================== */}
        {activeTab === 'mcp' && (
          <div style={{ flex: 1, padding: 20, overflow: 'auto' }}>
            <Group justify="space-between" mb="md">
              <div>
                <h3 style={{ margin: 0, fontSize: 15, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  MCP 服务器管理
                </h3>
                <Text size="xs" c="dimmed" mt={2}>
                  在 [{summary?.target_name || selectedTargetId}] 上注册的协议服务器。
                </Text>
              </div>
            </Group>

            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
              {filteredMcpServers.map((srv) => {
                const key = mcpKey(srv)
                const pluginManaged = isPluginManagedSource(srv.source)
                const ping = mcpPingStatus[key]
                return (
                  <Card key={key} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                    <Group justify="space-between" align="flex-start" wrap="nowrap">
                      <div>
                        <Group gap="xs">
                          <IconLayersLinked size={16} color="var(--astr-muted)" />
                          <Text fw={700} size="sm">{srv.name}</Text>
                          <Badge size="xs" color={agentBadgeColor(srv.agent)}>{srv.agent}</Badge>
                        </Group>
                        <Group gap={6} mt={6}>
                          <Badge size="xs" variant="outline" color={srv.transport === 'sse/http' ? 'blue' : 'violet'}>
                            {srv.transport.toUpperCase()}
                          </Badge>
                          <Badge size="xs" color={srv.enabled ? 'teal' : 'gray'} variant="light">
                            {srv.enabled ? '● 已启用' : '○ 已停用'}
                          </Badge>
                          {pluginManaged && (
                            <Badge size="xs" color="orange" variant="light">
                              插件: {pluginSourceName(srv.source)}
                            </Badge>
                          )}
                        </Group>
                      </div>

                      <Group gap={4} wrap="nowrap">
                        <Switch
                          checked={srv.enabled}
                          disabled={pluginManaged}
                          size="sm"
                          color="teal"
                          aria-label={`切换 MCP 服务 ${srv.name} 状态`}
                          onChange={() => void handleToggleMcp(srv)}
                        />
                        {!pluginManaged && (
                          <>
                            <Button size="compact-xs" variant="subtle" color="gray" aria-label={`编辑 MCP 服务 ${srv.name}`} onClick={() => handleOpenEditMcp(srv)}>
                              <IconEdit size={14} />
                            </Button>
                            <Button size="compact-xs" variant="subtle" color="indigo" aria-label={`分发 MCP 服务 ${srv.name}`} onClick={() => handleOpenSingleDispatchModal('mcp', srv)}>
                              <IconSend size={14} />
                            </Button>
                            <Button size="compact-xs" variant="subtle" color="red" aria-label={`删除 MCP 服务 ${srv.name}`} onClick={() => void handleDeleteMcp(srv)}>
                              <IconTrash size={14} />
                            </Button>
                          </>
                        )}
                      </Group>
                    </Group>

                    <div style={{ marginTop: 10, fontSize: 12, color: 'var(--cap-text-secondary)', wordBreak: 'break-all' }}>
                      {srv.url ? (
                        <div><strong style={{ color: 'var(--cap-text-primary)' }}>URL:</strong> {srv.url}</div>
                      ) : (
                        <div><strong style={{ color: 'var(--cap-text-primary)' }}>CLI:</strong> {srv.command} {(srv.args || []).join(' ')}</div>
                      )}
                    </div>

                    {srv.tools && srv.tools.length > 0 && (
                      <div style={{ marginTop: 8, paddingTop: 6, borderTop: '1px solid var(--cap-border)' }}>
                        <Text size="xs" c="dimmed" mb={4}>已暴露工具 ({srv.tools.length}):</Text>
                        <Group gap={4} wrap="wrap">
                          {srv.tools.map((t) => (
                            <Badge key={t} size="xs" variant="light" color="indigo" style={{ fontFamily: 'monospace' }}>
                              {t}
                            </Badge>
                          ))}
                        </Group>
                      </div>
                    )}

                    <Group justify="space-between" mt="md" pt="xs" style={{ borderTop: '1px solid var(--cap-border)' }}>
                      <Text size="xs" c={ping?.ok === false ? 'red' : ping?.ok ? 'teal' : 'dimmed'}>
                        {ping?.loading ? '正在探活...' : ping?.text || '就绪'}
                      </Text>
                      <Button size="xs" variant="subtle" color="teal" loading={ping?.loading} onClick={() => void handlePingMcp(srv)}>
                        探活测试
                      </Button>
                    </Group>
                  </Card>
                )
              })}
            </SimpleGrid>
            {filteredMcpServers.length === 0 && (
              <div style={{ padding: 48, textAlign: 'center', color: 'var(--cap-text-muted)', fontSize: 13 }}>
                当前节点下暂无匹配的 MCP 服务器配置
              </div>
            )}
          </div>
        )}

        {/* ===================== TAB 3: 复合插件管理 ===================== */}
        {activeTab === 'plugins' && (
          <div style={{ flex: 1, padding: 20, overflow: 'auto' }}>
            <Group justify="space-between" mb="md">
              <div>
                <h3 style={{ margin: 0, fontSize: 15, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  复合插件包 (Plugins)
                </h3>
                <Text size="xs" c="dimmed" mt={2}>
                  管理 Antigravity、Codex、Hermes 与 Grok 的多组件插件包。
                </Text>
              </div>
              <Button
                size="xs"
                variant="light"
                color="violet"
                leftSection={<IconPlus size={14} />}
                onClick={() => {
                  setPluginInstallMode('install')
                  setPluginInstallOpen(true)
                }}
              >
                安装 / 导入 Antigravity 插件
              </Button>
            </Group>

            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
              {filteredPlugins.map((plg) => {
                const version = typeof plg.config?.version === 'string' ? plg.config.version : ''
                const description = typeof plg.config?.description === 'string' ? plg.config.description : ''
                const bundledSkills = Array.isArray(plg.config?.skills) ? plg.config.skills.map(String) : []
                const bundledMcp = Array.isArray(plg.config?.mcp_servers) ? plg.config.mcp_servers.map(String) : []
                const hasHooks = plg.config?.has_hooks === true
                return (
                  <Card key={plg.id} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                    <Group justify="space-between" align="flex-start" wrap="nowrap">
                      <div>
                        <Group gap="xs">
                          <IconPlug size={16} color="var(--cap-amber)" />
                          <Text fw={700} size="sm">{plg.name}</Text>
                          <Badge size="xs" color={agentBadgeColor(plg.agent)}>{plg.agent}</Badge>
                          {version && <Badge size="xs" variant="outline" color="gray">v{version}</Badge>}
                        </Group>
                        <Group gap={6} mt={6}>
                          {plg.marketplace && <Badge size="xs" variant="light" color="orange">来源: {plg.marketplace}</Badge>}
                          {hasHooks && <Badge size="xs" variant="light" color="cyan">含 Hooks</Badge>}
                        </Group>
                      </div>
                      <Switch
                        checked={plg.enabled}
                        size="sm"
                        color="orange"
                        aria-label={`切换插件 ${plg.name} 状态`}
                        onChange={() => void handleTogglePlugin(plg)}
                      />
                    </Group>

                    {description && <Text size="xs" c="dimmed" mt="xs" lineClamp={2}>{description}</Text>}
                    <Text size="xs" c="dimmed" mt="xs" style={{ fontFamily: 'monospace' }}>{plg.full_name || plg.id}</Text>

                    {(bundledSkills.length > 0 || bundledMcp.length > 0) && (
                      <Group gap={4} mt="xs" wrap="wrap">
                        {bundledSkills.map((sk) => (
                          <Badge key={`sk-${sk}`} size="xs" variant="light" color="indigo">Skill: {sk}</Badge>
                        ))}
                        {bundledMcp.map((mc) => (
                          <Badge key={`mcp-${mc}`} size="xs" variant="light" color="teal">MCP: {mc}</Badge>
                        ))}
                      </Group>
                    )}

                    <Group justify="flex-end" mt="md" pt="xs" style={{ borderTop: '1px solid var(--cap-border)' }}>
                      <Button size="xs" variant="subtle" color="indigo" leftSection={<IconSend size={12} />} onClick={() => handleOpenSingleDispatchModal('plugin', plg)}>
                        跨端分发
                      </Button>
                      <Button size="xs" variant="subtle" color="red" leftSection={<IconTrash size={12} />} onClick={() => void handleUninstallPlugin(plg)}>
                        卸载
                      </Button>
                    </Group>
                  </Card>
                )
              })}
            </SimpleGrid>
            {filteredPlugins.length === 0 && (
              <div style={{ padding: 48, textAlign: 'center', color: 'var(--cap-text-muted)', fontSize: 13 }}>
                当前节点下暂无匹配的插件包
              </div>
            )}
          </div>
        )}

        {/* ===================== TAB 4: 全网能力市场 ===================== */}
        {activeTab === 'marketplaces' && (
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: 20, overflow: 'hidden' }}>
            <div style={{ marginBottom: 16 }}>
              <Group justify="space-between" mb="xs">
                <h3 style={{ margin: 0, fontSize: 15, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  已接入的能力订阅源 (Marketplaces)
                </h3>
                <Button size="xs" color="indigo" leftSection={<IconPlus size={14} />} onClick={handleOpenAddMarket}>
                  添加订阅源
                </Button>
              </Group>

              <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="sm">
                {filteredMarketplaces.map((m) => (
                  <Card key={m.id} withBorder radius="md" p="sm" style={{ background: 'var(--cap-card-bg)' }}>
                    <Group justify="space-between">
                      <Group gap={4}>
                        <Text fw={700} size="xs">{m.name}</Text>
                        {m.agent && m.agent !== 'all' && <Badge size="xs" color="gray">{m.agent}</Badge>}
                      </Group>
                      <Badge size="xs" color="pink">{m.source_type}</Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={4} lineClamp={1} title={m.source}>{m.source}</Text>
                    <Group justify="flex-end" mt="xs" gap={4}>
                      <Button size="compact-xs" variant="subtle" color="teal" onClick={() => void handlePullMarket(m)}>Pull 同步</Button>
                      {m.id !== 'global::skills.sh' && (
                        <Button size="compact-xs" variant="subtle" color="red" onClick={() => void handleDeleteMarket(m)}>删除</Button>
                      )}
                    </Group>
                  </Card>
                ))}
              </SimpleGrid>
            </div>

            <Group gap="xs" mb="xs">
              <Text size="xs" fw={600} c="dimmed">热门推荐:</Text>
              {['github', 'react', 'docker', 'video', 'python', 'pdf', 'browser'].map((tag) => (
                <Badge
                  key={tag}
                  size="xs"
                  variant="light"
                  color="indigo"
                  style={{ cursor: 'pointer' }}
                  onClick={() => {
                    setMarketQuery(tag)
                    void handleMarketSearch(tag)
                  }}
                >
                  #{tag}
                </Badge>
              ))}
            </Group>

            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
              <div style={{ marginBottom: 12 }}>
                <TextInput
                  placeholder="搜索全网 Agent 技能与能力扩展（如: react, github, video, docker）..."
                  value={marketQuery}
                  onChange={(e) => setMarketQuery(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && void handleMarketSearch()}
                  leftSection={<IconSearch size={16} />}
                  rightSection={
                    <Button size="xs" color="indigo" loading={searchingMarket} onClick={() => void handleMarketSearch()}>
                      搜索全网
                    </Button>
                  }
                />
              </div>

              <ScrollArea style={{ flex: 1 }}>
                {searchingMarket && (
                  <div style={{ textAlign: 'center', padding: 48, color: 'var(--cap-text-secondary)', fontSize: 13 }}>
                    正在检索全网能力市场...
                  </div>
                )}
                <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
                  {marketResults.map((item) => (
                    <Card key={item.pkg} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                      <Group justify="space-between" align="flex-start">
                        <div>
                          <Text fw={700} size="sm">{item.name || item.pkg}</Text>
                          <Text size="xs" c="dimmed" mt={2}>{item.repo}</Text>
                        </div>
                        <Badge size="xs" color="gray">{item.installs}</Badge>
                      </Group>
                      <Group justify="space-between" mt="md">
                        <a href={item.url} target="_blank" rel="noreferrer" style={{ fontSize: 12, color: 'var(--cap-cyan)' }}>
                          查看主页 ↗
                        </a>
                        <Button
                          size="xs"
                          variant="light"
                          color="indigo"
                          leftSection={<IconDownload size={14} />}
                          onClick={() => { setCustomPackage(item.pkg); setInstallTargets([selectedTargetId]); setInstallModalOpen(true) }}
                        >
                          跨端分发安装
                        </Button>
                      </Group>
                    </Card>
                  ))}
                </SimpleGrid>
                {marketResults.length === 0 && !searchingMarket && (
                  <div style={{ textAlign: 'center', padding: 40, color: 'var(--cap-text-muted)', fontSize: 13 }}>
                    输入关键词搜索全球社区技能包
                  </div>
                )}
              </ScrollArea>
            </div>
          </div>
        )}
      </div>

      {/* 后台分发实时进度浮条（不阻塞任何操作，切走窗口也照常运行） */}
      {primaryRunningJob && (
        <div className="cap-live-job-bar" role="status" aria-live="polite">
          <div className="cap-live-job-meta">
            <span className="cap-jobs-dot is-running" aria-hidden="true" />
            <Text size="xs" fw={650} truncate>
              正在后台分发 {(primaryRunningJob.items || []).length} 项能力
              {primaryRunningJob.current_label ? ` · 当前：${primaryRunningJob.current_label}` : ''}
            </Text>
            <Text size="xs" c="dimmed" style={{ flexShrink: 0 }}>
              {primaryRunningJob.completed_steps} / {primaryRunningJob.total_steps}
            </Text>
          </div>
          <div className="cap-live-job-progress">
            <Progress
              value={primaryRunningJob.total_steps > 0 ? Math.round((primaryRunningJob.completed_steps / primaryRunningJob.total_steps) * 100) : 0}
              size="sm"
              radius="xl"
              animated
              color="indigo"
            />
          </div>
          <Button size="compact-xs" variant="light" color="indigo" onClick={() => setJobsDrawerOpen(true)}>
            查看进度
          </Button>
        </div>
      )}

      {/* 底部状态条 */}
      <div className="capabilities-footer-bar">
        <span>{summary?.target_name || '能力中心'} · 分发以目标文件回读核验为准</span>
        <span>{availableSkillAgents.map((agent) => shortAgentLabel(agent.id, agent.label)).join(' · ')}</span>
      </div>

      {/* =======================================================
          DRAWER: 后台分发任务队列与核验报告
      ======================================================= */}
      <Drawer
        opened={jobsDrawerOpen}
        onClose={() => setJobsDrawerOpen(false)}
        position="right"
        size="lg"
        title={
          <Group gap="xs">
            <IconListDetails size={17} />
            <Text fw={700} size="sm">后台分发任务队列</Text>
            {runningJobs.length > 0 && (
              <Badge size="xs" color="indigo" variant="light">{runningJobs.length} 运行中</Badge>
            )}
          </Group>
        }
      >
        <Stack gap="md">
          <Group justify="space-between">
            <Text size="xs" c="dimmed">
              分发任务在服务端后台执行，切换或关闭设置窗口不会中断。
            </Text>
            <Group gap={6}>
              <Button size="compact-xs" variant="default" leftSection={<IconRefresh size={12} />} onClick={() => void loadDispatchJobs()}>
                刷新
              </Button>
              {dispatchJobs.some((j) => j.status !== 'running') && (
                <Button size="compact-xs" variant="subtle" color="gray" onClick={() => void handleClearFinishedJobs()}>
                  清理已完成
                </Button>
              )}
            </Group>
          </Group>

          {dispatchJobs.length === 0 ? (
            <div style={{ padding: 40, textAlign: 'center', color: 'var(--cap-text-muted)', fontSize: 13 }}>
              暂无后台分发任务记录
            </div>
          ) : (
            <Stack gap="sm">
              {dispatchJobs.map((job) => {
                const pct = job.total_steps > 0 ? Math.round((job.completed_steps / job.total_steps) * 100) : 0
                const statusLabel =
                  job.status === 'running'
                    ? '后台分发中'
                    : job.status === 'completed'
                      ? '全部成功'
                      : job.status === 'partial'
                        ? '部分成功'
                        : '失败'
                const statusColor =
                  job.status === 'running'
                    ? 'indigo'
                    : job.status === 'completed'
                      ? 'teal'
                      : job.status === 'partial'
                        ? 'yellow'
                        : 'red'
                return (
                  <div key={job.id} className="cap-job-card">
                    <Group justify="space-between" align="flex-start" mb={6}>
                      <div>
                        <Group gap={6}>
                          <Badge size="xs" color={statusColor} variant="light">{statusLabel}</Badge>
                          <Text size="xs" fw={700}>
                            {(job.items || []).length} 项 {(job.capability_type || 'skill').toUpperCase()} → {(job.target_matrix || []).length} 个目标
                          </Text>
                        </Group>
                        <Text size="xs" c="dimmed" mt={2}>
                          源节点: {job.source_target_name || job.source_target_id} · 进度 {job.completed_steps}/{job.total_steps}
                          {job.status !== 'running' ? `（成功 ${job.success_count}，失败 ${job.failed_count}）` : ''}
                        </Text>
                      </div>
                      {job.status !== 'running' && job.failed_count > 0 && (
                        <Button
                          size="compact-xs"
                          variant="light"
                          color="orange"
                          leftSection={<IconRotateClockwise size={12} />}
                          onClick={() => void handleRetryFailedJob(job)}
                        >
                          重试失败项 ({job.failed_count})
                        </Button>
                      )}
                    </Group>

                    <Progress
                      value={pct}
                      size="xs"
                      radius="xl"
                      animated={job.status === 'running'}
                      color={statusColor}
                      mb={8}
                    />

                    <Group gap={4} mb={8} wrap="wrap">
                      {(job.items || []).map((it) => (
                        <Badge key={`${job.id}-${it.item_id}`} size="xs" variant="outline" color="gray">
                          {it.label || it.item_id}
                        </Badge>
                      ))}
                    </Group>

                    {(job.reports || []).length > 0 && (
                      <Stack gap={4} mt={6}>
                        {(job.reports || []).map((rep, idx) => (
                          <div key={`${job.id}-rep-${idx}`} className="cap-job-report-row">
                            <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                              {rep.ok ? (
                                <IconCheck size={13} color="var(--cap-green)" style={{ flexShrink: 0 }} />
                              ) : (
                                <IconX size={13} color="var(--cap-red)" style={{ flexShrink: 0 }} />
                              )}
                              <span style={{ fontWeight: 600, whiteSpace: 'nowrap' }}>
                                {rep.item_label || rep.item_id}
                              </span>
                              <span style={{ color: 'var(--cap-text-muted)', whiteSpace: 'nowrap' }}>
                                → {rep.target_name || rep.target_id} · {shortAgentLabel(rep.agent)}
                              </span>
                            </Group>
                            <span style={{ color: rep.ok ? 'var(--cap-text-secondary)' : 'var(--cap-red)', textAlign: 'right', wordBreak: 'break-word' }}>
                              {rep.message}
                            </span>
                          </div>
                        ))}
                      </Stack>
                    )}
                  </div>
                )
              })}
            </Stack>
          )}
        </Stack>
      </Drawer>

      {/* =======================================================
          MODAL 1: 新建/编辑 MCP 服务
      ======================================================= */}
      <Modal
        opened={mcpModalOpen}
        onClose={() => setMcpModalOpen(false)}
        title={mcpEditing ? `编辑 MCP 服务 (${mcpFormName})` : '新建 / 配置 MCP 服务'}
        size="lg"
      >
        <Stack gap="sm">
          <Group grow>
            <Select
              label="目标主机环境"
              data={targets.map((t) => ({ value: t.id, label: `${t.name} (${t.kind})` }))}
              value={mcpFormTarget}
              onChange={(val) => setMcpFormTarget(val || 'local')}
              disabled={mcpEditing}
              required
            />
            <Select
              label="归属 Agent"
              data={[
                { value: 'codex', label: 'OpenAI Codex (~/.codex/config.toml)' },
                { value: 'hermes', label: 'Hermes Agent (~/.hermes/config.yaml)' },
                { value: 'grok', label: 'xAI Grok (~/.grok/config.toml)' },
                { value: 'antigravity', label: 'Google Antigravity (~/.gemini/config/mcp_config.json)' },
              ].filter((opt) => mcpEditing || isAgentEnabled(opt.value))}
              value={mcpFormAgent}
              onChange={(val) => setMcpFormAgent(val || 'codex')}
              disabled={mcpEditing}
              required
            />
          </Group>

          <TextInput
            label="MCP 服务标识名称"
            placeholder="例如: devspace, custom-mcp"
            value={mcpFormName}
            onChange={(e) => setMcpFormName(e.target.value)}
            disabled={mcpEditing}
            required
          />

          <Group grow align="flex-end">
            <Select
              label="传输协议模式"
              data={[
                { value: 'sse/http', label: 'Streamable HTTP / SSE 远程端点 (推荐)' },
                { value: 'stdio', label: 'Stdio 本地/远程子进程命令行管道' },
              ]}
              value={mcpFormTransport}
              onChange={(val) => setMcpFormTransport(val as any)}
              required
            />
            <Switch
              label="启用该 MCP 服务"
              checked={mcpFormEnabled}
              onChange={(e) => setMcpFormEnabled(e.currentTarget.checked)}
              mb={6}
            />
          </Group>

          {mcpFormTransport === 'sse/http' ? (
            <>
              <TextInput
                label="服务端点 URL"
                placeholder="https://example.com/mcp"
                value={mcpFormUrl}
                onChange={(e) => setMcpFormUrl(e.target.value)}
                required
              />
              <JsonInput
                label="HTTP Headers 配置 (JSON 格式，留空或 [REDACTED] 保留原密钥)"
                placeholder='{"Authorization": "Bearer xxx"}'
                value={mcpFormHeaders}
                onChange={setMcpFormHeaders}
                formatOnBlur
                autosize
                minRows={2}
              />
            </>
          ) : (
            <Group grow>
              <TextInput
                label="启动执行程序 (Command)"
                placeholder="例如: python, npx, node"
                value={mcpFormCommand}
                onChange={(e) => setMcpFormCommand(e.target.value)}
                required
              />
              <TextInput
                label="执行参数 (Args 以空格分隔)"
                placeholder="-m my_mcp_server"
                value={mcpFormArgs}
                onChange={(e) => setMcpFormArgs(e.target.value)}
              />
            </Group>
          )}

          <JsonInput
            label="环境变量 Env 配置 (JSON 格式，[REDACTED] 保留原密钥)"
            placeholder='{"API_KEY": "xxx", "DEBUG": "1"}'
            value={mcpFormEnv}
            onChange={setMcpFormEnv}
            formatOnBlur
            autosize
            minRows={2}
          />

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setMcpModalOpen(false)}>取消</Button>
            <Button color="indigo" loading={mcpSaving} onClick={() => void handleSaveMcp()}>保存并写入 Agent 配置</Button>
          </Group>
        </Stack>
      </Modal>

      {/* =======================================================
          MODAL 1B: 安装 / 导入 Antigravity 插件
      ======================================================= */}
      <Modal
        opened={pluginInstallOpen}
        onClose={() => setPluginInstallOpen(false)}
        title="安装 / 导入 Antigravity 插件"
        size="md"
      >
        <Stack gap="sm">
          <Select
            label="操作模式"
            data={[
              { value: 'install', label: '直接安装插件 (agy plugin install)' },
              { value: 'import', label: '从其他 Agent 导入插件 (agy plugin import)' },
            ]}
            value={pluginInstallMode}
            onChange={(val) => setPluginInstallMode((val as 'install' | 'import') || 'install')}
          />

          {pluginInstallMode === 'install' ? (
            <TextInput
              label="插件来源 (目录路径 / .zip / URL / plugin@marketplace)"
              placeholder="例如: /path/to/my-plugin 或 my-plugin@marketplace"
              value={pluginInstallSource}
              onChange={(e) => setPluginInstallSource(e.target.value)}
              required
            />
          ) : (
            <Select
              label="导入来源 Agent"
              data={[
                { value: 'claude', label: 'Claude Code (~/.claude/plugins/)' },
                { value: 'gemini', label: 'Gemini CLI (~/.gemini/extensions/)' },
                { value: 'auto', label: '自动检测可导入插件' },
              ]}
              value={pluginImportSource}
              onChange={(val) => setPluginImportSource(val || 'claude')}
            />
          )}

          <Text size="xs" c="dimmed">
            将在当前选中节点 [{summary?.target_name || selectedTargetId}] 上调用原生 agy plugin 命令执行。
          </Text>

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setPluginInstallOpen(false)}>取消</Button>
            <Button color="violet" loading={pluginInstalling} onClick={() => void handleInstallOrImportAgyPlugin()}>
              {pluginInstallMode === 'install' ? '执行安装' : '执行导入'}
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* =======================================================
          MODAL 2: 添加市场源
      ======================================================= */}
      <Modal
        opened={marketModalOpen}
        onClose={() => setMarketModalOpen(false)}
        title="订阅新能力市场源 (Marketplace)"
        size="md"
      >
        <Stack gap="sm">
          <Select
            label="归属 Agent 运行时"
            data={[
              { value: 'codex', label: 'OpenAI Codex' },
              { value: 'grok', label: 'xAI Grok' },
            ]}
            value={marketFormAgent}
            onChange={(val) => setMarketFormAgent(val || 'codex')}
            required
          />
          <TextInput
            label="市场标识名称"
            placeholder="例如: ponytail, xai-official, custom-market"
            value={marketFormName}
            onChange={(e) => setMarketFormName(e.target.value)}
            required
          />
          <TextInput
            label="Git 仓库地址 / Registry URL"
            placeholder="https://github.com/.../marketplace.git"
            value={marketFormSource}
            onChange={(e) => setMarketFormSource(e.target.value)}
            required
          />
          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setMarketModalOpen(false)}>取消</Button>
            <Button color="indigo" loading={marketSaving} onClick={() => void handleSaveMarket()}>订阅并同步</Button>
          </Group>
        </Stack>
      </Modal>

      {/* =======================================================
          MODAL 3: 单项 / 多项技能跨端后台分发矩阵
      ======================================================= */}
      <Modal
        opened={dispatchModalOpen}
        onClose={() => setDispatchModalOpen(false)}
        title={
          <Group gap="xs">
            <IconNetwork size={18} color="var(--astr-accent, #7540bd)" />
            <Text fw={700}>
              {dispatchItems.length > 1
                ? `批量分发矩阵（${dispatchItems.length} 项能力）`
                : '跨节点分发矩阵 (Background Dispatch)'}
            </Text>
          </Group>
        }
        size="lg"
      >
        <Stack gap="md">
          <Paper p="sm" radius="md" style={{ background: 'var(--cap-card-bg)', border: '1px solid var(--cap-border)' }}>
            <Group justify="space-between" mb={6}>
              <Text size="xs" fw={650} c="dimmed">
                待分发清单（{dispatchItems.length} 项 {dispatchType.toUpperCase()}）
              </Text>
              <Badge size="xs" color="indigo" variant="light">
                源节点: {summary?.target_name || selectedTargetId}
              </Badge>
            </Group>
            <Group gap={6} wrap="wrap">
              {dispatchItems.map((draft) => (
                <span key={`${draft.source_agent}:${draft.item_id}`} className="cap-dispatch-item-chip">
                  <span>{draft.label}</span>
                  {dispatchItems.length > 1 && (
                    <button
                      type="button"
                      aria-label={`移除 ${draft.label}`}
                      onClick={() =>
                        setDispatchItems((prev) =>
                          prev.filter((x) => !(x.item_id === draft.item_id && x.source_agent === draft.source_agent)),
                        )
                      }
                    >
                      <IconX size={12} />
                    </button>
                  )}
                </span>
              ))}
            </Group>
          </Paper>

          <div>
            <Group justify="space-between" mb="xs" wrap="wrap" gap={6}>
              <Text size="xs" fw={700} style={{ color: 'var(--cap-text-secondary)' }}>
                勾选目标「托管节点 × Agent 运行时」:
              </Text>
              <Group gap={6}>
                {singleDraftInstalledSet && (
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="teal"
                    onClick={() => {
                      const next = availableSkillAgents
                        .filter((ag) => !singleDraftInstalledSet.has(ag.id.toLowerCase()))
                        .map((ag) => ({ target_id: selectedTargetId, agent: ag.id }))
                      setDispatchMatrix(next)
                    }}
                  >
                    仅选当前节点未安装
                  </Button>
                )}
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="indigo"
                  onClick={() => {
                    const allPairs: Array<{ target_id: string; agent: string }> = []
                    for (const tgt of targets) {
                      for (const ag of availableSkillAgents) {
                        const incompatible =
                          dispatchType === 'plugin' &&
                          dispatchItems.length === 1 &&
                          Boolean(dispatchItems[0].raw?.agent) &&
                          dispatchItems[0].raw.agent !== ag.id
                        if (!incompatible) {
                          allPairs.push({ target_id: tgt.id, agent: ag.id })
                        }
                      }
                    }
                    setDispatchMatrix(allPairs)
                  }}
                >
                  全选全部
                </Button>
                <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setDispatchMatrix([])}>
                  清空
                </Button>
              </Group>
            </Group>

            <SimpleGrid cols={2} spacing="xs">
              {targets.map((tgt) => {
                const nodeEligibleAgents = availableSkillAgents.filter(({ id: agentName }) => {
                  const incompatiblePlugin =
                    dispatchType === 'plugin' &&
                    dispatchItems.length === 1 &&
                    Boolean(dispatchItems[0].raw?.agent) &&
                    dispatchItems[0].raw.agent !== agentName
                  return !incompatiblePlugin
                })
                const nodeAllSelected =
                  nodeEligibleAgents.length > 0 &&
                  nodeEligibleAgents.every((ag) =>
                    dispatchMatrix.some((m) => m.target_id === tgt.id && m.agent === ag.id),
                  )
                return (
                  <Card key={tgt.id} withBorder radius="sm" p="xs" style={{ background: 'var(--cap-surface-elevated)' }}>
                    <Group justify="space-between" mb={6}>
                      <Text fw={700} size="xs">{tgt.name} ({tgt.kind})</Text>
                      {nodeEligibleAgents.length > 0 && (
                        <button
                          type="button"
                          style={{ border: 0, background: 'transparent', color: 'var(--cap-accent)', fontSize: 11, fontWeight: 600, cursor: 'pointer', padding: 0 }}
                          onClick={() => {
                            if (nodeAllSelected) {
                              setDispatchMatrix((prev) => prev.filter((m) => m.target_id !== tgt.id))
                            } else {
                              setDispatchMatrix((prev) => {
                                const withoutNode = prev.filter((m) => m.target_id !== tgt.id)
                                return [
                                  ...withoutNode,
                                  ...nodeEligibleAgents.map((ag) => ({ target_id: tgt.id, agent: ag.id })),
                                ]
                              })
                            }
                          }}
                        >
                          {nodeAllSelected ? '取消全选' : '全选本节点'}
                        </button>
                      )}
                    </Group>
                    <Stack gap={5}>
                      {availableSkillAgents.map(({ id: agentName, label }) => {
                        const isChecked = dispatchMatrix.some(
                          (m) => m.target_id === tgt.id && m.agent === agentName,
                        )
                        const incompatiblePlugin =
                          dispatchType === 'plugin' &&
                          dispatchItems.length === 1 &&
                          Boolean(dispatchItems[0].raw?.agent) &&
                          dispatchItems[0].raw.agent !== agentName
                        const alreadyInstalledHere =
                          tgt.id === selectedTargetId &&
                          Boolean(singleDraftInstalledSet?.has(agentName.toLowerCase()))
                        return (
                          <Checkbox
                            key={`${tgt.id}::${agentName}`}
                            size="xs"
                            label={
                              incompatiblePlugin
                                ? `${label}（仅限同类 Agent）`
                                : alreadyInstalledHere
                                  ? `${label}（已安装，将覆盖更新）`
                                  : label
                            }
                            disabled={incompatiblePlugin}
                            checked={isChecked}
                            onChange={(e) => {
                              if (e.currentTarget.checked) {
                                setDispatchMatrix((prev) => [...prev, { target_id: tgt.id, agent: agentName }])
                              } else {
                                setDispatchMatrix((prev) =>
                                  prev.filter((m) => !(m.target_id === tgt.id && m.agent === agentName)),
                                )
                              }
                            }}
                          />
                        )
                      })}
                    </Stack>
                  </Card>
                )
              })}
            </SimpleGrid>
          </div>

          <Group justify="space-between" align="center" mt="xs">
            <Text size="xs" c="dimmed">
              {totalDispatchSteps > 0
                ? `共 ${dispatchItems.length} 项 × ${dispatchMatrix.length} 个目标 = ${totalDispatchSteps} 步（提交后转入后台执行）`
                : '请选择目标节点与 Agent'}
            </Text>
            <Group gap="xs">
              <Button variant="default" onClick={() => setDispatchModalOpen(false)}>取消</Button>
              <Button
                color="indigo"
                loading={submittingJob}
                disabled={dispatchItems.length === 0 || dispatchMatrix.length === 0}
                leftSection={<IconSend size={14} />}
                onClick={() => void handleStartBackgroundDispatch()}
              >
                放入后台分发
              </Button>
            </Group>
          </Group>
        </Stack>
      </Modal>

      {/* 手动导入包 Modal */}
      <Modal
        opened={installModalOpen}
        onClose={() => setInstallModalOpen(false)}
        title="导入 Agent 能力包"
        size="md"
      >
        <Stack gap="md">
          <TextInput
            label="能力包或仓库地址 (Package / Git / URI)"
            placeholder="例如: anthropics/anthropic-quickstarts 或 package@market"
            value={customPackage}
            onChange={(e) => setCustomPackage(e.target.value)}
            required
          />

          <div>
            <Text size="xs" fw={600} mb="xs">选择安装目标节点:</Text>
            <Stack gap="xs">
              {targets.map((tgt) => (
                <Checkbox
                  key={tgt.id}
                  label={`${tgt.name} (${tgt.kind})`}
                  checked={installTargets.includes(tgt.id)}
                  onChange={(e) => {
                    if (e.currentTarget.checked) {
                      setInstallTargets([...installTargets, tgt.id])
                    } else {
                      setInstallTargets(installTargets.filter((x) => x !== tgt.id))
                    }
                  }}
                />
              ))}
            </Stack>
          </div>

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setInstallModalOpen(false)}>取消</Button>
            <Button variant="light" color="gray" loading={installing} onClick={() => void handleInstall()}>
              直接安装到本地
            </Button>
            <Button color="indigo" loading={installing} onClick={() => void handleInstall()}>
              安装到所选节点
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}

export const SkillsSettingsCard = CapabilitiesSettingsCard
