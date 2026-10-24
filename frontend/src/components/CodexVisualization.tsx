import { useEffect, useState } from 'react'
import type { Session } from '../domain/types'

export function extractVisualizations(text: string): { text: string; paths: string[] } {
  const paths: string[] = []
  const cleaned = text.replace(/\uE200visualize\uE202([^\uE201]+)\uE201/g, (raw, payload: string) => {
    try {
      const value = JSON.parse(payload) as { path?: unknown }
      if (typeof value.path !== 'string' || !/\.html?$/i.test(value.path) || /[\r\n\0]/.test(value.path) || !/^(\/|[a-z]:[/\\])/i.test(value.path) || value.path.startsWith('//')) return raw
      paths.push(value.path)
      return ''
    } catch { return raw }
  })
  return { text: cleaned, paths: [...new Set(paths)] }
}

export function CodexVisualization({ path, session }: { path: string; session?: Session | null }) {
  const [html, setHtml] = useState<string | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setHtml(null)
    setError('')
    if (!session) { setError('缺少会话信息，无法加载交互预览'); return }
    const query = new URLSearchParams({ path, session_id: session.id })
    if (session.connection_id) query.set('connection_id', session.connection_id)
    const headers: Record<string, string> = {}
    const token = localStorage.getItem('astrorder:token')
    if (token) headers.Authorization = `Bearer ${token}`
    void fetch(`/api/v1/files/raw?${query}`, { headers, signal: controller.signal })
      .then(async response => { if (!response.ok) throw new Error(`加载交互预览失败（${response.status}）`); return response.text() })
      .then(setHtml).catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '加载交互预览失败') })
    return () => controller.abort()
  }, [path, session?.id, session?.connection_id])
  if (error) return <div role="alert">{error}</div>
  if (html === null) return <div role="status">正在加载交互预览…</div>
  return <iframe title={`交互预览：${path.split(/[/\\]/).pop()}`} srcDoc={html} sandbox="allow-scripts" style={{ width: '100%', height: 640, border: '1px solid var(--astr-border)', borderRadius: 8 }} />
}
