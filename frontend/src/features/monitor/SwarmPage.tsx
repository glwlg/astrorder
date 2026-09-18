import { useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Button,
  Group,
  Modal,
  NativeSelect,
  Stack,
  Text,
  TextInput,
  Textarea,
  Title,
} from '@mantine/core'
import {
  IconPlus,
  IconTopologyStarRing,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAstrorderStore } from '../../state/store'
import { isEphemeralSession, newCommandId, scopeKey } from '../../domain/semantics'
import { api } from '../../api/client'
import type { Agent } from '../../domain/types'
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
  const sessionMap = useAstrorderStore((state) => state.sessions)
  const sessions = useMemo(() => {
    return Object.values(sessionMap)
      .filter((session) => !isEphemeralSession(session))
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
  }, [sessionMap])
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
        const name =
          cid === 'local'
            ? '本机 (Local Windows)'
            : cid.includes('c5f75ce2')
            ? 'WSL2 Linux (127.0.0.1)'
            : cid.includes('1a32825b')
            ? 'Debian 远端主机 (192.168.1.100)'
            : cid
        list.push({ value: cid, label: name })
      }
    }
    return list
  }, [agents])

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

  // 统计主星与伴星数量
  const { rootCount, workerCount } = useMemo(() => {
    let roots = 0
    let workers = 0
    for (const s of sessions) {
      if (s.parent_session_id || s.parent_session_key) {
        workers++
      } else {
        const isParent = sessions.some(
          (c) =>
            c.parent_session_id === s.id ||
            c.parent_session_key === scopeKey(s.agent_id, s.id)
        )
        if (isParent) roots++
      }
    }
    return { rootCount: roots, workerCount: workers }
  }, [sessions])

  const handleOpenCreate = () => {
    const firstAgent = availableAgents[0]
    if (firstAgent) {
      setSelectedAgentId(firstAgent.id)
      const match = sessions.find((s) => s.agent_id === firstAgent.id);
      setWorkspace(match?.workspace || 'P:/workspace/glwlg/ai/astrorder')
    }
    setTitle('')
    setModelKey('')
    setEffort('')
    setInitialPrompt('用 @群星 指令发起协同：')
    setCreateOpened(true)
  }

  const handleCreate = async () => {
    if (!selectedAgentId || busy) return
    setBusy(true)
    try {
      const created = await api.createSession({
        agent_id: selectedAgentId,
        workspace: workspace.trim() || null,
        title: title.trim() || '星系主星协同会话',
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
      if (effort) {
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

      notifications.show({
        color: 'teal',
        message: `🌟 主星会话「${title.trim() || '新星系'}」已成功点亮启动`,
      })
      setCreateOpened(false)
    } catch (e: any) {
      notifications.show({
        color: 'red',
        message: e.message || '启动主星会话失败',
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        padding: '16px 20px',
        boxSizing: 'border-box',
        background: 'var(--astr-canvas-bg, #f8f9fb)',
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
            <IconTopologyStarRing size={24} color="#5B5BD6" />
            <Title order={2} size="h3" fw={700} c="var(--astr-text, #111827)">
              星图阵列
            </Title>
          </Group>
          <div style={{ width: 1, height: 18, background: 'var(--astr-border, #E5E7EB)' }} />
          <Group gap={6}>
            <Badge size="sm" variant="light" color="indigo">
              {rootCount} 个活跃星系
            </Badge>
            <Badge size="sm" variant="light" color="cyan">
              {workerCount} 个协同伴星
            </Badge>
          </Group>
        </Group>

        <Group gap="xs">
          <Button
            color="indigo"
            size="sm"
            leftSection={<IconPlus size={16} />}
            onClick={handleOpenCreate}
            style={{ fontWeight: 600, boxShadow: '0 1px 3px rgba(91, 91, 214, 0.25)' }}
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
            <IconTopologyStarRing size={20} color="#5B5BD6" />
            <Text fw={700} size="md">
              启动群星 · 新建主星协同任务
            </Text>
          </Group>
        }
        size="lg"
        radius="lg"
        centered
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            主星（Alpha）负责作为任务调度中枢，派生并统筹其他服务器上的伴星 Agent 协同作战。
          </Text>

          {/* 1. 服务器 / 主机环境 */}
          <NativeSelect
            label="目标运行服务器 (Server / Host)"
            description="选择调度中枢运行所在的物理机或远程容器"
            value={selectedMachine}
            onChange={(e) => {
              setSelectedMachine(e.currentTarget.value)
              const first = Object.values(agents).find(
                (a: Agent) =>
                  a.status === 'ready' &&
                  (e.currentTarget.value === 'all' || (a.connection_id || 'local') === e.currentTarget.value)
              )
              if (first) {
                setSelectedAgentId(first.id)
                const match = sessions.find((s) => s.agent_id === first.id);
                if (match?.workspace) setWorkspace(match.workspace)
              }
            }}
            data={machineChoices}
            disabled={busy}
          />

          {/* 2. 选择 Agent */}
          <NativeSelect
            label="指派主星 Agent"
            description="执行调度中枢职责的智能体"
            value={selectedAgentId}
            onChange={(e) => {
              setSelectedAgentId(e.currentTarget.value)
              const match = sessions.find((s) => s.agent_id === e.currentTarget.value);
              if (match?.workspace) setWorkspace(match.workspace)
            }}
            data={[
              { value: '', label: '请选择主星 Agent' },
              ...availableAgents.map((a: Agent) => ({
                value: a.id,
                label: `${a.name || a.id} (${a.connection_id || '本机'})`,
              })),
            ]}
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
            <NativeSelect
              label="运行模型 (Model)"
              value={modelKey}
              onChange={(e) => setModelKey(e.currentTarget.value)}
              data={modelChoices}
              disabled={busy || !selectedAgentId}
            />

            <NativeSelect
              label="思考程度 (Reasoning Effort)"
              value={effort}
              onChange={(e) => setEffort(e.currentTarget.value)}
              data={EFFORT_OPTIONS}
              disabled={busy}
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
            placeholder="例如：用 @群星 功能采集各个集群节点的负载并生成体检报告"
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
              color="indigo"
              onClick={() => void handleCreate()}
              loading={busy}
              disabled={!selectedAgentId}
            >
              启动主星并进入星图
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
