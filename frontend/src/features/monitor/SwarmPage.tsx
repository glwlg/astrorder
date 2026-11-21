import { useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Button,
  Group,
  Modal,
  Stack,
  Text,
  TextInput,
  Textarea,
  Title,
} from '@mantine/core'
import {
  IconBolt,
  IconCpu,
  IconPlus,
  IconServer,
  IconTopologyStarRing,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { selectSessions, useAstrorderStore } from '../../state/store'
import { newCommandId, scopeKey } from '../../domain/semantics'
import { api } from '../../api/client'
import type { Agent } from '../../domain/types'
import { buildSwarmTopology } from '../../domain/swarmTopology'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { BrandedSelect } from '../../components/BrandedSelect'
import { SwarmDagView } from './SwarmDagView'

const STANDARD_MODELS: Record<string, Array<{ provider: string; model: string; label: string }>> = {
  codex: [
    { provider: 'openai', model: 'gpt-5.6-sol', label: 'GPT-5.6 Sol (前沿代码智能体)' },
    { provider: 'openai', model: 'gpt-5.6-luna', label: 'GPT-5.6 Luna (极速轻量)' },
    { provider: 'openai', model: 'gpt-6-astra', label: 'GPT-6 Astra (复杂多步骤推演)' },
    { provider: 'openai', model: 'gpt-5.6-terra', label: 'GPT-5.6 Terra (平衡型编码)' },
  ],
  hermes: [
    { provider: 'google', model: 'gemini-3.8-flash', label: 'Gemini 3.8 Flash (极速调度)' },
    { provider: 'openai', model: 'gpt-5.6-sol', label: 'GPT-5.6 Sol' },
    { provider: 'anthropic', model: 'claude-3-7-sonnet', label: 'Claude 3.7 Sonnet' },
  ],
  grok: [
    { provider: 'xai', model: 'grok-4.6', label: 'Grok 4.6 (深度推演)' },
    { provider: 'xai', model: 'grok-composer-2.5-fast', label: 'Grok Composer 2.5 Fast' },
  ],
  antigravity: [
    { provider: 'antigravity', model: 'gemini-3.8-pro', label: 'Gemini 3.8 Pro (复杂协同推演)' },
    { provider: 'antigravity', model: 'gemini-3.8-flash', label: 'Gemini 3.8 Flash (极速调度)' },
  ],
}

const EFFORT_OPTIONS = [
  { value: '', label: '默认思考程度' },
  { value: 'low', label: '低 (Low - 极速响应)' },
  { value: 'medium', label: '中 (Medium - 平衡推理)' },
  { value: 'high', label: '高 (High - 深入严谨)' },
  { value: 'xhigh', label: '超高 (Extra High - 极繁复杂)' },
  { value: 'max', label: '最大 (Max - 极限算力)' },
]

export function SwarmPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const activeRootKey = searchParams.get('root')
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)

  const [createOpened, setCreateOpened] = useState(false)
  const [selectedMachine, setSelectedMachine] = useState('all')
  const [selectedAgentId, setSelectedAgentId] = useState('')
  const [workspace, setWorkspace] = useState('')
  const [title, setTitle] = useState('')
  const [modelKey, setModelKey] = useState('')
  const [effort, setEffort] = useState('')
  const [initialPrompt, setInitialPrompt] = useState('')
  const [busy, setBusy] = useState(false)
  const [connectionNames, setConnectionNames] = useState<Record<string, string>>({})

  useEffect(() => {
    if (!createOpened) return
    let active = true
    void api.getConnections().then(({ ssh }) => {
      if (active) setConnectionNames(Object.fromEntries(ssh.items.map((connection) => [connection.id, connection.display_name])))
    }).catch(() => {})
    return () => { active = false }
  }, [createOpened])

  useEffect(() => {
    const onOpen = () => handleOpenCreate()
    window.addEventListener('astrorder:open-create-swarm', onOpen)
    return () => window.removeEventListener('astrorder:open-create-swarm', onOpen)
  }, [])

  // 提取所有可用服务器/主机环境列表
  const machineChoices = useMemo(() => {
    const list: Array<{ value: string; label: string }> = [{ value: 'all', label: '全部服务器' }]
    const seen = new Set<string>()
    for (const a of Object.values(agents)) {
      const cid = a.connection_id || 'local'
      if (!seen.has(cid)) {
        seen.add(cid)
        list.push({ value: cid, label: cid === 'local' ? '本机' : connectionNames[cid] || cid })
      }
    }
    return list
  }, [agents, connectionNames])

  // 根据选定服务器过滤可选 Agent
  const availableAgents = useMemo(() => {
    return Object.values(agents).filter((a: Agent) => {
      if (a.status !== 'ready') return false
      if (selectedMachine === 'all') return true
      return (a.connection_id || 'local') === selectedMachine
    })
  }, [agents, selectedMachine])

  // 选定 Agent 的模型候选表
  const modelChoices = useMemo(() => {
    const agent = agents[selectedAgentId]
    if (!agent) return []
    const presets = STANDARD_MODELS[agent.kind] || STANDARD_MODELS.codex
    return [
      { value: '', label: '使用 Agent 默认模型' },
      ...presets.map((m) => ({
        value: JSON.stringify([m.provider, m.model]),
        label: m.label,
      })),
    ]
  }, [agents, selectedAgentId])

  // 统计主星与伴星数量（采用纯拓扑 helper）
  const topology = useMemo(() => {
    return buildSwarmTopology(sessions)
  }, [sessions])
  const rootCount = topology.totalRootCount
  const workerCount = topology.totalWorkerCount

  const handleOpenCreate = () => {
    const firstAgent = availableAgents[0]
    if (firstAgent) {
      setSelectedAgentId(firstAgent.id)
      const match = sessions.find((s) => s.agent_id === firstAgent.id);
      setWorkspace(match?.workspace || '')
    }
    setTitle('')
    setModelKey('')
    setEffort('')
    setInitialPrompt('')
    setCreateOpened(true)
  }

  const handleCreate = async () => {
    if (!selectedAgentId || busy) return
    setBusy(true)
    try {
      const created = await api.createSession({
        agent_id: selectedAgentId,
        workspace: workspace.trim() || null,
        title: title.trim() || '协作主星会话',
      })

      useAstrorderStore.setState((state) => ({
        sessions: {
          ...state.sessions,
          [scopeKey(created.agent_id, created.id)]: created,
        },
      }))

      // 配置所选模型
      if (modelKey) {
        try {
          const [provider, model] = JSON.parse(modelKey)
          await api.setSessionModel(created.id, created.agent_id, provider, model)
        } catch {}
      }

      // 配置思考程度
      if (effort && agents[selectedAgentId]?.kind !== 'antigravity') {
        try {
          await api.setSessionReasoning(created.id, created.agent_id, effort)
        } catch {}
      }

      // 发送初始指令
      if (initialPrompt.trim()) {
        await api.createCommand({
          id: newCommandId(),
          agent_id: created.agent_id,
          session_id: created.id,
          action: 'send',
          text: initialPrompt.trim(),
          attachment_ids: [],
          target_id: null,
        })
      }

      const createdKey = scopeKey(created.agent_id, created.id)
      navigate(`/swarm?root=${encodeURIComponent(createdKey)}`)

      notifications.show({
        color: 'teal',
        message: `🌟 会话「${title.trim() || created.title || '新会话'}」已启动，Agent 后续自主委派子会话将自动在星图显式展开`,
      })
      setCreateOpened(false)
    } catch (e: any) {
      notifications.show({
        color: 'red',
        message: e.message || '启动会话失败',
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="route-page swarm-page"
      style={{
        display: 'flex',
        flexDirection: 'column',
        maxWidth: 'none',
        padding: '16px 20px',
        boxSizing: 'border-box',
        background: 'var(--astr-canvas-bg, var(--astr-bg))',
        overflow: 'hidden',
      }}
    >
      {/* 顶部标题与阵列控制区 */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 14,
          flexShrink: 0,
        }}
      >
        <Group gap="sm">
          <Group gap="xs">
            <IconTopologyStarRing size={24} color="var(--astr-indigo)" />
            <Title order={2} size="h3" fw={700} c="var(--astr-text)">
              星图阵列
            </Title>
          </Group>
          <div style={{ width: 1, height: 18, background: 'var(--astr-border)' }} />
          <Group gap={6}>
            <Badge size="sm" variant="light" color="violet">
              {rootCount} 个活跃星系
            </Badge>
            <Badge size="sm" variant="light" color="violet">
              {workerCount} 个协同伴星
            </Badge>
          </Group>
        </Group>

        <Group gap="xs">
          <Button
            color="violet"
            size="sm"
            leftSection={<IconPlus size={16} />}
            onClick={handleOpenCreate}
            style={{ fontWeight: 600, boxShadow: '0 2px 10px color-mix(in oklab, var(--astr-indigo) 28%, transparent)' }}
          >
            启动主星会话
          </Button>
        </Group>
      </div>

      {/* 独立全高度星图画布图层 */}
      <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
        <SwarmDagView
          allSessions={sessions}
          agents={agents}
          activeRootKey={activeRootKey}
          onOpenSession={(s) =>
            navigate(`/chat/${encodeURIComponent(s.id)}?agent_id=${encodeURIComponent(s.agent_id)}`)
          }
        />
      </div>

      {/* 启动主星专属弹窗：选服务器、项目、Agent、模型、思考程度 */}
      <Modal
        opened={createOpened}
        onClose={() => !busy && setCreateOpened(false)}
        title={
          <Group gap="xs">
            <IconTopologyStarRing size={20} color="var(--astr-indigo)" />
            <Text fw={700} size="md">
              启动协同 · 新建主星会话
            </Text>
          </Group>
        }
        size="lg"
        radius="lg"
        centered
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            创建会话后，Agent 自主通过委派（Delegation）发起子会话将自动在星图形成多层星系拓扑，无需提前设定先决门槛或特定群星标记。
          </Text>

          {/* 1. 服务器 / 主机环境 */}
          <BrandedSelect
            label="目标运行服务器 (Server / Host)"
            description="选择调度中枢运行所在的物理机或远程容器"
            value={selectedMachine}
            onChange={(val) => {
              setSelectedMachine(val)
              const first = Object.values(agents).find(
                (a: Agent) =>
                  a.status === 'ready' &&
                  (val === 'all' || (a.connection_id || 'local') === val)
              )
              if (first) {
                setSelectedAgentId(first.id)
                const match = sessions.find((s) => s.agent_id === first.id)
                setWorkspace(match?.workspace || '')
              }
            }}
            options={machineChoices.map((m) => ({
              value: m.value,
              label: m.label,
              icon: <IconServer size={14} color="var(--astr-indigo)" />,
            }))}
            disabled={busy}
          />

          {/* 2. 选择 Agent */}
          <BrandedSelect
            label="指派主星 Agent"
            description="执行调度中枢职责的智能体"
            placeholder="请选择主星 Agent"
            value={selectedAgentId}
            onChange={(nextId) => {
              setSelectedAgentId(nextId)
              const match = sessions.find((s) => s.agent_id === nextId)
              setWorkspace(match?.workspace || '')
            }}
            options={availableAgents.map((a: Agent) => ({
              value: a.id,
              label: `${a.name || a.id} (${a.connection_id || '本机'})`,
              icon: <AgentBrandIcon kind={a.kind} size={15} />,
            }))}
            disabled={busy}
          />

          {/* 3. 关联工作区与项目 */}
          <TextInput
            label="工作区目录 (Workspace)"
            placeholder="执行代码库绝对路径"
            value={workspace}
            onChange={(e) => setWorkspace(e.currentTarget.value)}
            disabled={busy}
          />

          {/* 4. 模型与思考程度双列组合 */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            <BrandedSelect
              label="运行模型 (Model)"
              placeholder="默认模型"
              value={modelKey}
              onChange={setModelKey}
              options={modelChoices.map((m) => ({
                value: m.value,
                label: m.label,
                icon: <IconCpu size={14} color="var(--astr-indigo)" />,
              }))}
              disabled={busy || !selectedAgentId}
            />

            <BrandedSelect
              label="思考程度 (Reasoning Effort)"
              placeholder="默认思考程度"
              value={agents[selectedAgentId]?.kind === 'antigravity' ? '' : effort}
              onChange={setEffort}
              options={EFFORT_OPTIONS.map((opt) => ({
                value: opt.value,
                label: opt.label,
                icon: <IconBolt size={14} color="var(--astr-indigo)" />,
              }))}
              disabled={busy || agents[selectedAgentId]?.kind === 'antigravity'}
            />
          </div>

          {/* 5. 任务标题 */}
          <TextInput
            label="星系任务名称 (选填)"
            placeholder="留空由 Agent 自动概括"
            value={title}
            onChange={(e) => setTitle(e.currentTarget.value)}
            disabled={busy}
          />

          {/* 6. 初始协同指令 */}
          <Textarea
            label="初始作战任务描述 / 指令"
            placeholder="例如：分析当前系统状态，并按需委派子会话并发处理各子任务"
            minRows={3}
            value={initialPrompt}
            onChange={(e) => setInitialPrompt(e.currentTarget.value)}
            disabled={busy}
          />

          <Group justify="flex-end" gap="sm" mt="sm">
            <Button variant="default" onClick={() => setCreateOpened(false)} disabled={busy}>
              取消
            </Button>
            <Button
              color="violet"
              onClick={() => void handleCreate()}
              loading={busy}
              disabled={!selectedAgentId}
            >
              创建主星并进入星图
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
