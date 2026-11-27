import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import type { AstrorderPluginContext, ViewerContext } from '@astrorder/sdk'
import { DownloadIcon, RefreshIcon, SaveIcon } from './icons'

const EXCALIDRAW_ORIGIN = 'https://excalidraw.com'

interface ExcalidrawViewerProps extends ViewerContext {
  ctx: AstrorderPluginContext
}

function buildDownloadUrl(readUrl: string): string {
  if (!readUrl) return ''
  return readUrl.includes('?') ? `${readUrl}&download=1` : `${readUrl}?download=1`
}

function extractIncomingScenePayload(msg: Record<string, unknown>): string | null {
  if (typeof msg.data === 'string') return msg.data
  if (typeof msg.content === 'string') return msg.content
  if (typeof msg.scene === 'string') return msg.scene
  if (msg.data && typeof msg.data === 'object') {
    try {
      return JSON.stringify(msg.data)
    } catch {
      return null
    }
  }
  if (Array.isArray(msg.elements)) {
    try {
      return JSON.stringify({
        type: 'excalidraw',
        version: 2,
        elements: msg.elements,
        appState: msg.appState && typeof msg.appState === 'object' ? msg.appState : {},
        files: msg.files && typeof msg.files === 'object' ? msg.files : {},
      })
    } catch {
      return null
    }
  }
  return null
}

export function ExcalidrawViewer({
  artifact,
  onSave,
  onDirtyChange,
  toolbarAction,
  isActive = true,
  ctx,
}: ExcalidrawViewerProps) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [fileData, setFileData] = useState('')
  const fileDataRef = useRef('')
  const initialDataRef = useRef('')
  const iframeReadyRef = useRef(false)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const postSceneToIframe = useCallback((raw: string) => {
    const targetWindow = iframeRef.current?.contentWindow
    if (!targetWindow || !raw) return
    try {
      targetWindow.postMessage(
        JSON.stringify({
          type: 'load',
          action: 'load',
          data: raw,
        }),
        EXCALIDRAW_ORIGIN,
      )
    } catch {
      // Ignore cross-origin embed postMessage errors
    }
  }, [])

  const fetchContent = useCallback(async () => {
    setLoading(true)
    try {
      const res = await ctx.api.fetch(artifact.readUrl)
      if (!mountedRef.current) return
      if (res.ok) {
        const text = await res.text()
        if (!mountedRef.current) return
        setFileData(text)
        fileDataRef.current = text
        initialDataRef.current = text
        setDirty(false)
        onDirtyChangeRef.current?.(false)
        if (iframeReadyRef.current && text) {
          postSceneToIframe(text)
        }
      }
    } catch {
      // 容错降级
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [artifact.readUrl, ctx.api, postSceneToIframe])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  // 与 Excalidraw embed iframe 的 postMessage 读写协议（严格校验 Origin 与 iframe 实例）
  useEffect(() => {
    const handleMessage = (event: MessageEvent) => {
      if (event.origin !== EXCALIDRAW_ORIGIN) return
      if (event.source !== iframeRef.current?.contentWindow) return

      try {
        const parsed = typeof event.data === 'string' ? JSON.parse(event.data) : event.data
        if (!parsed || typeof parsed !== 'object') return
        const msg = parsed as Record<string, unknown>
        const kind =
          typeof msg.event === 'string'
            ? msg.event
            : typeof msg.type === 'string'
              ? msg.type
              : ''
        if (!kind) return

        if (kind === 'ready' || kind === 'init') {
          iframeReadyRef.current = true
          if (fileDataRef.current) {
            postSceneToIframe(fileDataRef.current)
          }
          return
        }

        if (kind === 'change' || kind === 'autosave' || kind === 'save') {
          const nextPayload = extractIncomingScenePayload(msg)
          if (nextPayload !== null && nextPayload !== fileDataRef.current) {
            setFileData(nextPayload)
            fileDataRef.current = nextPayload
            const isDirty = nextPayload !== initialDataRef.current
            setDirty(isDirty)
            onDirtyChangeRef.current?.(isDirty)
          }
        }
      } catch {
        // 忽略非协议 JSON 消息
      }
    }

    window.addEventListener('message', handleMessage)
    return () => {
      window.removeEventListener('message', handleMessage)
    }
  }, [postSceneToIframe])

  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const payload = fileDataRef.current || fileData
      const ok = await onSave(payload)
      if (ok && mountedRef.current) {
        initialDataRef.current = payload
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      if (mountedRef.current) {
        setSaving(false)
      }
    }
  }, [artifact.writable, fileData, onSave])

  // 监听 Ctrl+S 快捷键保存
  useEffect(() => {
    if (!isActive) return
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        e.stopPropagation()
        if (dirty && !saving) {
          void handleSave()
        }
      }
    }
    window.addEventListener('keydown', onKeyDown, { capture: true })
    return () => {
      window.removeEventListener('keydown', onKeyDown, { capture: true })
    }
  }, [dirty, saving, handleSave, isActive])

  return (
    <div className="excalidraw-viewer-pane">
      <Paper p="xs" withBorder className="excalidraw-viewer__toolbar">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" className="excalidraw-viewer__title-group">
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            {dirty && (
              <Text size="xs" c="yellow" fw={500}>
                ● 未保存
              </Text>
            )}
          </Group>
          <Group gap={6} wrap="nowrap">
            {toolbarAction}
            {artifact.writable && onSave && (
              <Tooltip label="保存到工作区">
                <Button
                  size="compact-xs"
                  variant="filled"
                  color="blue"
                  leftSection={<SaveIcon size={13} />}
                  disabled={!dirty || saving}
                  loading={saving}
                  onClick={() => void handleSave()}
                >
                  保存
                </Button>
              </Tooltip>
            )}
            <ActionIcon
              variant="subtle"
              size="sm"
              title="重新加载"
              onClick={() => void fetchContent()}
            >
              <RefreshIcon size={14} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="sm"
              title="下载文件"
              onClick={() => void ctx.api.downloadFile(buildDownloadUrl(artifact.readUrl), artifact.name)}
            >
              <DownloadIcon size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div className="excalidraw-viewer__stage">
        <LoadingOverlay visible={loading} />
        <iframe
          ref={iframeRef}
          src={EXCALIDRAW_ORIGIN}
          title={artifact.name}
          className="excalidraw-viewer__iframe"
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
          allow="clipboard-read; clipboard-write"
          onLoad={() => {
            iframeReadyRef.current = true
            setLoading(false)
            if (fileDataRef.current) {
              postSceneToIframe(fileDataRef.current)
            }
          }}
        />
      </div>
    </div>
  )
}
