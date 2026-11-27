import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import type { AstrorderPluginContext, ViewerContext } from '@astrorder/sdk'
import { DownloadIcon, RefreshIcon, SaveIcon } from './icons'

const DEFAULT_DRAWIO_ORIGIN = 'https://embed.diagrams.net'
const EMPTY_MXGRAPH_XML =
  '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel>'

interface DrawioViewerProps extends ViewerContext {
  ctx: AstrorderPluginContext
}

function buildDownloadUrl(readUrl: string): string {
  if (!readUrl) return ''
  return readUrl.includes('?') ? `${readUrl}&download=1` : `${readUrl}?download=1`
}

function resolveDrawioEndpoint(
  provider: string,
  customOrigin: string,
): { origin: string; baseUrl: string } {
  const trimmed = customOrigin.trim()
  if (provider === 'self-hosted' && trimmed) {
    try {
      const parsed = new URL(trimmed)
      if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
        return {
          origin: parsed.origin,
          baseUrl: trimmed.replace(/\/+$/, ''),
        }
      }
    } catch {
      // invalid URL falls back to official diagrams.net
    }
  }
  return {
    origin: DEFAULT_DRAWIO_ORIGIN,
    baseUrl: DEFAULT_DRAWIO_ORIGIN,
  }
}

export function DrawioViewer({
  artifact,
  onSave,
  onDirtyChange,
  onClose: _onClose,
  toolbarAction,
  isActive = true,
  ctx,
}: DrawioViewerProps) {
  const provider = ctx.api.usePluginConfig<string>('provider', 'diagrams-net')
  const customOrigin = ctx.api.usePluginConfig<string>('customOrigin', '')
  const autosave = ctx.api.usePluginConfig<boolean>('autosave', true)
  const theme = ctx.api.usePluginConfig<string>('theme', 'min')

  const embedEndpoint = useMemo(
    () => resolveDrawioEndpoint(provider, customOrigin),
    [provider, customOrigin],
  )
  const embedOrigin = embedEndpoint.origin

  const embedUrl = useMemo(() => {
    const params = new URLSearchParams({
      embed: '1',
      proto: 'json',
      configure: '1',
      spin: '1',
      libraries: '1',
    })
    if (theme && theme !== 'auto') params.set('ui', theme)
    return `${embedEndpoint.baseUrl}/?${params.toString()}`
  }, [embedEndpoint.baseUrl, theme])

  const iframeRef = useRef<HTMLIFrameElement>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [xmlContent, setXmlContent] = useState<string>('')
  const xmlLoadedRef = useRef<string>('')
  const iframeInitializedRef = useRef(false)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    iframeInitializedRef.current = false
  }, [embedUrl])

  const postLoadToIframe = useCallback(
    (xml: string) => {
      const targetWindow = iframeRef.current?.contentWindow
      if (!targetWindow) return
      targetWindow.postMessage(
        JSON.stringify({
          action: 'load',
          autosave: autosave ? 1 : 0,
          xml: xml || EMPTY_MXGRAPH_XML,
        }),
        embedOrigin,
      )
    },
    [autosave, embedOrigin],
  )
  const postLoadToIframeRef = useRef(postLoadToIframe)
  postLoadToIframeRef.current = postLoadToIframe

  // 1. 获取图表内容
  const fetchContent = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await ctx.api.fetch(artifact.readUrl)
      if (!mountedRef.current) return
      if (!res.ok) throw new Error(`读取失败 (${res.status})`)
      const text = await res.text()
      if (!mountedRef.current) return
      setXmlContent(text)
      xmlLoadedRef.current = text
      setDirty(false)
      onDirtyChangeRef.current?.(false)
      if (iframeInitializedRef.current) {
        postLoadToIframeRef.current(text)
      }
    } catch (err: unknown) {
      if (!mountedRef.current) return
      const msg = err instanceof Error ? err.message : '文件读取失败'
      setError(msg)
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [artifact.readUrl, ctx.api])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  // 2. 与 Draw.io iframe 进行标准的 postMessage 交互
  useEffect(() => {
    const handleMessage = (event: MessageEvent) => {
      // 安全校验：校验 Origin 与 iframe 实例
      if (event.origin !== embedOrigin) return
      if (event.source !== iframeRef.current?.contentWindow) return

      try {
        const msg = typeof event.data === 'string' ? JSON.parse(event.data) : event.data
        if (!msg || typeof msg !== 'object' || !msg.event) return

        // 步骤 1: 响应 configure
        if (msg.event === 'configure') {
          iframeRef.current?.contentWindow?.postMessage(
            JSON.stringify({
              action: 'configure',
              config: {
                defaultFonts: ['Inter', 'PingFang SC', 'Microsoft YaHei'],
              },
            }),
            embedOrigin,
          )
          return
        }

        // 步骤 2: 响应 init，装载 XML
        if (msg.event === 'init') {
          iframeInitializedRef.current = true
          postLoadToIframe(xmlLoadedRef.current)
          return
        }

        // 步骤 3: 监听 autosave / save
        if (msg.event === 'autosave' || msg.event === 'save') {
          if (typeof msg.xml === 'string' && msg.xml !== xmlLoadedRef.current) {
            setXmlContent(msg.xml)
            xmlLoadedRef.current = msg.xml
            setDirty(true)
            onDirtyChangeRef.current?.(true)
          }
        }
      } catch {
        // 忽略非协议 JSON 消息
      }
    }

    window.addEventListener('message', handleMessage)
    return () => window.removeEventListener('message', handleMessage)
  }, [embedOrigin, postLoadToIframe])

  // 3. 执行保存
  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const payload = xmlLoadedRef.current || xmlContent
      const ok = await onSave(payload)
      if (ok && mountedRef.current) {
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      if (mountedRef.current) {
        setSaving(false)
      }
    }
  }, [artifact.writable, onSave, xmlContent])

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
    <div className="drawio-viewer-pane">
      <Paper p="xs" withBorder className="drawio-viewer__toolbar">
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" className="drawio-viewer__title-group">
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

      <div className="drawio-viewer__stage">
        <LoadingOverlay visible={loading} />
        {error ? (
          <div className="drawio-viewer__error">
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => void fetchContent()}>
              重试
            </Button>
          </div>
        ) : (
          <iframe
            ref={iframeRef}
            key={embedUrl}
            src={embedUrl}
            title={`流程图: ${artifact.name}`}
            className="drawio-viewer__iframe"
            sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
            allow="clipboard-read; clipboard-write"
          />
        )}
      </div>
    </div>
  )
}
