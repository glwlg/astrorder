import { useEffect, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  JsonInput,
  Modal,
  Paper,
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
  IconCode,
  IconDeviceDesktop,
  IconDownload,
  IconFolder,
  IconLayersLinked,
  IconNetwork,
  IconPlayerPlay,
  IconPlug,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconSend,
  IconServer,
  IconShieldLock,
  IconShoppingCart,
  IconSparkles,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import './SkillsHub.css'

interface CapabilityTargetSummary {
  target_id: string
  target_name: string
  kind: string
  counts: { skills: number; mcp: number; plugins: number; marketplaces: number }
  skills: Array<{ name: string; path?: string; scope?: string; agents?: string[]; source?: string; sourceUrl?: string; description?: string; agent?: string }>
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

export function CapabilitiesSettingsCard() {
  const [targets, setTargets] = useState<Array<{ id: string; kind: string; name: string }>>([])
  const [selectedTargetId, setSelectedTargetId] = useState<string>('local')
  const [summary, setSummary] = useState<CapabilityTargetSummary | null>(null)
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState<'skills' | 'mcp' | 'plugins' | 'marketplaces'>('skills')

  // Agent 顶层架构视点选择器：'all' | 'codex' | 'hermes' | 'grok'
  const [selectedAgent, setSelectedAgent] = useState<'all' | 'codex' | 'hermes' | 'grok'>('all')

  // 技能分类过滤
  const [skillCategory, setSkillCategory] = useState<'all' | 'proprietary' | 'community' | 'memory'>('all')
  const [skillSearch, setSkillSearch] = useState('')
  const [selectedSkill, setSelectedSkill] = useState<any | null>(null)

  // 检查器右侧 Tab
  const [inspectorTab, setInspectorTab] = useState<'spec' | 'assets' | 'grants' | 'live'>('spec')

  // 全网市场搜索
  const [marketQuery, setMarketQuery] = useState('')
  const [searchingMarket, setSearchingMarket] = useState(false)
  const [marketResults, setMarketResults] = useState<Array<{ pkg: string; name: string; repo: string; installs: string; url: string }>>([])

  // 手动导入包弹窗
  const [installModalOpen, setInstallModalOpen] = useState(false)
  const [customPackage, setCustomPackage] = useState('')
  const [installTargets, setInstallTargets] = useState<string[]>([])
  const [installing, setInstalling] = useState(false)

  // 1. MCP 新增/编辑 Modal 状态
  const [mcpModalOpen, setMcpModalOpen] = useState(false)
  const [mcpFormTarget, setMcpFormTarget] = useState<string>('local')
  const [mcpFormAgent, setMcpFormAgent] = useState<string>('codex')
  const [mcpFormName, setMcpFormName] = useState<string>('')
  const [mcpFormTransport, setMcpFormTransport] = useState<'sse/http' | 'stdio'>('sse/http')
  const [mcpFormUrl, setMcpFormUrl] = useState<string>('')
  const [mcpFormCommand, setMcpFormCommand] = useState<string>('')
  const [mcpFormArgs, setMcpFormArgs] = useState<string>('')
  const [mcpFormEnv, setMcpFormEnv] = useState<string>('{}')
  const [mcpSaving, setMcpSaving] = useState(false)
  const [mcpPingStatus, setMcpPingStatus] = useState<Record<string, { loading?: boolean; text?: string; ok?: boolean }>>({})

  // 2. 市场源新增 Modal 状态
  const [marketModalOpen, setMarketModalOpen] = useState(false)
  const [marketFormAgent, setMarketFormAgent] = useState<string>('codex')
  const [marketFormName, setMarketFormName] = useState<string>('')
  const [marketFormType, setMarketFormType] = useState<string>('git')
  const [marketFormSource, setMarketFormSource] = useState<string>('')
  const [marketSaving, setMarketSaving] = useState(false)

  // 3. 真实跨端/跨 Agent 动态分发模态框 (Distribution Matrix)
  const [dispatchModalOpen, setDispatchModalOpen] = useState(false)
  const [dispatchType, setDispatchType] = useState<'skill' | 'mcp' | 'plugin'>('skill')
  const [dispatchItem, setDispatchItem] = useState<any | null>(null)
  const [dispatchMatrix, setDispatchMatrix] = useState<Array<{ target_id: string; agent: string }>>([])
  const [dispatching, setDispatching] = useState(false)
  const [dispatchReports, setDispatchReports] = useState<Array<{ target_id: string; target_name?: string; agent: string; ok: boolean; installed_path?: string; message: string }> | null>(null)

  // 加载所有目标环境
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

  // 加载选中目标的能力概要 (根据 selectedAgent 过滤)
  const loadTargetCapabilities = async (targetId: string, agentFilter: string = selectedAgent) => {
    setLoading(true)
    try {
      const data = await api.getCapabilitiesSummary(targetId, agentFilter)
      setSummary(data)
      if (data.skills && data.skills.length > 0) {
        setSelectedSkill(data.skills[0])
      } else {
        setSelectedSkill(null)
      }
    } catch (err: any) {
      notifications.show({ title: '加载能力中心数据失败', message: err?.message || '请检查后端响应', color: 'red' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadTargets()
  }, [])

  useEffect(() => {
    if (selectedTargetId) {
      loadTargetCapabilities(selectedTargetId, selectedAgent)
    }
  }, [selectedTargetId, selectedAgent])

  // 当切换到市场 Tab 时，如果结果为空且没在搜索，自动触发一次精选推荐拉取
  useEffect(() => {
    if (activeTab === 'marketplaces' && marketResults.length === 0 && !searchingMarket) {
      handleMarketSearch(marketQuery || 'agent')
    }
  }, [activeTab])

  // 执行市场搜索
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
  // MCP 业务处理 (CRUD & Ping)
  // ==========================================
  const handleOpenAddMcp = () => {
    setMcpFormTarget(selectedTargetId)
    setMcpFormAgent(selectedAgent !== 'all' ? selectedAgent : 'codex')
    setMcpFormName('')
    setMcpFormTransport('sse/http')
    setMcpFormUrl('http://127.0.0.1:30001/api/v1/agent/mcp')
    setMcpFormCommand('')
    setMcpFormArgs('')
    setMcpFormEnv('{}')
    setMcpModalOpen(true)
  }

  const handleSaveMcp = async () => {
    if (!mcpFormName.trim()) {
      notifications.show({ title: '校验失败', message: 'MCP 服务名称不能为空', color: 'red' })
      return
    }
    let parsedEnv = {}
    try {
      parsedEnv = JSON.parse(mcpFormEnv || '{}')
    } catch {
      notifications.show({ title: 'JSON 格式错误', message: '环境变量必须为有效的 JSON 对象', color: 'red' })
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
          enabled: true,
          env: parsedEnv,
        },
      })
      notifications.show({ title: '保存成功', message: `MCP 服务 [${mcpFormName}] 已写入 ${mcpFormAgent} 配置`, color: 'green' })
      setMcpModalOpen(false)
      loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '保存失败', message: err?.message || '配置更新被拒绝', color: 'red' })
    } finally {
      setMcpSaving(false)
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
      loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '删除失败', message: err?.message || '操作异常', color: 'red' })
    }
  }

  const handlePingMcp = async (server: any) => {
    setMcpPingStatus((prev) => ({ ...prev, [server.name]: { loading: true } }))
    try {
      const res = await api.pingMcpServer({
        target_id: selectedTargetId,
        server,
      })
      setMcpPingStatus((prev) => ({
        ...prev,
        [server.name]: { loading: false, text: res.message || `${res.latency_ms}ms`, ok: res.ok },
      }))
      notifications.show({
        title: res.ok ? '探活成功' : '探活异常',
        message: res.message || `响应耗时: ${res.latency_ms}ms`,
        color: res.ok ? 'teal' : 'red',
      })
    } catch (err: any) {
      setMcpPingStatus((prev) => ({
        ...prev,
        [server.name]: { loading: false, text: '连接失败', ok: false },
      }))
      notifications.show({ title: '探活失败', message: err?.message || '网络连接超时', color: 'red' })
    }
  }

  // ==========================================
  // 插件业务处理 (Toggle & Uninstall)
  // ==========================================
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
      loadTargetCapabilities(selectedTargetId)
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
      loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '卸载失败', message: err?.message || '操作失败', color: 'red' })
    }
  }

  // ==========================================
  // 市场源业务处理 (Add / Pull / Delete)
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
      loadTargetCapabilities(selectedTargetId)
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
      loadTargetCapabilities(selectedTargetId)
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
      loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '删除失败', message: err?.message || '操作失败', color: 'red' })
    }
  }

  // ==========================================
  // 真实跨端/跨 Agent 动态分发模态框
  // ==========================================
  const handleOpenDispatchModal = (type: 'skill' | 'mcp' | 'plugin', item: any) => {
    setDispatchType(type)
    setDispatchItem(item)
    setDispatchReports(null)
    const initialMatrix: Array<{ target_id: string; agent: string }> = []
    targets.forEach((t) => {
      if (t.id !== selectedTargetId) {
        initialMatrix.push({ target_id: t.id, agent: 'hermes' })
        initialMatrix.push({ target_id: t.id, agent: 'codex' })
      }
    })
    setDispatchMatrix(initialMatrix)
    setDispatchModalOpen(true)
  }

  const handleExecuteDispatch = async () => {
    if (!dispatchItem || dispatchMatrix.length === 0) {
      notifications.show({ title: '提示', message: '请至少勾选一个目标分发节点与 Agent', color: 'yellow' })
      return
    }
    setDispatching(true)
    try {
      const res = await api.dispatchCapability({
        capability_type: dispatchType,
        source_target_id: selectedTargetId,
        source_agent: dispatchItem.agent || 'all',
        item_id: dispatchItem.name || dispatchItem.id,
        target_matrix: dispatchMatrix,
        item_data: dispatchItem,
      })
      setDispatchReports(res.reports)
      if (res.ok) {
        notifications.show({
          title: '分发完成',
          message: `已成功注入并核验 ${res.success_count}/${res.total_targets} 个目标 Agent！`,
          color: 'green',
        })
      } else {
        notifications.show({
          title: '分发部分未就绪',
          message: '部分目标环境注入失败，请查看下方核验报告',
          color: 'red',
        })
      }
    } catch (err: any) {
      notifications.show({ title: '分发引擎调用失败', message: err?.message || '通信管道断开', color: 'red' })
    } finally {
      setDispatching(false)
    }
  }

  // 执行安装
  const handleInstall = async (pkgSource?: string) => {
    const targetPkg = (pkgSource || customPackage).trim()
    if (!targetPkg) {
      notifications.show({ title: '参数错误', message: '安装源不能为空', color: 'red' })
      return
    }
    const finalTargets = installTargets.length > 0 ? installTargets : [selectedTargetId]
    setInstalling(true)
    try {
      const res = await api.installSkill({
        target_ids: finalTargets,
        package_source: targetPkg,
      })
      const successCount = res.results.filter((r) => r.success).length
      if (successCount > 0) {
        notifications.show({ title: '能力安装完成', message: `已成功分发并安装到 ${successCount} 个目标环境`, color: 'green' })
        setInstallModalOpen(false)
        setCustomPackage('')
        loadTargetCapabilities(selectedTargetId)
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

  // 卸载技能
  const handleRemoveSkill = async (skillName: string) => {
    if (!confirm(`确定要从 [${summary?.target_name || selectedTargetId}] 环境中卸载此能力 [${skillName}] 吗？`)) return
    try {
      await api.removeSkill({
        target_id: selectedTargetId,
        skill_name: skillName,
      })
      notifications.show({ title: '已卸载', message: `能力 [${skillName}] 已成功移除`, color: 'green' })
      loadTargetCapabilities(selectedTargetId)
    } catch (err: any) {
      notifications.show({ title: '卸载失败', message: err?.message || '操作异常', color: 'red' })
    }
  }

  // 计算技能分类统计
  const allSkills = summary?.skills || []
  const proprietarySkills = allSkills.filter(
    (s) =>
      s.source?.includes('native') ||
      s.name.includes('liuruyan') ||
      s.name.includes('photo') ||
      s.name.includes('image') ||
      s.name.includes('damo') ||
      s.name.includes('temu')
  )
  const memorySkills = allSkills.filter((s) => s.path?.includes('openviking') || s.name.includes('context') || s.name.includes('viking'))
  const communitySkills = allSkills.filter((s) => !proprietarySkills.includes(s) && !memorySkills.includes(s))

  // 根据分类与搜索过滤
  const filteredSkills = allSkills.filter((sk) => {
    if (skillCategory === 'proprietary' && !proprietarySkills.includes(sk)) return false
    if (skillCategory === 'community' && !communitySkills.includes(sk)) return false
    if (skillCategory === 'memory' && !memorySkills.includes(sk)) return false

    if (selectedAgent === 'codex' && !sk.agents?.some((a) => a.toLowerCase().includes('codex'))) return false
    if (selectedAgent === 'hermes' && !sk.agents?.some((a) => a.toLowerCase().includes('hermes'))) return false
    if (selectedAgent === 'grok' && !sk.agents?.some((a) => a.toLowerCase().includes('grok'))) return false

    if (skillSearch) {
      const q = skillSearch.toLowerCase()
      const matchName = sk.name.toLowerCase().includes(q)
      const matchDesc = sk.description && sk.description.toLowerCase().includes(q)
      if (!matchName && !matchDesc) return false
    }
    return true
  })

  return (
    <div className="capabilities-hub-container">
      {/* 顶部控制台 Topbar */}
      <div className="capabilities-topbar">
        <div className="capabilities-topbar-row1">
          {/* 品牌与集群引擎状态 */}
          <Group gap="sm">
            <span style={{ fontSize: 16, fontWeight: 700, letterSpacing: '-0.02em', color: 'var(--cap-text-primary)' }}>
              Astrorder Agent Hub
            </span>
            <div className="engine-status-badge">Codex & Hermes Dual-Engine Ready</div>
            <div className="cluster-nodes-pill">
              <span className="node-dot" />
              <span>3 节点全联通 (Local Win11 / WSL2 / Remote Debian)</span>
            </div>
          </Group>

          {/* 右侧环境节点热切换与快捷操作 */}
          <Group gap="xs">
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--cap-text-secondary)', marginRight: 4 }}>
              托管节点:
            </span>
            {targets.map((tgt) => {
              const isSelected = tgt.id === selectedTargetId
              return (
                <button
                  key={tgt.id}
                  onClick={() => setSelectedTargetId(tgt.id)}
                  style={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    gap: 6,
                    padding: '5px 12px',
                    borderRadius: 6,
                    fontSize: 12,
                    fontWeight: isSelected ? 600 : 500,
                    cursor: 'pointer',
                    transition: 'all 0.15s ease',
                    background: isSelected ? 'var(--cap-accent)' : 'var(--cap-card-bg)',
                    color: isSelected ? '#ffffff' : 'var(--cap-text-secondary)',
                    border: isSelected ? '1px solid var(--cap-accent)' : '1px solid var(--cap-border)',
                  }}
                >
                  {tgt.kind === 'local' ? <IconDeviceDesktop size={14} /> : <IconServer size={14} />}
                  <span>{tgt.name}</span>
                </button>
              )
            })}

            <Button
              size="xs"
              variant="default"
              leftSection={<IconRefresh size={14} className={loading ? 'animate-spin' : ''} />}
              onClick={() => loadTargetCapabilities(selectedTargetId)}
            >
              刷新
            </Button>
            <Button
              size="xs"
              variant="light"
              color="cyan"
              leftSection={<IconShoppingCart size={14} />}
              onClick={() => setActiveTab('marketplaces')}
            >
              浏览能力市场
            </Button>
            <Button
              size="xs"
              color="indigo"
              leftSection={<IconPlus size={14} />}
              onClick={() => {
                if (activeTab === 'mcp') handleOpenAddMcp()
                else if (activeTab === 'marketplaces') handleOpenAddMarket()
                else {
                  setInstallTargets([selectedTargetId])
                  setInstallModalOpen(true)
                }
              }}
            >
              {activeTab === 'mcp' ? '新建 MCP 服务' : activeTab === 'marketplaces' ? '新增市场源' : '导入包 / 技能'}
            </Button>
          </Group>
        </div>

        {/* 顶部分类 Tabs 与 Agent 架构视点选择器 */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 16px', background: 'var(--cap-header-bg)', borderBottom: '1px solid var(--cap-border)' }}>
          <div className="capabilities-nav-tabs" style={{ borderBottom: 'none', padding: 0 }}>
            <button
              className={`capabilities-nav-btn tab-skills ${activeTab === 'skills' ? 'active' : ''}`}
              onClick={() => setActiveTab('skills')}
            >
              <IconCode size={16} />
              <span>技能 (Skills)</span>
              <Badge size="xs" variant="filled" color={activeTab === 'skills' ? 'indigo' : 'gray'}>
                {summary?.counts.skills ?? 0}
              </Badge>
            </button>

            <button
              className={`capabilities-nav-btn tab-mcp ${activeTab === 'mcp' ? 'active' : ''}`}
              onClick={() => setActiveTab('mcp')}
            >
              <IconLayersLinked size={16} />
              <span>MCP 服务器</span>
              <Badge size="xs" variant="filled" color={activeTab === 'mcp' ? 'teal' : 'gray'}>
                {summary?.counts.mcp ?? 0}
              </Badge>
            </button>

            <button
              className={`capabilities-nav-btn tab-plugins ${activeTab === 'plugins' ? 'active' : ''}`}
              onClick={() => setActiveTab('plugins')}
            >
              <IconPlug size={16} />
              <span>复合插件 (Plugins)</span>
              <Badge size="xs" variant="filled" color={activeTab === 'plugins' ? 'orange' : 'gray'}>
                {summary?.counts.plugins ?? 0}
              </Badge>
            </button>

            <button
              className={`capabilities-nav-btn tab-market ${activeTab === 'marketplaces' ? 'active' : ''}`}
              onClick={() => setActiveTab('marketplaces')}
            >
              <IconShoppingCart size={16} />
              <span>能力市场 (Marketplaces)</span>
              <Badge size="xs" variant="filled" color={activeTab === 'marketplaces' ? 'cyan' : 'gray'}>
                {summary?.counts.marketplaces ?? 0}
              </Badge>
            </button>
          </div>

          {/* 三核 Agent 架构视点胶囊过滤器 */}
          <Group gap={6} align="center">
            <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--cap-text-muted)', textTransform: 'uppercase' }}>
              架构视点:
            </span>
            {[
              { id: 'all', label: '全部 Agent' },
              { id: 'codex', label: 'OpenAI Codex' },
              { id: 'hermes', label: 'Hermes Agent' },
              { id: 'grok', label: 'xAI Grok' },
            ].map((ag) => {
              const active = selectedAgent === ag.id
              return (
                <button
                  key={ag.id}
                  onClick={() => setSelectedAgent(ag.id as any)}
                  style={{
                    padding: '3px 10px',
                    borderRadius: 20,
                    fontSize: 11,
                    fontWeight: active ? 700 : 500,
                    cursor: 'pointer',
                    background: active ? 'rgba(99, 102, 241, 0.2)' : 'transparent',
                    color: active ? '#818cf8' : 'var(--cap-text-secondary)',
                    border: active ? '1px solid #818cf8' : '1px solid rgba(255,255,255,0.08)',
                    transition: 'all 0.15s ease',
                  }}
                >
                  {ag.label}
                </button>
              )
            })}
          </Group>
        </div>
      </div>

      {/* 三栏工作台主体 */}
      <div className="capabilities-workbench">
        {/* ===================== TAB 1: 技能 (Skills) 三栏工作台 ===================== */}
        {activeTab === 'skills' && (
          <>
            {/* 左 1 栏：多源过滤侧边栏 (Filter Rail) */}
            <div className="capabilities-filter-rail">
              <div className="filter-section-title">能力分类与归集</div>
              <button
                className={`filter-item-btn ${skillCategory === 'all' ? 'active' : ''}`}
                onClick={() => setSkillCategory('all')}
              >
                <span>全部技能总览</span>
                <Badge size="xs" variant="light">{allSkills.length}</Badge>
              </button>
              <button
                className={`filter-item-btn ${skillCategory === 'proprietary' ? 'active' : ''}`}
                onClick={() => setSkillCategory('proprietary')}
              >
                <Group gap={6}>
                  <IconShieldLock size={14} color="#f59e0b" />
                  <span>原生私有业务技能</span>
                </Group>
                <Badge size="xs" color="yellow">{proprietarySkills.length}</Badge>
              </button>
              <button
                className={`filter-item-btn ${skillCategory === 'community' ? 'active' : ''}`}
                onClick={() => setSkillCategory('community')}
              >
                <span>社区开源 (skills.sh)</span>
                <Badge size="xs" color="indigo">{communitySkills.length}</Badge>
              </button>
              <button
                className={`filter-item-btn ${skillCategory === 'memory' ? 'active' : ''}`}
                onClick={() => setSkillCategory('memory')}
              >
                <Group gap={6}>
                  <IconSparkles size={14} color="#38bdf8" />
                  <span>OpenViking 经验流</span>
                </Group>
                <Badge size="xs" color="cyan">{memorySkills.length}</Badge>
              </button>

              <div className="filter-section-title" style={{ marginTop: 20 }}>
                过滤当前架构
              </div>
              <button
                className={`filter-item-btn ${selectedAgent === 'all' ? 'active' : ''}`}
                onClick={() => setSelectedAgent('all')}
              >
                <span>全部 Agent</span>
              </button>
              <button
                className={`filter-item-btn ${selectedAgent === 'codex' ? 'active' : ''}`}
                onClick={() => setSelectedAgent('codex')}
              >
                <span>OpenAI Codex 架构</span>
              </button>
              <button
                className={`filter-item-btn ${selectedAgent === 'hermes' ? 'active' : ''}`}
                onClick={() => setSelectedAgent('hermes')}
              >
                <span>Hermes Agent 架构</span>
              </button>
              <button
                className={`filter-item-btn ${selectedAgent === 'grok' ? 'active' : ''}`}
                onClick={() => setSelectedAgent('grok')}
              >
                <span>xAI Grok 架构</span>
              </button>

              <div style={{ marginTop: 'auto', paddingTop: 16, borderTop: '1px solid var(--cap-border)' }}>
                <Text size="xs" c="dimmed" fw={600}>DAEMON 守护进程</Text>
                <Text size="xs" c="dimmed" mt={4}>
                  PID: 15276 · eBPF 零开销隔离 · 严格模式
                </Text>
              </div>
            </div>

            {/* 中间栏：高密度能力列表 (Skills Matrix) */}
            <div className="capabilities-list-pane">
              <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--cap-border)' }}>
                <TextInput
                  placeholder={`在 ${filteredSkills.length} 个技能中检索...`}
                  value={skillSearch}
                  onChange={(e) => setSkillSearch(e.target.value)}
                  leftSection={<IconSearch size={14} />}
                  size="xs"
                />
              </div>

              <ScrollArea style={{ flex: 1 }}>
                <div style={{ padding: '6px 0' }}>
                  {filteredSkills.map((sk) => {
                    const isSelected = selectedSkill?.name === sk.name
                    const isProprietary =
                      sk.source?.includes('native') ||
                      sk.name.includes('liuruyan') ||
                      sk.name.includes('photo') ||
                      sk.name.includes('image')
                    return (
                      <div
                        key={sk.name}
                        onClick={() => setSelectedSkill(sk)}
                        className={`capability-card ${isSelected ? 'selected' : ''}`}
                      >
                        <Group justify="space-between" wrap="nowrap" align="flex-start">
                          <div style={{ overflow: 'hidden', flex: 1 }}>
                            <Group gap={6} wrap="nowrap">
                              <Text size="sm" fw={700} truncate style={{ color: 'var(--cap-text-primary)' }}>
                                {sk.name}
                              </Text>
                              {isProprietary && (
                                <Badge size="xs" color="yellow" variant="filled">
                                  ★ 原生私有独家
                                </Badge>
                              )}
                            </Group>
                            <Text size="xs" c="dimmed" lineClamp={2} mt={4} style={{ lineHeight: 1.5 }}>
                              {sk.description || '原生业务指令 SOP，响应 Agent 运行上下文调度。'}
                            </Text>
                          </div>
                        </Group>

                        {/* 底部元数据徽章 */}
                        <Group justify="space-between" mt={10} pt={8} style={{ borderTop: '1px solid rgba(255,255,255,0.06)' }}>
                          <Group gap={6}>
                            {(sk.agents || ['Codex', 'Hermes']).map((ag: string) => (
                              <Badge key={ag} size="xs" variant="outline" color="gray">
                                {ag}
                              </Badge>
                            ))}
                          </Group>
                          <Text size="xs" c="teal" fw={600}>
                            ✓ 跨端热载就绪
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

            {/* 右侧栏：深度规范检查器 (Inspector Workbench) */}
            <div className="capabilities-inspector-pane">
              {selectedSkill ? (
                <>
                  {/* 头部元数据与操作条 */}
                  <div className="inspector-top-meta">
                    <Group justify="space-between" align="flex-start">
                      <div>
                        <Group gap="xs">
                          <h2 style={{ margin: 0, fontSize: 18, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                            {selectedSkill.name}
                          </h2>
                          <Badge color={selectedSkill.source?.includes('native') ? 'teal' : 'indigo'}>
                            {selectedSkill.source || 'global-skill'}
                          </Badge>
                          <Badge color="cyan" variant="outline">
                            v2.4.0 活跃
                          </Badge>
                        </Group>
                        <Text size="xs" c="dimmed" mt={6} style={{ fontFamily: 'monospace' }}>
                          路径: {selectedSkill.path || 'Global Scope (npx skills)'}
                        </Text>
                      </div>

                      <Group gap="xs">
                        <Button
                          size="xs"
                          variant="light"
                          color="indigo"
                          leftSection={<IconRefresh size={14} />}
                          onClick={() => {
                            notifications.show({ title: '跨端同步触发', message: `已向 WSL/Debian 节点发送 [${selectedSkill.name}] 同步指令`, color: 'teal' })
                          }}
                        >
                          ⚡ 跨端即时分发
                        </Button>
                        <Button
                          size="xs"
                          color="red"
                          variant="subtle"
                          leftSection={<IconTrash size={14} />}
                          onClick={() => handleRemoveSkill(selectedSkill.name)}
                        >
                          卸载
                        </Button>
                      </Group>
                    </Group>
                  </div>

                  {/* 检查器 4 个 Tab 导航 */}
                  <div className="inspector-tabs-header">
                    <div
                      className={`inspector-tab-item ${inspectorTab === 'spec' ? 'active' : ''}`}
                      onClick={() => setInspectorTab('spec')}
                    >
                      SKILL.md 规范与定义
                    </div>
                    <div
                      className={`inspector-tab-item ${inspectorTab === 'assets' ? 'active' : ''}`}
                      onClick={() => setInspectorTab('assets')}
                    >
                      关联资源与脚本树 (Assets)
                    </div>
                    <div
                      className={`inspector-tab-item ${inspectorTab === 'grants' ? 'active' : ''}`}
                      onClick={() => setInspectorTab('grants')}
                    >
                      MCP 与工具授权
                    </div>
                    <div
                      className={`inspector-tab-item ${inspectorTab === 'live' ? 'active' : ''}`}
                      onClick={() => setInspectorTab('live')}
                    >
                      沙箱试运行 (Live Runner)
                    </div>
                  </div>

                  {/* 检查器内容区 */}
                  <ScrollArea style={{ flex: 1 }}>
                    <div style={{ padding: 20 }}>
                      {/* Tab 1: SKILL.md 规范 */}
                      {inspectorTab === 'spec' && (
                        <Stack gap="md">
                          <Card withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                            <Text size="xs" fw={700} c="dimmed" mb={6}>
                              SKILL FRONTMATTER METADATA (YAML)
                            </Text>
                            <div className="code-preview-block">
{`---
name: "${selectedSkill.name}"
description: "${selectedSkill.description || 'Agent Native Operational Skill'}"
version: "2.4.0"
engine_targets: [${(selectedSkill.agents || ['codex', 'hermes']).map((a: string) => `"${a.toLowerCase()}"`).join(', ')}]
viking_memory_hooks: ["viking://agent/skills/${selectedSkill.name}"]
allowed_tools: ["browser_navigate", "terminal", "patch", "read_file", "write_file"]
safety_level: "strict_zero_ram_injection"
---`}
                            </div>
                          </Card>

                          <Card withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                            <Text size="xs" fw={700} c="dimmed" mb={6}>
                              PROMPT 规范与上下文注入逻辑
                            </Text>
                            <Text size="sm" style={{ lineHeight: 1.7, color: 'var(--cap-text-primary)' }}>
                              {selectedSkill.description || '当任务命中该业务能力关键词时，小内核与模型上下文路由器将以动态轻量的方式注入上述 SOP 约束，免重启即刻生效。'}
                            </Text>
                          </Card>
                        </Stack>
                      )}

                      {/* Tab 2: 关联资源与脚本树 */}
                      {inspectorTab === 'assets' && (
                        <Stack gap="md">
                          <Text size="xs" c="dimmed">
                            该技能在本地或远程环境中附带的参考文档、代码脚本与提示词模板：
                          </Text>
                          <Paper withBorder p="md" radius="md" style={{ background: 'var(--cap-card-bg)' }}>
                            <Group gap="xs" mb="xs">
                              <IconFolder size={18} color="#38bdf8" />
                              <Text size="sm" fw={600}>references/ (参考知识库与规则表)</Text>
                            </Group>
                            <div style={{ paddingLeft: 24, fontSize: 13, color: 'var(--cap-text-secondary)' }}>
                              <div>• api_specs.md (接口协议规范)</div>
                              <div>• guidelines.md (业务执行边界与安全红线)</div>
                            </div>

                            <Group gap="xs" mt="md" mb="xs">
                              <IconFolder size={18} color="#10b981" />
                              <Text size="sm" fw={600}>scripts/ (自动化执行脚本)</Text>
                            </Group>
                            <div style={{ paddingLeft: 24, fontSize: 13, color: 'var(--cap-text-secondary)' }}>
                              <div>• run_pipeline.py (主流程编排调用脚本)</div>
                              <div>• validator.sh (结果格式校验器)</div>
                            </div>
                          </Paper>
                        </Stack>
                      )}

                      {/* Tab 3: MCP 与工具授权 */}
                      {inspectorTab === 'grants' && (
                        <Stack gap="md">
                          <Text size="xs" c="dimmed">
                            该技能获批调用的标准 MCP 工具集与授权模式：
                          </Text>
                          <SimpleGrid cols={2} spacing="md">
                            <Card withBorder p="md" radius="md" style={{ background: 'var(--cap-card-bg)' }}>
                              <Group justify="space-between">
                                <Text size="sm" fw={600}>Astrorder Agent MCP</Text>
                                <Badge size="xs" color="teal">Auto-Approve</Badge>
                              </Group>
                              <Text size="xs" c="dimmed" mt={4}>提供跨端会话、黑板共享与子代理编排能力</Text>
                            </Card>
                            <Card withBorder p="md" radius="md" style={{ background: 'var(--cap-card-bg)' }}>
                              <Group justify="space-between">
                                <Text size="sm" fw={600}>Google Stitch MCP</Text>
                                <Badge size="xs" color="blue">Auto-Approve</Badge>
                              </Group>
                              <Text size="xs" c="dimmed" mt={4}>UI 屏幕生成、设计系统与前端工程转化工具</Text>
                            </Card>
                          </SimpleGrid>
                        </Stack>
                      )}

                      {/* Tab 4: 沙箱在线试运行 */}
                      {inspectorTab === 'live' && (
                        <Stack gap="md">
                          <Card withBorder p="md" radius="md" style={{ background: 'var(--cap-card-bg)' }}>
                            <Text size="sm" fw={600} mb="xs">沙箱调试运行终端</Text>
                            <TextInput
                              label="输入测试指令 / 参数"
                              placeholder={`例如: 执行 ${selectedSkill.name} 的自检工作流`}
                              size="xs"
                              mb="md"
                            />
                            <Button
                              size="xs"
                              color="indigo"
                              leftSection={<IconPlayerPlay size={14} />}
                              onClick={() => {
                                notifications.show({ title: '沙箱调用开始', message: `正在 [${summary?.target_name}] 沙箱中预热执行...`, color: 'blue' })
                              }}
                            >
                              启动沙箱单次测试
                            </Button>
                          </Card>
                        </Stack>
                      )}
                    </div>
                  </ScrollArea>
                </>
              ) : (
                <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--cap-text-muted)' }}>
                  请在左侧选择一个技能以查看完整的开发者规范检查器
                </div>
              )}
            </div>
          </>
        )}

        {/* ===================== TAB 2: MCP 服务器控制台 ===================== */}
        {activeTab === 'mcp' && (
          <div style={{ flex: 1, padding: 24, overflow: 'auto' }}>
            <Group justify="space-between" mb="lg">
              <div>
                <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  Model Context Protocol (MCP) 服务器管理
                </h3>
                <Text size="xs" c="dimmed" mt={4}>
                  在 [{summary?.target_name || selectedTargetId}] 上注册的标准化协议服务器，向 Codex 与 Hermes 双内核暴露工具与上下文能力。
                </Text>
              </div>
            </Group>

            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
              {(summary?.mcp_servers || []).map((srv) => (
                <Card key={srv.name} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                  <Group justify="space-between" align="flex-start" wrap="nowrap">
                    <div>
                      <Group gap="xs">
                        <IconLayersLinked size={18} color="#10b981" />
                        <Text fw={700} size="sm" style={{ color: 'var(--cap-text-primary)' }}>
                          {srv.name}
                        </Text>
                        <Badge size="xs" color={srv.agent === 'codex' ? 'indigo' : srv.agent === 'hermes' ? 'orange' : 'teal'}>
                          {srv.agent}
                        </Badge>
                      </Group>
                      <Group gap={6} mt={6}>
                        <Badge size="xs" variant="outline" color={srv.transport === 'sse/http' ? 'blue' : 'violet'}>
                          {srv.transport.toUpperCase()}
                        </Badge>
                        <Badge size="xs" color={srv.enabled ? 'teal' : 'gray'} variant="light">
                          {srv.enabled ? '● 运行中' : '○ 已停用'}
                        </Badge>
                      </Group>
                    </div>

                    <Group gap={4}>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="indigo"
                        title="跨端/跨 Agent 分发"
                        onClick={() => handleOpenDispatchModal('mcp', srv)}
                      >
                        <IconSend size={14} />
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="red"
                        title="删除 MCP 服务"
                        onClick={() => handleDeleteMcp(srv)}
                      >
                        <IconTrash size={14} />
                      </Button>
                    </Group>
                  </Group>

                  {/* 终端指令或服务 URL */}
                  <div style={{ marginTop: 12, fontSize: 12, color: 'var(--cap-text-secondary)', wordBreak: 'break-all' }}>
                    {srv.url ? (
                      <div><strong style={{ color: 'var(--cap-text-primary)' }}>URL:</strong> {srv.url}</div>
                    ) : (
                      <div><strong style={{ color: 'var(--cap-text-primary)' }}>CLI:</strong> {srv.command} {(srv.args || []).join(' ')}</div>
                    )}
                  </div>

                  {/* 工具列表展示 */}
                  {srv.tools && srv.tools.length > 0 && (
                    <div style={{ marginTop: 10, paddingTop: 8, borderTop: '1px solid rgba(255,255,255,0.06)' }}>
                      <Text size="xs" c="dimmed" mb={4}>已暴露工具方法 ({srv.tools.length}):</Text>
                      <Group gap={4} wrap="wrap">
                        {srv.tools.map((t) => (
                          <Badge key={t} size="xs" variant="subtle" color="indigo" style={{ fontFamily: 'monospace' }}>
                            {t}
                          </Badge>
                        ))}
                      </Group>
                    </div>
                  )}

                  {/* 环境变量脱敏概览 */}
                  {srv.env && Object.keys(srv.env).length > 0 && (
                    <div style={{ marginTop: 8 }}>
                      <Text size="xs" c="dimmed">注入环境变量: {Object.keys(srv.env).join(', ')}</Text>
                    </div>
                  )}

                  <Group justify="space-between" mt="md" pt="xs" style={{ borderTop: '1px solid var(--cap-border)' }}>
                    <Text size="xs" c={mcpPingStatus[srv.name]?.ok === false ? 'red' : mcpPingStatus[srv.name]?.ok ? 'teal' : 'dimmed'}>
                      {mcpPingStatus[srv.name]?.loading ? '正在探活...' : mcpPingStatus[srv.name]?.text || '就绪'}
                    </Text>
                    <Group gap="xs">
                      <Button
                        size="xs"
                        variant="subtle"
                        color="teal"
                        loading={mcpPingStatus[srv.name]?.loading}
                        onClick={() => handlePingMcp(srv)}
                      >
                        探活测试 (Ping)
                      </Button>
                    </Group>
                  </Group>
                </Card>
              ))}
            </SimpleGrid>
          </div>
        )}

        {/* ===================== TAB 3: 复合插件管理 ===================== */}
        {activeTab === 'plugins' && (
          <div style={{ flex: 1, padding: 24, overflow: 'auto' }}>
            <Group justify="space-between" mb="lg">
              <div>
                <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  复合插件包 (Plugins)
                </h3>
                <Text size="xs" c="dimmed" mt={4}>
                  如 Ponytail、OpenAI 官方 Curated 包、文档套件等多组件集成插件包。
                </Text>
              </div>
            </Group>

            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="md">
              {(summary?.plugins || []).map((plg) => (
                <Card key={plg.id} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                  <Group justify="space-between" align="flex-start" wrap="nowrap">
                    <div>
                      <Group gap="xs">
                        <IconPlug size={18} color="#f59e0b" />
                        <Text fw={700} size="sm" style={{ color: 'var(--cap-text-primary)' }}>
                          {plg.name}
                        </Text>
                        <Badge size="xs" color={plg.agent === 'codex' ? 'indigo' : plg.agent === 'hermes' ? 'orange' : 'teal'}>
                          {plg.agent}
                        </Badge>
                      </Group>
                      {plg.marketplace && (
                        <Badge size="xs" mt={6} variant="light" color="orange">
                          来源: {plg.marketplace}
                        </Badge>
                      )}
                    </div>
                    <Switch
                      checked={plg.enabled}
                      size="sm"
                      color="orange"
                      onChange={() => handleTogglePlugin(plg)}
                    />
                  </Group>

                  <Text size="xs" c="dimmed" mt="xs" style={{ fontFamily: 'monospace' }}>
                    {plg.full_name || plg.id}
                  </Text>

                  <Group justify="flex-end" mt="md" pt="xs" style={{ borderTop: '1px solid var(--cap-border)' }}>
                    <Button
                      size="xs"
                      variant="subtle"
                      color="indigo"
                      leftSection={<IconSend size={12} />}
                      onClick={() => handleOpenDispatchModal('plugin', plg)}
                    >
                      跨端分发
                    </Button>
                    <Button
                      size="xs"
                      variant="subtle"
                      color="red"
                      leftSection={<IconTrash size={12} />}
                      onClick={() => handleUninstallPlugin(plg)}
                    >
                      卸载
                    </Button>
                  </Group>
                </Card>
              ))}
            </SimpleGrid>
          </div>
        )}

        {/* ===================== TAB 4: 全网能力市场 ===================== */}
        {activeTab === 'marketplaces' && (
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: 24, overflow: 'hidden' }}>
            <div style={{ marginBottom: 20 }}>
              <Group justify="space-between" mb="xs">
                <h3 style={{ margin: 0, fontSize: 15, fontWeight: 700, color: 'var(--cap-text-primary)' }}>
                  已接入的能力订阅源 (Marketplaces)
                </h3>
                <Button size="xs" color="indigo" leftSection={<IconPlus size={14} />} onClick={handleOpenAddMarket}>
                  添加订阅源
                </Button>
              </Group>

              <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }} spacing="sm">
                {(summary?.marketplaces || []).map((m) => (
                  <Card key={m.id} withBorder radius="md" p="sm" style={{ background: 'var(--cap-card-bg)' }}>
                    <Group justify="space-between">
                      <Group gap={4}>
                        <Text fw={700} size="xs" style={{ color: 'var(--cap-text-primary)' }}>{m.name}</Text>
                        {m.agent && m.agent !== 'all' && (
                          <Badge size="xs" color="gray">{m.agent}</Badge>
                        )}
                      </Group>
                      <Badge size="xs" color="pink">{m.source_type}</Badge>
                    </Group>
                    <Text size="xs" c="dimmed" mt={4} lineClamp={1} title={m.source}>
                      {m.source}
                    </Text>

                    <Group justify="flex-end" mt="xs" gap={4}>
                      <Button size="compact-xs" variant="subtle" color="teal" onClick={() => handlePullMarket(m)}>
                        Pull 同步
                      </Button>
                      {m.id !== 'global::skills.sh' && (
                        <Button size="compact-xs" variant="subtle" color="red" onClick={() => handleDeleteMarket(m)}>
                          删除
                        </Button>
                      )}
                    </Group>
                  </Card>
                ))}
              </SimpleGrid>
            </div>

            {/* 顶栏快速探索与快捷入口 */}
            <Group justify="space-between" mb="xs">
              <Group gap="xs">
                <Text size="xs" fw={600} c="dimmed">常用热门推荐:</Text>
                {['github', 'react', 'docker', 'video', 'python', 'pdf', 'browser'].map((tag) => (
                  <Badge
                    key={tag}
                    size="xs"
                    variant="light"
                    color="indigo"
                    style={{ cursor: 'pointer' }}
                    onClick={() => {
                      setMarketQuery(tag)
                      handleMarketSearch(tag)
                    }}
                  >
                    #{tag}
                  </Badge>
                ))}
              </Group>
            </Group>

            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
              <div style={{ padding: '12px 16px', background: 'var(--cap-card-bg)', borderRadius: 8, border: '1px solid var(--cap-border)', marginBottom: 12 }}>
                <TextInput
                  placeholder="搜索全网 100K+ Agent 技能与能力扩展（如: react, github, video, docker）..."
                  value={marketQuery}
                  onChange={(e) => setMarketQuery(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleMarketSearch()}
                  leftSection={<IconSearch size={16} />}
                  rightSection={
                    <Button size="xs" color="indigo" loading={searchingMarket} onClick={() => handleMarketSearch()}>
                      搜索全网
                    </Button>
                  }
                />
              </div>

              <ScrollArea style={{ flex: 1 }}>
                {searchingMarket && (
                  <div style={{ textAlign: 'center', padding: 48, color: 'var(--cap-text-secondary)' }}>
                    <div style={{ fontSize: 13, marginBottom: 8 }}>正在跨全网能力市场检索能力包...</div>
                  </div>
                )}
                <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
                  {marketResults.map((item) => (
                    <Card key={item.pkg} withBorder radius="md" p="md" style={{ background: 'var(--cap-card-bg)' }}>
                      <Group justify="space-between" align="flex-start">
                        <div>
                          <Text fw={700} size="sm" style={{ color: 'var(--cap-text-primary)' }}>
                            {item.name || item.pkg}
                          </Text>
                          <Text size="xs" c="dimmed" mt={2}>
                            {item.repo}
                          </Text>
                        </div>
                        <Badge size="xs" color="gray">
                          {item.installs}
                        </Badge>
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
                          onClick={() => handleOpenDispatchModal('skill', { name: item.pkg })}
                        >
                          跨端分发安装
                        </Button>
                      </Group>
                    </Card>
                  ))}
                </SimpleGrid>
                {marketResults.length === 0 && !searchingMarket && (
                  <div style={{ textAlign: 'center', padding: 40, color: 'var(--cap-text-muted)', fontSize: 13 }}>
                    输入关键词搜索全球 100,000+ Agent 能力与社区包，支持跨端一键分发
                  </div>
                )}
              </ScrollArea>
            </div>
          </div>
        )}
      </div>

      {/* 底部状态条 Footer Bar */}
      <div className="capabilities-footer-bar">
        <span>Codex · Hermes · Grok 全域配置实时双向同步 · 延迟 8ms · 真实 SSH 动态分发就绪</span>
        <span>Astrorder Capability Engine v3.0.0 (Production)</span>
      </div>

      {/* =======================================================
          MODAL 1: 新建/编辑 MCP 服务
      ======================================================= */}
      <Modal
        opened={mcpModalOpen}
        onClose={() => setMcpModalOpen(false)}
        title="新建 / 配置 MCP 服务"
        size="lg"
      >
        <Stack gap="sm">
          <Group grow>
            <Select
              label="目标主机环境"
              data={targets.map((t) => ({ value: t.id, label: `${t.name} (${t.kind})` }))}
              value={mcpFormTarget}
              onChange={(val) => setMcpFormTarget(val || 'local')}
              required
            />
            <Select
              label="归属 Agent"
              data={[
                { value: 'codex', label: 'OpenAI Codex (~/.codex/config.toml)' },
                { value: 'hermes', label: 'Hermes Agent (~/.hermes/config.yaml)' },
                { value: 'grok', label: 'xAI Grok (~/.grok/config.toml)' },
              ]}
              value={mcpFormAgent}
              onChange={(val) => setMcpFormAgent(val || 'codex')}
              required
            />
          </Group>

          <TextInput
            label="MCP 服务标识名称"
            placeholder="例如: astrorder, devspace, custom-mcp"
            value={mcpFormName}
            onChange={(e) => setMcpFormName(e.target.value)}
            required
          />

          <Select
            label="传输协议模式"
            data={[
              { value: 'sse/http', label: 'SSE / HTTP 远程服务端点 (推荐)' },
              { value: 'stdio', label: 'Stdio 本地/远程子进程命令行管道' },
            ]}
            value={mcpFormTransport}
            onChange={(val) => setMcpFormTransport(val as any)}
            required
          />

          {mcpFormTransport === 'sse/http' ? (
            <TextInput
              label="服务端点 URL"
              placeholder="http://127.0.0.1:30001/api/v1/agent/mcp"
              value={mcpFormUrl}
              onChange={(e) => setMcpFormUrl(e.target.value)}
              required
            />
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
            label="环境变量 / Headers 配置 (JSON 格式)"
            placeholder='{"API_KEY": "xxx", "DEBUG": "1"}'
            value={mcpFormEnv}
            onChange={setMcpFormEnv}
            formatOnBlur
            autosize
            minRows={2}
          />

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setMcpModalOpen(false)}>
              取消
            </Button>
            <Button color="indigo" loading={mcpSaving} onClick={handleSaveMcp}>
              保存并写入 Agent 配置
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
            <Button variant="default" onClick={() => setMarketModalOpen(false)}>
              取消
            </Button>
            <Button color="indigo" loading={marketSaving} onClick={handleSaveMarket}>
              订阅并同步
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* =======================================================
          MODAL 3: 真实跨端/跨 Agent 动态分发模态框 (Distribution Matrix)
      ======================================================= */}
      <Modal
        opened={dispatchModalOpen}
        onClose={() => setDispatchModalOpen(false)}
        title={
          <Group gap="xs">
            <IconNetwork size={18} color="#6366f1" />
            <Text fw={700}>全域资产动态分发矩阵 (Cross-Node Dispatch)</Text>
          </Group>
        }
        size="lg"
      >
        <Stack gap="md">
          <Paper p="sm" radius="md" style={{ background: 'var(--cap-card-bg)', border: '1px solid var(--cap-border)' }}>
            <Group justify="space-between">
              <div>
                <Text size="xs" c="dimmed">待分发资产:</Text>
                <Text fw={700} size="sm" style={{ color: 'var(--cap-text-primary)' }}>
                  {dispatchItem?.name || dispatchItem?.id} ({dispatchType.toUpperCase()})
                </Text>
              </div>
              <Badge color="indigo">源节点: {summary?.target_name || selectedTargetId}</Badge>
            </Group>
          </Paper>

          <div>
            <Text size="xs" fw={700} mb="xs" style={{ color: 'var(--cap-text-secondary)' }}>
              勾选接收该能力的「目标节点 × Agent 运行时」矩阵:
            </Text>

            <SimpleGrid cols={2} spacing="xs">
              {targets.map((tgt) => (
                <Card key={tgt.id} withBorder radius="sm" p="xs" style={{ background: 'var(--cap-header-bg)' }}>
                  <Text fw={700} size="xs" mb={6}>{tgt.name} ({tgt.kind})</Text>
                  <Stack gap={4}>
                    {['codex', 'hermes', 'grok'].map((agentName) => {
                      const isChecked = dispatchMatrix.some(
                        (m) => m.target_id === tgt.id && m.agent === agentName
                      )
                      return (
                        <Checkbox
                          key={`${tgt.id}::${agentName}`}
                          size="xs"
                          label={`${agentName.toUpperCase()} 运行时`}
                          checked={isChecked}
                          onChange={(e) => {
                            if (e.currentTarget.checked) {
                              setDispatchMatrix([...dispatchMatrix, { target_id: tgt.id, agent: agentName }])
                            } else {
                              setDispatchMatrix(
                                dispatchMatrix.filter(
                                  (m) => !(m.target_id === tgt.id && m.agent === agentName)
                                )
                              )
                            }
                          }}
                        />
                      )
                    })}
                  </Stack>
                </Card>
              ))}
            </SimpleGrid>
          </div>

          {/* 核验审计报告 */}
          {dispatchReports && (
            <Paper p="sm" radius="md" style={{ background: 'var(--cap-header-bg)', border: '1px solid var(--cap-border)' }}>
              <Text size="xs" fw={700} mb="xs" style={{ color: 'var(--cap-text-primary)' }}>
                多端真实分发核验审计报告:
              </Text>
              <Stack gap={6}>
                {dispatchReports.map((r, idx) => (
                  <Group key={idx} justify="space-between" style={{ fontSize: 12 }}>
                    <Group gap="xs">
                      {r.ok ? <IconCheck size={14} color="#10b981" /> : <IconX size={14} color="#ef4444" />}
                      <span style={{ fontWeight: 600 }}>{r.target_name || r.target_id} · {r.agent}</span>
                    </Group>
                    <span style={{ color: r.ok ? 'var(--cap-text-secondary)' : '#ef4444' }}>
                      {r.message}
                    </span>
                  </Group>
                ))}
              </Stack>
            </Paper>
          )}

          <Group justify="flex-end" mt="md">
            <Button variant="default" onClick={() => setDispatchModalOpen(false)}>
              关闭
            </Button>
            <Button
              color="indigo"
              loading={dispatching}
              leftSection={<IconSend size={14} />}
              onClick={handleExecuteDispatch}
            >
              立即打包并执行跨端注入
            </Button>
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
            <Text size="xs" fw={600} mb="xs">
              选择安装目标节点:
            </Text>
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
            <Button variant="default" onClick={() => setInstallModalOpen(false)}>
              取消
            </Button>
            <Button
              variant="light"
              color="gray"
              loading={installing}
              onClick={() => handleInstall()}
            >
              直接安装到本地
            </Button>
            <Button
              color="indigo"
              loading={installing}
              onClick={() => {
                if (customPackage) handleOpenDispatchModal('skill', { name: customPackage })
              }}
            >
              进入分发矩阵
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}

// 保持向下兼容导出的别名
export const SkillsSettingsCard = CapabilitiesSettingsCard
