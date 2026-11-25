import { useCallback, useEffect, useMemo, useState } from 'react'
import { useReducedMotion } from '@mantine/hooks'
import type { Approval, Message } from '../../domain/types'
import { isMessageActivity } from '../../domain/messagePresentation'
import { isCompactionMessage } from './CompactionDivider'
import './turnMinimap.css'

export type MinimapNodeKind = 'user' | 'assistant' | 'code' | 'activity' | 'approval'

export interface MinimapNode {
  id: string
  kind: MinimapNodeKind
  label: string
  preview: string
  time: string
}

interface TurnMinimapProps {
  visibleMessages: Message[]
  activityPacks: Map<number, Message[]>
  approvals: Approval[]
  scroller: HTMLDivElement | null
}

function formatClock(iso?: string): string {
  if (!iso) return ''
  const ts = Date.parse(iso)
  if (!Number.isFinite(ts)) return ''
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

function cleanSnippet(text: string): string {
  const flat = text
    .replace(/```[\s\S]*?```/g, '[代码片段]')
    .replace(/[#>*`]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
  if (!flat) return '内容记录'
  return flat.length > 64 ? `${flat.slice(0, 64)}…` : flat
}

export function buildMinimapNodes(
  visibleMessages: Message[],
  packs: Map<number, Message[]>,
  approvals: Approval[],
): MinimapNode[] {
  const nodes: MinimapNode[] = []
  visibleMessages.forEach((message, idx) => {
    if (isCompactionMessage(message)) return
    if (isMessageActivity(message)) {
      const pack = packs.get(idx)
      if (!pack) return
      nodes.push({
        id: message.id,
        kind: 'activity',
        label: `工具与思考 · ${pack.length} 步`,
        preview: cleanSnippet(pack[pack.length - 1]?.text || '过程执行步骤'),
        time: formatClock(message.created_at),
      })
      return
    }
    if (message.role === 'user') {
      nodes.push({
        id: message.id,
        kind: 'user',
        label: '用户指令',
        preview: cleanSnippet(message.text || '附件指令'),
        time: formatClock(message.created_at),
      })
      return
    }
    const hasCode = /```|::code-comment/.test(message.text || '')
    nodes.push({
      id: message.id,
      kind: hasCode ? 'code' : 'assistant',
      label: hasCode ? '代码与结论' : 'Agent 回复',
      preview: cleanSnippet(message.text || '已回复'),
      time: formatClock(message.created_at),
    })
  })
  if (approvals.length > 0) {
    nodes.push({
      id: '__approvals__',
      kind: 'approval',
      label: `待处理审批 · ${approvals.length}`,
      preview: cleanSnippet(approvals[0].title || approvals[0].detail || '等待授权'),
      time: '待处理',
    })
  }
  return nodes
}

export function TurnMinimap({ visibleMessages, activityPacks, approvals, scroller }: TurnMinimapProps) {
  const reducedMotion = useReducedMotion()
  const [activeId, setActiveId] = useState<string | null>(null)
  const [hoveredId, setHoveredId] = useState<string | null>(null)

  const nodes = useMemo(
    () => buildMinimapNodes(visibleMessages, activityPacks, approvals),
    [activityPacks, approvals, visibleMessages],
  )

  // Cap rendered density gracefully on massive 200+ turn logs while preserving every user/approval anchor
  const renderedNodes = useMemo(() => {
    if (nodes.length <= 42) return nodes
    const stride = Math.ceil(nodes.length / 42)
    return nodes.filter((node, index) =>
      node.kind === 'user' || node.kind === 'approval' || index === nodes.length - 1 || index % stride === 0,
    )
  }, [nodes])

  useEffect(() => {
    if (!scroller || renderedNodes.length < 3) return
    let rafId = 0
    const syncActive = () => {
      rafId = 0
      const box = scroller.getBoundingClientRect()
      const anchorY = box.top + box.height * 0.36
      const elements = scroller.querySelectorAll<HTMLElement>('[data-turn-id]')
      let bestId: string | null = null
      let bestDist = Infinity
      elements.forEach((el) => {
        const rect = el.getBoundingClientRect()
        const dist = Math.abs(rect.top - anchorY)
        if (dist < bestDist) {
          bestDist = dist
          bestId = el.dataset.turnId || null
        }
      })
      if (bestId) setActiveId(bestId)
    }
    const onScroll = () => {
      if (!rafId) rafId = requestAnimationFrame(syncActive)
    }
    syncActive()
    scroller.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      scroller.removeEventListener('scroll', onScroll)
      if (rafId) cancelAnimationFrame(rafId)
    }
  }, [renderedNodes.length, scroller])

  const jumpToNode = useCallback(
    (nodeId: string) => {
      if (!scroller) return
      const selector = nodeId === '__approvals__'
        ? '.transcript-approvals'
        : `[data-turn-id="${CSS.escape(nodeId)}"]`
      const target = scroller.querySelector<HTMLElement>(selector)
      if (!target) return
      target.scrollIntoView({ behavior: reducedMotion ? 'auto' : 'smooth', block: 'center' })
      setActiveId(nodeId)
      target.classList.remove('turn-jump-highlight')
      // Force reflow so repeat clicks re-trigger highlight pulse
      void target.offsetWidth
      target.classList.add('turn-jump-highlight')
    },
    [reducedMotion, scroller],
  )

  if (renderedNodes.length < 3) return null

  const hoveredNode = hoveredId ? renderedNodes.find((n) => n.id === hoveredId) || null : null

  return (
    <nav className="turn-minimap" aria-label="对话星轨时间轴">
      <div className="turn-minimap-rail">
        {renderedNodes.map((node) => {
          const isActive = activeId === node.id
          return (
            <button
              key={node.id}
              type="button"
              className={`turn-minimap-tick is-${node.kind}${isActive ? ' is-active' : ''}`}
              aria-label={`${node.label}：${node.preview}`}
              aria-current={isActive ? 'location' : undefined}
              onMouseEnter={() => setHoveredId(node.id)}
              onMouseLeave={() => setHoveredId((prev) => (prev === node.id ? null : prev))}
              onFocus={() => setHoveredId(node.id)}
              onBlur={() => setHoveredId((prev) => (prev === node.id ? null : prev))}
              onClick={() => jumpToNode(node.id)}
            />
          )
        })}
      </div>

      {hoveredNode && (
        <div
          className={`turn-minimap-preview is-${hoveredNode.kind}`}
          role="tooltip"
        >
          <div className="turn-minimap-preview-head">
            <span className={`turn-minimap-dot is-${hoveredNode.kind}`} aria-hidden="true" />
            <strong>{hoveredNode.label}</strong>
            {hoveredNode.time && <span className="turn-minimap-time">{hoveredNode.time}</span>}
          </div>
          <p className="turn-minimap-preview-body">{hoveredNode.preview}</p>
        </div>
      )}
    </nav>
  )
}
