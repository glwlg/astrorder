import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, LoadingOverlay, Menu, Paper, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconCopy,
  IconDatabase,
  IconDownload,
  IconFile,
  IconFolder,
  IconFolderOpen,
  IconRefresh,
} from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { downloadFile, fileDesktop } from '../../fileTransfer'
import { resolveArtifactFromPath } from '../../resolver'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { artifactViewerRegistry } from '../../registry'
import { usePluginSettingsStore } from '../../../plugins/pluginSettingsStore'
import { DBX_PLUGIN_ID, isSqlFilePath, openSqlFileInDbx } from '../dbx'

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

/** Turns a failed tree response into a message that says why, not just the status. */
async function describeTreeFailure(res: Response): Promise<string> {
  let detail = ''
  try {
    const body: unknown = await res.json()
    if (body && typeof body === 'object' && 'detail' in body && typeof body.detail === 'string') detail = body.detail
  } catch {
    // The body is optional; the status alone still identifies the failure.
  }
  return detail ? `获取文件树失败 (${res.status})：${detail}` : `获取文件树失败 (${res.status})`
}

function findNodeByPath(nodes: TreeNode[], path: string): TreeNode | null {
  for (const node of nodes) {
    if (node.path === path) return node
    if (node.children && path.startsWith(node.path)) {
      const found = findNodeByPath(node.children, path)
      if (found) return found
    }
  }
  return null
}

function attachChildren(nodes: TreeNode[], path: string, children: TreeNode[]): TreeNode[] {
  return nodes.map((node) => {
    if (node.path === path) return { ...node, children }
    if (node.children && path.startsWith(node.path)) return { ...node, children: attachChildren(node.children, path, children) }
    return node
  })
}

function cleanPath(p: string): string {
  let cleaned = p.trim().replace(/^<|>$/g, '').replace(/^['"]|['"]$/g, '')
  if (cleaned.startsWith('file:///')) {
    cleaned = cleaned.slice(8)
  } else if (cleaned.startsWith('file://')) {
    cleaned = cleaned.slice(7)
  }
  try {
    cleaned = decodeURIComponent(cleaned)
  } catch {
    // ignore
  }
  return cleaned.replace(/\\/g, '/').replace(/\/+$/, '')
}

function pathKey(path: string): string {
  const normalized = cleanPath(path)
  return /^[a-z]:\//i.test(normalized) ? normalized.toLowerCase() : normalized
}

function isSamePath(a: string | null | undefined, b: string | null | undefined): boolean {
  if (!a || !b) return false
  const keyA = pathKey(a)
  const keyB = pathKey(b)
  if (keyA === keyB) return true
  if (keyA.endsWith('/' + keyB) || keyB.endsWith('/' + keyA)) {
    return true
  }
  return false
}

function getRelativePath(fullPath: string, basePath: string): string {
  if (!basePath) return fullPath
  const normFull = fullPath.replace(/\\/g, '/')
  const normBase = basePath.replace(/\\/g, '/').replace(/\/+$/, '')
  if (normFull.toLowerCase().startsWith(normBase.toLowerCase() + '/')) {
    return normFull.slice(normBase.length + 1)
  }
  if (normFull.toLowerCase() === normBase.toLowerCase()) {
    return '.'
  }
  return fullPath
}

function findNodeAndParents(
  nodes: TreeNode[],
  target: string,
  parents: string[] = [],
  exactOnly = true,
): { node: TreeNode; parents: string[] } | null {
  for (const node of nodes) {
    const matched = exactOnly ? pathKey(node.path) === pathKey(target) : isSamePath(node.path, target)
    if (matched) return { node, parents }
    if (node.children) {
      const found = findNodeAndParents(node.children, target, [...parents, node.path], exactOnly)
      if (found) return found
    }
  }
  return null
}

function resolveTargetInTree(nodes: TreeNode[], target: string) {
  return findNodeAndParents(nodes, target, [], true) || findNodeAndParents(nodes, target, [], false)
}

const FILE_TREE_ROW_HEIGHT = 26
const FILE_TREE_OVERSCAN = 14
const FILE_TREE_FALLBACK_VIEWPORT_HEIGHT = 2080

interface FlattenedTreeRow {
  node: TreeNode
  level: number
  opened: boolean
}

function isExpandedKey(nodePath: string, expandedKeys: Set<string>): boolean {
  if (expandedKeys.size === 0) return false
  const key = pathKey(nodePath)
  if (expandedKeys.has(key)) return true
  for (const candidate of expandedKeys) {
    if (key.endsWith('/' + candidate) || candidate.endsWith('/' + key)) return true
  }
  return false
}

function flattenVisibleTree(nodes: TreeNode[], expandedKeys: Set<string>, level = 0, out: FlattenedTreeRow[] = []): FlattenedTreeRow[] {
  for (const node of nodes) {
    const opened = node.is_dir && isExpandedKey(node.path, expandedKeys)
    out.push({ node, level, opened })
    if (opened && node.children && node.children.length > 0) {
      flattenVisibleTree(node.children, expandedKeys, level + 1, out)
    }
  }
  return out
}

const FileTreeRow = memo(function FileTreeRow({
  node,
  level,
  opened,
  isSelected,
  isRevealed,
  revealPulseKey,
  onSelectPath,
  onOpenNode,
  onToggleDirectory,
  onContextMenu,
}: {
  node: TreeNode
  level: number
  opened: boolean
  isSelected: boolean
  isRevealed: boolean
  revealPulseKey: number
  onSelectPath: (path: string) => void
  onOpenNode: (node: TreeNode) => void
  onToggleDirectory: (path: string) => void
  onContextMenu: (node: TreeNode, e: React.MouseEvent) => void
}) {
  return (
    <Group
      key={`${node.path}:${isRevealed ? revealPulseKey : 'idle'}`}
      gap={4}
      wrap="nowrap"
      onClick={(e) => {
        e.stopPropagation()
        onSelectPath(node.path)
      }}
      onDoubleClick={(e) => {
        e.stopPropagation()
        onOpenNode(node)
      }}
      onContextMenu={(e) => {
        e.preventDefault()
        e.stopPropagation()
        onSelectPath(node.path)
        onContextMenu(node, e)
      }}
      style={{
        height: FILE_TREE_ROW_HEIGHT,
        boxSizing: 'border-box',
        padding: '0 6px',
        paddingLeft: 6 + level * 14,
        borderRadius: 4,
        cursor: 'pointer',
        userSelect: 'none',
        fontSize: 12,
        backgroundColor: isSelected ? 'var(--astr-hover, rgba(59, 130, 246, 0.16))' : undefined,
        outline: isSelected ? '1px solid var(--astr-blue, #3b82f6)' : 'none',
        boxShadow: isSelected ? 'inset 3px 0 0 var(--astr-blue, #2563eb)' : 'none',
      }}
      className={`file-tree-row${isSelected ? ' is-selected' : ''}${isRevealed ? ' is-revealed' : ''}`}
      data-file-path={node.path}
    >
      {node.is_dir ? (
        <>
          <span
            onClick={(e) => {
              e.stopPropagation()
              onToggleDirectory(node.path)
            }}
            style={{ display: 'inline-flex', alignItems: 'center' }}
          >
            {opened ? <IconChevronDown size={13} color="gray" /> : <IconChevronRight size={13} color="gray" />}
          </span>
          {opened ? <IconFolderOpen size={14} color="#f59e0b" /> : <IconFolder size={14} color="#f59e0b" />}
        </>
      ) : (
        <>
          <span style={{ width: 13 }} />
          <IconFile size={14} color="var(--astr-muted)" />
        </>
      )}

      <Text
        size="xs"
        truncate
        style={{
          flex: 1,
          fontWeight: isSelected ? 600 : 400,
          color: isSelected ? 'var(--astr-blue, #2563eb)' : undefined,
        }}
      >
        {node.name}
      </Text>

      {!node.is_dir && node.size !== undefined && node.size > 0 && (
        <Text size="10px" c="dimmed">
          {node.size > 1024 ? `${(node.size / 1024).toFixed(1)}KB` : `${node.size}B`}
        </Text>
      )}
    </Group>
  )
})

export function FileTreeViewer({ artifact, isActive = true }: ViewerContext) {
  const paneRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [treeData, setTreeData] = useState<TreeNode[]>([])
  const [rootPath, setRootPath] = useState('')
  const storageKey = expansionStorageKey(artifact)
  const [expandedPaths, setExpandedPaths] = useState<Set<string>>(() => readExpandedPaths(storageKey))
  const [selectedPath, setSelectedPath] = useState<string | null>(null)
  const [revealedPath, setRevealedPath] = useState<string | null>(null)
  const [revealPulseKey, setRevealPulseKey] = useState<number>(0)
  const [contextMenu, setContextMenu] = useState<{ node: TreeNode; x: number; y: number } | null>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewportHeight, setViewportHeight] = useState(0)

  const expandedKeys = useMemo(() => new Set([...expandedPaths].map(pathKey)), [expandedPaths])
  const visibleRows = useMemo(
    () => (isActive ? flattenVisibleTree(treeData, expandedKeys) : []),
    [expandedKeys, isActive, treeData],
  )

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport || !isActive) return
    const updateHeight = () => {
      const next = viewport.clientHeight
      if (next > 0) setViewportHeight((prev) => (prev === next ? prev : next))
    }
    updateHeight()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(updateHeight)
    observer.observe(viewport)
    return () => observer.disconnect()
  }, [isActive, error])

  const handleViewportScroll = useCallback((event: React.UIEvent<HTMLDivElement>) => {
    const top = event.currentTarget.scrollTop
    setScrollTop((prev) => (Math.abs(prev - top) >= FILE_TREE_ROW_HEIGHT / 2 ? top : prev))
  }, [])

  const handleContextMenu = useCallback((node: TreeNode, e: React.MouseEvent) => {
    setContextMenu({
      node,
      x: e.clientX,
      y: e.clientY,
    })
  }, [])
  const [copying, setCopying] = useState(false)
  const loadedRef = useRef<Set<string>>(new Set())
  const revealPath = typeof artifact.metadata?.revealPath === 'string' ? artifact.metadata.revealPath : null
  const revealAt = artifact.metadata?.revealAt

  const toggleDirectory = useCallback((path: string) => {
    setExpandedPaths((current) => {
      const next = new Set(current)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      writeExpandedPaths(storageKey, next)
      return next
    })
  }, [storageKey])

  const handleSelectPath = useCallback((path: string) => {
    setSelectedPath(path)
    setRevealedPath(null)
  }, [])

  const openArtifact = useSidecarStore((s) => s.openArtifact)
  const handleOpenNode = useCallback((node: TreeNode) => {
    if (node.is_dir) {
      toggleDirectory(node.path)
      return
    }
    const targetArtifact = resolveArtifactFromPath(node.path, {
      id: artifact.sessionId,
      agent_id: artifact.agentId,
      connection_id: artifact.connectionId,
      workspace: null,
      title: '',
      status: 'idle',
      updated_at: '',
    })
    const viewer = artifactViewerRegistry.findViewer(targetArtifact)
    if (viewer) {
      openArtifact(targetArtifact, viewer.id)
    }
  }, [artifact.agentId, artifact.connectionId, artifact.sessionId, openArtifact, toggleDirectory])

  const fetchTree = useCallback(async () => {
    setLoading(true)
    setError(null)
    loadedRef.current = new Set()
    try {
      const queryPath = artifact.path || ''
      const res = await authenticatedApiFetch(
        `/api/v1/files/tree?depth=6&path=${encodeURIComponent(artifact.path || '')}&reveal_path=${encodeURIComponent(revealPath || '')}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`,
      )
      if (!res.ok) throw new Error(await describeTreeFailure(res))
      const data = await res.json()
      setTreeData(data.items || [])
      setRootPath(data.root || queryPath)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : '加载工作区文件树异常')
    } finally {
      setLoading(false)
    }
  }, [artifact.connectionId, artifact.path, artifact.sessionId, revealPath])

  // The server bounds one listing; directories it left unexpanded are loaded when opened.
  const loadChildren = useCallback(async (path: string) => {
    if (loadedRef.current.has(path)) return
    loadedRef.current.add(path)
    try {
      const res = await authenticatedApiFetch(
        `/api/v1/files/tree?depth=2&path=${encodeURIComponent(path)}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`,
      )
      if (!res.ok) throw new Error(await describeTreeFailure(res))
      const data = await res.json()
      setTreeData((current) => attachChildren(current, path, data.items || []))
    } catch (err: unknown) {
      loadedRef.current.delete(path)
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '加载目录失败' })
    }
  }, [artifact.connectionId, artifact.sessionId])

  useEffect(() => {
    void fetchTree()
  }, [fetchTree])

  useEffect(() => {
    for (const path of expandedPaths) {
      const node = findNodeByPath(treeData, path)
      if (node && node.is_dir && node.children === undefined) void loadChildren(path)
    }
  }, [expandedPaths, treeData, loadChildren])

  useEffect(() => {
    if (!revealPath || !treeData.length) return
    const match = resolveTargetInTree(treeData, revealPath)
    if (!match) return
    const { node: targetNode, parents } = match

    setSelectedPath(targetNode.path)
    setRevealedPath(targetNode.path)
    setRevealPulseKey(Date.now())

    setExpandedPaths((current) => {
      const next = new Set([...current, ...parents])
      writeExpandedPaths(storageKey, next)
      const nextKeys = new Set([...next].map(pathKey))
      const flattened = flattenVisibleTree(treeData, nextKeys)
      const targetIndex = flattened.findIndex((row) => isSamePath(row.node.path, targetNode.path))
      if (targetIndex >= 0 && isActive) {
        const viewport = viewportRef.current
        const viewH = viewport?.clientHeight || viewportHeight || FILE_TREE_FALLBACK_VIEWPORT_HEIGHT
        const desiredTop = Math.max(0, targetIndex * FILE_TREE_ROW_HEIGHT - Math.floor(viewH / 2))
        if (viewport) viewport.scrollTop = desiredTop
        setScrollTop(desiredTop)
      }
      return next
    })

    if (!isActive) return

    let attempts = 0
    const maxAttempts = 15
    let cancelled = false

    const tryScroll = () => {
      if (cancelled) return
      const pane = paneRef.current
      if (!pane) return
      const rows = pane.querySelectorAll<HTMLElement>('[data-file-path]')
      const row = [...rows].find((element) => isSamePath(element.dataset.filePath, targetNode.path))
      if (row) {
        row.scrollIntoView({ block: 'center', behavior: 'smooth' })
        setTimeout(() => {
          if (!cancelled) {
            row.scrollIntoView({ block: 'center', behavior: 'auto' })
          }
        }, 260)
        return
      }
      attempts += 1
      if (attempts < maxAttempts) {
        setTimeout(tryScroll, 50)
      }
    }

    requestAnimationFrame(tryScroll)

    return () => {
      cancelled = true
    }
  }, [isActive, revealAt, revealPath, storageKey, treeData, viewportHeight])

  const handleCopyFiles = useCallback(async (paths: string[]) => {
    if (!paths.length) return
    setCopying(true)
    const id = notifications.show({
      loading: true,
      title: '正在复制文件',
      message: '正在准备文件句柄与剪贴板数据…',
      autoClose: false,
    })

    try {
      const res = await authenticatedApiFetch('/api/v1/files/stage', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          copy_to_clipboard: !fileDesktop()?.setClipboardFiles,
          paths,
          session_id: artifact.sessionId,
          connection_id: artifact.connectionId || null,
        }),
      })

      if (!res.ok) {
        const errJson = await res.json().catch(() => ({}))
        throw new Error(errJson.detail || `暂存文件失败 (${res.status})`)
      }

      const data = await res.json()
      const localPaths: string[] = data.local_paths || []
      if (!localPaths.length) {
        throw new Error('未获取到有效物理文件路径')
      }

      if (!data.clipboard_set) {
        const desktopApi = fileDesktop()
        if (!desktopApi?.setClipboardFiles) throw new Error('当前环境无法写入文件剪贴板')
        const ok = await desktopApi.setClipboardFiles(localPaths)
        if (!ok) throw new Error('写入 Windows 剪贴板失败')
      }

      notifications.update({
        id,
        color: 'teal',
        title: '已复制到系统剪贴板',
        message: `已复制 ${localPaths.length} 个文件/文件夹，可直接在 Xftp 或资源管理器中按 Ctrl+V 传输！`,
        icon: <IconCheck size={16} />,
        autoClose: 4000,
        loading: false,
      })
    } catch (err: unknown) {
      notifications.update({
        id,
        color: 'red',
        title: '复制文件失败',
        message: err instanceof Error ? err.message : '未知错误',
        autoClose: 5000,
        loading: false,
      })
    } finally {
      setCopying(false)
    }
  }, [artifact.connectionId, artifact.sessionId])

  const handlePaneKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c') {
        const activeTag = (document.activeElement?.tagName || '').toLowerCase()
        if (activeTag === 'input' || activeTag === 'textarea') return
        const sel = window.getSelection()
        if (sel && sel.toString().trim().length > 0) return

        if (selectedPath) {
          e.preventDefault()
          e.stopPropagation()
          void handleCopyFiles([selectedPath])
        }
      }
  }

  return (
    <div
      className="filetree-viewer-pane"
      ref={paneRef}
      style={{ display: 'flex', flexDirection: 'column', height: '100%' }}
      tabIndex={0}
      onClick={() => {
        setSelectedPath(null)
        setRevealedPath(null)
      }}
      onKeyDown={handlePaneKeyDown}
    >
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
            {selectedPath && (
              <Button
                size="compact-xs"
                variant="light"
                color="blue"
                leftSection={<IconCopy size={12} />}
                loading={copying}
                onClick={(e) => {
                  e.stopPropagation()
                  void handleCopyFiles([selectedPath])
                }}
              >
                复制到剪贴板
              </Button>
            )}
            <ActionIcon variant="subtle" size="sm" title="刷新文件树" onClick={(e) => { e.stopPropagation(); void fetchTree() }}>
              <IconRefresh size={14} />
            </ActionIcon>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
        <LoadingOverlay visible={loading || copying} />
        {error ? (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => void fetchTree()}>
              重试
            </Button>
          </div>
        ) : (() => {
          const effectiveHeight = viewportHeight > 0 ? viewportHeight : FILE_TREE_FALLBACK_VIEWPORT_HEIGHT
          const totalRows = visibleRows.length
          const startIndex = Math.max(0, Math.floor(scrollTop / FILE_TREE_ROW_HEIGHT) - FILE_TREE_OVERSCAN)
          const visibleCount = Math.ceil(effectiveHeight / FILE_TREE_ROW_HEIGHT) + FILE_TREE_OVERSCAN * 2
          const endIndex = Math.min(totalRows, startIndex + visibleCount)
          const topSpacerHeight = startIndex * FILE_TREE_ROW_HEIGHT
          const bottomSpacerHeight = Math.max(0, (totalRows - endIndex) * FILE_TREE_ROW_HEIGHT)
          const windowRows = visibleRows.slice(startIndex, endIndex)
          return (
            <div
              ref={viewportRef}
              onScroll={handleViewportScroll}
              style={{ height: '100%', overflowY: 'auto', padding: '8px 4px', boxSizing: 'border-box' }}
            >
              {topSpacerHeight > 0 && <div style={{ height: topSpacerHeight }} aria-hidden="true" />}
              {windowRows.map(({ node, level, opened }) => (
                <FileTreeRow
                  key={node.path}
                  node={node}
                  level={level}
                  opened={opened}
                  isSelected={Boolean(selectedPath && isSamePath(selectedPath, node.path))}
                  isRevealed={Boolean(revealedPath && isSamePath(revealedPath, node.path))}
                  revealPulseKey={revealPulseKey}
                  onSelectPath={handleSelectPath}
                  onOpenNode={handleOpenNode}
                  onToggleDirectory={toggleDirectory}
                  onContextMenu={handleContextMenu}
                />
              ))}
              {bottomSpacerHeight > 0 && <div style={{ height: bottomSpacerHeight }} aria-hidden="true" />}
            </div>
          )
        })()}
      </div>

      <Menu
        shadow="md"
        width={220}
        opened={contextMenu !== null}
        onChange={(opened) => {
          if (!opened) setContextMenu(null)
        }}
        position="bottom-start"
        withinPortal
        closeOnClickOutside
        closeOnItemClick
      >
        <Menu.Target>
          <div
            style={{
              position: 'fixed',
              left: contextMenu?.x ?? 0,
              top: contextMenu?.y ?? 0,
              width: 1,
              height: 1,
              pointerEvents: 'none',
              visibility: 'hidden',
            }}
          />
        </Menu.Target>

        {contextMenu && (
          <Menu.Dropdown style={{ minWidth: 210 }}>
            {!contextMenu.node.is_dir &&
              isSqlFilePath(contextMenu.node.path) &&
              usePluginSettingsStore.getState().isPluginEnabled(DBX_PLUGIN_ID) && (
                <>
                  <Menu.Item
                    leftSection={<IconDatabase size={14} />}
                    style={{ whiteSpace: 'nowrap' }}
                    onClick={() => {
                      const node = contextMenu.node
                      setContextMenu(null)
                      void openSqlFileInDbx({
                        path: node.path,
                        sessionId: artifact.sessionId,
                        agentId: artifact.agentId,
                        connectionId: artifact.connectionId,
                      })
                    }}
                  >
                    在右侧 DBX 中打开
                  </Menu.Item>
                  <Menu.Divider />
                </>
              )}
            <Menu.Item
              leftSection={<IconCopy size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                void handleCopyFiles([node.path])
              }}
            >
              复制文件 (Ctrl+C)
            </Menu.Item>
            <Menu.Item
              leftSection={<IconDownload size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              disabled={contextMenu.node.is_dir}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                const url = `/api/v1/files/raw?path=${encodeURIComponent(node.path)}&download=1&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`
                void downloadFile(url, node.name)
              }}
            >
              下载 / 另存为
            </Menu.Item>
            {(!artifact.connectionId || artifact.connectionId === 'local') && (
              <Menu.Item
                leftSection={<IconFolderOpen size={14} />}
                style={{ whiteSpace: 'nowrap' }}
                onClick={async () => {
                  const node = contextMenu.node
                  setContextMenu(null)
                  const response = await authenticatedApiFetch('/api/v1/system/open-file', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ path: node.path, action: 'reveal' }),
                  })
                  if (!response.ok) notifications.show({ color: 'red', message: '无法在资源管理器中打开' })
                }}
              >
                在资源管理器中打开
              </Menu.Item>
            )}
            <Menu.Divider />
            <Menu.Item
              leftSection={<IconCopy size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                void navigator.clipboard.writeText(node.path)
                notifications.show({ message: '全路径已复制到剪贴板', color: 'teal', icon: <IconCheck size={14} /> })
              }}
            >
              复制全路径
            </Menu.Item>
            <Menu.Item
              leftSection={<IconCopy size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                const rel = getRelativePath(node.path, rootPath)
                void navigator.clipboard.writeText(rel)
                notifications.show({ message: '相对路径已复制到剪贴板', color: 'teal', icon: <IconCheck size={14} /> })
              }}
            >
              复制相对路径
            </Menu.Item>
          </Menu.Dropdown>
        )}
      </Menu>
    </div>
  )
}
