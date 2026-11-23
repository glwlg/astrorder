import { useCallback, useMemo, useState } from 'react'
import { Badge, Button, Group, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQueryClient } from '@tanstack/react-query'
import {
  IconAlertTriangle,
  IconArrowRight,
  IconBolt,
  IconCheck,
  IconChecks,
  IconInbox,
  IconMessageQuestion,
  IconSend,
  IconShieldCheck,
} from '@tabler/icons-react'
import { ApiError, api } from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { AsyncUserInputCard } from '../../components/AsyncUserInputCard'
import { UserInputCard } from '../../components/UserInputCard'
import { formatRelativeTime, sessionActivityStatus } from '../../components/sessionRailModel'
import type { ActionInboxItem, ActionInboxKind } from '../../domain/actionInbox'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Command } from '../../domain/types'
import { selectSessions, useAstrorderStore } from '../../state/store'
import { useShallow } from 'zustand/react/shallow'
import { useActionInbox } from './useActionInbox'
import './actionInbox.css'

type InboxFilter = 'all' | ActionInboxKind

const FILTER_LABELS: Array<{ id: InboxFilter; label: string }> = [
  { id: 'all', label: '全部' },
  { id: 'approval', label: '待审批' },
  { id: 'question', label: '待回答' },
  { id: 'error', label: '异常' },
  { id: 'ready', label: '待验收' },
]

function kindBadge(kind: ActionInboxKind): { label: string; color: string } {
  switch (kind) {
    case 'approval':
      return { label: '待审批', color: 'yellow' }
    case 'question':
      return { label: '待回答', color: 'violet' }
    case 'error':
      return { label: '异常', color: 'red' }
    case 'ready':
      return { label: '待验收', color: 'teal' }
  }
}

export interface ActionInboxViewProps {
  compact?: boolean
  onOpenSession: (agentId: string, sessionId: string) => void
}

export function ActionInboxView({ compact = false, onOpenSession }: ActionInboxViewProps) {
  const queryClient = useQueryClient()
  const { items, markRead } = useActionInbox()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const commandsMap = useAstrorderStore((state) => state.commands)
  const [filter, setFilter] = useState<InboxFilter>('all')
  const [busyIds, setBusyIds] = useState<Record<string, boolean>>({})
  const [replyDrafts, setReplyDrafts] = useState<Record<string, string>>({})

  const runningCount = useMemo(
    () => sessions.filter((s) => sessionActivityStatus(s) === 'running').length,
    [sessions],
  )

  const counts = useMemo(() => {
    const map: Record<InboxFilter, number> = { all: items.length, approval: 0, question: 0, error: 0, ready: 0 }
    for (const item of items) {
      map[item.kind] += 1
    }
    return map
  }, [items])

  const filteredItems = useMemo(
    () => (filter === 'all' ? items : items.filter((item) => item.kind === filter)),
    [items, filter],
  )

  const handleDecision = useCallback(
    async (item: ActionInboxItem, action: 'approve' | 'cancel') => {
      const approval = item.approval
      if (!approval || busyIds[item.id]) return
      setBusyIds((prev) => ({ ...prev, [item.id]: true }))
      const id = newCommandId()
      const command: Command = {
        id,
        session_id: item.sessionId,
        agent_id: item.agentId,
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
          agent_id: item.agentId,
          session_id: item.sessionId,
          action,
          text: '',
          attachment_ids: [],
          target_id: approval.target_id || approval.id,
        })
        store.mergeCommands([result])
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', item.agentId, item.sessionId] })
        notifications.show({
          color: action === 'approve' ? 'teal' : 'gray',
          message: action === 'approve' ? '已批准该请求' : '已拒绝该请求',
        })
      } catch (cause) {
        const msg = cause instanceof Error ? cause.message : '审批操作失败'
        store.markOutboxError(
          item.agentId,
          item.sessionId,
          id,
          msg,
          cause instanceof ApiError && cause.status < 500 ? 'failed' : 'unknown',
        )
        notifications.show({ color: 'red', message: msg })
      } finally {
        setBusyIds((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
      }
    },
    [busyIds, queryClient],
  )

  const handleQuickReply = useCallback(
    async (item: ActionInboxItem) => {
      const text = (replyDrafts[item.id] || '').trim()
      if (!text || busyIds[item.id]) return
      setBusyIds((prev) => ({ ...prev, [item.id]: true }))
      const id = newCommandId()
      try {
        const result = await api.createCommand({
          id,
          agent_id: item.agentId,
          session_id: item.sessionId,
          action: 'send',
          text,
          attachment_ids: [],
          target_id: null,
        })
        useAstrorderStore.getState().mergeCommands([result])
        markRead(item.sessionKey)
        setReplyDrafts((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', item.agentId, item.sessionId] })
        notifications.show({ color: 'teal', message: `已发送至「${item.title}」` })
      } catch (cause) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '发送失败',
        })
      } finally {
        setBusyIds((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
      }
    },
    [busyIds, markRead, queryClient, replyDrafts],
  )

  const markAllDismissableRead = () => {
    for (const item of items) {
      if (item.kind === 'ready' || item.kind === 'error') markRead(item.sessionKey)
    }
  }
  const dismissableCount = counts.ready + counts.error

  return (
    <div className={`action-inbox${compact ? ' is-compact' : ''}`} aria-label="待我决策收件箱">
      {!compact && (
        <header className="action-inbox-header">
          <div className="action-inbox-heading">
            <div className="action-inbox-title-row">
              <IconInbox size={20} className="action-inbox-title-icon" aria-hidden="true" />
              <h1>待我决策</h1>
              {items.length > 0 && <Badge size="sm" variant="light" color="violet">{items.length}</Badge>}
            </div>
            <Text size="xs" c="dimmed">
              聚合跨会话待审批、Agent 提问、运行异常与刚完成待验收的结果，原地拍板放行。
            </Text>
          </div>
          <Group gap="xs">
            {runningCount > 0 && (
              <span className="action-inbox-running-pill">
                <IconBolt size={13} aria-hidden="true" />
                <span>{runningCount} 个后台运行中</span>
              </span>
            )}
            {dismissableCount > 0 && (
              <Button size="xs" variant="subtle" color="gray" leftSection={<IconChecks size={14} />} onClick={markAllDismissableRead}>
                全部标为已读
              </Button>
            )}
          </Group>
        </header>
      )}

      <div className="action-inbox-toolbar">
        <div className="action-inbox-filters" role="tablist" aria-label="筛选待办类型">
          {FILTER_LABELS.map((tab) => {
            const count = counts[tab.id]
            const active = filter === tab.id
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`action-inbox-filter-pill${active ? ' is-active' : ''}`}
                onClick={() => setFilter(tab.id)}
              >
                <span>{tab.label}</span>
                {count > 0 && <span className="action-inbox-filter-count">{count}</span>}
              </button>
            )
          })}
        </div>
        {compact && dismissableCount > 0 && (
          <button type="button" className="action-inbox-clear-btn" onClick={markAllDismissableRead}>
            <IconChecks size={14} aria-hidden="true" />
            <span>已读全部</span>
          </button>
        )}
      </div>

      {filteredItems.length === 0 ? (
        <div className="action-inbox-empty">
          <div className="action-inbox-empty-icon" aria-hidden="true">
            <IconCheck size={24} />
          </div>
          <strong>{filter === 'all' ? '全部处理完毕' : '当前分类暂无待处理项'}</strong>
          <p>
            {runningCount > 0
              ? `目前没有卡住等你拍板的会话，另有 ${runningCount} 个会话正在后台执行。`
              : '所有会话均无待审批、未回答问题或未读完成结果。'}
          </p>
        </div>
      ) : (
        <div className="action-inbox-list">
          {filteredItems.map((item) => {
            const badge = kindBadge(item.kind)
            const busy = Boolean(busyIds[item.id])
            const draft = replyDrafts[item.id] || ''
            const sessionCommands = Object.values(commandsMap).filter(
              (c) => scopeKey(c.agent_id, c.session_id) === item.sessionKey,
            )
            return (
              <article key={item.id} className={`action-inbox-card is-${item.kind}`}>
                <div className="action-inbox-card-head">
                  <div className="action-inbox-card-meta">
                    <span className="action-inbox-brand" aria-hidden="true">
                      {item.agent ? (
                        <AgentBrandIcon kind={item.agent.kind} size={15} />
                      ) : item.kind === 'question' ? (
                        <IconMessageQuestion size={15} />
                      ) : item.kind === 'error' ? (
                        <IconAlertTriangle size={15} />
                      ) : (
                        <IconShieldCheck size={15} />
                      )}
                    </span>
                    <strong className="action-inbox-card-title" title={item.title}>{item.title}</strong>
                    {item.session?.project_name && (
                      <span className="action-inbox-project-tag">{item.session.project_name}</span>
                    )}
                    <Badge size="xs" variant="light" color={badge.color}>{badge.label}</Badge>
                  </div>
                  <div className="action-inbox-card-right">
                    {item.timestamp && (
                      <span className="action-inbox-time">{formatRelativeTime(item.timestamp)}</span>
                    )}
                    <button
                      type="button"
                      className="action-inbox-open-btn"
                      onClick={() => {
                        if (item.kind === 'ready' || item.kind === 'error') markRead(item.sessionKey)
                        onOpenSession(item.agentId, item.sessionId)
                      }}
                    >
                      <span>进入</span>
                      <IconArrowRight size={13} aria-hidden="true" />
                    </button>
                  </div>
                </div>

                {item.kind === 'question' && item.approval ? (
                  <div className="action-inbox-embedded-card">
                    <UserInputCard approval={item.approval} />
                  </div>
                ) : item.kind === 'question' && item.questionMessage ? (
                  <div className="action-inbox-embedded-card">
                    <AsyncUserInputCard message={item.questionMessage} commands={sessionCommands} />
                  </div>
                ) : (
                  <>
                    <p className="action-inbox-summary">{item.summary}</p>
                    {item.detail && <pre className="action-inbox-code">{item.detail}</pre>}
                  </>
                )}

                {item.kind === 'approval' && item.canApproveInline && (
                  <div className="action-inbox-actions">
                    <Button
                      size="xs"
                      variant="subtle"
                      color="gray"
                      disabled={busy}
                      onClick={() => void handleDecision(item, 'cancel')}
                    >
                      拒绝
                    </Button>
                    <Button
                      size="xs"
                      color="violet"
                      loading={busy}
                      leftSection={<IconCheck size={14} />}
                      onClick={() => void handleDecision(item, 'approve')}
                    >
                      批准执行
                    </Button>
                  </div>
                )}

                {(item.kind === 'ready' || item.kind === 'error') && (
                  <div className="action-inbox-reply-row">
                    {item.canQuickReply && (
                      <form
                        className="action-inbox-quick-form"
                        onSubmit={(e) => {
                          e.preventDefault()
                          void handleQuickReply(item)
                        }}
                      >
                        <input
                          type="text"
                          aria-label={`快捷回复 ${item.title}`}
                          placeholder={item.kind === 'error' ? '输入重试或处理指令…' : '输入下一步指令，直接派活…'}
                          value={draft}
                          disabled={busy}
                          onChange={(e) => setReplyDrafts((prev) => ({ ...prev, [item.id]: e.target.value }))}
                        />
                        <button
                          type="submit"
                          className="action-inbox-send-btn"
                          aria-label="发送快捷指令"
                          disabled={busy || !draft.trim()}
                        >
                          <IconSend size={15} aria-hidden="true" />
                        </button>
                      </form>
                    )}
                    {(item.kind === 'ready' || item.kind === 'error') && (
                      <button
                        type="button"
                        className="action-inbox-read-btn"
                        onClick={() => markRead(item.sessionKey)}
                      >
                        标为已读
                      </button>
                    )}
                  </div>
                )}
              </article>
            )
          })}
        </div>
      )}
    </div>
  )
}
