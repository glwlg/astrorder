import { useRef, useState } from 'react'

export function MobileTaskNotices<T extends { id: string; title: string }>({ tasks, onOpen, scope }: { tasks: T[]; onOpen: (task: T) => void; scope: string }) {
  const storageKey = `astrorder:task-notices:${scope}`
  const [dismissed, setDismissed] = useState<Set<string>>(() => {
    try { const saved: unknown = JSON.parse(localStorage.getItem(storageKey) || '[]'); return new Set(Array.isArray(saved) ? saved.filter((id): id is string => typeof id === 'string') : []) } catch { return new Set() }
  })
  const start = useRef<{ x: number; y: number } | null>(null)
  const suppressClick = useRef(false)
  const [drag, setDrag] = useState<{ id: string; x: number } | null>(null)
  const dismiss = (id: string) => {
    const next = new Set([...dismissed, id])
    localStorage.setItem(storageKey, JSON.stringify([...next]))
    setDismissed(next)
  }
  const visible = tasks.filter(task => !dismissed.has(task.id))
  if (!visible.length) return null
  return <div className="m-active-tasks">{visible.map(task => <button key={task.id}
    title="左滑关闭提示，任务继续运行"
    style={{ touchAction: 'pan-y', transform: drag?.id === task.id ? `translateX(${drag.x}px)` : undefined }}
    onTouchStart={event => { event.stopPropagation(); const p = event.touches[0]; start.current = { x: p.clientX, y: p.clientY }; suppressClick.current = false }}
    onTouchMove={event => {
      event.stopPropagation()
      if (!start.current) return
      const p = event.touches[0], dx = p.clientX - start.current.x, dy = p.clientY - start.current.y
      if (Math.abs(dy) > Math.abs(dx)) { start.current = null; setDrag(null); return }
      if (dx < -8) { suppressClick.current = true; setDrag({ id: task.id, x: Math.max(-160, dx) }) }
    }}
    onTouchEnd={event => {
      event.stopPropagation()
      const origin = start.current, p = event.changedTouches[0]
      start.current = null; setDrag(null)
      if (origin && p && origin.x - p.clientX >= 64 && Math.abs(p.clientY - origin.y) < (origin.x - p.clientX) / 2) { suppressClick.current = true; dismiss(task.id) }
    }}
    onTouchCancel={() => { start.current = null; setDrag(null) }}
    onKeyDown={event => { if (event.key === 'Delete') { event.preventDefault(); dismiss(task.id) } }}
    onClick={() => { if (!suppressClick.current) onOpen(task); suppressClick.current = false }}
  ><span className="m-dot online" />{task.title}</button>)}</div>
}
