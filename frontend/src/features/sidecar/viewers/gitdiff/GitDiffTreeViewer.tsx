import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Group,
  LoadingOverlay,
  Paper,
  SegmentedControl,
  Text,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import {
  IconChevronDown,
  IconChevronRight,
  IconFileCode,
  IconFileDiff,
  IconFolder,
  IconFolderOpen,
  IconList,
  IconRefresh,
  IconSitemap,
} from '@tabler/icons-react'
import type { ViewerContext } from '../../types'
import { useSidecarStore } from '../../sidecarStore'

interface GitFileEntry {
  status: string
  path: string
}

interface GitTreeNode {
  name: string
  path: string
  isDir: boolean
  status?: string
  children?: GitTreeNode[]
}

function buildGitTree(files: GitFileEntry[]): GitTreeNode[] {
  const root: GitTreeNode = { name: '', path: '', isDir: true, children: [] }

  for (const file of files) {
    const parts = file.path.split('/')
    let current = root

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i]
      const isLast = i === parts.length - 1
      const currentPath = parts.slice(0, i + 1).join('/')

      if (isLast) {
        current.children = current.children || []
        current.children.push({
          name: part,
          path: file.path,
          isDir: false,
          status: file.status,
        })
      } else {
        current.children = current.children || []
        let dirNode = current.children.find((c) => c.isDir && c.name === part)
        if (!dirNode) {
          dirNode = {
            name: part,
            path: currentPath,
            isDir: true,
            children: [],
          }
          current.children.push(dirNode)
        }
        current = dirNode
      }
    }
  }

  // 排序：文件夹在前，文件在后
  const sortNodes = (nodes: GitTreeNode[]) => {
    nodes.sort((a, b) => {
      if (a.isDir === b.isDir) return a.name.localeCompare(b.name)
      return a.isDir ? -1 : 1
    })
    for (const n of nodes) {
      if (n.children) sortNodes(n.children)
    }
  }

  if (root.children) {
    sortNodes(root.children)
    return root.children
  }
  return []
}

function GitTreeNodeItem({
  node,
  level = 0,
  onOpenFileDiff,
}: {
  node: GitTreeNode
  level?: number
  onOpenFileDiff: (filePath: string) => void
}) {
  const [opened, setOpened] = useState(true)

  if (node.isDir) {
    return (
      <div>
        <UnstyledButton
          onClick={() => setOpened(!opened)}
          style={{
            display: 'flex',
            alignItems: 'center',
            width: '100%',
            gap: 4,
            padding: '3px 6px',
            paddingLeft: 6 + level * 14,
            borderRadius: 4,
            fontSize: 12,
            userSelect: 'none',
          }}
          styles={{
            root: {
              '&:hover': { background: 'var(--astr-card-hover, rgba(0,0,0,0.04))' },
            },
          }}
        >
          {opened ? <IconChevronDown size={13} color="gray" /> : <IconChevronRight size={13} color="gray" />}
          {opened ? <IconFolderOpen size={14} color="#f59e0b" /> : <IconFolder size={14} color="#f59e0b" />}
          <Text size="xs" fw={500} style={{ color: 'var(--astr-text)' }}>
            {node.name}
          </Text>
        </UnstyledButton>
        {opened && node.children && (
          <div>
            {node.children.map((child) => (
              <GitTreeNodeItem
                key={child.path}
                node={child}
                level={level + 1}
                onOpenFileDiff={onOpenFileDiff}
              />
            ))}
          </div>
        )}
      </div>
    )
  }

  const isAdded = node.status?.includes('A') || node.status?.includes('?')
  const isDeleted = node.status?.includes('D')
  const statusColor = isAdded
    ? 'var(--astr-green, #10b981)'
    : isDeleted
    ? 'var(--astr-red, #ef4444)'
    : 'var(--astr-blue, #3b82f6)'

  return (
    <UnstyledButton
      onClick={() => onOpenFileDiff(node.path)}
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        width: '100%',
        padding: '3px 6px',
        paddingLeft: 6 + level * 14,
        borderRadius: 4,
        fontSize: 12,
        cursor: 'pointer',
      }}
      styles={{
        root: {
          '&:hover': { background: 'var(--astr-card-hover, rgba(0,0,0,0.05))' },
        },
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0, flex: 1 }}>
        <span
          style={{
            fontSize: '11px',
            fontWeight: 700,
            fontFamily: 'monospace',
            color: statusColor,
            width: '16px',
            textAlign: 'center',
          }}
        >
          {node.status}
        </span>
        <IconFileCode size={14} color="var(--astr-muted)" style={{ flexShrink: 0 }} />
        <Text size="xs" fw={500} truncate style={{ color: 'var(--astr-text)' }}>
          {node.name}
        </Text>
      </div>
    </UnstyledButton>
  )
}

export function GitDiffTreeViewer({ artifact }: ViewerContext) {
  const baseBranch = useMemo(() => {
    try {
      return new URLSearchParams(artifact.readUrl || '').get('base_branch') || undefined
    } catch {
      return undefined
    }
  }, [artifact.readUrl])
  const [loading, setLoading] = useState(true)
  const [files, setFiles] = useState<GitFileEntry[]>([])
  const [insertions, setInsertions] = useState(0)
  const [deletions, setDeletions] = useState(0)
  const [viewMode, setViewMode] = useState<'list' | 'tree'>('tree') // 默认树形目录展示
  const openArtifact = useSidecarStore((s) => s.openArtifact)

  const fetchStatus = useCallback(async () => {
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
      const params = new URLSearchParams()
      if (artifact.path) params.set('workspace', artifact.path)
      if (artifact.sessionId) params.set('session_id', artifact.sessionId)
      if (artifact.connectionId) params.set('connection_id', artifact.connectionId)
      if (baseBranch) params.set('base_branch', baseBranch)

      const resp = await fetch(`/api/v1/git/status?${params.toString()}`, { headers })
      if (resp.ok) {
        const data = await resp.json()
        setFiles(data.files || [])
        setInsertions(data.insertions || 0)
        setDeletions(data.deletions || 0)
      }
    } finally {
      setLoading(false)
    }
  }, [artifact.path, artifact.sessionId, artifact.connectionId, baseBranch])

  useEffect(() => {
    void fetchStatus()
  }, [fetchStatus])

  const handleOpenFileDiff = (filePath: string) => {
    const params = new URLSearchParams({
      path: filePath,
    })
    if (artifact.path) params.set('workspace', artifact.path)
    if (artifact.sessionId) params.set('session_id', artifact.sessionId)
    if (artifact.connectionId) params.set('connection_id', artifact.connectionId)
    if (baseBranch) params.set('base_branch', baseBranch)

    const diffArtifact = {
      id: `gitdiff:${artifact.sessionId || 'current'}:${filePath}`,
      name: `Diff: ${filePath.split('/').pop() || filePath}`,
      kind: 'workspace_file' as const,
      mediaType: 'text/x-diff',
      readUrl: `/api/v1/git/diff-raw?${params.toString()}`,
      writable: false,
      path: filePath,
      sessionId: artifact.sessionId,
      agentId: artifact.agentId,
      connectionId: artifact.connectionId,
    }

    openArtifact(diffArtifact, 'diff-viewer')
  }

  const handleOpenAllDiff = () => {
    const params = new URLSearchParams()
    if (artifact.path) params.set('workspace', artifact.path)
    if (artifact.sessionId) params.set('session_id', artifact.sessionId)
    if (artifact.connectionId) params.set('connection_id', artifact.connectionId)
    if (baseBranch) params.set('base_branch', baseBranch)

    const allDiffArtifact = {
      id: `gitdiff:${artifact.sessionId || 'current'}:ALL`,
      name: `全部变更 (${files.length} 个文件)`,
      kind: 'workspace_file' as const,
      mediaType: 'text/x-diff',
      readUrl: `/api/v1/git/diff-raw?${params.toString()}`,
      writable: false,
      sessionId: artifact.sessionId,
      agentId: artifact.agentId,
      connectionId: artifact.connectionId,
    }

    openArtifact(allDiffArtifact, 'diff-viewer')
  }

  const treeNodes = useMemo(() => buildGitTree(files), [files])

  return (
    <div className="git-diff-tree-viewer" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <IconFileDiff size={16} color="var(--astr-blue, #3b82f6)" />
            <Text size="xs" fw={600} truncate>
              {baseBranch ? `对照 ${baseBranch} (${files.length})` : `未暂存 (${files.length})`}
            </Text>
            <Badge size="xs" color="teal" variant="light">
              +{insertions}
            </Badge>
            <Badge size="xs" color="red" variant="light">
              -{deletions}
            </Badge>
          </Group>
          <Group gap={6} wrap="nowrap">
            {/* 切换树形/列表视图模式 */}
            <SegmentedControl
              size="xs"
              value={viewMode}
              onChange={(val) => setViewMode(val as 'list' | 'tree')}
              data={[
                {
                  value: 'tree',
                  label: (
                    <Tooltip label="树形目录结构">
                      <Group gap={3} wrap="nowrap">
                        <IconSitemap size={12} />
                        <span>树形</span>
                      </Group>
                    </Tooltip>
                  ),
                },
                {
                  value: 'list',
                  label: (
                    <Tooltip label="平铺文件列表">
                      <Group gap={3} wrap="nowrap">
                        <IconList size={12} />
                        <span>列表</span>
                      </Group>
                    </Tooltip>
                  ),
                },
              ]}
            />
            <ActionIcon variant="subtle" size="sm" title="刷新状态" onClick={fetchStatus}>
              <IconRefresh size={14} />
            </ActionIcon>
            <Tooltip label="查看所有文件的合并 Diff">
              <UnstyledButton
                onClick={handleOpenAllDiff}
                style={{
                  fontSize: '11px',
                  padding: '2px 8px',
                  borderRadius: '4px',
                  background: 'var(--astr-card)',
                  border: '1px solid var(--astr-border)',
                  cursor: 'pointer',
                  fontWeight: 500,
                  whiteSpace: 'nowrap',
                }}
              >
                完整 Diff
              </UnstyledButton>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', overflow: 'auto', padding: '6px' }}>
        <LoadingOverlay visible={loading} />
        {files.length === 0 && !loading ? (
          <div style={{ padding: '32px 16px', textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">工作区很干净，暂无未提交的代码变更</Text>
          </div>
        ) : viewMode === 'tree' ? (
          /* 树形视图 */
          <div style={{ display: 'flex', flexDirection: 'column', gap: '1px' }}>
            {treeNodes.map((node) => (
              <GitTreeNodeItem
                key={node.path}
                node={node}
                level={0}
                onOpenFileDiff={handleOpenFileDiff}
              />
            ))}
          </div>
        ) : (
          /* 列表平铺视图 */
          <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
            {files.map((file) => {
              const isAdded = file.status.includes('A') || file.status.includes('?')
              const isDeleted = file.status.includes('D')

              const statusColor = isAdded
                ? 'var(--astr-green, #10b981)'
                : isDeleted
                ? 'var(--astr-red, #ef4444)'
                : 'var(--astr-blue, #3b82f6)'

              const fileName = file.path.split('/').pop() || file.path
              const dirPath = file.path.includes('/') ? file.path.substring(0, file.path.lastIndexOf('/')) : ''

              return (
                <UnstyledButton
                  key={file.path}
                  onClick={() => handleOpenFileDiff(file.path)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '6px 10px',
                    borderRadius: '6px',
                    transition: 'background 0.12s ease',
                    cursor: 'pointer',
                  }}
                  styles={{
                    root: {
                      '&:hover': {
                        background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.05))',
                      },
                    },
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0, flex: 1 }}>
                    <span
                      style={{
                        fontSize: '11px',
                        fontWeight: 700,
                        fontFamily: 'monospace',
                        color: statusColor,
                        width: '18px',
                        textAlign: 'center',
                      }}
                    >
                      {file.status}
                    </span>
                    <IconFileCode size={15} color="var(--astr-muted)" style={{ flexShrink: 0 }} />
                    <div style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      <Text size="xs" fw={500} component="span" style={{ color: 'var(--astr-text)' }}>
                        {fileName}
                      </Text>
                      {dirPath && (
                        <Text size="xs" c="dimmed" component="span" style={{ marginLeft: 6, fontSize: '11px' }}>
                          {dirPath}
                        </Text>
                      )}
                    </div>
                  </div>
                </UnstyledButton>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
