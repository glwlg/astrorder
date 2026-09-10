import { useEffect, useRef } from 'react'
import { IconCheck, IconChevronDown } from '@tabler/icons-react'
import type { Message } from '../../domain/types'
import { MobileMarkdown as MarkdownContent } from './MobileMarkdown'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { sessionSwipeGesture, sessionDragPreview, startsAtSessionDrawerEdge, type SessionSwipeGesture } from './mobileGestures'
import { describeTool, PackSummary, ToolLineIcon } from '../chat/toolPresentation'

export interface MessageActionAnchor { text: string; x: number; y: number; element: HTMLElement }

export function MobileTranscript({ messages, busy, loadOlder, hasOlder, loadingOlder, onMessageAction, onImage, onSwipe, onSwipePreview }: {
  messages: Message[]; busy: boolean; loadOlder: () => unknown; hasOlder: boolean; loadingOlder: boolean
  onMessageAction: (anchor: MessageActionAnchor) => void; onImage: (url: string) => void; onSwipe: (gesture: SessionSwipeGesture) => void
  onSwipePreview?: (pose: import('./mobileGestures').SessionCardPose | null) => void
}) {
  const { setContainerRef, following, onScroll, scrollToBottom, capturePrependAnchor } = useStickToBottom<HTMLDivElement>({ contentVersion: messages.map(m => m.id + ':' + m.text.length).join('|') })
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
      {rows.map((m, i) => {
        if (activity(m)) {
          if (i && activity(rows[i - 1])) return null
          const pack: Message[] = []
          for (let n = i; n < rows.length && activity(rows[n]); n++) pack.push(rows[n])
          return <LazyDetails className="m-think-pack" key={m.id} summary={<PackSummary pack={pack} />}>
            <div className="m-think-rail">{pack.map(item => {
              const desc = describeTool(item)
              return <LazyDetails className={`m-fold ${desc.isFailed ? 'is-failed' : ''}`} key={item.id} summary={<span className="m-fold-summary">
                <span className="m-fold-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
                <span className="m-fold-title">{desc.target || desc.fullTitle}</span>
                {desc.isFailed && <span className="m-badge is-failed">失败</span>}
                {desc.isRunning && <span className="m-badge is-running">执行中</span>}
              </span>}>
                {item.text && (item.kind === 'thinking' ? <div className="m-thinking-content"><MarkdownContent value={item.text} /></div> : <pre className="m-tool-output">{item.text}</pre>)}
                {item.tool?.arguments != null && Object.keys(item.tool.arguments).length > 0 && <pre className="m-tool-args">{JSON.stringify(item.tool.arguments, null, 2)}</pre>}
                {!!item.attachments?.length && <div className="m-thumbs">{item.attachments.map(a => a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(a.url)}><img src={a.url} alt={a.name} /></button> : <a key={a.id} href={a.url} target="_blank" rel="noreferrer">{a.name}</a>)}</div>}
              </LazyDetails>
            })}</div>
          </LazyDetails>
        }
        return <article key={m.id} className={`m-msg ${m.role === 'user' ? 'm-user' : 'm-assistant'}`} data-message-id={m.id}
          tabIndex={0}
          onContextMenu={e => { e.preventDefault(); cancelHold(); const rect = e.currentTarget.getBoundingClientRect(); onMessageAction({ text: m.text, x: e.clientX || rect.left + rect.width / 2, y: e.clientY || rect.top + 20, element: e.currentTarget }) }}
          onTouchStart={e => { cancelHold(); const element = e.currentTarget; const point = e.touches[0]; hold.current = setTimeout(() => { onMessageAction({ text: m.text, x: point.clientX, y: point.clientY, element }); navigator.vibrate?.(20) }, 500) }}>
          {m.text.trim() && <div className="m-bubble"><MessageBody value={m.text} user={m.role === 'user'} renderMarkdown={value => <MarkdownContent value={value} />} /></div>}
          {!!m.attachments.length && <div className="m-thumbs">{m.attachments.map(a => a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(a.url)}><img src={a.url} alt={a.name} /></button> : <a key={a.id} href={a.url} target="_blank" rel="noreferrer">{a.name}</a>)}</div>}
          {m.role === 'user' && <small className="m-delivered"><IconCheck size={12} /> 已送达</small>}
        </article>
      })}
      {busy && (!rows.length || rows[rows.length - 1].role === 'user') && <div className="m-thinking-placeholder">··· 正在思考与响应中…</div>}
    </div>
    {!following && <button className="m-return-bottom" aria-label="回到底部" onClick={scrollToBottom}><IconChevronDown size={19} /></button>}
  </>
}
