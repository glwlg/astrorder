import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, LoadingOverlay, Menu, Modal, Paper, Stack, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconCode,
  IconCopy,
  IconDatabase,
  IconDownload,
  IconEdit,
  IconEye,
  IconFile,
  IconFilePlus,
  IconFileSpreadsheet,
  IconFileText,
  IconFileTypeDocx,
  IconFileTypePdf,
  IconFolder,
  IconFolderOpen,
  IconFolderPlus,
  IconPhoto,
  IconPresentation,
  IconRefresh,
  IconSearch,
  IconTrash,
  IconUpload,
  IconX,
} from '@tabler/icons-react'
import { api, authenticatedApiFetch } from '../../../../api/client'
import { useIsOfficeExperience } from '../../../office/appProfile'
import { copyFilesToClipboard, downloadFile } from '../../fileTransfer'
import { resolveArtifactFromPath } from '../../resolver'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { artifactViewerRegistry } from '../../registry'
import { hasActiveDatabaseTool, isSqlFilePath, openSqlFileInDatabaseTool } from '../../databaseTool'

function renderFileIcon(name: string) {
  const lower = name.toLowerCase()
  if (lower.endsWith('.docx') || lower.endsWith('.doc') || lower.endsWith('.wps')) {
    return <IconFileTypeDocx size={14} color="#2563eb" />
  }
  if (lower.endsWith('.xlsx') || lower.endsWith('.xls') || lower.endsWith('.csv') || lower.endsWith('.tsv') || lower.endsWith('.et')) {
    return <IconFileSpreadsheet size={14} color="#15803d" />
  }
  if (lower.endsWith('.pptx') || lower.endsWith('.ppt') || lower.endsWith('.dps')) {
    return <IconPresentation size={14} color="#ea580c" />
  }
  if (lower.endsWith('.pdf')) {
    return <IconFileTypePdf size={14} color="#dc2626" />
  }
  if (lower.endsWith('.md') || lower.endsWith('.markdown') || lower.endsWith('.txt')) {
    return <IconFileText size={14} color="#475569" />
  }
  if (/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/i.test(lower)) {
    return <IconPhoto size={14} color="#7c3aed" />
  }
  return <IconFile size={14} color="var(--astr-muted)" />
}

function joinChildPath(parentDir: string, name: string): string {
  const sep = parentDir.includes('\\') && !parentDir.includes('/') ? '\\' : '/'
  const trimmed = parentDir.replace(/[\\/]+$/, '')
  return trimmed ? `${trimmed}${sep}${name}` : name
}

function parentDirectoryOf(filePath: string): string {
  const normalized = filePath.replace(/[\\/]+$/, '')
  const lastSlash = Math.max(normalized.lastIndexOf('/'), normalized.lastIndexOf('\\'))
  if (lastSlash <= 0) return ''
  return normalized.slice(0, lastSlash)
}

function filterTreeNodes(nodes: TreeNode[], query: string): TreeNode[] {
  const q = query.trim().toLowerCase()
  if (!q) return nodes
  const filtered: TreeNode[] = []
  for (const node of nodes) {
    const nameMatched = node.name.toLowerCase().includes(q)
    const matchedChildren = node.children ? filterTreeNodes(node.children, q) : []
    if (nameMatched || matchedChildren.length > 0) {
      filtered.push(
        node.is_dir
          ? { ...node, children: nameMatched ? node.children : matchedChildren }
          : node,
      )
    }
  }
  return filtered
}

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

function flattenVisibleTree(
  nodes: TreeNode[],
  expandedKeys: Set<string>,
  level = 0,
  out: FlattenedTreeRow[] = [],
  expandAll = false,
): FlattenedTreeRow[] {
  for (const node of nodes) {
    const opened = node.is_dir && (expandAll || isExpandedKey(node.path, expandedKeys))
    out.push({ node, level, opened })
    if (opened && node.children && node.children.length > 0) {
      flattenVisibleTree(node.children, expandedKeys, level + 1, out, expandAll)
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
          {renderFileIcon(node.name)}
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

export function FileTreeViewer({ artifact, session, isActive = true }: ViewerContext) {
  const isOffice = useIsOfficeExperience(session)
  const paneRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement>(null)
  const uploadInputRef = useRef<HTMLInputElement>(null)
  const uploadTargetDirRef = useRef<string>('')
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
  const [searchQuery, setSearchQuery] = useState('')
  const [draggingFiles, setDraggingFiles] = useState(false)
  const [uploading, setUploading] = useState(false)

  // File & folder CRUD modals
  const [createDocModal, setCreateDocModal] = useState<{ parentDir: string } | null>(null)
  const [newDocName, setNewDocName] = useState('')
  const [createDirModal, setCreateDirModal] = useState<{ parentDir: string } | null>(null)
  const [newDirName, setNewDirName] = useState('')
  const [renameTarget, setRenameTarget] = useState<TreeNode | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<TreeNode | null>(null)
  const [mutating, setMutating] = useState(false)

  const expandedKeys = useMemo(() => new Set([...expandedPaths].map(pathKey)), [expandedPaths])
  const filteredTree = useMemo(
    () => (searchQuery.trim() ? filterTreeNodes(treeData, searchQuery) : treeData),
    [searchQuery, treeData],
  )
  const visibleRows = useMemo(
    () => (isActive ? flattenVisibleTree(filteredTree, expandedKeys, 0, [], Boolean(searchQuery.trim())) : []),
    [expandedKeys, filteredTree, isActive, searchQuery],
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
    if (!isOffice && hasActiveDatabaseTool() && isSqlFilePath(node.path)) {
      void openSqlFileInDatabaseTool({
        path: node.path,
        sessionId: artifact.sessionId,
        agentId: artifact.agentId,
        connectionId: artifact.connectionId,
      })
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
  }, [artifact.agentId, artifact.connectionId, artifact.sessionId, isOffice, openArtifact, toggleDirectory])

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
    try {
      await copyFilesToClipboard(paths, {
        sessionId: artifact.sessionId,
        connectionId: artifact.connectionId || null,
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

  const resolveDefaultParentDir = useCallback((): string => {
    const baseRoot = rootPath || artifact.path || ''
    if (!selectedPath) return baseRoot
    const node = findNodeByPath(treeData, selectedPath)
    if (node?.is_dir) return node.path
    return parentDirectoryOf(selectedPath) || baseRoot
  }, [artifact.path, rootPath, selectedPath, treeData])

  const uploadFilesToDir = useCallback(async (files: FileList | File[], targetDir?: string) => {
    const list = Array.from(files)
    if (!list.length) return
    const destDir = targetDir || resolveDefaultParentDir()
    setUploading(true)
    try {
      let okCount = 0
      for (const file of list) {
        await api.uploadFile({
          file,
          parent: destDir || undefined,
          session_id: artifact.sessionId || undefined,
          connection_id: artifact.connectionId || null,
        })
        okCount += 1
      }
      if (destDir) {
        setExpandedPaths((prev) => {
          const next = new Set(prev)
          next.add(destDir)
          writeExpandedPaths(storageKey, next)
          return next
        })
      }
      await fetchTree()
      notifications.show({
        color: 'teal',
        message: okCount === 1 ? `已上传文件：${list[0].name}` : `已上传 ${okCount} 个文件`,
      })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '上传文件失败',
      })
    } finally {
      setUploading(false)
    }
  }, [artifact.connectionId, artifact.sessionId, fetchTree, resolveDefaultParentDir, storageKey])

  const handleCreateDocument = async () => {
    if (!createDocModal) return
    const raw = newDocName.trim()
    if (!raw) return
    if (/[\\/]/.test(raw)) {
      notifications.show({ color: 'yellow', message: '文档名称不能包含路径分隔符' })
      return
    }
    const finalName = raw.includes('.') ? raw : `${raw}.md`
    const targetPath = joinChildPath(createDocModal.parentDir, finalName)
    const titleBase = finalName.replace(/\.[^.]+$/, '')
    const initialContent = finalName.toLowerCase().endsWith('.md') ? `# ${titleBase}\n\n` : ''
    setMutating(true)
    try {
      await api.writeFile({
        path: targetPath,
        content: initialContent,
        session_id: artifact.sessionId,
        connection_id: artifact.connectionId || null,
      })
      if (createDocModal.parentDir) {
        setExpandedPaths((prev) => {
          const next = new Set(prev)
          next.add(createDocModal.parentDir)
          writeExpandedPaths(storageKey, next)
          return next
        })
      }
      setCreateDocModal(null)
      setNewDocName('')
      await fetchTree()
      setSelectedPath(targetPath)
      const createdArtifact = resolveArtifactFromPath(targetPath, {
        id: artifact.sessionId,
        agent_id: artifact.agentId,
        connection_id: artifact.connectionId,
        workspace: rootPath || null,
        title: '',
        status: 'idle',
        updated_at: '',
      })
      const viewer = artifactViewerRegistry.findViewer(createdArtifact)
      if (viewer) {
        openArtifact(createdArtifact, viewer.id)
      }
      notifications.show({ color: 'teal', message: `已创建文档：${finalName}` })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '创建文档失败' })
    } finally {
      setMutating(false)
    }
  }

  const handleCreateFolder = async () => {
    if (!createDirModal) return
    const trimmed = newDirName.trim()
    if (!trimmed) return
    setMutating(true)
    try {
      await api.createDirectory(createDirModal.parentDir, trimmed, artifact.connectionId || null)
      if (createDirModal.parentDir) {
        setExpandedPaths((prev) => {
          const next = new Set(prev)
          next.add(createDirModal.parentDir)
          writeExpandedPaths(storageKey, next)
          return next
        })
      }
      setCreateDirModal(null)
      setNewDirName('')
      await fetchTree()
      notifications.show({ color: 'teal', message: `已新建文件夹：${trimmed}` })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '新建文件夹失败' })
    } finally {
      setMutating(false)
    }
  }

  const handleRenameSubmit = async () => {
    if (!renameTarget) return
    const trimmed = renameValue.trim()
    if (!trimmed || trimmed === renameTarget.name) {
      setRenameTarget(null)
      return
    }
    setMutating(true)
    try {
      const res = await api.renameFile({
        path: renameTarget.path,
        name: trimmed,
        session_id: artifact.sessionId || undefined,
        connection_id: artifact.connectionId || null,
      })
      if (selectedPath && isSamePath(selectedPath, renameTarget.path)) {
        setSelectedPath(res.path)
      }
      setRenameTarget(null)
      await fetchTree()
      notifications.show({ color: 'teal', message: `已重命名为：${res.name}` })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '重命名失败' })
    } finally {
      setMutating(false)
    }
  }

  const handleDeleteConfirm = async () => {
    if (!deleteTarget) return
    setMutating(true)
    try {
      await api.deleteFile({
        path: deleteTarget.path,
        session_id: artifact.sessionId || undefined,
        connection_id: artifact.connectionId || null,
      })
      if (selectedPath && isSamePath(selectedPath, deleteTarget.path)) {
        setSelectedPath(null)
      }
      const deletedName = deleteTarget.name
      setDeleteTarget(null)
      await fetchTree()
      notifications.show({ color: 'teal', message: `已删除：${deletedName}` })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '删除失败' })
    } finally {
      setMutating(false)
    }
  }

  return (
    <div
      className="filetree-viewer-pane"
      ref={paneRef}
      style={{ display: 'flex', flexDirection: 'column', height: '100%', position: 'relative' }}
      tabIndex={0}
      onClick={() => {
        setSelectedPath(null)
        setRevealedPath(null)
      }}
      onKeyDown={handlePaneKeyDown}
      onDragOver={(e) => {
        if (e.dataTransfer?.types?.includes('Files')) {
          e.preventDefault()
          e.stopPropagation()
          setDraggingFiles(true)
        }
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          setDraggingFiles(false)
        }
      }}
      onDrop={(e) => {
        if (e.dataTransfer?.files?.length) {
          e.preventDefault()
          e.stopPropagation()
          setDraggingFiles(false)
          void uploadFilesToDir(e.dataTransfer.files)
        }
      }}
    >
      <input
        ref={uploadInputRef}
        type="file"
        multiple
        style={{ display: 'none' }}
        onChange={(e) => {
          const files = e.currentTarget.files
          if (files && files.length > 0) {
            const target = uploadTargetDirRef.current || resolveDefaultParentDir()
            uploadTargetDirRef.current = ''
            void uploadFilesToDir(files, target)
          }
          e.currentTarget.value = ''
        }}
      />

      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Stack gap={6}>
          <Group justify="space-between" wrap="nowrap" gap={6}>
            <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
              <IconFolderOpen size={16} style={{ color: '#f59e0b', flexShrink: 0 }} />
              <Text size="xs" fw={600} truncate title={rootPath || artifact.name}>
                {artifact.name || (isOffice ? '工作区文档' : '工作区文件树')}
              </Text>
              <Badge size="xs" variant="light" color={isOffice ? 'gray' : 'orange'} style={{ flexShrink: 0 }}>
                {isOffice ? '文档文件' : '文件浏览器'}
              </Badge>
            </Group>
            <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
              <Tooltip label="新建文档">
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="新建文档"
                  onClick={(e) => {
                    e.stopPropagation()
                    setNewDocName('')
                    setCreateDocModal({ parentDir: resolveDefaultParentDir() })
                  }}
                >
                  <IconFilePlus size={14} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label="新建文件夹">
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="新建文件夹"
                  onClick={(e) => {
                    e.stopPropagation()
                    setNewDirName('')
                    setCreateDirModal({ parentDir: resolveDefaultParentDir() })
                  }}
                >
                  <IconFolderPlus size={14} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label="上传文件">
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="上传文件"
                  loading={uploading}
                  onClick={(e) => {
                    e.stopPropagation()
                    uploadTargetDirRef.current = resolveDefaultParentDir()
                    uploadInputRef.current?.click()
                  }}
                >
                  <IconUpload size={14} />
                </ActionIcon>
              </Tooltip>
              {selectedPath && (
                <Tooltip label="复制文件到剪贴板">
                  <ActionIcon
                    variant="light"
                    color="blue"
                    size="sm"
                    aria-label="复制到剪贴板"
                    loading={copying}
                    onClick={(e) => {
                      e.stopPropagation()
                      void handleCopyFiles([selectedPath])
                    }}
                  >
                    <IconCopy size={13} />
                  </ActionIcon>
                </Tooltip>
              )}
              <Tooltip label="刷新文件列表">
                <ActionIcon variant="subtle" size="sm" aria-label="刷新文件树" onClick={(e) => { e.stopPropagation(); void fetchTree() }}>
                  <IconRefresh size={14} />
                </ActionIcon>
              </Tooltip>
            </Group>
          </Group>

          <TextInput
            size="xs"
            leftSection={<IconSearch size={13} />}
            placeholder={isOffice ? '搜索文档名称…' : '筛选文件或目录…'}
            aria-label="筛选工作区文件"
            value={searchQuery}
            onClick={(e) => e.stopPropagation()}
            onChange={(e) => setSearchQuery(e.currentTarget.value)}
            rightSection={
              searchQuery ? (
                <ActionIcon
                  size="xs"
                  variant="subtle"
                  aria-label="清空筛选"
                  onClick={(e) => {
                    e.stopPropagation()
                    setSearchQuery('')
                  }}
                >
                  <IconX size={12} />
                </ActionIcon>
              ) : null
            }
          />
        </Stack>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
        <LoadingOverlay visible={loading || copying || uploading} />
        {draggingFiles && (
          <div
            style={{
              position: 'absolute',
              inset: 8,
              zIndex: 20,
              borderRadius: 10,
              border: '2px dashed var(--astr-blue, #3b82f6)',
              background: 'rgba(59, 130, 246, 0.08)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              pointerEvents: 'none',
            }}
          >
            <Text size="sm" fw={600} c="blue">松开鼠标上传文件到当前目录</Text>
          </div>
        )}
        {error ? (
          <div style={{ padding: 24, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => void fetchTree()}>
              重试
            </Button>
          </div>
        ) : visibleRows.length === 0 ? (
          <div style={{ padding: 28, textAlign: 'center', color: 'var(--astr-muted)' }}>
            <Text size="xs">
              {searchQuery.trim() ? `未找到匹配「${searchQuery.trim()}」的文件` : '当前目录暂无文件，可点击上方新建文档或拖入文件上传'}
            </Text>
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
            {!contextMenu.node.is_dir ? (
              <Menu.Item
                leftSection={<IconEye size={14} />}
                style={{ whiteSpace: 'nowrap' }}
                onClick={() => {
                  const node = contextMenu.node
                  setContextMenu(null)
                  handleOpenNode(node)
                }}
              >
                打开预览
              </Menu.Item>
            ) : (
              <>
                <Menu.Item
                  leftSection={<IconFilePlus size={14} />}
                  style={{ whiteSpace: 'nowrap' }}
                  onClick={() => {
                    const node = contextMenu.node
                    setContextMenu(null)
                    setNewDocName('')
                    setCreateDocModal({ parentDir: node.path })
                  }}
                >
                  在此新建文档
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconFolderPlus size={14} />}
                  style={{ whiteSpace: 'nowrap' }}
                  onClick={() => {
                    const node = contextMenu.node
                    setContextMenu(null)
                    setNewDirName('')
                    setCreateDirModal({ parentDir: node.path })
                  }}
                >
                  在此新建子文件夹
                </Menu.Item>
                <Menu.Item
                  leftSection={<IconUpload size={14} />}
                  style={{ whiteSpace: 'nowrap' }}
                  onClick={() => {
                    const node = contextMenu.node
                    setContextMenu(null)
                    uploadTargetDirRef.current = node.path
                    uploadInputRef.current?.click()
                  }}
                >
                  上传文件到此目录
                </Menu.Item>
                <Menu.Divider />
              </>
            )}
            {!isOffice &&
              !contextMenu.node.is_dir &&
              isSqlFilePath(contextMenu.node.path) &&
              hasActiveDatabaseTool() && (
                <>
                  <Menu.Item
                    leftSection={<IconDatabase size={14} />}
                    style={{ whiteSpace: 'nowrap' }}
                    onClick={() => {
                      const node = contextMenu.node
                      setContextMenu(null)
                      void openSqlFileInDatabaseTool({
                        path: node.path,
                        sessionId: artifact.sessionId,
                        agentId: artifact.agentId,
                        connectionId: artifact.connectionId,
                      })
                    }}
                  >
                    在数据库工具中打开
                  </Menu.Item>
                  <Menu.Item
                    leftSection={<IconCode size={14} />}
                    style={{ whiteSpace: 'nowrap' }}
                    onClick={() => {
                      const node = contextMenu.node
                      setContextMenu(null)
                      const targetArtifact = resolveArtifactFromPath(node.path, {
                        id: artifact.sessionId,
                        agent_id: artifact.agentId,
                        connection_id: artifact.connectionId,
                        workspace: null,
                        title: '',
                        status: 'idle',
                        updated_at: '',
                      })
                      openArtifact(targetArtifact, 'monaco-viewer')
                    }}
                  >
                    在代码编辑器中打开
                  </Menu.Item>
                  <Menu.Divider />
                </>
              )}
            <Menu.Item
              leftSection={<IconEdit size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                setRenameTarget(node)
                setRenameValue(node.name)
              }}
            >
              重命名
            </Menu.Item>
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
            <Menu.Divider />
            <Menu.Item
              color="red"
              leftSection={<IconTrash size={14} />}
              style={{ whiteSpace: 'nowrap' }}
              onClick={() => {
                const node = contextMenu.node
                setContextMenu(null)
                setDeleteTarget(node)
              }}
            >
              删除
            </Menu.Item>
          </Menu.Dropdown>
        )}
      </Menu>

      <Modal
        opened={createDocModal !== null}
        onClose={() => setCreateDocModal(null)}
        title="新建文档"
        size="sm"
        withinPortal
      >
        <Stack gap="sm" onClick={(e) => e.stopPropagation()}>
          <TextInput
            label="文档名称"
            description="未填扩展名时将自动创建为 Markdown 文档 (.md)"
            placeholder="例如：Q4 季度汇报方案.md"
            autoFocus
            value={newDocName}
            onChange={(e) => setNewDocName(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && newDocName.trim()) {
                e.preventDefault()
                void handleCreateDocument()
              }
            }}
          />
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="subtle" color="gray" onClick={() => setCreateDocModal(null)}>
              取消
            </Button>
            <Button size="xs" loading={mutating} disabled={!newDocName.trim()} onClick={() => void handleCreateDocument()}>
              创建并打开
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={createDirModal !== null}
        onClose={() => setCreateDirModal(null)}
        title="新建文件夹"
        size="sm"
        withinPortal
      >
        <Stack gap="sm" onClick={(e) => e.stopPropagation()}>
          <TextInput
            label="文件夹名称"
            placeholder="例如：会议资料"
            autoFocus
            value={newDirName}
            onChange={(e) => setNewDirName(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && newDirName.trim()) {
                e.preventDefault()
                void handleCreateFolder()
              }
            }}
          />
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="subtle" color="gray" onClick={() => setCreateDirModal(null)}>
              取消
            </Button>
            <Button size="xs" loading={mutating} disabled={!newDirName.trim()} onClick={() => void handleCreateFolder()}>
              创建
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={renameTarget !== null}
        onClose={() => setRenameTarget(null)}
        title="重命名"
        size="sm"
        withinPortal
      >
        <Stack gap="sm" onClick={(e) => e.stopPropagation()}>
          <TextInput
            label="新名称"
            autoFocus
            value={renameValue}
            onChange={(e) => setRenameValue(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && renameValue.trim()) {
                e.preventDefault()
                void handleRenameSubmit()
              }
            }}
          />
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="subtle" color="gray" onClick={() => setRenameTarget(null)}>
              取消
            </Button>
            <Button size="xs" loading={mutating} disabled={!renameValue.trim()} onClick={() => void handleRenameSubmit()}>
              确认重命名
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        title="确认删除"
        size="sm"
        withinPortal
      >
        <Stack gap="sm" onClick={(e) => e.stopPropagation()}>
          <Text size="sm">
            确定要删除{deleteTarget?.is_dir ? '文件夹及其全部内容' : '文件'} <strong>{deleteTarget?.name}</strong> 吗？此操作无法撤销。
          </Text>
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="subtle" color="gray" onClick={() => setDeleteTarget(null)}>
              取消
            </Button>
            <Button size="xs" color="red" loading={mutating} onClick={() => void handleDeleteConfirm()}>
              确认删除
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
