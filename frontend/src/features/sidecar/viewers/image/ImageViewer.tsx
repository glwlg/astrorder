import { useEffect, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDownload, IconRefresh } from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { imageMediaType } from './formats'

export function ImageViewer({ artifact, toolbarAction }: ViewerContext) {
  const [source, setSource] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [actualSize, setActualSize] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    let objectUrl = ''
    setLoading(true)
    setError('')
    setSource('')
    void (async () => {
      try {
        const response = await authenticatedApiFetch(artifact.readUrl, { signal: controller.signal })
        if (!response.ok) throw new Error(`读取图片失败 (${response.status})`)
        const blob = await response.blob()
        if (controller.signal.aborted) return
        const type = imageMediaType(artifact.name) || (blob.type.startsWith('image/') ? blob.type : artifact.mediaType.startsWith('image/') ? artifact.mediaType : 'application/octet-stream')
        objectUrl = URL.createObjectURL(new Blob([blob], { type }))
        setSource(objectUrl)
      } catch (reason) {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : '图片读取失败')
        setLoading(false)
      }
    })()
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [artifact.readUrl, artifact.name, artifact.mediaType, revision])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <Paper p="xs" withBorder style={{ borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Text size="xs" fw={600} truncate title={artifact.path || artifact.name}>{artifact.name}</Text>
          <Group gap="xs" wrap="nowrap">
            {toolbarAction}
            <Button size="compact-xs" variant="subtle" onClick={() => setActualSize(value => !value)}>{actualSize ? '适应窗口' : '原始尺寸'}</Button>
            <Tooltip label="刷新"><ActionIcon variant="subtle" aria-label="刷新图片" onClick={() => setRevision(value => value + 1)}><IconRefresh size={14} /></ActionIcon></Tooltip>
            <Tooltip label="下载"><ActionIcon variant="subtle" aria-label="下载图片" onClick={() => void downloadFile(artifact.readUrl, artifact.name)}><IconDownload size={14} /></ActionIcon></Tooltip>
          </Group>
        </Group>
      </Paper>
      <div style={{ flex: 1, minHeight: 0, position: 'relative', overflow: 'auto', textAlign: 'center', padding: 16 }}>
        <LoadingOverlay visible={loading} />
        {error ? <div><Text size="sm">{error}</Text><Button size="xs" mt="md" variant="light" onClick={() => setRevision(value => value + 1)}>重试</Button></div> : source && (
          <img src={source} alt={artifact.name} style={{ maxWidth: actualSize ? undefined : '100%', maxHeight: actualSize ? undefined : '100%', objectFit: 'contain' }} onLoad={() => setLoading(false)} onError={() => { setLoading(false); setError('图片解码失败，文件可能已损坏或格式不受支持') }} />
        )}
      </div>
    </div>
  )
}
