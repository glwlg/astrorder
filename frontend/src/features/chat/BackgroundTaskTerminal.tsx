import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { Task } from '../../domain/types'

// Observe the existing process output. Opening/collapsing never starts or stops it.
export function BackgroundTaskTerminal({ task }: { task: Task }) {
  const host = useRef<HTMLDivElement>(null)
  const terminal = useRef<Terminal | null>(null)
  const previous = useRef('')
  const output = typeof task.progress?.output === 'string' ? task.progress.output : ''
  useEffect(() => {
    if (!host.current) return
    const term = new Terminal({ disableStdin: true, cursorBlink: false, convertEol: true, fontSize: 12, fontFamily: 'Consolas, monospace', theme: { background: '#121316', foreground: '#e6edf3' } })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host.current)
    terminal.current = term
    previous.current = ''
    const resize = () => { if (host.current?.clientWidth) fit.fit() }
    const observer = new ResizeObserver(resize)
    observer.observe(host.current)
    resize()
    return () => { observer.disconnect(); terminal.current = null; term.dispose() }
  }, [task.id])
  useEffect(() => {
    const term = terminal.current
    if (!term || output === previous.current) return
    if (output.startsWith(previous.current)) term.write(output.slice(previous.current.length))
    else { term.reset(); term.write(output) }
    previous.current = output
  }, [output, task.id])
  return <section aria-label={`后台任务终端：${task.title || task.target_id}`}>
    {task.command && <pre className="background-task-command">$ {task.command}</pre>}
    <div ref={host} className="background-task-terminal" />
    {!output && <span className="background-task-empty">等待终端输出…</span>}
  </section>
}
