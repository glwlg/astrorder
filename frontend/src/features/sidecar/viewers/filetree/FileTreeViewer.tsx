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

const FILE_TREE_EXPANSION_PREFIX = 'astrorder:filetree:expanded:v1:'

function expansionStorageKey(artifact: ViewerContext['artifact']): string {
  return `${FILE_TREE_EXPANSION_PREFIX}${encodeURIComponent([
    artifact.agentId,
    artifact.sessionId,
    artifact.connectionId || 'local',
    artifact.path || '.',
  ].join('\u0000'))}`
}

function readExpandedPaths(key: string): Set<string> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) || '[]')
    return new Set(Array.isArray(value) ? value.filter((path): path is string => typeof path === 'string') : [])
  } catch {
    return new Set()
  }
}

function writeExpandedPaths(key: string, paths: Set<string>): void {
  try {
    localStorage.setItem(key, JSON.stringify([...paths]))
  } catch {
    // Unavailable storage must not prevent browsing files.
  }
}

function FileTreeNodeItem({
  node,
  sessionId,
  agentId,
  connectionId,
  expandedPaths,
  onToggleDirectory,
  level = 0,
}: {
  node: TreeNode
  sessionId: string
  agentId: string
  connectionId?: string
  expandedPaths: Set<string>
  onToggleDirectory: (path: string) => void
  level?: number
}) {
  const opened = expandedPaths.has(node.path)
  const openArtifact = useSidecarStore((s) => s.openArtifact)

  const handleFileClick = () => {
    if (node.is_dir) {
      onToggleDirectory(node.path)
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
              expandedPaths={expandedPaths}
              onToggleDirectory={onToggleDirectory}
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
  const storageKey = expansionStorageKey(artifact)
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(() => readExpandedPaths(storageKey))

  const toggleDirectory = useCallback((path: string) => {
    setExpandedPaths((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      writeExpandedPaths(storageKey, next)
      return next
    })
  }, [storageKey])

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
  }, [artifact.connectionId, artifact.path, artifact.sessionId])

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
                expandedPaths={expandedPaths}
                onToggleDirectory={toggleDirectory}
                level={0}
              />
            ))}
          </ScrollArea>
        )}
      </div>
    </div>
  )
}
