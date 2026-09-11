import { useCallback, useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Collapse, Group, LoadingOverlay, Paper, ScrollArea, Text } from '@mantine/core'
import {
  IconChevronDown,
  IconChevronRight,
  IconFile,
  IconFolder,
  IconFolderOpen,
  IconRefresh,
} from '@tabler/icons-react'
import { resolveArtifactFromPath } from '../../resolver'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { artifactViewerRegistry } from '../../registry'

interface TreeNode {
  name: string
  path: string
  is_dir: boolean
  size?: number
  children?: TreeNode[]
}

function FileTreeNodeItem({
  node,
  sessionId,
  agentId,
  connectionId,
  level = 0,
}: {
  node: TreeNode
  sessionId: string
  agentId: string
  connectionId?: string
  level?: number
}) {
  const [opened, setOpened] = useState(level < 1)
  const openArtifact = useSidecarStore((s) => s.openArtifact)

  const handleFileClick = () => {
    if (node.is_dir) {
      setOpened(!opened)
      return
    }

    const artifact = resolveArtifactFromPath(node.path, {
      id: sessionId,
      agent_id: agentId,
      connection_id: connectionId,
      workspace: null,
      title: '',
      status: 'idle',
      updated_at: '',
    })

    const viewer = artifactViewerRegistry.findViewer(artifact)
    if (viewer) {
      openArtifact(artifact, viewer.id)
    } else {
      // 默认用代码编辑器打开查看
      openArtifact(artifact, 'monaco-viewer')
    }
  }

  return (
    <div>
      <Group
        gap={4}
        wrap="nowrap"
        onClick={handleFileClick}
        style={{
          padding: '3px 6px',
          paddingLeft: 6 + level * 14,
          borderRadius: 4,
          cursor: 'pointer',
          userSelect: 'none',
          fontSize: 12,
        }}
        className="file-tree-row"
      >
        {node.is_dir ? (
          <>
            {opened ? <IconChevronDown size={13} color="gray" /> : <IconChevronRight size={13} color="gray" />}
            {opened ? <IconFolderOpen size={14} color="#f59e0b" /> : <IconFolder size={14} color="#f59e0b" />}
          </>
        ) : (
          <>
            <span style={{ width: 13 }} />
            <IconFile size={14} color="var(--astr-muted)" />
          </>
        )}

        <Text size="xs" truncate style={{ flex: 1 }}>
          {node.name}
        </Text>

        {!node.is_dir && node.size !== undefined && node.size > 0 && (
          <Text size="10px" c="dimmed">
            {node.size > 1024 ? `${(node.size / 1024).toFixed(1)}KB` : `${node.size}B`}
          </Text>
        )}
      </Group>

      {node.is_dir && node.children && (
        <Collapse expanded={opened}>
          {node.children.map((child) => (
            <FileTreeNodeItem
              key={child.path}
              node={child}
              sessionId={sessionId}
              agentId={agentId}
              connectionId={connectionId}
              level={level + 1}
            />
          ))}
        </Collapse>
      )}
    </div>
  )
}

export function FileTreeViewer({ artifact }: ViewerContext) {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [treeData, setTreeData] = useState<TreeNode[]>([])
  const [rootPath, setRootPath] = useState('')

  const fetchTree = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const queryPath = artifact.path || ''
      const res = await fetch(
        `/api/v1/files/tree?path=${encodeURIComponent(artifact.path || '')}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`,
      )
      if (!res.ok) throw new Error(`获取文件树失败 (${res.status})`)
      const data = await res.json()
      setTreeData(data.items || [])
      setRootPath(data.root || queryPath)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : '加载工作区文件树异常')
    } finally {
      setLoading(false)
    }
  }, [artifact.path])

  useEffect(() => {
    void fetchTree()
  }, [fetchTree])

  return (
    <div className="filetree-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <IconFolderOpen size={16} style={{ color: '#f59e0b' }} />
            <Text size="xs" fw={600} truncate title={rootPath || artifact.name}>
              {artifact.name || '工作区文件树'}
            </Text>
            <Badge size="xs" variant="light" color="orange">
              文件浏览器
            </Badge>
          </Group>
          <Group gap={6} wrap="nowrap">
            <ActionIcon variant="subtle" size="sm" title="刷新文件树" onClick={() => void fetchTree()}>
              <IconRefresh size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
        <LoadingOverlay visible={loading} />
        {error ? (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => void fetchTree()}>
              重试
            </Button>
          </div>
        ) : (
          <ScrollArea style={{ height: '100%', padding: '8px 4px' }}>
            {treeData.map((node) => (
              <FileTreeNodeItem
                key={node.path}
                node={node}
                sessionId={artifact.sessionId}
                agentId={artifact.agentId}
                connectionId={artifact.connectionId}
                level={0}
              />
            ))}
          </ScrollArea>
        )}
      </div>
    </div>
  )
}
