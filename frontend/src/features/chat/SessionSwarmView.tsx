import { useMemo, useState } from 'react'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconArrowUpRight,
  IconMessage2,
  IconPlayerStop,
  IconRefresh,
  IconUsersGroup,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import { newCommandId } from '../../domain/semantics'
import type {
  Agent,
  Session,
  SessionSwarm,
  SessionSwarmBoardItem,
  SessionSwarmMember,
  SessionSwarmMemberState,
} from '../../domain/types'
import { useSessionSwarmMemberPreview } from '../../hooks/useSessionSwarm'
import { useAstrorderStore } from '../../state/store'
import './workspaceChanges.css'

export interface SessionSwarmViewProps {
  session: Session
  agent?: Agent
  swarm?: SessionSwarm
  loading?: boolean
  fetching?: boolean
  error?: unknown
  onRefresh: () => void
  onOpenSession: (agentId: string, sessionId: string) => void
  compact?: boolean
}

const STATE_LABELS: Record<SessionSwarmMemberState, string> = {
  working: '运行中',
  waiting: '等待中',
  stopped: '已结束',
  killed: '已回收',
}

const WORKSPACE_LABELS: Record<string, string> = {
  share: '独立工作副本',
  branch: '独立克隆',
  inherit: '共享工作区',
}

const BOARD_KIND_LABELS: Record<string, string> = {
  brief: '分工',
  message: '消息',
  model_change: '切模型',
  kill: '回收',
}

function shortModelName(model?: string): string {
  if (!model) return ''
  const trimmed = model.trim()
  if (!trimmed) return ''
  const parts = trimmed.split('/')
  return parts[parts.length - 1] || trimmed
}

function formatTime(iso?: string): string {
  if (!iso) return ''
  const ts = Date.parse(iso)
  if (!Number.isFinite(ts)) return ''
  return new Date(ts).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

function memberKey(member: SessionSwarmMember, index: number): string {
  return member.conversation_id || `${member.name}:${index}`
}

export function SessionSwarmView({
  session,
  agent,
  swarm,
  loading = false,
  fetching = false,
  error,
  onRefresh,
  onOpenSession,
  compact = false,
}: SessionSwarmViewProps) {
  const [innerTab, setInnerTab] = useState<'roster' | 'board'>('roster')
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [confirmStop, setConfirmStop] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [openingId, setOpeningId] = useState<string | null>(null)

  const members = swarm?.members || []
  const board = useMemo<SessionSwarmBoardItem[]>(
    () => [...(swarm?.board || [])].reverse(),
    [swarm?.board],
  )

  const selectedMember = useMemo(() => {
    if (!selectedKey) return null
    return members.find((m, i) => memberKey(m, i) === selectedKey) || null
  }, [members, selectedKey])

  const previewQuery = useSessionSwarmMemberPreview(
    session,
    agent,
    selectedMember?.conversation_id || null,
    Boolean(selectedMember?.conversation_id),
  )

  const canStopMember = Boolean(
    selectedMember &&
      selectedMember.state === 'working' &&
      selectedMember.conversation_id &&
      agent?.capabilities.includes('stop'),
  )

  async function handleEnterSession(conversationId: string, inCatalog?: boolean) {
    if (!conversationId || openingId) return
    const targetAgentId = session.agent_id
    setOpeningId(conversationId)
    try {
      const storeHasSession = Boolean(
        useAstrorderStore.getState().sessions[`${targetAgentId}::${conversationId}`],
      )
      if (!inCatalog && !storeHasSession) {
        try {
          await api.refreshSessionActivity(targetAgentId)
          const boot = await api.getBootstrap()
          useAstrorderStore.getState().hydrateBootstrap(boot)
        } catch {
          // Best-effort catalog sync; still try navigating if present
        }
      }
      onOpenSession(targetAgentId, conversationId)
    } finally {
      setOpeningId(null)
    }
  }

  async function handleStopMember(member: SessionSwarmMember) {
    if (!member.conversation_id || stopping) return
    setStopping(true)
    try {
      const result = await api.createCommand({
        id: newCommandId(),
        agent_id: member.agent_id || session.agent_id,
        session_id: member.conversation_id,
        action: 'stop',
        text: '',
        attachment_ids: [],
        target_id: member.conversation_id,
      })
      useAstrorderStore.getState().mergeCommands([result])
      if (result.state === 'failed' || result.state === 'unknown') {
        throw new Error(result.error || '停止请求未被原生运行时确认')
      }
      notifications.show({
        color: 'teal',
        message: `已向成员 @${member.name} 发送停止指令`,
      })
      setConfirmStop(false)
      onRefresh()
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '停止成员失败',
      })
    } finally {
      setStopping(false)
    }
  }

  if (loading && !swarm) {
    return (
      <div className={`swarm-panel${compact ? ' is-compact' : ''}`}>
        <p className="workbench-empty" role="status">
          正在读取 Antigravity Swarm 状态…
        </p>
      </div>
    )
  }

  if (error && !swarm) {
    return (
      <div className={`swarm-panel${compact ? ' is-compact' : ''}`}>
        <div className="swarm-empty-card" role="alert">
          <p className="swarm-empty-title">无法读取 Swarm 状态</p>
          <p className="swarm-empty-desc">
            {error instanceof Error ? error.message : '请稍后刷新重试。'}
          </p>
          <button type="button" className="swarm-pill-btn" onClick={onRefresh}>
            <IconRefresh size={13} />
            <span>重试</span>
          </button>
        </div>
      </div>
    )
  }

  if (swarm?.needs_daemon_upgrade) {
    return (
      <div className={`swarm-panel${compact ? ' is-compact' : ''}`}>
        <div className="swarm-empty-card">
          <IconUsersGroup size={22} className="swarm-empty-icon" />
          <p className="swarm-empty-title">需要更新小内核以读取 Swarm</p>
          <p className="swarm-empty-desc">
            当前运行的小内核尚未激活原生 Swarm 查询能力。请在系统托盘点击「小内核 → 更新小内核」后刷新。
          </p>
          <button type="button" className="swarm-pill-btn" onClick={onRefresh} disabled={fetching}>
            <IconRefresh size={13} />
            <span>重新检测</span>
          </button>
        </div>
      </div>
    )
  }

  if (!swarm?.available) {
    return (
      <div className={`swarm-panel${compact ? ' is-compact' : ''}`}>
        <div className="swarm-empty-card">
          <IconUsersGroup size={22} className="swarm-empty-icon" />
          <p className="swarm-empty-title">当前会话尚未启动 Swarm 团队</p>
          <p className="swarm-empty-desc">
            在 Antigravity 对话中通过 <code>/swarm</code>、<code>/teamwork</code> 或子代理委派组建团队后，这里会实时展示成员名册、模型分工与共享消息板。
          </p>
          <button type="button" className="swarm-pill-btn" onClick={onRefresh} disabled={fetching}>
            <IconRefresh size={13} />
            <span>刷新状态</span>
          </button>
        </div>
      </div>
    )
  }

  // Inline Member Drill-Down Preview (read-only observation without taking ACP lock)
  if (selectedMember) {
    const previewMessages = previewQuery.data?.member_messages || []
    const isCurrentSession = selectedMember.conversation_id === session.id
    return (
      <div className={`swarm-panel swarm-detail-view${compact ? ' is-compact' : ''}`}>
        <div className="swarm-detail-nav">
          <button
            type="button"
            className="swarm-back-btn"
            onClick={() => {
              setSelectedKey(null)
              setConfirmStop(false)
            }}
          >
            <IconArrowLeft size={14} />
            <span>返回团队</span>
          </button>
          <div className="swarm-detail-actions">
            {selectedMember.conversation_id && !isCurrentSession && (
              <button
                type="button"
                className="swarm-primary-btn"
                disabled={openingId === selectedMember.conversation_id}
                onClick={() =>
                  void handleEnterSession(selectedMember.conversation_id, selectedMember.in_catalog)
                }
              >
                <span>{openingId === selectedMember.conversation_id ? '进入中…' : '进入会话'}</span>
                <IconArrowUpRight size={13} />
              </button>
            )}
            {canStopMember && !confirmStop && (
              <button
                type="button"
                className="swarm-danger-btn"
                onClick={() => setConfirmStop(true)}
                aria-label={`停止成员 ${selectedMember.name}`}
              >
                <IconPlayerStop size={13} />
                <span>停止</span>
              </button>
            )}
          </div>
        </div>

        {confirmStop && (
          <div className="swarm-confirm-bar" role="alert">
            <span>确认停止成员 @{selectedMember.name} 的当前运行？</span>
            <div className="swarm-confirm-actions">
              <button
                type="button"
                className="swarm-pill-btn"
                disabled={stopping}
                onClick={() => setConfirmStop(false)}
              >
                取消
              </button>
              <button
                type="button"
                className="swarm-danger-btn is-solid"
                disabled={stopping}
                onClick={() => void handleStopMember(selectedMember)}
              >
                {stopping ? '停止中…' : '确认停止'}
              </button>
            </div>
          </div>
        )}

        <div className="swarm-detail-card">
          <div className="swarm-member-top">
            <div className="swarm-member-identity">
              <span className={`swarm-state-dot is-${selectedMember.state}`} aria-hidden="true" />
              <strong>{selectedMember.name}</strong>
              {selectedMember.is_coordinator && <span className="swarm-role-tag">协调者</span>}
              {isCurrentSession && <span className="swarm-current-tag">当前</span>}
            </div>
            <span className={`swarm-state-pill is-${selectedMember.state}`}>
              {STATE_LABELS[selectedMember.state] || selectedMember.state}
            </span>
          </div>
          <div className="swarm-meta-chips">
            {selectedMember.model && (
              <span className="swarm-meta-chip" title={selectedMember.model}>
                {shortModelName(selectedMember.model)}
              </span>
            )}
            {selectedMember.workspace_mode && (
              <span className="swarm-meta-chip">
                {WORKSPACE_LABELS[selectedMember.workspace_mode] || selectedMember.workspace_mode}
              </span>
            )}
            {selectedMember.spawned_by && (
              <span className="swarm-meta-chip">由 @{selectedMember.spawned_by} 创建</span>
            )}
          </div>
          {selectedMember.brief && (
            <div className="swarm-brief-box">
              <small>任务分工</small>
              <p>{selectedMember.brief}</p>
            </div>
          )}
        </div>

        <div className="swarm-preview-head">
          <span>只读动态预览</span>
          <button
            type="button"
            className="swarm-icon-link"
            onClick={() => void previewQuery.refetch()}
            disabled={previewQuery.isFetching}
            aria-label="刷新成员预览"
          >
            <IconRefresh size={13} />
          </button>
        </div>

        <div className="swarm-preview-list" aria-label="成员只读消息预览">
          {!selectedMember.conversation_id ? (
            <p className="workbench-empty">该成员尚未分配独立子会话。</p>
          ) : previewQuery.isPending ? (
            <p className="workbench-empty" role="status">
              正在读取成员最新动态…
            </p>
          ) : previewQuery.isError ? (
            <p className="workbench-empty" role="alert">
              暂无法读取该成员的只读记录，请刷新重试。
            </p>
          ) : previewMessages.length === 0 ? (
            <p className="workbench-empty">该成员暂无可预览的消息记录。</p>
          ) : (
            previewMessages.map((msg) => (
              <article
                key={msg.id}
                className={`swarm-msg-item is-${msg.role === 'user' ? 'user' : msg.kind === 'tool' ? 'tool' : 'assistant'}`}
              >
                <header>
                  <span>
                    {msg.role === 'user'
                      ? '指派 / 输入'
                      : msg.kind === 'tool'
                        ? `工具 · ${msg.tool_name || '调用'}`
                        : selectedMember.name}
                  </span>
                  <small>{formatTime(msg.created_at)}</small>
                </header>
                <p>{msg.text || (msg.tool_status ? `状态：${msg.tool_status}` : '（无文本输出）')}</p>
              </article>
            ))
          )}
        </div>
        <p className="swarm-readonly-footnote">只读观测，不会中断或抢占运行中的子代理会话。</p>
      </div>
    )
  }

  const summary = swarm.summary || { total: members.length, working: 0, waiting: 0, stopped: 0, killed: 0 }

  return (
    <div className={`swarm-panel${compact ? ' is-compact' : ''}`}>
      <div className="swarm-summary-card">
        <div className="swarm-summary-top">
          <div className="swarm-title-wrap">
            <strong title={swarm.title || 'Antigravity Swarm'}>
              {swarm.title || (swarm.kind === 'subagents' ? '子代理协作' : 'Antigravity Swarm')}
            </strong>
            <span className="swarm-kind-badge">
              {swarm.kind === 'subagents' ? 'Subagents' : 'Swarm'}
            </span>
          </div>
          <span className={`swarm-state-pill is-${swarm.active ? 'working' : 'stopped'}`}>
            {swarm.active ? '运行中' : '已结束'}
          </span>
        </div>
        <div className="swarm-summary-metrics">
          <span>共 {summary.total} 名成员</span>
          {summary.working > 0 && <span className="is-working">{summary.working} 运行中</span>}
          {summary.waiting > 0 && <span className="is-waiting">{summary.waiting} 等待中</span>}
          {summary.stopped > 0 && <span>{summary.stopped} 已结束</span>}
          {summary.killed > 0 && <span>{summary.killed} 已回收</span>}
        </div>
        {!swarm.is_coordinator && swarm.coordinator_session_id && (
          <button
            type="button"
            className="swarm-coord-banner"
            onClick={() => void handleEnterSession(swarm.coordinator_session_id, true)}
          >
            <span>当前为子成员视角 · 返回主协调者</span>
            <IconArrowUpRight size={13} />
          </button>
        )}
      </div>

      <div className="swarm-subtabs" role="tablist" aria-label="Swarm 视图切换">
        <button
          type="button"
          role="tab"
          aria-selected={innerTab === 'roster'}
          className={innerTab === 'roster' ? 'is-active' : ''}
          onClick={() => setInnerTab('roster')}
        >
          <IconUsersGroup size={13} />
          <span>成员名册 {members.length}</span>
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={innerTab === 'board'}
          className={innerTab === 'board' ? 'is-active' : ''}
          onClick={() => setInnerTab('board')}
        >
          <IconMessage2 size={13} />
          <span>共享消息板 {board.length}</span>
        </button>
      </div>

      {innerTab === 'roster' ? (
        <div className="swarm-member-list" aria-label="Swarm 成员名册">
          {members.map((member, idx) => {
            const key = memberKey(member, idx)
            const isCurrent = member.conversation_id === session.id
            const snippet = member.last_message || member.brief || member.title || ''
            return (
              <div
                key={key}
                className={`swarm-member-card is-${member.state}${(member.nesting_depth || 0) > 1 ? ' is-nested' : ''}`}
              >
                <button
                  type="button"
                  className="swarm-member-main"
                  onClick={() => {
                    setSelectedKey(key)
                    setConfirmStop(false)
                  }}
                >
                  <div className="swarm-member-top">
                    <div className="swarm-member-identity">
                      <span className={`swarm-state-dot is-${member.state}`} aria-hidden="true" />
                      <strong>{member.name}</strong>
                      {member.is_coordinator && <span className="swarm-role-tag">协调者</span>}
                      {(member.nesting_depth || 0) > 1 && <span className="swarm-role-tag">助手</span>}
                      {isCurrent && <span className="swarm-current-tag">当前</span>}
                    </div>
                    <span className={`swarm-state-pill is-${member.state}`}>
                      {STATE_LABELS[member.state] || member.state}
                    </span>
                  </div>
                  {(member.model || (member.workspace_mode && member.workspace_mode !== 'inherit')) && (
                    <div className="swarm-meta-chips">
                      {member.model && (
                        <span className="swarm-meta-chip" title={member.model}>
                          {shortModelName(member.model)}
                        </span>
                      )}
                      {member.workspace_mode && member.workspace_mode !== 'inherit' && (
                        <span className="swarm-meta-chip">
                          {WORKSPACE_LABELS[member.workspace_mode] || member.workspace_mode}
                        </span>
                      )}
                    </div>
                  )}
                  {snippet && <p className="swarm-member-snippet">{snippet}</p>}
                </button>
                {member.conversation_id && !isCurrent && (
                  <button
                    type="button"
                    className="swarm-member-jump"
                    title={`直接进入 @${member.name} 会话`}
                    aria-label={`进入 ${member.name} 会话`}
                    disabled={openingId === member.conversation_id}
                    onClick={() => void handleEnterSession(member.conversation_id, member.in_catalog)}
                  >
                    <IconArrowUpRight size={14} />
                  </button>
                )}
              </div>
            )
          })}
        </div>
      ) : (
        <div className="swarm-board-list" aria-label="Swarm 共享消息板">
          {board.length === 0 ? (
            <p className="workbench-empty">共享消息板暂无广播或分工记录。</p>
          ) : (
            board.map((item) => (
              <article key={item.id} className={`swarm-board-item is-${item.kind}`}>
                <header>
                  <span className={`swarm-board-kind is-${item.kind}`}>
                    {BOARD_KIND_LABELS[item.kind] || '动态'}
                  </span>
                  <strong>@{item.author}</strong>
                  {item.targets && item.targets.length > 0 && (
                    <span className="swarm-board-targets">
                      → {item.targets.map((t) => `@${t}`).join(' ')}
                    </span>
                  )}
                  <small>{formatTime(item.created_at)}</small>
                </header>
                <p>{item.text}</p>
              </article>
            ))
          )}
        </div>
      )}
    </div>
  )
}
