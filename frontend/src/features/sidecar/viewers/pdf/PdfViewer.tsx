import { useEffect, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconDownload, IconExternalLink, IconMessageForward, IconRefresh } from '@tabler/icons-react'
import { api, authenticatedApiFetch } from '../../../../api/client'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'

export function PdfViewer({ artifact, session, toolbarAction }: ViewerContext) {
  const [blobUrl, setBlobUrl] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [quoteOpen, setQuoteOpen] = useState(false)
  const [quotePage, setQuotePage] = useState('')
  const [quoteNote, setQuoteNote] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    let objectUrl = ''
    setLoading(true)
    setError('')
    setBlobUrl('')

    void (async () => {
      try {
        const response = await authenticatedApiFetch(artifact.readUrl, { signal: controller.signal })
        if (!response.ok) {
          throw new Error(`读取 PDF 失败 (${response.status})`)
        }
        const blob = await response.blob()
        if (controller.signal.aborted) return
        const pdfBlob = blob.type === 'application/pdf' ? blob : new Blob([blob], { type: 'application/pdf' })
        objectUrl = URL.createObjectURL(pdfBlob)
        setBlobUrl(objectUrl)
      } catch (reason) {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : 'PDF 加载失败')
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      }
    })()

    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [artifact.readUrl, revision])

  const handleQuoteToChat = () => {
    const targetPath = artifact.path || artifact.name
    const pagePart = quotePage.trim() ? `（第 ${quotePage.trim()} 页）` : ''
    const notePart = quoteNote.trim() ? `\n说明：${quoteNote.trim()}` : ''
    const text = `请结合 PDF 文档 \`${targetPath}\`${pagePart} 进行分析：${notePart}`
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: session?.agent_id || artifact.agentId,
          sessionId: session?.id || artifact.sessionId,
          text,
        },
      }),
    )
    setQuoteOpen(false)
    setQuotePage('')
    setQuoteNote('')
    notifications.show({ color: 'teal', message: '已将 PDF 引用填入对话输入框' })
  }

  const handleOpenExternal = async () => {
    if (!artifact.path) return
    try {
      await api.openFile(artifact.path, 'open')
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '调用系统应用打开失败',
      })
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, position: 'relative' }}>
      <Paper p="xs" withBorder style={{ flex: 'none', margin: '8px 8px 0', zIndex: 10, borderRadius: 10 }}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Text size="xs" fw={600} truncate title={artifact.path || artifact.name} style={{ minWidth: 0 }}>
            {artifact.name}
          </Text>
          <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
            {toolbarAction}
            <Button
              size="compact-xs"
              variant={quoteOpen ? 'filled' : 'light'}
              color="indigo"
              leftSection={<IconMessageForward size={13} />}
              onClick={() => setQuoteOpen((v) => !v)}
            >
              引用到对话
            </Button>
            {artifact.path && (!artifact.connectionId || artifact.connectionId === 'local') && (
              <Tooltip label="用系统默认应用打开">
                <ActionIcon variant="subtle" size="sm" aria-label="系统应用打开" onClick={() => void handleOpenExternal()}>
                  <IconExternalLink size={14} />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label="刷新">
              <ActionIcon variant="subtle" size="sm" aria-label="刷新 PDF" onClick={() => setRevision((v) => v + 1)}>
                <IconRefresh size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载 PDF">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下载 PDF"
                onClick={() => void downloadFile(`${artifact.readUrl}&download=1`, artifact.name)}
              >
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
        {quoteOpen && (
          <Group gap="xs" mt="xs" pt="xs" style={{ borderTop: '1px solid var(--mantine-color-default-border)' }} wrap="wrap">
            <TextInput
              size="xs"
              w={120}
              aria-label="关注页码"
              placeholder="如 3 或 5-8"
              value={quotePage}
              onChange={(e) => setQuotePage(e.currentTarget.value)}
            />
            <TextInput
              size="xs"
              style={{ flex: 1, minWidth: 160 }}
              aria-label="补充问题"
              placeholder="如：提炼核心结论与风险项"
              value={quoteNote}
              onChange={(e) => setQuoteNote(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleQuoteToChat()
              }}
            />
            <Button size="compact-xs" onClick={handleQuoteToChat}>填入输入框</Button>
            <Button size="compact-xs" variant="subtle" onClick={() => setQuoteOpen(false)}>取消</Button>
          </Group>
        )}
      </Paper>

      <div style={{ flex: 1, minHeight: 0, position: 'relative', padding: 8 }}>
        <LoadingOverlay visible={loading} />
        {error ? (
          <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12 }}>
            <Text size="sm" c="red">{error}</Text>
            <Button size="xs" variant="light" onClick={() => setRevision((v) => v + 1)}>重试加载</Button>
          </div>
        ) : blobUrl ? (
          <iframe
            title={`PDF 预览：${artifact.name}`}
            src={blobUrl}
            style={{
              width: '100%',
              height: '100%',
              border: '1px solid var(--mantine-color-default-border)',
              borderRadius: 8,
              background: '#525659',
            }}
          />
        ) : null}
      </div>
    </div>
  )
}
