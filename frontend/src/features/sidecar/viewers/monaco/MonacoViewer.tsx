import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Select, Text, Tooltip } from '@mantine/core'
import { IconBook2, IconDeviceFloppy, IconDownload, IconRefresh, IconSparkles, IconCode } from '@tabler/icons-react'
import { BlackboardItemRenderer } from '../../../monitor/BlackboardJsonRender'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import Editor from '@monaco-editor/react'
import type { ViewerContext } from '../../types'

const EXT_TO_LANG: Record<string, string> = {
  ts: 'typescript',
  tsx: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  py: 'python',
  json: 'json',
  rs: 'rust',
  go: 'go',
  c: 'c',
  cpp: 'cpp',
  h: 'c',
  css: 'css',
  scss: 'scss',
  less: 'less',
  html: 'html',
  xml: 'xml',
  sql: 'sql',
  yaml: 'yaml',
  yml: 'yaml',
  sh: 'shell',
  bash: 'shell',
  bat: 'bat',
  ps1: 'powershell',
  md: 'markdown',
  dockerfile: 'dockerfile',
}

function detectLanguage(fileName: string): string {
  const lower = fileName.toLowerCase()
  if (lower.endsWith('dockerfile')) return 'dockerfile'
  const dot = lower.lastIndexOf('.')
  if (dot === -1) return 'plaintext'
  const ext = lower.slice(dot + 1)
  return EXT_TO_LANG[ext] || 'plaintext'
}

export function MonacoViewer({ artifact, onSave, onDirtyChange, toolbarAction }: ViewerContext) {
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [language, setLanguage] = useState(() => detectLanguage(artifact.name))
  const isMarkdown = /\.(?:md|markdown)$/i.test(artifact.name)
  const [isReadingMode, setIsReadingMode] = useState(isMarkdown)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [visualRendered, setVisualRendered] = useState<any>(null)
  const [visualizing, setVisualizing] = useState(false)
  const [isVisualMode, setIsVisualMode] = useState(false)

  const handleJevVisualize = async () => {
    if (visualRendered) {
      setIsVisualMode(!isVisualMode)
      return
    }
    if (!content.trim() || visualizing) return
    setVisualizing(true)
    try {
      const res = await fetch('/api/v1/agent/invoke', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          capability: 'blackboard.auto_render',
          input: {
            key: 'temp_preview',
            content: content.slice(0, 3500),
            title: artifact.name || '工件智能视觉化',
          }
        })
      })
      if (res.ok) {
        const data = await res.json()
        setVisualRendered(data.rendered)
        setIsVisualMode(true)
      }
    } catch {
      // fallback
    } finally {
      setVisualizing(false)
    }
  }
  const initialContentRef = useRef('')

  const fetchContent = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const headers: Record<string, string> = {}
      try {
        const token = localStorage.getItem('astrorder:token')
        if (token) headers['Authorization'] = `Bearer ${token}`
      } catch {}
      const res = await fetch(artifact.readUrl, { headers })
      if (!res.ok) throw new Error(`读取失败 (${res.status})`)
      const text = await res.text()
      setContent(text)
      initialContentRef.current = text
      setDirty(false)
      onDirtyChangeRef.current?.(false)
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

  const editorRef = useRef<any>(null)
  const handleEditorMount = (editor: any) => {
    editorRef.current = editor
    const line = (artifact.metadata as any)?.line
    if (typeof line === 'number' && line > 0) {
      setTimeout(() => {
        try {
          editor.revealLineInCenter(line)
          editor.setPosition({ lineNumber: line, column: 1 })
        } catch {}
      }, 80)
    }
  }

  const handleEditorChange = (value: string | undefined) => {
    const val = value ?? ''
    setContent(val)
    const isNowDirty = val !== initialContentRef.current
    setDirty(isNowDirty)
    onDirtyChangeRef.current?.(isNowDirty)
  }

  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const ok = await onSave(content)
      if (ok) {
        initialContentRef.current = content
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      setSaving(false)
    }
  }, [artifact.writable, content, onSave])

  // 监听键盘快捷键 (Ctrl+S / Cmd+S)
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
    <div className="monaco-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            <Select
              size="xs"
              data={[
                { value: 'typescript', label: 'TypeScript' },
                { value: 'javascript', label: 'JavaScript' },
                { value: 'python', label: 'Python' },
                { value: 'json', label: 'JSON' },
                { value: 'rust', label: 'Rust' },
                { value: 'go', label: 'Go' },
                { value: 'sql', label: 'SQL' },
                { value: 'yaml', label: 'YAML' },
                { value: 'shell', label: 'Shell' },
                { value: 'markdown', label: 'Markdown' },
                { value: 'plaintext', label: '纯文本' },
              ]}
              value={language}
              onChange={(val) => val && setLanguage(val)}
              style={{ width: 110 }}
            />
            {dirty && (
              <Text size="xs" c="yellow" fw={500}>
                ● 未保存
              </Text>
            )}
          </Group>
          <Group gap={6} wrap="nowrap">
            {toolbarAction}
            {isMarkdown && (
              <Tooltip label={isReadingMode ? '编辑 Markdown' : '阅读 Markdown'}>
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label={isReadingMode ? '编辑 Markdown' : '阅读 Markdown'}
                  onClick={() => setIsReadingMode((value) => !value)}
                >
                  {isReadingMode ? <IconCode size={14} /> : <IconBook2 size={14} />}
                </ActionIcon>
              </Tooltip>
            )}
            {artifact.writable && onSave && (
              <Tooltip label="保存修改 (Ctrl+S)">
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
            <Button
              size="compact-xs"
              variant="light"
              color="teal"
              leftSection={isVisualMode ? <IconCode size={13} /> : <IconSparkles size={13} />}
              loading={visualizing}
              onClick={handleJevVisualize}
              title={isVisualMode ? '切换回代码编辑器' : '使用 Jev 决策自动转为 Generative UI 卡片'}
            >
              {isVisualMode ? '代码视图' : 'Jev 视觉化'}
            </Button>
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
        ) : isReadingMode && isMarkdown ? (
          <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto', background: 'var(--astr-surface)' }}>
            <MarkdownContent value={content} />
          </div>
        ) : isVisualMode && visualRendered ? (
          <div style={{ padding: 16, height: '100%', overflowY: 'auto', background: 'var(--astr-surface)' }}>
            <BlackboardItemRenderer itemKey="visual_preview" itemValue={visualRendered} />
          </div>
        ) : (
          <Editor
            onMount={handleEditorMount}
            height="100%"
            language={language}
            value={content}
            theme="vs-dark"
            onChange={handleEditorChange}
            options={{
              minimap: { enabled: true },
              fontSize: 13,
              scrollBeyondLastLine: false,
              wordWrap: 'on',
              automaticLayout: true,
              tabSize: 2,
            }}
          />
        )}
      </div>
    </div>
  )
}
