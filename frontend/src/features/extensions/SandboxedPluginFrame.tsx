import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { authenticatedApiFetch } from '../../api/client'
import type { Session } from '../../domain/types'
import type { OpenPluginPanelEventDetail } from './types'
import './extensions.css'

export interface SandboxedPluginFrameProps {
  pluginId: string
  src: string
  title: string
  session?: Session | null
  params?: Record<string, unknown>
  autoResize?: boolean
  minHeight?: number
  maxHeight?: number
  className?: string
  onOpenPanel?: (panelId: string, params?: Record<string, unknown>) => void
}

interface BridgeMessage {
  type?: string
  id?: string | number
  requestId?: string | number
  payload?: Record<string, unknown>
  path?: string
  init?: RequestInit
  message?: string
  tone?: string
  panelId?: string
  params?: Record<string, unknown>
  text?: string
  height?: number
}

function getCurrentTheme(): 'dark' | 'light' {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.getAttribute('data-mantine-color-scheme') === 'dark' ? 'dark' : 'light'
}

function mapToastColor(tone?: string): string {
  switch (tone) {
    case 'success':
    case 'ok':
    case 'teal':
      return 'teal'
    case 'error':
    case 'danger':
    case 'red':
      return 'red'
    case 'warning':
    case 'warn':
    case 'yellow':
      return 'yellow'
    default:
      return 'blue'
  }
}

export function resolveSafeExtensionFetchPath(pluginId: string, rawPath: string): string | null {
  if (!rawPath || typeof rawPath !== 'string') return null
  const trimmed = rawPath.trim()
  if (!trimmed) return null
  // Reject absolute schemes, protocol-relative URLs, backslashes, or null bytes.
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(trimmed) || trimmed.startsWith('//') || trimmed.includes('\\') || trimmed.includes('\0')) {
    return null
  }

  let decoded: string
  try {
    decoded = decodeURIComponent(trimmed)
  } catch {
    return null
  }
  if (decoded.includes('\\') || decoded.includes('\0')) {
    return null
  }

  const [pathOnly] = trimmed.split(/[?#]/)
  const decodedPathOnly = decoded.split(/[?#]/)[0]
  if (
    pathOnly.split('/').some((seg) => seg === '..') ||
    decodedPathOnly.split('/').some((seg) => seg === '..')
  ) {
    return null
  }

  const encodedId = encodeURIComponent(pluginId)
  const allowedPrefix = `/api/v1/ext/${encodedId}`

  let candidate: string
  if (trimmed.startsWith('/api/')) {
    if (trimmed !== allowedPrefix && !trimmed.startsWith(`${allowedPrefix}/`) && !trimmed.startsWith(`${allowedPrefix}?`)) {
      return null
    }
    candidate = trimmed
  } else {
    const clean = trimmed.replace(/^\/+/, '')
    candidate = `${allowedPrefix}/${clean}`
  }

  try {
    const origin = typeof window !== 'undefined' ? window.location.origin : 'http://localhost'
    const parsed = new URL(candidate, origin)
    if (parsed.origin !== origin) return null
    if (parsed.pathname !== allowedPrefix && !parsed.pathname.startsWith(`${allowedPrefix}/`)) {
      return null
    }
    return `${parsed.pathname}${parsed.search}`
  } catch {
    return null
  }
}

function buildSafeFetchInit(init?: RequestInit): RequestInit {
  const safe: RequestInit = {}
  if (!init || typeof init !== 'object') return safe
  if (typeof init.method === 'string') {
    safe.method = init.method.toUpperCase()
  }
  if (init.headers) {
    const headers = new Headers()
    const incoming = new Headers(init.headers as HeadersInit)
    incoming.forEach((value, key) => {
      const lower = key.toLowerCase()
      if (lower === 'authorization' || lower === 'cookie' || lower === 'host') return
      headers.set(key, value)
    })
    safe.headers = headers
  }
  if (init.body !== undefined && init.body !== null) {
    safe.body = typeof init.body === 'string' ? init.body : JSON.stringify(init.body)
  }
  return safe
}

export function SandboxedPluginFrame({
  pluginId,
  src,
  title,
  session,
  params,
  autoResize = false,
  minHeight = 220,
  maxHeight = 1200,
  className,
  onOpenPanel,
}: SandboxedPluginFrameProps) {
  const iframeRef = useRef<HTMLIFrameElement | null>(null)
  const [frameHeight, setFrameHeight] = useState<number>(minHeight)

  const contextPayload = useMemo(
    () => ({
      pluginId,
      theme: getCurrentTheme(),
      sessionId: session?.id || null,
      agentId: session?.agent_id || null,
      workspace: session?.workspace || null,
      connectionId: session?.connection_id || null,
      params: params || {},
    }),
    [pluginId, session?.id, session?.agent_id, session?.workspace, session?.connection_id, params],
  )

  const pushContext = useCallback(() => {
    const win = iframeRef.current?.contentWindow
    if (!win) return
    win.postMessage(
      {
        type: 'astrorder:context-changed',
        payload: contextPayload,
        context: contextPayload,
      },
      '*',
    )
  }, [contextPayload])

  useEffect(() => {
    pushContext()
  }, [pushContext])

  useEffect(() => {
    const handleMessage = (event: MessageEvent) => {
      const targetWindow = iframeRef.current?.contentWindow
      if (!targetWindow || event.source !== targetWindow) {
        return
      }
      const msg = event.data as BridgeMessage | null
      if (!msg || typeof msg !== 'object' || typeof msg.type !== 'string') {
        return
      }
      const requestId = msg.requestId ?? msg.id
      const payload = (msg.payload && typeof msg.payload === 'object' ? msg.payload : {}) as Record<string, unknown>

      const reply = (body: Record<string, unknown>) => {
        targetWindow.postMessage(
          {
            type: 'astrorder:response',
            id: requestId,
            requestId,
            ...body,
          },
          '*',
        )
      }

      if (msg.type === 'astrorder:get-context') {
        reply({ ok: true, data: contextPayload, context: contextPayload, result: contextPayload })
        return
      }

      if (msg.type === 'astrorder:fetch') {
        const rawPath = (typeof msg.path === 'string' ? msg.path : payload.path) as string
        const rawInit = (msg.init ?? payload.init) as RequestInit | undefined
        const safeUrl = resolveSafeExtensionFetchPath(pluginId, rawPath)
        if (!safeUrl) {
          reply({
            ok: false,
            error: '禁止访问该路径：扩展仅可请求自身 /api/v1/ext/<pluginId>/ 命名空间接口',
            data: { ok: false, status: 403, text: 'Forbidden extension path', json: null },
            result: { ok: false, status: 403, text: 'Forbidden extension path', json: null },
          })
          return
        }
        void (async () => {
          try {
            const resp = await authenticatedApiFetch(safeUrl, buildSafeFetchInit(rawInit))
            const text = await resp.text()
            let json: unknown = null
            if (text) {
              try {
                json = JSON.parse(text) as unknown
              } catch {
                json = null
              }
            }
            const fetchResult = { ok: resp.ok, status: resp.status, text, json }
            reply({ ok: true, data: fetchResult, result: fetchResult })
          } catch (err) {
            const errorMsg = err instanceof Error ? err.message : '网络请求失败'
            reply({
              ok: false,
              error: errorMsg,
              data: { ok: false, status: 0, text: errorMsg, json: null },
              result: { ok: false, status: 0, text: errorMsg, json: null },
            })
          }
        })()
        return
      }

      if (msg.type === 'astrorder:toast') {
        const message = typeof msg.message === 'string'
          ? msg.message
          : typeof payload.message === 'string'
            ? payload.message
            : ''
        const tone = typeof msg.tone === 'string'
          ? msg.tone
          : typeof payload.tone === 'string'
            ? payload.tone
            : undefined
        if (message) {
          notifications.show({
            color: mapToastColor(tone),
            message,
          })
        }
        if (requestId !== undefined) {
          reply({ ok: true, data: { ok: true }, result: { ok: true } })
        }
        return
      }

      if (msg.type === 'astrorder:open-panel') {
        const panelId = typeof msg.panelId === 'string'
          ? msg.panelId
          : typeof payload.panelId === 'string'
            ? payload.panelId
            : ''
        const nextParams = (msg.params ?? payload.params) as Record<string, unknown> | undefined
        if (panelId) {
          if (onOpenPanel) {
            onOpenPanel(panelId, nextParams)
          } else {
            const detail: OpenPluginPanelEventDetail = {
              pluginId,
              panelId,
              params: nextParams,
              session,
            }
            window.dispatchEvent(new CustomEvent<OpenPluginPanelEventDetail>('astrorder:open-plugin-panel', { detail }))
          }
        }
        if (requestId !== undefined) {
          reply({ ok: true, data: { ok: true }, result: { ok: true } })
        }
        return
      }

      if (msg.type === 'astrorder:insert-composer') {
        const text = typeof msg.text === 'string'
          ? msg.text
          : typeof payload.text === 'string'
            ? payload.text
            : ''
        if (text) {
          window.dispatchEvent(new CustomEvent('astrorder:insert-composer', { detail: { text, pluginId } }))
        }
        if (requestId !== undefined) {
          reply({ ok: true, data: { ok: true }, result: { ok: true } })
        }
        return
      }

      if (msg.type === 'astrorder:resize') {
        const nextHeight = typeof msg.height === 'number'
          ? msg.height
          : typeof payload.height === 'number'
            ? payload.height
            : NaN
        if (autoResize && Number.isFinite(nextHeight) && nextHeight > 0) {
          setFrameHeight(Math.max(minHeight, Math.min(maxHeight, Math.round(nextHeight))))
        }
        if (requestId !== undefined) {
          reply({ ok: true, data: { ok: true }, result: { ok: true } })
        }
      }
    }

    window.addEventListener('message', handleMessage)
    return () => window.removeEventListener('message', handleMessage)
  }, [pluginId, contextPayload, session, autoResize, minHeight, maxHeight, onOpenPanel])

  return (
    <iframe
      ref={iframeRef}
      src={src}
      title={title}
      sandbox="allow-scripts allow-forms"
      className={`ext-sandbox-frame${autoResize ? ' is-auto-resize' : ''}${className ? ` ${className}` : ''}`}
      style={autoResize ? { height: `${frameHeight}px`, minHeight: `${minHeight}px` } : undefined}
      onLoad={pushContext}
    />
  )
}
