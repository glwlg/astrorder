import { useCallback, useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Group, LoadingOverlay, Paper, Text } from '@mantine/core'
import { IconCopy, IconDownload, IconRefresh } from '@tabler/icons-react'
import type { ViewerContext } from '../../types'

interface DiffLine {
  type: 'add' | 'del' | 'context' | 'header'
  text: string
  oldLineNum?: number
  newLineNum?: number
}

function parseDiff(diffText: string): DiffLine[] {
  const lines = diffText.split('\n')
  const result: DiffLine[] = []
  let oldLine = 1
  let newLine = 1

  for (const line of lines) {
    if (line.startsWith('---') || line.startsWith('+++') || line.startsWith('diff --git')) {
      result.push({ type: 'header', text: line })
    } else if (line.startsWith('@@')) {
      result.push({ type: 'header', text: line })
      const match = /@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
      if (match) {
        oldLine = parseInt(match[1], 10)
        newLine = parseInt(match[2], 10)
      }
    } else if (line.startsWith('+')) {
      result.push({ type: 'add', text: line.slice(1), newLineNum: newLine++ })
    } else if (line.startsWith('-')) {
      result.push({ type: 'del', text: line.slice(1), oldLineNum: oldLine++ })
    } else {
      result.push({ type: 'context', text: line.startsWith(' ') ? line.slice(1) : line, oldLineNum: oldLine++, newLineNum: newLine++ })
    }
  }

  return result
}

export function DiffViewer({ artifact }: ViewerContext) {
  const [loading, setLoading] = useState(true)
  const [rawDiff, setRawDiff] = useState('')
  const [copied, setCopied] = useState(false)

  const fetchContent = useCallback(async () => {
    setLoading(true)
    try {
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = {}
      if (localToken) {
        headers['Authorization'] = `Bearer ${localToken}`
      }
      const res = await fetch(artifact.readUrl, { headers })
      if (res.ok) {
        const text = await res.text()
        setRawDiff(text)
      } else {
        setRawDiff(`加载 Diff 失败 (HTTP ${res.status})`)
      }
    } catch (e: unknown) {
      setRawDiff(`加载 Diff 异常: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setLoading(false)
    }
  }, [artifact.readUrl])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  const parsedLines = parseDiff(rawDiff)
  const addedCount = parsedLines.filter((l) => l.type === 'add').length
  const deletedCount = parsedLines.filter((l) => l.type === 'del').length

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(rawDiff)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {}
  }

  const handleDownload = () => {
    const blob = new Blob([rawDiff], { type: 'text/x-diff;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = artifact.name.endsWith('.diff') || artifact.name.endsWith('.patch') ? artifact.name : `${artifact.name}.patch`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="diff-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            <Badge size="xs" color="teal" variant="light">
              +{addedCount}
            </Badge>
            <Badge size="xs" color="red" variant="light">
              -{deletedCount}
            </Badge>
          </Group>
          <Group gap={6} wrap="nowrap">
            <ActionIcon variant="subtle" size="sm" title="刷新" onClick={fetchContent}>
              <IconRefresh size={14} />
            </ActionIcon>
            <Button
              variant="subtle"
              size="compact-xs"
              leftSection={<IconCopy size={12} />}
              onClick={handleCopy}
            >
              {copied ? '已复制' : '复制 Diff'}
            </Button>
            <ActionIcon variant="subtle" size="sm" title="下载 Patch 文件" onClick={handleDownload}>
              <IconDownload size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', overflow: 'auto', background: 'var(--astr-bg)' }}>
        <LoadingOverlay visible={loading} />
        <pre
          style={{
            margin: 0,
            padding: '12px 16px',
            fontFamily: 'Consolas, Monaco, "Courier New", monospace',
            fontSize: '12px',
            lineHeight: 1.5,
          }}
        >
          {parsedLines.map((line, idx) => {
            let bg = 'transparent'
            let color = 'inherit'
            if (line.type === 'add') {
              bg = 'rgba(16, 185, 129, 0.12)'
              color = 'var(--astr-green, #10b981)'
            } else if (line.type === 'del') {
              bg = 'rgba(239, 68, 68, 0.12)'
              color = 'var(--astr-red, #ef4444)'
            } else if (line.type === 'header') {
              bg = 'rgba(59, 130, 246, 0.08)'
              color = 'var(--astr-blue, #3b82f6)'
            }

            return (
              <div
                key={idx}
                style={{
                  display: 'flex',
                  backgroundColor: bg,
                  color,
                  padding: '1px 4px',
                  borderRadius: 2,
                }}
              >
                <span
                  style={{
                    width: '36px',
                    textAlign: 'right',
                    marginRight: '8px',
                    opacity: 0.4,
                    userSelect: 'none',
                    fontSize: '11px',
                  }}
                >
                  {line.oldLineNum || ''}
                </span>
                <span
                  style={{
                    width: '36px',
                    textAlign: 'right',
                    marginRight: '12px',
                    opacity: 0.4,
                    userSelect: 'none',
                    fontSize: '11px',
                  }}
                >
                  {line.newLineNum || ''}
                </span>
                <span style={{ flex: 1, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  {line.type === 'add' ? `+ ${line.text}` : line.type === 'del' ? `- ${line.text}` : `  ${line.text}`}
                </span>
              </div>
            )
          })}
        </pre>
      </div>
    </div>
  )
}
