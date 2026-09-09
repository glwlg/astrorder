import { useState, type ReactNode } from 'react'

/** Closed means unmounted: large markdown/tool payloads are parsed only on demand. */
export function LazyDetails({ className, summary, children }: { className?: string; summary: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  return <details className={className} open={open} onToggle={event => { if (event.target === event.currentTarget) setOpen(event.currentTarget.open) }}>
    <summary onClick={event => { event.preventDefault(); setOpen(value => !value) }}>{summary}</summary>
    {open && children}
  </details>
}
