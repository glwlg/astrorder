import { useCallback, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { Badge, Button, Group, Menu, SimpleGrid, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  IconAlertTriangle,
  IconArrowRight,
  IconBolt,
  IconCheck,
  IconChevronDown,
  IconClock,
  IconCpu,
  IconFolder,
  IconFolderPlus,
  IconLayoutGrid,
  IconSend,
  IconUsers,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { connectionName } from '../../components/AgentSessionFilter'
import { BrandMark } from '../../components/BrandMark'
import { ShinyText } from '../../components/animations/ShinyText'
import { displaySessionTitle, sessionActivityStatus } from '../../components/sessionRailModel'
import { isUserInput } from '../../components/UserInputCard'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Approval, Command, Project, Session } from '../../domain/types'
import { selectProjects, useAstrorderStore } from '../../state/store'
import { formatTokens } from '../analytics/usageFormat'
import { useShortcutStore } from '../shortcuts/shortcuts'
import './welcomeDeck.css'

interface WelcomeViewProps {
  sessions: Session[]
  agents: Record<string, Agent>
  onNewSession?: () => void
}

interface WorkspaceChoice {
  key: string
  label: string
  workspace: string | null
  connectionId: string | null
  projectName: string | null
}

function agentEnv(agent: Agent): string {
  return agent.connection_id || 'local'
}

function shortAgentLabel(agent: Agent): string {
  const kind = agentKindLabel(agent.kind)
  const env = connectionName(agent)
  return `${kind} · ${env}`
}

export function WelcomeView({ sessions, agents, onNewSession }: WelcomeViewProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const inputId = useId()
  const formRef = useRef<HTMLFormElement>(null)

  const projects = useAstrorderStore(useShallow(selectProjects))
  const approvalsMap = useAstrorderStore((state) => state.approvals)

  const todayUsage = useQuery({
    queryKey: ['astrorder', 'analytics', 'today-models'],
    queryFn: () => api.getAnalyticsUsage({ range: 'today', surface: 'all' }),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  })

  const readyAgents = useMemo(
    () => Object.values(agents).filter((agent) => agent.status === 'ready'),
    [agents],
  )

  const workspaceChoices = useMemo<WorkspaceChoice[]>(() => {
    const seen = new Set<string>()
    const list: WorkspaceChoice[] = []
    // Prioritize recently active session workspaces first so Omni-Composer defaults to the user's current project
    for (const session of [...sessions].sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))) {
      const ws = session.workspace?.trim() || null
      if (!ws) continue
      const conn = session.connection_id || agents[session.agent_id]?.connection_id || 'local'
      const dedupeKey = `${conn}::${ws}`
      if (seen.has(dedupeKey)) continue
      seen.add(dedupeKey)
      const label = session.project_name || ws.split(/[/\\]/).filter(Boolean).pop() || ws
      list.push({ key: dedupeKey, label, workspace: ws, connectionId: conn, projectName: label })
    }
    const addProject = (project: Project) => {
      const ws = project.workspace?.trim() || null
      if (!ws) return
      const conn = project.connection_id || (project.agent_id ? agents[project.agent_id]?.connection_id || 'local' : null)
      const dedupeKey = `${conn || 'any'}::${ws}`
      if (seen.has(dedupeKey)) return
      seen.add(dedupeKey)
      const label = project.project_name || ws.split(/[/\\]/).filter(Boolean).pop() || ws
      list.push({ key: dedupeKey, label, workspace: ws, connectionId: conn, projectName: label })
    }
    for (const project of projects) addProject(project)
    return list
  }, [agents, projects, sessions])

  const [prompt, setPrompt] = useState('')
  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState<string>('')
  const [selectedAgentId, setSelectedAgentId] = useState<string>('')
  const [submitting, setSubmitting] = useState(false)
  const [busyApprovals, setBusyApprovals] = useState<Record<string, boolean>>({})

  const effectiveWorkspaceKey = useMemo(() => {
    if (selectedWorkspaceKey === '__none__') return '__none__'
    if (selectedWorkspaceKey && workspaceChoices.some((item) => item.key === selectedWorkspaceKey)) {
      return selectedWorkspaceKey
    }
    return workspaceChoices[0]?.key || '__none__'
  }, [selectedWorkspaceKey, workspaceChoices])

  const activeWorkspace = useMemo(
    () => workspaceChoices.find((item) => item.key === effectiveWorkspaceKey) || null,
    [effectiveWorkspaceKey, workspaceChoices],
  )

  const compatibleAgents = useMemo(() => {
    if (!activeWorkspace?.connectionId) return readyAgents
    const matched = readyAgents.filter((agent) => agentEnv(agent) === activeWorkspace.connectionId)
    return matched.length > 0 ? matched : readyAgents
  }, [activeWorkspace, readyAgents])

  const effectiveAgentId = useMemo(() => {
    if (selectedAgentId && compatibleAgents.some((agent) => agent.id === selectedAgentId)) {
      return selectedAgentId
    }
    // Prefer the most recently used compatible ready agent
    const recentAgentId = sessions.find((s) => compatibleAgents.some((a) => a.id === s.agent_id))?.agent_id
    return recentAgentId || compatibleAgents[0]?.id || ''
  }, [compatibleAgents, selectedAgentId, sessions])

  const activeAgent = effectiveAgentId ? agents[effectiveAgentId] : undefined

  const pendingApprovals = useMemo(
    () => Object.values(approvalsMap).filter((approval) => approval.state === 'pending' && approval.agent_id && approval.session_id),
    [approvalsMap],
  )

  const attentionSessions = useMemo(() => {
    const approvalKeys = new Set(pendingApprovals.map((a) => scopeKey(a.agent_id || '', a.session_id || '')))
    const recentErrorCutoff = Date.now() - 2 * 60 * 60 * 1000
    return sessions.filter((session) => {
      const key = scopeKey(session.agent_id, session.id)
      if (approvalKeys.has(key)) return false
      if (session.status === 'waiting_approval') return true
      if (session.status === 'error' && session.updated_at) {
        const ts = Date.parse(session.updated_at)
        return Number.isFinite(ts) && ts >= recentErrorCutoff
      }
      return false
    })
  }, [pendingApprovals, sessions])

  const runningCount = useMemo(
    () => sessions.filter((s) => sessionActivityStatus(s) === 'running').length,
    [sessions],
  )

  // Float active running sessions first, followed by most recently updated sessions
  const recentSessions = useMemo(
    () =>
      [...sessions]
        .sort((a, b) => {
          const aRun = sessionActivityStatus(a) === 'running' ? 1 : 0
          const bRun = sessionActivityStatus(b) === 'running' ? 1 : 0
          if (aRun !== bRun) return bRun - aRun
          return (b.updated_at || '').localeCompare(a.updated_at || '')
        })
        .slice(0, 4),
    [sessions],
  )

  const todaySummary = useMemo(() => {
    const data = todayUsage.data
    if (!data) return null
    if (data.range === 'today') {
      return {
        requests: Number(data.summary.requests || 0),
        totalTokens: Number(data.summary.totalTokens || 0),
      }
    }
    const now = new Date()
    const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    const day = data.days.find((row) => row.date === today)
    return day ? { requests: Number(day.requests || 0), totalTokens: Number(day.totalTokens || 0) } : null
  }, [todayUsage.data])

  const bindings = useShortcutStore((state) => state.bindings)
  const shortcuts = [
    { id: 'quickOpen', key: bindings.quickOpen || '未设置', desc: '搜索文件' },
    { id: 'sidebar', key: bindings.sidebar || '未设置', desc: '收起侧栏' },
    { id: 'sideChat', key: bindings.sideChat || '未设置', desc: '侧边副会话' },
    { id: 'terminal', key: bindings.terminal || '未设置', desc: '内置终端' },
    { id: 'browser', key: bindings.browser || '未设置', desc: '内置浏览器' },
  ]

  const handleOmniSubmit = useCallback(
    async (event?: FormEvent<HTMLFormElement>) => {
      event?.preventDefault()
      const text = prompt.trim()
      if (!text || !effectiveAgentId || submitting) return
      setSubmitting(true)
      try {
        const created = await api.createSession({
          agent_id: effectiveAgentId,
          workspace: activeWorkspace?.workspace || null,
          title: null,
          project_name: activeWorkspace?.projectName || null,
        })
        useAstrorderStore.setState((state) => ({
          sessions: {
            ...state.sessions,
            [scopeKey(created.agent_id, created.id)]: created,
          },
        }))
        const commandId = newCommandId()
        const sent = await api.createCommand({
          id: commandId,
          agent_id: created.agent_id,
          session_id: created.id,
          action: 'send',
          text,
          attachment_ids: [],
          target_id: null,
        })
        useAstrorderStore.getState().mergeCommands([sent])
        setPrompt('')
        navigate(`/chat/${encodeURIComponent(created.id)}?agent_id=${encodeURIComponent(created.agent_id)}`)
      } catch (error) {
        notifications.show({
          color: 'red',
          message: error instanceof Error ? error.message : '任务启动失败，请重试',
        })
      } finally {
        setSubmitting(false)
      }
    },
    [activeWorkspace, effectiveAgentId, navigate, prompt, submitting],
  )

  // IME-safe Enter-to-submit: guards native isComposing and Safari/IME keyCode 229
  const handleTextareaKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      if (event.nativeEvent.isComposing || event.keyCode === 229) {
        return
      }
      formRef.current?.requestSubmit()
    }
  }

  const handleInlineApproval = useCallback(
    async (approval: Approval, action: 'approve' | 'cancel') => {
      if (!approval.agent_id || !approval.session_id || busyApprovals[approval.id]) return
      const agent = agents[approval.agent_id]
      if (!agent?.capabilities.includes('approvals')) return
      setBusyApprovals((prev) => ({ ...prev, [approval.id]: true }))
      const id = newCommandId()
      const command: Command = {
        id,
        session_id: approval.session_id,
        agent_id: approval.agent_id,
        action,
        state: 'received',
        text: '',
        attachments: [],
        created_at: new Date().toISOString(),
        error: null,
        target_id: approval.target_id || approval.id,
      }
      const store = useAstrorderStore.getState()
      store.addOutbox(command, 'submitting')
      try {
        const result = await api.createCommand({
          id,
          agent_id: approval.agent_id,
          session_id: approval.session_id,
          action,
          text: '',
          attachment_ids: [],
          target_id: approval.target_id || approval.id,
        })
        store.mergeCommands([result])
        await queryClient.invalidateQueries({
          queryKey: ['astrorder', 'commands', approval.agent_id, approval.session_id],
        })
        notifications.show({
          color: action === 'approve' ? 'teal' : 'gray',
          message: action === 'approve' ? '已批准该请求' : '已拒绝该请求',
        })
      } catch (error) {
        const msg = error instanceof Error ? error.message : '审批操作失败'
        store.markOutboxError(
          approval.agent_id,
          approval.session_id,
          id,
          msg,
          error instanceof ApiError && error.status < 500 ? 'failed' : 'unknown',
        )
        notifications.show({ color: 'red', message: msg })
      } finally {
        setBusyApprovals((prev) => {
          const next = { ...prev }
          delete next[approval.id]
          return next
        })
      }
    },
    [agents, busyApprovals, queryClient],
  )

  const hasAttentionSection = pendingApprovals.length > 0 || attentionSessions.length > 0

  return (
    <div className="welcome-workspace-stage">
      <div className="welcome-ambient-backdrop" aria-hidden="true" />
      <div className="welcome-inner-container">
        <Stack align="center" gap={6} mb="lg">
          <div className="welcome-logo-badge">
            <BrandMark size={52} className="welcome-logo-img" />
          </div>
          <Title order={1} size="h3" fw={700} style={{ letterSpacing: '-0.01em', margin: 0 }}>
            <ShinyText text="星序 · 协作指挥台" speed={2.5} />
          </Title>
          <Text c="dimmed" size="xs" ta="center" maw={480}>
            群星各有所长，协作自有秩序。直接下达新任务，或从星阵态势切入现场。
          </Text>
        </Stack>

        {/* 全域星令输入框 (Omni-Composer) */}
        <form
          ref={formRef}
          className="welcome-omni-form"
          onSubmit={(event) => void handleOmniSubmit(event)}
          aria-label="全域任务启动器"
        >
          <div
            className="welcome-omni-shell"
            data-liquid-surface="panel"
            data-liquid-snapshot=".welcome-ambient-backdrop"
          >
            <label htmlFor={inputId} className="welcome-omni-label">
              输入新任务指令
            </label>
            <textarea
              id={inputId}
              className="welcome-omni-textarea"
              placeholder={
                readyAgents.length > 0
                  ? '描述你要推进的任务，Enter 直接委派星辰启动会话（Shift+Enter 换行）…'
                  : '当前暂无就绪 Agent，请先在左下角「设置 → 连接」接入工作环境…'
              }
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              onKeyDown={handleTextareaKeyDown}
              disabled={submitting || readyAgents.length === 0}
            />
            <div className="welcome-omni-footer">
              <div className="welcome-omni-selectors">
                <Menu withinPortal hideDetached={false} position="bottom-start" offset={6} radius="md" shadow="lg" width={268}>
                  <Menu.Target>
                    <button
                      type="button"
                      className="welcome-omni-select-trigger"
                      aria-label="选择目标工作区"
                      title={activeWorkspace?.workspace || '选择目标项目或工作区'}
                      disabled={submitting}
                    >
                      <IconFolder size={13} color="var(--astr-indigo)" aria-hidden="true" />
                      <span className="welcome-omni-select-label">
                        {activeWorkspace ? activeWorkspace.label : '不指定工作区'}
                      </span>
                      <IconChevronDown size={12} className="welcome-omni-chevron" aria-hidden="true" />
                    </button>
                  </Menu.Target>
                  <Menu.Dropdown className="welcome-omni-menu-dropdown" data-liquid-surface="menu" data-liquid-engine="css">
                    <div className="welcome-omni-menu-scroll">
                      {workspaceChoices.map((choice) => {
                        const selected = choice.key === effectiveWorkspaceKey
                        return (
                          <Menu.Item
                            key={choice.key}
                            className={`welcome-omni-menu-item${selected ? ' is-selected' : ''}`}
                            leftSection={<IconFolder size={14} color={selected ? 'var(--astr-indigo)' : 'var(--astr-muted)'} />}
                            rightSection={selected ? <IconCheck size={14} color="var(--astr-indigo)" /> : null}
                            onClick={() => setSelectedWorkspaceKey(choice.key)}
                          >
                            <div className="welcome-omni-menu-copy">
                              <span className="welcome-omni-menu-title">{choice.label}</span>
                              {choice.workspace && (
                                <span className="welcome-omni-menu-sub">{choice.workspace}</span>
                              )}
                            </div>
                          </Menu.Item>
                        )
                      })}
                      {workspaceChoices.length > 0 && <Menu.Divider />}
                      <Menu.Item
                        className={`welcome-omni-menu-item${effectiveWorkspaceKey === '__none__' ? ' is-selected' : ''}`}
                        leftSection={<IconFolder size={14} color="var(--astr-muted)" />}
                        rightSection={effectiveWorkspaceKey === '__none__' ? <IconCheck size={14} color="var(--astr-indigo)" /> : null}
                        onClick={() => setSelectedWorkspaceKey('__none__')}
                      >
                        <span className="welcome-omni-menu-title">不指定工作区</span>
                      </Menu.Item>
                    </div>
                  </Menu.Dropdown>
                </Menu>

                <Menu withinPortal hideDetached={false} position="bottom-start" offset={6} radius="md" shadow="lg" width={240}>
                  <Menu.Target>
                    <button
                      type="button"
                      className="welcome-omni-select-trigger"
                      aria-label="选择执行 Agent"
                      title="选择执行 Agent"
                      disabled={submitting || compatibleAgents.length === 0}
                    >
                      {activeAgent ? (
                        <AgentBrandIcon kind={activeAgent.kind} size={13} />
                      ) : (
                        <IconCpu size={13} color="var(--astr-muted)" aria-hidden="true" />
                      )}
                      <span className="welcome-omni-select-label">
                        {activeAgent ? shortAgentLabel(activeAgent) : '暂无就绪 Agent'}
                      </span>
                      <IconChevronDown size={12} className="welcome-omni-chevron" aria-hidden="true" />
                    </button>
                  </Menu.Target>
                  <Menu.Dropdown className="welcome-omni-menu-dropdown" data-liquid-surface="menu" data-liquid-engine="css">
                    <div className="welcome-omni-menu-scroll">
                      {compatibleAgents.map((agent) => {
                        const selected = agent.id === effectiveAgentId
                        return (
                          <Menu.Item
                            key={agent.id}
                            className={`welcome-omni-menu-item${selected ? ' is-selected' : ''}`}
                            leftSection={<AgentBrandIcon kind={agent.kind} size={15} />}
                            rightSection={selected ? <IconCheck size={14} color="var(--astr-indigo)" /> : null}
                            onClick={() => setSelectedAgentId(agent.id)}
                          >
                            <span className="welcome-omni-menu-title">{shortAgentLabel(agent)}</span>
                          </Menu.Item>
                        )
                      })}
                    </div>
                  </Menu.Dropdown>
                </Menu>
              </div>

              <Group gap="xs" wrap="nowrap">
                <span className="welcome-omni-hint">Enter 发送</span>
                <button
                  type="submit"
                  className="welcome-omni-submit"
                  disabled={submitting || !prompt.trim() || !effectiveAgentId}
                >
                  <IconSend size={13} aria-hidden="true" />
                  <span>{submitting ? '正在启动…' : '启动任务'}</span>
                </button>
              </Group>
            </div>
          </div>
        </form>

        {/* 待你介入 (Conditional Needs Attention Strip) */}
        {hasAttentionSection && (
          <div className="welcome-section" style={{ marginBottom: 20 }}>
            <Group justify="space-between" align="center" mb={8}>
              <Group gap={6}>
                <IconAlertTriangle size={14} color="var(--astr-yellow)" />
                <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.04em' }}>
                  待你介入
                </Text>
                <Badge size="xs" variant="light" color="yellow">
                  {pendingApprovals.length + attentionSessions.length}
                </Badge>
              </Group>
            </Group>
            <Stack gap="xs">
              {pendingApprovals.slice(0, 3).map((approval) => {
                const session = sessions.find(
                  (s) => s.agent_id === approval.agent_id && s.id === approval.session_id,
                )
                const agent = approval.agent_id ? agents[approval.agent_id] : undefined
                const canDecide = !isUserInput(approval) && agent?.capabilities.includes('approvals') === true
                const busy = Boolean(busyApprovals[approval.id])
                return (
                  <div
                    key={approval.id}
                    className="welcome-card welcome-attention-card"
                    data-liquid-surface="card"
                    data-liquid-snapshot=".welcome-ambient-backdrop"
                  >
                    <Group justify="space-between" align="center" gap="sm" wrap="nowrap">
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <AgentBrandIcon kind={agent?.kind} size={16} />
                        <div style={{ minWidth: 0 }}>
                          <Text fw={650} size="sm" truncate>
                            {session ? displaySessionTitle(session) : '待审批请求'}
                          </Text>
                          <Text size="xs" c="dimmed" truncate>
                            {approval.title || approval.detail || '等待授权确认'}
                          </Text>
                        </div>
                      </Group>
                      <Group gap={6} wrap="nowrap">
                        {canDecide ? (
                          <>
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="gray"
                              disabled={busy}
                              onClick={() => void handleInlineApproval(approval, 'cancel')}
                            >
                              拒绝
                            </Button>
                            <Button
                              size="compact-xs"
                              color="violet"
                              loading={busy}
                              leftSection={<IconCheck size={12} />}
                              onClick={() => void handleInlineApproval(approval, 'approve')}
                            >
                              批准
                            </Button>
                          </>
                        ) : null}
                        {approval.agent_id && approval.session_id && (
                          <Button
                            size="compact-xs"
                            variant="light"
                            color="yellow"
                            rightSection={<IconArrowRight size={12} />}
                            onClick={() =>
                              navigate(`/chat/${encodeURIComponent(approval.session_id!)}?agent_id=${encodeURIComponent(approval.agent_id!)}`)
                            }
                          >
                            处理
                          </Button>
                        )}
                      </Group>
                    </Group>
                  </div>
                )
              })}
              {attentionSessions.slice(0, 2).map((session) => (
                <UnstyledButton
                  key={scopeKey(session.agent_id, session.id)}
                  className={`welcome-card welcome-session-card welcome-attention-card${session.status === 'error' ? ' is-error' : ''}`}
                  data-liquid-surface="card"
                  data-liquid-snapshot=".welcome-ambient-backdrop"
                  onClick={() =>
                    navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
                  }
                >
                  <Group justify="space-between" align="center" wrap="nowrap">
                    <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                      <AgentBrandIcon kind={agents[session.agent_id]?.kind} size={16} />
                      <Text fw={650} size="sm" truncate>{displaySessionTitle(session)}</Text>
                    </Group>
                    <Badge size="xs" variant="light" color={session.status === 'error' ? 'red' : 'yellow'}>
                      {session.status === 'error' ? '异常待查' : '等待确认'}
                    </Badge>
                  </Group>
                </UnstyledButton>
              ))}
            </Stack>
          </div>
        )}

        {/* 实时脉搏 & 最近活跃会话 */}
        {recentSessions.length > 0 && (
          <div className="welcome-section" style={{ marginBottom: 18 }}>
            <Group justify="space-between" align="center" mb={8}>
              <Text size="xs" fw={650} c="dimmed" style={{ letterSpacing: '0.04em' }}>
                {runningCount > 0 ? `正在运转与最近活跃 · ${runningCount} 运行中` : '最近活跃会话'}
              </Text>
              {onNewSession && (
                <UnstyledButton className="welcome-new-btn" onClick={onNewSession}>
                  <Group gap={4}>
                    <IconFolderPlus size={14} />
                    <span>新建会话</span>
                  </Group>
                </UnstyledButton>
              )}
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
              {recentSessions.map((session) => {
                const agent = agents[session.agent_id]
                const isRunning = sessionActivityStatus(session) === 'running'
                return (
                  <UnstyledButton
                    key={scopeKey(session.agent_id, session.id)}
                    className="welcome-card welcome-session-card"
                    data-liquid-surface="card"
                    data-liquid-snapshot=".welcome-ambient-backdrop"
                    onClick={() =>
                      navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
                    }
                  >
                    <Group justify="space-between" align="flex-start" wrap="nowrap" mb={6}>
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <AgentBrandIcon kind={agent?.kind} size={16} />
                        <Text fw={600} size="sm" truncate>
                          {displaySessionTitle(session)}
                        </Text>
                      </Group>
                      {isRunning && (
                        <Group gap={4} className="welcome-status-running">
                          <IconBolt size={12} />
                          <span style={{ fontSize: 11 }}>运行中</span>
                        </Group>
                      )}
                    </Group>
                    <Group gap={8} c="dimmed" style={{ fontSize: 11 }}>
                      {session.project_name && (
                        <Group gap={4} wrap="nowrap">
                          <IconFolder size={12} />
                          <span style={{ maxWidth: 130, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {session.project_name}
                          </span>
                        </Group>
                      )}
                      <Group gap={4} wrap="nowrap" className="welcome-time-mono">
                        <IconClock size={12} />
                        <span>{session.updated_at ? new Date(session.updated_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '刚刚'}</span>
                      </Group>
                    </Group>
                  </UnstyledButton>
                )
              })}
            </SimpleGrid>
          </div>
        )}

        {/* 今日算力脉搏与快速通道 */}
        <div
          className="welcome-telemetry-bar"
          data-liquid-surface="toolbar"
          data-liquid-snapshot=".welcome-ambient-backdrop"
        >
          <div className="welcome-telemetry-metrics">
            <span className={`welcome-metric${runningCount > 0 ? ' is-highlight' : ''}`}>
              <span>运行中</span>
              <strong>{runningCount}</strong>
            </span>
            <span className="welcome-metric">
              <span>就绪 Agent</span>
              <strong>{readyAgents.length}</strong>
            </span>
            {todaySummary && (
              <>
                <span className="welcome-metric">
                  <span>今日请求</span>
                  <strong>{todaySummary.requests.toLocaleString('zh-CN')}</strong>
                </span>
                <span className="welcome-metric">
                  <span>今日 Token</span>
                  <strong>{formatTokens(todaySummary.totalTokens)}</strong>
                </span>
              </>
            )}
          </div>
          <div className="welcome-quick-links">
            <UnstyledButton className="welcome-quick-link" onClick={() => navigate('/monitor')}>
              <IconLayoutGrid size={13} color="var(--astr-indigo)" />
              <span>矩阵</span>
            </UnstyledButton>
            <UnstyledButton className="welcome-quick-link" onClick={() => navigate('/groups')}>
              <IconUsers size={13} color="var(--astr-indigo)" />
              <span>群聊</span>
            </UnstyledButton>
          </div>
        </div>

        {/* 精简快捷键提示条 */}
        <div className="welcome-shortcut-strip" aria-label="常用快捷键">
          {shortcuts.map((item) => (
            <span key={item.id} className="welcome-shortcut-chip">
              <span>{item.desc}</span>
              <kbd className="welcome-kbd">{item.key}</kbd>
            </span>
          ))}
        </div>
      </div>
    </div>
  )
}
