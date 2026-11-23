import { UserInputCard, isUserInput } from '../../components/UserInputCard'
import { AsyncUserInputCard, isAsyncQuestion, hasLaterNativeInput } from '../../components/AsyncUserInputCard'
import { useEffect, useMemo, useRef, useState } from 'react'
import { IconCheck, IconChecks, IconChevronDown, IconHistory, IconMovie } from '@tabler/icons-react'
import { isMobileVideoFilePath } from './mobileFilePath'
import type { SessionCheckpoint } from '../../api/client'
import { mapCheckpointsToMessages } from '../../domain/turnCheckpoints'
import type { Approval, Message, Session } from '../../domain/types'
import { attachmentHref } from '../../domain/attachmentHref'
import { hasMessageContent, isMessageActivity, normalizeMessagePresentation } from '../../domain/messagePresentation'
import { activityPacks } from '../../domain/activityPacks'
import { MobileMarkdown as MarkdownContent } from './MobileMarkdown'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { sessionSwipeGesture, sessionDragPreview, startsAtSessionDrawerEdge, type SessionSwipeGesture } from './mobileGestures'
import { describeTool, extractToolArgs, FileChangeDiffBlock, fileChangeDiffs, formatCommandRowDisplay, PackSummary, ShellOutputBlock, ToolLineIcon, unwrapCommand } from '../chat/toolPresentation'
import { ToolOutputBlock } from '../chat/ToolOutputBlock'
import { isCompactionMessage, CompactionDivider } from '../chat/CompactionDivider'
import { HandoffSummaryCard } from '../chat/HandoffSummaryCard'

export interface MessageActionAnchor { text: string; x: number; y: number; element: HTMLElement; selectText?: () => void }

export function MobileTranscript({ session, messages, activityBreaks, commands = [], approvals = [], onApproval, busy, loadOlder, hasOlder, loadingOlder, onMessageAction, onImage, onVideo, onFile, workspace, connectionId, readOnly = false, onSwipe, onSwipePreview, checkpoints, onOpenCheckpoint, highlightMessageId }: {
  session?: Session | null
  messages: Message[]; commands?: import('../../domain/types').Command[]; approvals?: Approval[]; onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  busy: boolean; loadOlder: () => unknown; hasOlder: boolean; loadingOlder: boolean
  onMessageAction: (anchor: MessageActionAnchor) => void; onImage: (url: string, gallery?: string[]) => void; onVideo?: (url: string, name: string) => void; onFile?: (path: string) => void; workspace?: string | null; connectionId?: string | null; onSwipe: (gesture: SessionSwipeGesture) => void
  readOnly?: boolean
  activityBreaks?: readonly string[]
  onSwipePreview?: (pose: import('./mobileGestures').SessionCardPose | null) => void
  checkpoints?: SessionCheckpoint[]
  onOpenCheckpoint?: (cp: SessionCheckpoint) => void
  highlightMessageId?: string | null
}) {
  const { setContainerRef, following, onScroll, scrollToBottom, capturePrependAnchor } = useStickToBottom<HTMLDivElement>({ contentVersion: `${messages.map(m => m.id + ':' + m.text.length).join('|')}|${approvals.map(a => a.id).join('|')}` })
  const older = useOlderMessages({ hasMore: hasOlder, loading: loadingOlder, load: loadOlder, capture: capturePrependAnchor })
  const touch = useRef<{ x: number; y: number } | null>(null)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [selectedText, setSelectedText] = useState<HTMLElement | null>(null)
  const [activeHighlightId, setActiveHighlightId] = useState<string | null>(null)
  const listNodeRef = useRef<HTMLDivElement | null>(null)
  const highlightScrolledRef = useRef<string | null>(null)
  const highlightLoadAttemptsRef = useRef<number>(0)
  useEffect(() => () => { if (hold.current) clearTimeout(hold.current) }, [])
  useEffect(() => {
    if (!selectedText) return
    const bubble = selectedText.querySelector<HTMLElement>('.m-bubble')
    if (!bubble) return
    bubble.classList.add('m-select-text')
    const range = document.createRange()
    range.selectNodeContents(bubble)
    const selection = window.getSelection()
    selection?.removeAllRanges()
    selection?.addRange(range)
    const clearOutside = (event: Event) => { if (!selectedText.contains(event.target as Node)) setSelectedText(null) }
    document.addEventListener('pointerdown', clearOutside, true)
    return () => {
      document.removeEventListener('pointerdown', clearOutside, true)
      bubble.classList.remove('m-select-text')
      if (selection?.anchorNode && bubble.contains(selection.anchorNode)) selection.removeAllRanges()
    }
  }, [selectedText])
  const cancelHold = () => { if (hold.current) clearTimeout(hold.current); hold.current = null }
  const action = (message: Message, element: HTMLElement, x: number, y: number) => onMessageAction({ text: message.text, x, y, element, selectText: () => setSelectedText(element) })
  const rows = messages.map(normalizeMessagePresentation).filter(m => isCompactionMessage(m) || hasMessageContent(m))
  const activity = (m: Message) => !isCompactionMessage(m) && isMessageActivity(m)
  const packs = activityPacks(rows, activityBreaks)
  const checkpointByMsgId = useMemo(
    () => mapCheckpointsToMessages(rows, checkpoints ?? []),
    [rows, checkpoints],
  )
  useEffect(() => {
    highlightScrolledRef.current = null
    highlightLoadAttemptsRef.current = 0
  }, [highlightMessageId, session?.id, session?.agent_id])
  useEffect(() => {
    if (!highlightMessageId || highlightScrolledRef.current === highlightMessageId) return
    const exists = rows.some((m) => m.id === highlightMessageId)
    if (exists) {
      const container = listNodeRef.current
      if (!container) return
      const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(highlightMessageId) : highlightMessageId.replace(/"/g, '\\"')
      const el = container.querySelector<HTMLElement>(`[data-message-id="${escaped}"]`)
      if (!el) return
      highlightScrolledRef.current = highlightMessageId
      el.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'center' })
      setActiveHighlightId(highlightMessageId)
      const timer = window.setTimeout(() => {
        setActiveHighlightId((current) => (current === highlightMessageId ? null : current))
      }, 2600)
      return () => window.clearTimeout(timer)
    }
    if (hasOlder && !loadingOlder && highlightLoadAttemptsRef.current < 3) {
      highlightLoadAttemptsRef.current += 1
      void loadOlder()
    }
  }, [hasOlder, highlightMessageId, loadOlder, loadingOlder, rows])
  return <>
    <div className="m-transcript" role="log" ref={(node) => { setContainerRef(node); listNodeRef.current = node }} onScroll={e => { onScroll(); older.onScroll(e) }} onWheel={older.onWheel}
      onTouchStart={e => { older.onTouchStart(e); if ((e.target as Element).closest('.m-select-text') || startsAtSessionDrawerEdge(e.touches[0].clientX)) { touch.current = null; onSwipePreview?.(null); return }; touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }}
      onTouchEnd={e => { cancelHold(); onSwipePreview?.(null); if (!touch.current) return; const start = touch.current; const end = e.changedTouches[0]; touch.current = null; const gesture = sessionSwipeGesture(start.x, start.y, end.clientX, end.clientY); if (gesture) onSwipe(gesture) }}
      onTouchMove={e => { cancelHold(); older.onTouchMove(e); if (!touch.current || !onSwipePreview) return; onSwipePreview(sessionDragPreview(e.touches[0].clientX - touch.current.x, e.touches[0].clientY - touch.current.y)) }} onTouchCancel={cancelHold}>
      {session?.handoff_context?.trim() && (
        <HandoffSummaryCard
          key={`handoff:${session.agent_id}:${session.id}`}
          session={session}
          compact
          renderMarkdown={(summary) => (
            <MarkdownContent
              value={summary}
              onFileClick={onFile}
              onImageClick={onImage}
              workspace={workspace}
              sessionId={session.id}
              connectionId={connectionId}
            />
          )}
        />
      )}
      {hasOlder && <button className="m-earlier" disabled={loadingOlder} onClick={() => void older.request()}>{loadingOlder ? '正在加载更早消息…' : '查看更早的消息 ↑'}</button>}
      {rows.map((m, i) => {
        if (isCompactionMessage(m)) {
          return (
            <div key={`m-compaction-${m.id}`}>
              <CompactionDivider message={m} />
            </div>
          )
        }
        if (activity(m)) {
          const pack = packs.get(i)
          if (!pack) return null
          const remaining = rows.slice(i + pack.length)
          const isPackRunning = Boolean(busy && !remaining.some(activity) && !remaining.some(item => item.role === 'user' || item.role === 'assistant' && item.kind === 'message'))
          return <LazyDetails className="m-think-pack" key={m.id} loading={isPackRunning} summary={<PackSummary pack={pack} isRunning={isPackRunning} />}>
            <div className="m-think-rail">{pack.map(item => {
              const desc = describeTool(item)
              const itemArgs = extractToolArgs(item.tool)
              const rawCmd = String(itemArgs.command || itemArgs.cmd || itemArgs.CommandLine || itemArgs.Command || '')
              const rawCwd = String(itemArgs.cwd || itemArgs.Cwd || '')
              const rawExitCode = (item.tool as { exit_code?: unknown } | null | undefined)?.exit_code
              const exitCode = typeof rawExitCode === 'number' && Number.isFinite(rawExitCode) ? rawExitCode : null
              const isCmd = desc.iconKey === 'terminal' || Boolean(rawCmd)
              const hasFileDiff = fileChangeDiffs(item).length > 0
              const unwrapped = unwrapCommand(rawCmd)
              const cmdDisplay = isCmd ? formatCommandRowDisplay(desc, unwrapped) : null
              const itemStatus = desc.isFailed ? 'failed' : desc.isRunning ? 'running' : 'success'
              const foldTitle = cmdDisplay
                ? `${cmdDisplay.action} ${cmdDisplay.text}`
                : item.kind !== 'thinking' && desc.target
                  ? `${desc.action} ${desc.target}`
                  : (desc.target || desc.fullTitle)
              return <LazyDetails className={`m-fold ${desc.isFailed ? 'is-failed' : ''}`} key={item.id} loading={desc.isRunning} summary={<span className="m-fold-summary">
                <span className="m-fold-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
                <span className="m-fold-title">{foldTitle}</span>
                {desc.isFailed && <span className="m-badge is-failed">失败</span>}
                {desc.isRunning && <span className="m-badge is-running">执行中</span>}
              </span>}>
                {item.kind === 'thinking' ? (
                  item.text && <div className="m-thinking-content"><MarkdownContent value={item.text} /></div>
                ) : isCmd ? (
                  <ShellOutputBlock
                    command={unwrapped || desc.fullTitle}
                    output={item.text}
                    status={itemStatus}
                    summary={desc.summary}
                    cwd={rawCwd || undefined}
                    exitCode={exitCode}
                  />
                ) : hasFileDiff ? (
                  <FileChangeDiffBlock message={item} session={session} />
                ) : (
                  <ToolOutputBlock
                    text={item.text}
                    payload={Object.keys(itemArgs).length > 0 ? itemArgs : null}
                    toolName={String(item.tool?.name || '')}
                    session={session}
                    onImageClick={onImage}
                  />
                )}
                {!!item.attachments?.length && <div className="m-thumbs">{item.attachments.map(a => {
                  const href = attachmentHref(a)
                  if (!href) return <span key={a.id} className="m-file-link">{a.name}（链接不可用）</span>
                  return a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(href)}><img src={href} alt={a.name} /></button> : <a key={a.id} className="m-file-link" href={href} target="_blank" rel="noreferrer">{a.name}</a>
                })}</div>}
              </LazyDetails>
            })}</div>
          </LazyDetails>
        }
        return <article key={m.id} className={`m-msg m-hold ${m.role === 'user' ? 'm-user' : 'm-assistant'}${activeHighlightId === m.id ? ' is-search-highlight' : ''}`} data-message-id={m.id}
          tabIndex={0}
          onContextMenu={e => { if ((e.target as Element).closest('.m-select-text')) return; e.preventDefault(); e.stopPropagation(); cancelHold(); const rect = e.currentTarget.getBoundingClientRect(); action(m, e.currentTarget, e.clientX || rect.left + rect.width / 2, e.clientY || rect.top + 20) }}
          onTouchStart={e => { cancelHold(); if ((e.target as Element).closest('.m-select-text')) return; const element = e.currentTarget; const point = e.touches[0]; hold.current = setTimeout(() => { action(m, element, point.clientX, point.clientY); navigator.vibrate?.(20) }, 500) }}>
          {isAsyncQuestion(m) && <AsyncUserInputCard message={m} commands={commands} readOnly={readOnly} continued={hasLaterNativeInput(rows, i)} />}
          {!isAsyncQuestion(m) && m.text.trim() && <div className="m-bubble"><MessageBody value={m.text} user={m.role === 'user'} onImageClick={onImage} attachmentNames={m.attachments.filter((attachment) => attachment.media_type.startsWith('image/')).map((attachment) => attachment.name)} renderMarkdown={value => <MarkdownContent value={value} onFileClick={onFile} onImageClick={onImage} onVideoClick={onVideo} workspace={workspace} sessionId={m.session_id} connectionId={connectionId} />} sessionId={m.session_id} connectionId={connectionId || undefined} /></div>}
          {!!m.attachments.length && (() => {
            const imageUrls = m.attachments
              .filter(a => a.media_type.startsWith('image/'))
              .map(a => attachmentHref(a))
              .filter((href): href is string => Boolean(href))
            return <div className="m-thumbs">{m.attachments.map(a => {
              const href = attachmentHref(a)
              if (!href) return <span key={a.id} className="m-file-link">{a.name}（链接不可用）</span>
              if (a.media_type.startsWith('image/')) {
                return <button key={a.id} onClick={() => imageUrls.length > 1 ? onImage(href, imageUrls) : onImage(href)}><img src={href} alt={a.name} /></button>
              }
              if (onVideo && (a.media_type.startsWith('video/') || isMobileVideoFilePath(a.name))) {
                return (
                  <button key={a.id} type="button" className="m-file-pill is-video" onClick={() => onVideo(href, a.name)}>
                    <IconMovie size={14} aria-hidden="true" />
                    <span>{a.name}</span>
                  </button>
                )
              }
              return <a key={a.id} className="m-file-link" href={href} target="_blank" rel="noreferrer">{a.name}</a>
            })}</div>
          })()}
          {m.role === 'user' && (() => {
            const hasAssistantReplied = rows.slice(i + 1).some(r => r.role !== 'user')
            const matchedCmd = m.command_id
              ? commands.find(c => c.id === m.command_id)
              : commands.filter(c => c.text.trim() === m.text.trim()).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
            const isAgentAccepted = hasAssistantReplied || Boolean(matchedCmd && (matchedCmd.state === 'accepted' || matchedCmd.state === 'running' || matchedCmd.state === 'completed'))
            return (
              <small className="m-delivered" style={{ color: isAgentAccepted ? 'var(--m-primary)' : 'var(--m-muted)' }}>
                {isAgentAccepted ? (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 2 }}><IconChecks size={13} /> {hasAssistantReplied ? '已响应' : '已接收'}</span>
                ) : (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 2 }}><IconCheck size={13} /> 已发送</span>
                )}
              </small>
            )
          })()}
          {m.role === 'assistant' && (() => {
            const cp = checkpointByMsgId.get(m.id)
            if (!cp || !onOpenCheckpoint) return null
            return (
              <button
                type="button"
                className="cp-inline-pill cp-inline-pill-mobile"
                onClick={() => onOpenCheckpoint(cp)}
              >
                <IconHistory size={13} aria-hidden="true" />
                <span>
                  #{cp.turn_number}
                  {cp.files_changed > 0 ? ` · ${cp.files_changed} 文件` : ''}
                </span>
                {cp.insertions > 0 && <span className="cp-stat-add">+{cp.insertions}</span>}
                {cp.deletions > 0 && <span className="cp-stat-del">-{cp.deletions}</span>}
              </button>
            )
          })()}
        </article>
      })}
      {busy && (!rows.length || rows[rows.length - 1].role === 'user') && <div className="m-thinking-placeholder">··· 正在思考与响应中…</div>}
      {approvals.length > 0 && (
        <section className="m-native-approvals" aria-label="对话待处理审批">
          {approvals.map(approval => isUserInput(approval) ? <UserInputCard key={approval.id} approval={approval} /> : (
            <div key={approval.id} className="approval-card" style={{ padding: '10px 12px', borderRadius: 10, margin: '8px 0' }}>
              <h4 style={{ margin: '0 0 6px', fontSize: 13, fontWeight: 600 }}>{approval.title}</h4>
              {approval.detail && <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxHeight: 160, overflow: 'auto', margin: '0 0 8px', fontSize: 12 }}>{approval.detail}</pre>}
              {onApproval && (
                <div style={{ display: 'flex', gap: 8 }}>
                  <button className="approval-button approval-approve" type="button" onClick={() => onApproval(approval, 'approve')}>允许本次</button>
                  <button className="approval-button approval-cancel" type="button" onClick={() => onApproval(approval, 'cancel')}>拒绝</button>
                </div>
              )}
            </div>
          ))}
        </section>
      )}
    </div>
    {!following && (
      <button className="m-return-bottom" aria-label="回到底部" onClick={scrollToBottom}>
        {busy ? <span className="return-bottom-wave" aria-hidden="true"><i /><i /><i /></span> : <IconChevronDown size={19} />}
      </button>
    )}
  </>
}
