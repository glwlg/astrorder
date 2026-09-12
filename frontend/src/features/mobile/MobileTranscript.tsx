import { useEffect, useRef } from 'react'
import { IconCheck, IconChevronDown } from '@tabler/icons-react'
import type { Approval, Message } from '../../domain/types'
import { MobileMarkdown as MarkdownContent } from './MobileMarkdown'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { sessionSwipeGesture, sessionDragPreview, startsAtSessionDrawerEdge, type SessionSwipeGesture } from './mobileGestures'
import { describeTool, PackSummary, ToolLineIcon } from '../chat/toolPresentation'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'

export interface MessageActionAnchor { text: string; x: number; y: number; element: HTMLElement }

export function MobileTranscript({ messages, approvals = [], onApproval, busy, loadOlder, hasOlder, loadingOlder, onMessageAction, onImage, onFile, onSwipe, onSwipePreview }: {
  messages: Message[]; approvals?: Approval[]; onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  busy: boolean; loadOlder: () => unknown; hasOlder: boolean; loadingOlder: boolean
  onMessageAction: (anchor: MessageActionAnchor) => void; onImage: (url: string) => void; onFile?: (path: string) => void; onSwipe: (gesture: SessionSwipeGesture) => void
  onSwipePreview?: (pose: import('./mobileGestures').SessionCardPose | null) => void
}) {
  const reducedMotion = useReducedMotion()
  const enter = reducedMotion ? {} : { opacity: 0, y: 10, scale: 0.99 }
  const { setContainerRef, following, onScroll, scrollToBottom, capturePrependAnchor } = useStickToBottom<HTMLDivElement>({ contentVersion: `${messages.map(m => m.id + ':' + m.text.length).join('|')}|${approvals.map(a => a.id).join('|')}` })
  const older = useOlderMessages({ hasMore: hasOlder, loading: loadingOlder, load: loadOlder, capture: capturePrependAnchor })
  const touch = useRef<{ x: number; y: number } | null>(null)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (hold.current) clearTimeout(hold.current) }, [])
  const cancelHold = () => { if (hold.current) clearTimeout(hold.current); hold.current = null }
  const rows = messages.filter(m => m.kind !== 'message' || m.text.trim() || m.attachments.length)
  const activity = (m: Message) => m.kind === 'thinking' || m.kind === 'tool' || m.role === 'tool'
  return <>
    <div className="m-transcript" role="log" ref={setContainerRef} onScroll={e => { onScroll(); older.onScroll(e) }} onWheel={older.onWheel}
      onTouchStart={e => { older.onTouchStart(e); if (startsAtSessionDrawerEdge(e.touches[0].clientX)) { touch.current = null; onSwipePreview?.(null); return }; touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }}
      onTouchEnd={e => { cancelHold(); onSwipePreview?.(null); if (!touch.current) return; const start = touch.current; const end = e.changedTouches[0]; touch.current = null; const gesture = sessionSwipeGesture(start.x, start.y, end.clientX, end.clientY); if (gesture) onSwipe(gesture) }}
      onTouchMove={e => { cancelHold(); older.onTouchMove(e); if (!touch.current || !onSwipePreview) return; onSwipePreview(sessionDragPreview(e.touches[0].clientX - touch.current.x, e.touches[0].clientY - touch.current.y)) }} onTouchCancel={cancelHold}>
      {hasOlder && <button className="m-earlier" disabled={loadingOlder} onClick={() => void older.request()}>{loadingOlder ? '正在加载更早消息…' : '查看更早的消息 ↑'}</button>}
      <AnimatePresence initial={false}>
      {rows.map((m, i) => {
        if (activity(m)) {
          if (i && activity(rows[i - 1])) return null
          const pack: Message[] = []
          for (let n = i; n < rows.length && activity(rows[n]); n++) pack.push(rows[n])
          return <LazyDetails className="m-think-pack" key={m.id} loading={busy && i + pack.length === rows.length} summary={<PackSummary pack={pack} />}>
            <div className="m-think-rail">{pack.map(item => {
              const desc = describeTool(item)
              return <LazyDetails className={`m-fold ${desc.isFailed ? 'is-failed' : ''}`} key={item.id} loading={desc.isRunning} summary={<span className="m-fold-summary">
                <span className="m-fold-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
                <span className="m-fold-title">{desc.target || desc.fullTitle}</span>
                {desc.isFailed && <span className="m-badge is-failed">失败</span>}
                {desc.isRunning && <span className="m-badge is-running">执行中</span>}
              </span>}>
                {item.text && (item.kind === 'thinking' ? <div className="m-thinking-content"><MarkdownContent value={item.text} /></div> : <pre className="m-tool-output">{item.text}</pre>)}
                {item.tool?.arguments != null && Object.keys(item.tool.arguments).length > 0 && <pre className="m-tool-args">{JSON.stringify(item.tool.arguments, null, 2)}</pre>}
                {!!item.attachments?.length && <div className="m-thumbs">{item.attachments.map(a => a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(a.url)}><img src={a.url} alt={a.name} /></button> : onFile ? <button key={a.id} className="m-file-link" onClick={() => onFile(a.url)}>{a.name}</button> : <a key={a.id} href={a.url} target="_blank" rel="noreferrer">{a.name}</a>)}</div>}
              </LazyDetails>
            })}</div>
          </LazyDetails>
        }
        return <motion.article key={m.id} layout="position" initial={enter} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }} className={`m-msg m-hold ${m.role === 'user' ? 'm-user' : 'm-assistant'}`} data-message-id={m.id}
          tabIndex={0}
          onContextMenu={e => { e.preventDefault(); cancelHold(); const rect = e.currentTarget.getBoundingClientRect(); onMessageAction({ text: m.text, x: e.clientX || rect.left + rect.width / 2, y: e.clientY || rect.top + 20, element: e.currentTarget }) }}
          onTouchStart={e => { cancelHold(); const element = e.currentTarget; const point = e.touches[0]; hold.current = setTimeout(() => { onMessageAction({ text: m.text, x: point.clientX, y: point.clientY, element }); navigator.vibrate?.(20) }, 500) }}>
          {m.text.trim() && <div className="m-bubble"><MessageBody value={m.text} user={m.role === 'user'} attachmentNames={m.attachments.filter((attachment) => attachment.media_type.startsWith('image/')).map((attachment) => attachment.name)} renderMarkdown={value => <MarkdownContent value={value} onFileClick={onFile} onImageClick={onImage} />} /></div>}
          {!!m.attachments.length && <div className="m-thumbs">{m.attachments.map(a => a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(a.url)}><img src={a.url} alt={a.name} /></button> : onFile ? <button key={a.id} className="m-file-link" onClick={() => onFile(a.url)}>{a.name}</button> : <a key={a.id} href={a.url} target="_blank" rel="noreferrer">{a.name}</a>)}</div>}
          {m.role === 'user' && <small className="m-delivered"><IconCheck size={12} /> 已送达</small>}
        </motion.article>
      })}
      </AnimatePresence>
      {busy && (!rows.length || rows[rows.length - 1].role === 'user') && <div className="m-thinking-placeholder">··· 正在思考与响应中…</div>}
      {approvals.length > 0 && (
        <section className="m-native-approvals" aria-label="对话待处理审批">
          {approvals.map(approval => (
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
    {!following && <button className="m-return-bottom" aria-label="回到底部" onClick={scrollToBottom}><IconChevronDown size={19} /></button>}
  </>
}
