import { useEffect, useRef } from 'react'
import { IconCheck, IconChevronDown } from '@tabler/icons-react'
import type { Message } from '../../domain/types'
import { MobileMarkdown as MarkdownContent } from './MobileMarkdown'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { LazyDetails } from '../../components/LazyDetails'

export interface MessageActionAnchor { text: string; x: number; y: number; element: HTMLElement }

export function MobileTranscript({ messages, busy, loadOlder, hasOlder, loadingOlder, onMessageAction, onImage, onSwipe }: {
  messages: Message[]; busy: boolean; loadOlder: () => unknown; hasOlder: boolean; loadingOlder: boolean
  onMessageAction: (anchor: MessageActionAnchor) => void; onImage: (url: string) => void; onSwipe: (direction: number) => void
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
      onTouchStart={e => { older.onTouchStart(e); touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }}
      onTouchEnd={e => { cancelHold(); if (!touch.current) return; const dx = e.changedTouches[0].clientX - touch.current.x; const dy = e.changedTouches[0].clientY - touch.current.y; touch.current = null; if (Math.abs(dx) > 90 && Math.abs(dy) < 45) onSwipe(dx < 0 ? 1 : -1) }}
      onTouchMove={e => { cancelHold(); older.onTouchMove(e) }} onTouchCancel={cancelHold}>
      {hasOlder && <button className="m-earlier" disabled={loadingOlder} onClick={() => void older.request()}>{loadingOlder ? '正在加载更早消息…' : '查看更早的消息 ↑'}</button>}
      {rows.map((m, i) => {
        if (activity(m)) {
          if (i && activity(rows[i - 1])) return null
          const pack: Message[] = []
          for (let n = i; n < rows.length && activity(rows[n]); n++) pack.push(rows[n])
          const live = busy && i + pack.length === rows.length
          return <LazyDetails className="m-think-pack" key={m.id} summary={`思考与工具 · ${pack.length} 项${live ? ' · 运行中' : ''}`}>
            <div className="m-think-rail">{pack.map(item => <LazyDetails className="m-fold" key={item.id} summary={item.kind === 'thinking' ? '思考' : `工具 · ${String(item.tool?.name || '工具')}`}>
              {item.text && <MarkdownContent value={item.text} />}
              {item.tool?.arguments != null && <pre>{JSON.stringify(item.tool.arguments, null, 2)}</pre>}
            </LazyDetails>)}</div>
          </LazyDetails>
        }
        return <article key={m.id} className={`m-msg ${m.role === 'user' ? 'm-user' : 'm-assistant'}`} data-message-id={m.id}
          tabIndex={0}
          onContextMenu={e => { e.preventDefault(); cancelHold(); const rect = e.currentTarget.getBoundingClientRect(); onMessageAction({ text: m.text, x: e.clientX || rect.left + rect.width / 2, y: e.clientY || rect.top + 20, element: e.currentTarget }) }}
          onTouchStart={e => { cancelHold(); const element = e.currentTarget; const point = e.touches[0]; hold.current = setTimeout(() => { onMessageAction({ text: m.text, x: point.clientX, y: point.clientY, element }); navigator.vibrate?.(20) }, 500) }}>
          <div className="m-msg-meta">{m.role === 'user' ? '用户' : '助手'}</div>
          <div className="m-bubble"><MarkdownContent value={m.text} /></div>
          {!!m.attachments.length && <div className="m-thumbs">{m.attachments.map(a => a.media_type.startsWith('image/') ? <button key={a.id} onClick={() => onImage(a.url)}><img src={a.url} alt={a.name} /></button> : <a key={a.id} href={a.url} target="_blank" rel="noreferrer">{a.name}</a>)}</div>}
          {m.role === 'user' && <small className="m-delivered"><IconCheck size={12} /> 已送达</small>}
        </article>
      })}
      {busy && (!rows.length || rows[rows.length - 1].role === 'user') && <div className="m-thinking-placeholder">··· 正在思考与响应中…</div>}
    </div>
    {!following && <button className="m-return-bottom" aria-label="回到底部" onClick={scrollToBottom}><IconChevronDown size={19} /></button>}
  </>
}
