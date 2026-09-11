import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDeviceFloppy, IconDownload, IconRefresh } from '@tabler/icons-react'
import type { ViewerContext } from '../../types'

const EXCALIDRAW_ORIGIN = 'https://excalidraw.com'

export function ExcalidrawViewer({ artifact, onSave, onDirtyChange }: ViewerContext) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [fileData, setFileData] = useState('')
  const initialDataRef = useRef('')

  const fetchContent = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetch(artifact.readUrl)
      if (res.ok) {
        const text = await res.text()
        setFileData(text)
        initialDataRef.current = text
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } catch {
      // 容错降级
    } finally {
      setLoading(false)
    }
  }, [artifact.readUrl])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const ok = await onSave(fileData)
      if (ok) {
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      setSaving(false)
    }
  }, [artifact.writable, fileData, onSave])

  // 监听 Ctrl+S 快捷键保存
  useEffect(() => {
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
  }, [dirty, saving, handleSave])

  return (
    <div className="excalidraw-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
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
        <iframe
          ref={iframeRef}
          src={EXCALIDRAW_ORIGIN}
          title={artifact.name}
          style={{ width: '100%', height: '100%', border: 'none' }}
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
          allow="clipboard-read; clipboard-write"
          onLoad={() => setLoading(false)}
        />
      </div>
    </div>
  )
}
