import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDeviceFloppy, IconDownload, IconRefresh } from '@tabler/icons-react'
import type { ViewerContext } from '../../types'

const DRAWIO_EMBED_ORIGIN = 'https://embed.diagrams.net'
const DRAWIO_EMBED_URL = `${DRAWIO_EMBED_ORIGIN}/?embed=1&proto=json&configure=1&spin=1&libraries=1`

export function DrawioViewer({ artifact, onSave, onDirtyChange, onClose: _onClose, toolbarAction }: ViewerContext) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [xmlContent, setXmlContent] = useState<string>('')
  const xmlLoadedRef = useRef<string>('')

  // 1. 获取图表内容
  const fetchContent = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await fetch(artifact.readUrl)
      if (!res.ok) throw new Error(`读取失败 (${res.status})`)
      const text = await res.text()
      setXmlContent(text)
      xmlLoadedRef.current = text
      setDirty(false)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '文件读取失败'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [artifact.readUrl])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  // 2. 与 Draw.io iframe 进行标准的 postMessage 交互
  useEffect(() => {
    const handleMessage = (event: MessageEvent) => {
      // 安全校验：校验 Origin 与 iframe 实例
      if (event.origin !== DRAWIO_EMBED_ORIGIN) return
      if (event.source !== iframeRef.current?.contentWindow) return

      try {
        const msg = typeof event.data === 'string' ? JSON.parse(event.data) : event.data
        if (!msg || !msg.event) return

        // 步骤 1: 响应 configure
        if (msg.event === 'configure') {
          iframeRef.current?.contentWindow?.postMessage(
            JSON.stringify({
              action: 'configure',
              config: {
                defaultFonts: ['Inter', 'PingFang SC', 'Microsoft YaHei'],
              },
            }),
            DRAWIO_EMBED_ORIGIN,
          )
        }

        // 步骤 2: 响应 init，装载 XML
        if (msg.event === 'init') {
          const contentToLoad = xmlLoadedRef.current || '<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel>'
          iframeRef.current?.contentWindow?.postMessage(
            JSON.stringify({
              action: 'load',
              autosave: 1,
              xml: contentToLoad,
            }),
            DRAWIO_EMBED_ORIGIN,
          )
        }

        // 步骤 3: 监听 autosave / save
        if (msg.event === 'autosave' || msg.event === 'save') {
          if (msg.xml && msg.xml !== xmlLoadedRef.current) {
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
  }, [])

  // 3. 执行保存
  const handleSave = async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const ok = await onSave(xmlContent)
      if (ok) {
        setDirty(false)
        onDirtyChange?.(false)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="drawio-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
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
                  leftSection={<IconDeviceFloppy size={13} />}
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
              <IconRefresh size={14} />
            </ActionIcon>
            <ActionIcon
              variant="subtle"
              size="sm"
              title="下载文件"
              onClick={() => window.open(`${artifact.readUrl}&download=1`, '_blank')}
            >
              <IconDownload size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
        <LoadingOverlay visible={loading} />
        {error ? (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => void fetchContent()}>
              重试
            </Button>
          </div>
        ) : (
          <iframe
            ref={iframeRef}
            src={DRAWIO_EMBED_URL}
            title={`Draw.io: ${artifact.name}`}
            style={{ width: '100%', height: '100%', border: 'none' }}
            sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
            allow="clipboard-read; clipboard-write"
          />
        )}
      </div>
    </div>
  )
}
