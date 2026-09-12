import { IconChevronRight } from '@tabler/icons-react'
import { useState, type ReactNode } from 'react'
import { AstrorderLoader } from './AnimatedStatus'

/** Closed means unmounted: large markdown/tool payloads are parsed only on demand. */
export function LazyDetails({ className, summary, children, loading = false }: { className?: string; summary: ReactNode; children: ReactNode; loading?: boolean }) {
  const [open, setOpen] = useState(false)
  return <details className={['lazy-details', className].filter(Boolean).join(' ')} open={open} onToggle={event => { if (event.target === event.currentTarget) setOpen(event.currentTarget.open) }}>
    <summary onClick={event => { event.preventDefault(); setOpen(value => !value) }}>
      <span className={`lazy-details-indicator${loading ? ' is-loading' : ''}`} aria-hidden="true">
        {loading ? <AstrorderLoader size={14} /> : <IconChevronRight size={14} />}
      </span>
      {summary}
    </summary>
    {open && children}
  </details>
}
