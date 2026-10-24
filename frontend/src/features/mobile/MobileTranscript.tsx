import { UserInputCard, isUserInput } from '../../components/UserInputCard'
import { AsyncUserInputCard, isAsyncQuestion, hasLaterNativeInput } from '../../components/AsyncUserInputCard'
import { useEffect, useRef, useState } from 'react'
import { IconCheck, IconChecks, IconChevronDown } from '@tabler/icons-react'
import type { Approval, Message } from '../../domain/types'
import { attachmentHref } from '../../domain/attachmentHref'
import { hasMessageContent, isMessageActivity, normalizeMessagePresentation } from '../../domain/messagePresentation'
import { activityPacks } from '../../domain/activityPacks'
import { MobileMarkdown as MarkdownContent } from './MobileMarkdown'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { sessionSwipeGesture, sessionDragPreview, startsAtSessionDrawerEdge, type SessionSwipeGesture } from './mobileGestures'
import { describeTool, FileChangeDiffBlock, fileChangeDiffs, PackSummary, ShellOutputBlock, ToolLineIcon, unwrapCommand } from '../chat/toolPresentation'
import { ToolOutputBlock } from '../chat/ToolOutputBlock'
import { isCompactionMessage, CompactionDivider } from '../chat/CompactionDivider'
import { useReducedMotion } from '@mantine/hooks'
import { AnimatePresence, motion } from 'motion/react'

export interface MessageActionAnchor { text: string; x: number; y: number; element: HTMLElement; selectText?: () => void }

export function MobileTranscript({ messages, activityBreaks, commands = [], approvals = [], onApproval, busy, loadOlder, hasOlder, loadingOlder, onMessageAction, onImage, onFile, workspace, connectionId, readOnly = false, onSwipe, onSwipePreview }: {
  messages: Message[]; commands?: import('../../domain/types').Command[]; approvals?: Approval[]; onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  busy: boolean; loadOlder: () => unknown; hasOlder: boolean; loadingOlder: boolean
  onMessageAction: (anchor: MessageActionAnchor) => void; onImage: (url: string) => void; onFile?: (path: string) => void; workspace?: string | null; connectionId?: string | null; onSwipe: (gesture: SessionSwipeGesture) => void
  readOnly?: boolean
  activityBreaks?: readonly string[]
  onSwipePreview?: (pose: import('./mobileGestures').SessionCardPose | null) => void
}) {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  const enter = reducedMotion ? {} : { opacity: 0, y: 10, scale: 0.99 }
  const { setContainerRef, following, onScroll, scrollToBottom, capturePrependAnchor } = useStickToBottom<HTMLDivElement>({ contentVersion: `${messages.map(m => m.id + ':' + m.text.length).join('|')}|${approvals.map(a => a.id).join('|')}` })
  const older = useOlderMessages({ hasMore: hasOlder, loading: loadingOlder, load: loadOlder, capture: capturePrependAnchor })
  const touch = useRef<{ x: number; y: number } | null>(null)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [selectedText, setSelectedText] = useState<HTMLElement | null>(null)
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
  return <>
    <div className="m-transcript" role="log" ref={setContainerRef} onScroll={e => { onScroll(); older.onScroll(e) }} onWheel={older.onWheel}
      onTouchStart={e => { older.onTouchStart(e); if ((e.target as Element).closest('.m-select-text') || startsAtSessionDrawerEdge(e.touches[0].clientX)) { touch.current = null; onSwipePreview?.(null); return }; touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }}
      onTouchEnd={e => { cancelHold(); onSwipePreview?.(null); if (!touch.current) return; const start = touch.current; const end = e.changedTouches[0]; touch.current = null; const gesture = sessionSwipeGesture(start.x, start.y, end.clientX, end.clientY); if (gesture) onSwipe(gesture) }}
      onTouchMove={e => { cancelHold(); older.onTouchMove(e); if (!touch.current || !onSwipePreview) return; onSwipePreview(sessionDragPreview(e.touches[0].clientX - touch.current.x, e.touches[0].clientY - touch.current.y)) }} onTouchCancel={cancelHold}>
      {hasOlder && <button className="m-earlier" disabled={loadingOlder} onClick={() => void older.request()}>{loadingOlder ? '正在加载更早消息…' : '查看更早的消息 ↑'}</button>}
      <AnimatePresence initial={false}>
      {rows.map((m, i) => {
        if (isCompactionMessage(m)) {
          return (
            <motion.div key={`m-compaction-${m.id}`} initial={enter} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}>
              <CompactionDivider message={m} />
            </motion.div>
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
              const itemArgs = (item.tool?.arguments && typeof item.tool.arguments === 'object') ? (item.tool.arguments as Record<string, unknown>) : {}
              const isCmd = desc.iconKey === 'terminal' || Boolean(itemArgs.command || itemArgs.cmd)
              const hasFileDiff = fileChangeDiffs(item).length > 0
              const unwrapped = unwrapCommand(String(itemArgs.command || itemArgs.cmd || ''))
              const itemStatus = desc.isFailed ? 'failed' : desc.isRunning ? 'running' : 'success'
              const foldTitle = isCmd
                ? `${desc.isRunning ? '运行' : desc.isFailed ? '运行失败' : '已运行'} ${unwrapped || desc.target || desc.fullTitle}`
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
                  <ShellOutputBlock command={unwrapped || desc.fullTitle} output={item.text} status={itemStatus} />
                ) : hasFileDiff ? (
                  <FileChangeDiffBlock message={item} />
                ) : (
                  <ToolOutputBlock
                    text={item.text}
                    payload={item.tool?.arguments != null && Object.keys(item.tool.arguments).length > 0 ? (item.tool.arguments as Record<string, unknown>) : null}
                    toolName={String(item.tool?.name || '')}
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
        return <motion.article key={m.id} layout="position" initial={enter} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }} className={`m-msg m-hold ${m.role === 'user' ? 'm-user' : 'm-assistant'}`} data-message-id={m.id}
          tabIndex={0}
          onContextMenu={e => { if ((e.target as Element).closest('.m-select-text')) return; e.preventDefault(); e.stopPropagation(); cancelHold(); const rect = e.currentTarget.getBoundingClientRect(); action(m, e.currentTarget, e.clientX || rect.left + rect.width / 2, e.clientY || rect.top + 20) }}
          onTouchStart={e => { cancelHold(); if ((e.target as Element).closest('.m-select-text')) return; const element = e.currentTarget; const point = e.touches[0]; hold.current = setTimeout(() => { action(m, element, point.clientX, point.clientY); navigator.vibrate?.(20) }, 500) }}>
          {isAsyncQuestion(m) && <AsyncUserInputCard message={m} commands={commands} readOnly={readOnly} continued={hasLaterNativeInput(rows, i)} />}
          {!isAsyncQuestion(m) && m.text.trim() && <div className="m-bubble"><MessageBody value={m.text} user={m.role === 'user'} onImageClick={onImage} attachmentNames={m.attachments.filter((attachment) => attachment.media_type.startsWith('image/')).map((attachment) => attachment.name)} renderMarkdown={value => <MarkdownContent value={value} onFileClick={onFile} onImageClick={onImage} workspace={workspace} sessionId={m.session_id} connectionId={connectionId} />} sessionId={m.session_id} connectionId={connectionId || undefined} /></div>}
          {!!m.attachments.length && <div className="m-thumbs">{m.attachments.map(a => {
            const href = attachmentHref(a)
            if (!href) return <span key={a.id} className="m-file-link">{a.name}（链接不可用）</span>
            return a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(href)}><img src={href} alt={a.name} /></button> : <a key={a.id} className="m-file-link" href={href} target="_blank" rel="noreferrer">{a.name}</a>
          })}</div>}
          {m.role === 'user' && (() => {
            const hasAssistantReplied = rows.slice(i + 1).some(r => r.role !== 'user')
            const matchedCmd = m.command_id
              ? commands.find(c => c.id === m.command_id)
              : commands.filter(c => c.text.trim() === m.text.trim()).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
            const isAgentAccepted = hasAssistantReplied || Boolean(matchedCmd && (matchedCmd.state === 'accepted' || matchedCmd.state === 'running' || matchedCmd.state === 'completed'))
            return (
              <small className="m-delivered" style={{ color: isAgentAccepted ? 'var(--m-primary, #4c6ef5)' : 'var(--m-muted, #868e96)' }}>
                {isAgentAccepted ? (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 2 }}><IconChecks size={13} /> {hasAssistantReplied ? '已响应' : '已接收'}</span>
                ) : (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 2 }}><IconCheck size={13} /> 已发送</span>
                )}
              </small>
            )
          })()}
        </motion.article>
      })}
      </AnimatePresence>
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
