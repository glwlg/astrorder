import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDeviceFloppy, IconDownload, IconRefresh } from '@tabler/icons-react'
import mermaid from 'mermaid'
import type { ViewerContext } from '../../types'

mermaid.initialize({
  startOnLoad: false,
  theme: 'default',
  securityLevel: 'loose',
})

export function MermaidViewer({ artifact, onSave, onDirtyChange, toolbarAction }: ViewerContext) {
  const containerRef = useRef<HTMLDivElement>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [isEditing, setIsEditing] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const initialCodeRef = useRef('')

  const renderChart = useCallback(async (chartCode: string) => {
    if (!containerRef.current) return
    setError(null)
    try {
      const id = `mermaid-${Date.now()}`
      const { svg } = await mermaid.render(id, chartCode)
      if (containerRef.current) {
        containerRef.current.innerHTML = svg
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Mermaid 图表语法解析错误'
      setError(msg)
    }
  }, [])

  const fetchContent = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await fetch(artifact.readUrl)
      if (!res.ok) throw new Error(`读取失败 (${res.status})`)
      const text = await res.text()
      setCode(text)
      initialCodeRef.current = text
      setDirty(false)
      onDirtyChangeRef.current?.(false)
      await renderChart(text)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '文件读取失败'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [artifact.readUrl, renderChart])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  const handleCodeChange = (newVal: string) => {
    setCode(newVal)
    const isNowDirty = newVal !== initialCodeRef.current
    setDirty(isNowDirty)
    onDirtyChangeRef.current?.(isNowDirty)
    void renderChart(newVal)
  };

  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const ok = await onSave(code)
      if (ok) {
        initialCodeRef.current = code
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      setSaving(false)
    }
  }, [artifact.writable, code, onSave])

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
    <div className="mermaid-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
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
            <Button
              size="compact-xs"
              variant={isEditing ? 'light' : 'subtle'}
              onClick={() => setIsEditing(!isEditing)}
            >
              {isEditing ? '隐藏代码' : '编辑代码'}
            </Button>
            {artifact.writable && onSave && (
              <Tooltip label="保存修改到工作区">
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
              title="下载源文件"
              onClick={() => window.open(`${artifact.readUrl}&download=1`, '_blank')}
            >
              <IconDownload size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      {isEditing && (
        <div style={{ borderBottom: '1px solid var(--astr-border)', padding: 8, background: 'var(--astr-surface-muted)' }}>
          <textarea
            value={code}
            onChange={(e) => handleCodeChange(e.target.value)}
            style={{
              width: '100%',
              height: 120,
              fontFamily: 'monospace',
              fontSize: 12,
              padding: 6,
              borderRadius: 4,
              border: '1px solid var(--astr-border)',
              background: 'var(--astr-surface)',
              color: 'var(--astr-text)',
              resize: 'vertical',
            }}
          />
        </div>
      )}

      <div style={{ flex: 1, position: 'relative', minHeight: 0, overflow: 'auto', padding: 16 }}>
        <LoadingOverlay visible={loading} />
        {error && (
          <Paper p="sm" withBorder mb="md" style={{ background: '#fff0f0', borderColor: '#ffc0c0', color: '#c00' }}>
            <Text size="xs" fw={500}>{error}</Text>
          </Paper>
        )}
        <div
          ref={containerRef}
          style={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            minHeight: '100%',
          }}
        />
      </div>
    </div>
  )
}
