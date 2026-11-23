import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQueryClient } from '@tanstack/react-query'
import {
  IconAlertTriangle,
  IconArrowRight,
  IconBolt,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconLayoutGrid,
  IconMessageQuestion,
  IconUsers,
  IconX,
} from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../api/client'
import { groupHasActiveTask } from '../domain/groupActivity'
import { newCommandId, scopeKey } from '../domain/semantics'
import type { Approval, Command, Session } from '../domain/types'
import { selectSessions, useAstrorderStore } from '../state/store'
import { AgentBrandIcon } from './AgentBrandIcon'
import { displaySessionTitle, sessionActivityStatus } from './sessionRailModel'
import { isUserInput } from './UserInputCard'
import './attentionIsland.css'

interface AttentionIslandProps {
  activeSessionKey?: string
}

function approvalSummary(approval: Approval): string {
  const raw = (approval.title || approval.detail || '').replace(/\s+/g, ' ').trim()
  if (!raw) return isUserInput(approval) ? '等待你的回答' : '请求执行授权'
  return raw.length > 52 ? `${raw.slice(0, 52)}…` : raw
}

export function AttentionIsland({ activeSessionKey }: AttentionIslandProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const rootRef = useRef<HTMLDivElement>(null)

  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const approvalsMap = useAstrorderStore((state) => state.approvals)
  const tasks = useAstrorderStore((state) => state.tasks)
  const liveActivityAt = useAstrorderStore((state) => state.liveActivityAt)
  const botGroups = useAstrorderStore((state) => state.botGroups)

  const [expanded, setExpanded] = useState(false)
  const [busyApprovalIds, setBusyApprovalIds] = useState<Record<string, boolean>>({})
  const [dismissedSignature, setDismissedSignature] = useState<string | null>(null)

  const isChatSessionRoute = location.pathname.startsWith('/chat/') && !location.pathname.startsWith('/chat/group-')
  const isMonitorRoute = location.pathname.startsWith('/monitor')
  const isGroupsRoute = location.pathname.startsWith('/groups') || location.pathname.startsWith('/chat/group-')

  const sessionMap = useMemo(() => {
    const map = new Map<string, Session>()
    for (const session of sessions) {
      map.set(scopeKey(session.agent_id, session.id), session)
    }
    return map
  }, [sessions])

  // Pending approvals outside the currently open chat session (always surfaced on Welcome/Matrix/other routes)
  const pendingApprovals = useMemo(() => {
    return Object.values(approvalsMap).filter((approval) => {
      if (approval.state !== 'pending' || !approval.agent_id || !approval.session_id) return false
      const key = scopeKey(approval.agent_id, approval.session_id)
      if (isChatSessionRoute && activeSessionKey && key === activeSessionKey) return false
      return true
    })
  }, [activeSessionKey, approvalsMap, isChatSessionRoute])

  // Sessions in waiting_approval status that do not already have a pending approval item
  const waitingSessions = useMemo(() => {
    const coveredKeys = new Set(
      pendingApprovals.map((a) => scopeKey(a.agent_id || '', a.session_id || '')),
    )
    return sessions.filter((session) => {
      if (session.status !== 'waiting_approval') return false
      const key = scopeKey(session.agent_id, session.id)
      if (coveredKeys.has(key)) return false
      if (isChatSessionRoute && activeSessionKey && key === activeSessionKey) return false
      return true
    })
  }, [activeSessionKey, isChatSessionRoute, pendingApprovals, sessions])

  // Running sessions outside the currently open chat session (suppressed on Matrix where user already monitors live)
  const runningSessions = useMemo(() => {
    if (isMonitorRoute) return []
    void tasks
    void liveActivityAt
    return sessions.filter((session) => {
      const key = scopeKey(session.agent_id, session.id)
      if (isChatSessionRoute && activeSessionKey && key === activeSessionKey) return false
      return sessionActivityStatus(session) === 'running'
    })
  }, [activeSessionKey, isChatSessionRoute, isMonitorRoute, liveActivityAt, sessions, tasks])

  const activeGroups = useMemo(() => {
    if (isGroupsRoute) return []
    return Object.values(botGroups).filter(groupHasActiveTask)
  }, [botGroups, isGroupsRoute])

  const attentionCount = pendingApprovals.length + waitingSessions.length
  const activityCount = runningSessions.length + activeGroups.length
  const hasAttention = attentionCount > 0
  const hasActivity = activityCount > 0

  // Signature for dismissible running-activity mode; automatically reappears when a new session/group becomes active
  const activitySignature = useMemo(() => {
    if (!hasActivity) return ''
    return [
      ...runningSessions.map((s) => scopeKey(s.agent_id, s.id)).sort(),
      ...activeGroups.map((g) => `group:${g.id}`).sort(),
    ].join('|')
  }, [activeGroups, hasActivity, runningSessions])

  const isVisible = hasAttention || (hasActivity && dismissedSignature !== activitySignature)

  // Close expanded tray on Escape or outside click
  useEffect(() => {
    if (!expanded) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setExpanded(false)
    }
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setExpanded(false)
      }
    }
    document.addEventListener('keydown', onKeyDown)
    document.addEventListener('pointerdown', onPointerDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.removeEventListener('pointerdown', onPointerDown)
    }
  }, [expanded])

  const openSession = useCallback(
    (agentId: string, sessionId: string) => {
      setExpanded(false)
      navigate(`/chat/${encodeURIComponent(sessionId)}?agent_id=${encodeURIComponent(agentId)}`)
    },
    [navigate],
  )

  const handleDecision = useCallback(
    async (approval: Approval, action: 'approve' | 'cancel') => {
      if (!approval.agent_id || !approval.session_id) return
      const agent = agents[approval.agent_id]
      if (!agent?.capabilities.includes('approvals')) return
      if (busyApprovalIds[approval.id]) return

      setBusyApprovalIds((prev) => ({ ...prev, [approval.id]: true }))
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
          message: action === 'approve' ? '已原地批准该请求' : '已拒绝该请求',
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
        setBusyApprovalIds((prev) => {
          const next = { ...prev }
          delete next[approval.id]
          return next
        })
      }
    },
    [agents, busyApprovalIds, queryClient],
  )

  if (!isVisible) return null

  const primaryApproval = pendingApprovals[0] || null
  const primaryWaitingSession = !primaryApproval ? waitingSessions[0] || null : null
  const primaryRunning = runningSessions[0] || null
  const primaryGroup = !primaryRunning ? activeGroups[0] || null : null

  const primarySession = primaryApproval
    ? sessionMap.get(scopeKey(primaryApproval.agent_id || '', primaryApproval.session_id || ''))
    : primaryWaitingSession || primaryRunning || null
  const primaryAgent = primaryApproval?.agent_id
    ? agents[primaryApproval.agent_id]
    : primarySession
      ? agents[primarySession.agent_id]
      : undefined
  const primaryCanDecide = Boolean(
    primaryApproval &&
      !isUserInput(primaryApproval) &&
      primaryAgent?.capabilities.includes('approvals'),
  )
  const primaryBusy = primaryApproval ? Boolean(busyApprovalIds[primaryApproval.id]) : false

  const tone = hasAttention ? 'attention' : 'running'

  return (
    <div
      ref={rootRef}
      className={`attention-island is-${tone}${expanded ? ' is-expanded' : ''}`}
      role="region"
      aria-label="星序灵动岛"
    >
      <div
        className="attention-island-pill"
        data-liquid-surface="toolbar"
        data-liquid-snapshot="#astr-shell-sky"
      >
        <UnstyledButton
          className="attention-island-trigger"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          aria-label={hasAttention ? `展开待处理审批，共 ${attentionCount} 项` : `展开运行中会话，共 ${activityCount} 项`}
        >
          <span className={`attention-island-dot is-${tone}`} aria-hidden="true" />
          {primaryAgent ? (
            <span className="attention-island-brand" aria-hidden="true">
              <AgentBrandIcon kind={primaryAgent.kind} size={14} />
            </span>
          ) : hasAttention ? (
            <IconAlertTriangle size={14} className="attention-island-icon-warn" aria-hidden="true" />
          ) : primaryGroup ? (
            <IconUsers size={14} className="attention-island-icon-run" aria-hidden="true" />
          ) : (
            <IconBolt size={14} className="attention-island-icon-run" aria-hidden="true" />
          )}

          <span className="attention-island-copy">
            {primaryApproval ? (
              <>
                <strong className="attention-island-title">
                  {primarySession ? displaySessionTitle(primarySession) : '待审批'}
                </strong>
                <span className="attention-island-sep" aria-hidden="true">·</span>
                <span className="attention-island-detail">{approvalSummary(primaryApproval)}</span>
              </>
            ) : primaryWaitingSession ? (
              <>
                <strong className="attention-island-title">{displaySessionTitle(primaryWaitingSession)}</strong>
                <span className="attention-island-sep" aria-hidden="true">·</span>
                <span className="attention-island-detail">等待确认</span>
              </>
            ) : primaryRunning ? (
              <>
                <strong className="attention-island-title">
                  {runningSessions.length > 1
                    ? `${runningSessions.length} 个会话运行中`
                    : displaySessionTitle(primaryRunning)}
                </strong>
                {runningSessions.length === 1 && (
                  <>
                    <span className="attention-island-sep" aria-hidden="true">·</span>
                    <span className="attention-island-detail">正在执行</span>
                  </>
                )}
              </>
            ) : primaryGroup ? (
              <>
                <strong className="attention-island-title">{primaryGroup.name || '群聊协作'}</strong>
                <span className="attention-island-sep" aria-hidden="true">·</span>
                <span className="attention-island-detail">接力进行中</span>
              </>
            ) : null}
          </span>

          {(attentionCount > 1 || (hasAttention && hasActivity)) && (
            <span className="attention-island-count">{attentionCount + (hasAttention ? 0 : activityCount)}</span>
          )}

          <span className="attention-island-chevron" aria-hidden="true">
            {expanded ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
          </span>
        </UnstyledButton>

        <div className="attention-island-inline-actions">
          {primaryApproval && primaryCanDecide && (
            <>
              <Button
                size="compact-xs"
                color="violet"
                variant="filled"
                radius="xl"
                loading={primaryBusy}
                onClick={() => void handleDecision(primaryApproval, 'approve')}
                leftSection={<IconCheck size={12} />}
              >
                批准
              </Button>
              <Button
                size="compact-xs"
                color="gray"
                variant="subtle"
                radius="xl"
                disabled={primaryBusy}
                onClick={() => void handleDecision(primaryApproval, 'cancel')}
              >
                拒绝
              </Button>
            </>
          )}
          {primaryApproval && !primaryCanDecide && primaryApproval.agent_id && primaryApproval.session_id && (
            <Button
              size="compact-xs"
              color="yellow"
              variant="light"
              radius="xl"
              onClick={() => openSession(primaryApproval.agent_id!, primaryApproval.session_id!)}
              rightSection={<IconArrowRight size={12} />}
            >
              {isUserInput(primaryApproval) ? '回答' : '查看'}
            </Button>
          )}
          {!primaryApproval && primaryWaitingSession && (
            <Button
              size="compact-xs"
              color="yellow"
              variant="light"
              radius="xl"
              onClick={() => openSession(primaryWaitingSession.agent_id, primaryWaitingSession.id)}
              rightSection={<IconArrowRight size={12} />}
            >
              查看
            </Button>
          )}
          {!hasAttention && (
            <Tooltip label="暂时收起状态提示" withArrow>
              <ActionIcon
                size="xs"
                variant="subtle"
                color="gray"
                radius="xl"
                aria-label="暂时收起状态提示"
                onClick={() => {
                  setExpanded(false)
                  setDismissedSignature(activitySignature)
                }}
              >
                <IconX size={12} />
              </ActionIcon>
            </Tooltip>
          )}
        </div>
      </div>

      {expanded && (
        <div
          className="attention-island-tray"
          data-liquid-surface="panel"
          data-liquid-snapshot="#astr-shell-sky"
          role="dialog"
          aria-label="跨会话实时动态"
        >
          <Stack gap="xs">
            {pendingApprovals.length > 0 && (
              <div className="attention-tray-section">
                <Group justify="space-between" mb={6}>
                  <Text size="xs" fw={700} c="dimmed">待审批请求</Text>
                  <Badge size="xs" variant="light" color="yellow">{pendingApprovals.length}</Badge>
                </Group>
                <Stack gap={6}>
                  {pendingApprovals.map((approval) => {
                    const session = sessionMap.get(scopeKey(approval.agent_id || '', approval.session_id || ''))
                    const agent = approval.agent_id ? agents[approval.agent_id] : undefined
                    const question = isUserInput(approval)
                    const canDecide = !question && agent?.capabilities.includes('approvals') === true
                    const busy = Boolean(busyApprovalIds[approval.id])
                    return (
                      <div key={approval.id} className="attention-tray-item is-attention">
                        <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
                          <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                            {question ? (
                              <IconMessageQuestion size={15} className="attention-island-icon-warn" />
                            ) : (
                              <AgentBrandIcon kind={agent?.kind} size={14} />
                            )}
                            <Text size="xs" fw={650} truncate>
                              {session ? displaySessionTitle(session) : '会话审批'}
                            </Text>
                          </Group>
                          {approval.agent_id && approval.session_id && (
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="violet"
                              onClick={() => openSession(approval.agent_id!, approval.session_id!)}
                            >
                              跳转
                            </Button>
                          )}
                        </Group>
                        <Text size="xs" c="dimmed" lineClamp={2} mt={3}>
                          {approval.title || approval.detail || '等待授权确认'}
                        </Text>
                        {approval.detail && approval.title && approval.detail !== approval.title && (
                          <code className="attention-tray-code">{approval.detail}</code>
                        )}
                        <Group justify="flex-end" gap={6} mt={6}>
                          {canDecide ? (
                            <>
                              <Button
                                size="compact-xs"
                                variant="subtle"
                                color="gray"
                                disabled={busy}
                                onClick={() => void handleDecision(approval, 'cancel')}
                              >
                                拒绝
                              </Button>
                              <Button
                                size="compact-xs"
                                color="violet"
                                loading={busy}
                                leftSection={<IconCheck size={12} />}
                                onClick={() => void handleDecision(approval, 'approve')}
                              >
                                批准执行
                              </Button>
                            </>
                          ) : (
                            approval.agent_id && approval.session_id && (
                              <Button
                                size="compact-xs"
                                color="yellow"
                                variant="light"
                                onClick={() => openSession(approval.agent_id!, approval.session_id!)}
                                rightSection={<IconArrowRight size={12} />}
                              >
                                {question ? '前往回答' : '查看详情'}
                              </Button>
                            )
                          )}
                        </Group>
                      </div>
                    )
                  })}
                </Stack>
              </div>
            )}

            {waitingSessions.length > 0 && (
              <div className="attention-tray-section">
                <Text size="xs" fw={700} c="dimmed" mb={6}>等待确认会话</Text>
                <Stack gap={4}>
                  {waitingSessions.map((session) => (
                    <UnstyledButton
                      key={scopeKey(session.agent_id, session.id)}
                      className="attention-tray-row"
                      onClick={() => openSession(session.agent_id, session.id)}
                    >
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <AgentBrandIcon kind={agents[session.agent_id]?.kind} size={14} />
                        <Text size="xs" fw={600} truncate>{displaySessionTitle(session)}</Text>
                      </Group>
                      <Badge size="xs" variant="light" color="yellow">待确认</Badge>
                    </UnstyledButton>
                  ))}
                </Stack>
              </div>
            )}

            {(runningSessions.length > 0 || activeGroups.length > 0) && (
              <div className="attention-tray-section">
                <Group justify="space-between" mb={6}>
                  <Text size="xs" fw={700} c="dimmed">后台运行中</Text>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="violet"
                    leftSection={<IconLayoutGrid size={12} />}
                    onClick={() => {
                      setExpanded(false)
                      navigate('/monitor')
                    }}
                  >
                    进入矩阵
                  </Button>
                </Group>
                <Stack gap={4}>
                  {runningSessions.slice(0, 5).map((session) => (
                    <UnstyledButton
                      key={scopeKey(session.agent_id, session.id)}
                      className="attention-tray-row"
                      onClick={() => openSession(session.agent_id, session.id)}
                    >
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <AgentBrandIcon kind={agents[session.agent_id]?.kind} size={14} />
                        <Text size="xs" fw={550} truncate>{displaySessionTitle(session)}</Text>
                        {session.project_name && (
                          <Text size="xs" c="dimmed" truncate style={{ maxWidth: 96 }}>
                            {session.project_name}
                          </Text>
                        )}
                      </Group>
                      <span className="attention-running-pill">运行中</span>
                    </UnstyledButton>
                  ))}
                  {activeGroups.map((group) => (
                    <UnstyledButton
                      key={group.id}
                      className="attention-tray-row"
                      onClick={() => {
                        setExpanded(false)
                        navigate(`/groups/${encodeURIComponent(group.id)}`)
                      }}
                    >
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        <IconUsers size={14} color="var(--astr-indigo)" />
                        <Text size="xs" fw={550} truncate>{group.name || '群聊协作'}</Text>
                      </Group>
                      <span className="attention-running-pill">接力中</span>
                    </UnstyledButton>
                  ))}
                </Stack>
              </div>
            )}
          </Stack>
        </div>
      )}
    </div>
  )
}
