import { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Drawer,
  Group,
  Loader,
  Paper,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBrain,
  IconChevronDown,
  IconChevronRight,
  IconFileText,
  IconFolder,
  IconFolderOpen,
  IconRefresh,
  IconSearch,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import { MarkdownContent } from '../../components/MarkdownContent'

interface LazyNode {
  name: string
  uri: string
  isDir: boolean
  abstract?: string
  loaded?: boolean
  loading?: boolean
  children?: LazyNode[]
}

interface TreeItemRowProps {
  node: LazyNode
  depth?: number
  expanded: Record<string, boolean>
  onToggle: (node: LazyNode) => void
  onSelectFile: (uri: string) => void
}

function TreeItemRow({
  node,
  depth = 0,
  expanded,
  onToggle,
  onSelectFile,
}: TreeItemRowProps) {
  const isExpanded = Boolean(expanded[node.uri])

  const sortedChildren = useMemo(() => {
    if (!node.children) return []
    return [...node.children].sort((a, b) => {
      if (a.isDir !== b.isDir) {
        return a.isDir ? -1 : 1
      }
      return a.name.localeCompare(b.name)
    })
  }, [node.children])

  return (
    <div>
      <Group
        gap={4}
        wrap="nowrap"
        style={{
          paddingLeft: depth * 14 + 6,
          paddingRight: 6,
          paddingTop: 3,
          paddingBottom: 3,
          borderRadius: 4,
          cursor: 'pointer',
          userSelect: 'none',
          transition: 'background 0.12s ease',
        }}
        className="viking-tree-node-row"
        onClick={() => {
          if (node.isDir) {
            onToggle(node)
          } else {
            onSelectFile(node.uri)
          }
        }}
      >
        {node.isDir ? (
          <UnstyledButton
            onClick={(e) => {
              e.stopPropagation()
              onToggle(node)
            }}
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: 16, height: 16 }}
          >
            {node.loading ? (
              <Loader size={10} color="indigo" />
            ) : isExpanded ? (
              <IconChevronDown size={13} color="var(--astr-muted, #94a3b8)" />
            ) : (
              <IconChevronRight size={13} color="var(--astr-muted, #94a3b8)" />
            )}
          </UnstyledButton>
        ) : (
          <span style={{ width: 16, display: 'inline-block' }} />
        )}

        {node.isDir ? (
          isExpanded ? (
            <IconFolderOpen size={15} color="var(--astr-blue, #3b82f6)" />
          ) : (
            <IconFolder size={15} color="var(--astr-blue, #3b82f6)" />
          )
        ) : (
          <IconFileText size={15} color="var(--astr-teal, #10b981)" />
        )}

        <Text size="xs" fw={node.isDir ? 550 : 400} style={{ flex: 1, minWidth: 0 }} truncate>
          {node.name}
        </Text>

        {node.abstract && (
          <Tooltip label={node.abstract} position="left" multiline w={240}>
            <Badge size="xs" variant="light" color="gray" style={{ flexShrink: 0, textTransform: 'none' }}>
              摘要
            </Badge>
          </Tooltip>
        )}
      </Group>

      {node.isDir && isExpanded && (
        <div>
          {node.children && node.children.length > 0 ? (
            sortedChildren.map((child) => (
              <TreeItemRow
                key={child.uri}
                node={child}
                depth={depth + 1}
                expanded={expanded}
                onToggle={onToggle}
                onSelectFile={onSelectFile}
              />
            ))
          ) : node.loaded ? (
            <Text size="xs" c="dimmed" style={{ paddingLeft: (depth + 1) * 14 + 22, paddingTop: 2, paddingBottom: 2 }}>
              (空目录)
            </Text>
          ) : null}
        </div>
      )}
    </div>
  )
}

export function SidecarMemoryPanel() {
  const [rootNodes, setRootNodes] = useState<LazyNode[]>([])
  const [loadingRoot, setLoadingRoot] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const [searchQuery, setSearchQuery] = useState('')
  const [searchResults, setSearchResults] = useState<Array<{ uri: string; score: number; abstract?: string }>>([])
  const [searching, setSearching] = useState(false)

  // 详情预览 Drawer
  const [selectedUri, setSelectedUri] = useState<string | null>(null)
  const [content, setContent] = useState('')
  const [loadingContent, setLoadingContent] = useState(false)

  // 根节点加载（只获取顶级三项：agent, resources, user）
  const loadRoot = async () => {
    setLoadingRoot(true)
    try {
      const res = await api.getMemoryLs('viking://')
      const items = res.items || []
      const nodes: LazyNode[] = items.map((item) => ({
        name: item.uri.replace(/^viking:\/\/?/, '').trim() || item.uri,
        uri: item.uri,
        isDir: item.isDir,
        abstract: item.abstract,
        loaded: false,
        loading: false,
        children: undefined,
      }))
      setRootNodes(nodes)
      setExpanded({})
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '加载知识树根节点失败' })
    } finally {
      setLoadingRoot(false)
    }
  }

  useEffect(() => {
    void loadRoot()
  }, [])

  // 深度更新树中某个节点
  const updateNodeInTree = (nodes: LazyNode[], targetUri: string, updater: (n: LazyNode) => LazyNode): LazyNode[] => {
    return nodes.map((node) => {
      if (node.uri === targetUri) {
        return updater(node)
      }
      if (node.children) {
        return {
          ...node,
          children: updateNodeInTree(node.children, targetUri, updater),
        }
      }
      return node
    })
  };

  // 展开一层加载一层（按需懒加载）
  const handleToggle = async (node: LazyNode) => {
    const isNowExpanded = !expanded[node.uri]

    setExpanded((prev) => ({
      ...prev,
      [node.uri]: isNowExpanded,
    }))

    // 如果是展开，且还没有加载过该层子节点，触发按需加载
    if (isNowExpanded && !node.loaded && !node.loading) {
      setRootNodes((prev) =>
        updateNodeInTree(prev, node.uri, (n) => ({ ...n, loading: true }))
      )

      try {
        const res = await api.getMemoryLs(node.uri)
        const subItems = res.items || []
        const childNodes: LazyNode[] = subItems.map((item) => {
          const parts = item.uri.replace(/^viking:\/\/?/, '').split('/')
          const name = parts[parts.length - 1] || item.uri
          return {
            name,
            uri: item.uri,
            isDir: item.isDir,
            abstract: item.abstract,
            loaded: false,
            loading: false,
            children: undefined,
          }
        })

        setRootNodes((prev) =>
          updateNodeInTree(prev, node.uri, (n) => ({
            ...n,
            loaded: true,
            loading: false,
            children: childNodes,
          }))
        )
      } catch (err: any) {
        notifications.show({
          color: 'red',
          message: err.message || `加载目录 ${node.name} 失败`,
        })
        setRootNodes((prev) =>
          updateNodeInTree(prev, node.uri, (n) => ({ ...n, loading: false }))
        )
      }
    }
  }

  const handleCollapseAll = () => {
    setExpanded({})
  }

  const handleSearch = async () => {
    if (!searchQuery.trim()) return
    setSearching(true)
    try {
      const res = await api.searchMemory({ query: searchQuery.trim(), limit: 10 })
      setSearchResults(res.items || [])
    } catch (err: any) {
      notifications.show({ color: 'red', message: err.message || '语义搜索失败' })
    } finally {
      setSearching(false)
    }
  }

  const handleSelectFile = async (uri: string) => {
    setSelectedUri(uri)
    setLoadingContent(true)
    try {
      const res = await api.getMemoryContent(uri)
      setContent(res.content || '*(文件内容为空)*')
    } catch (err: any) {
      setContent(`*(读取失败: ${err.message || '网络异常'})*`)
    } finally {
      setLoadingContent(false)
    }
  }

  const sortedRootNodes = useMemo(() => {
    return [...rootNodes].sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  }, [rootNodes])

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', padding: '12px 14px' }}>
      {/* 搜索与工具栏 */}
      <Group justify="space-between" mb="xs">
        <Group gap={6}>
          <IconBrain size={18} color="var(--astr-indigo, #6366f1)" />
          <Text fw={600} size="sm">知识与记忆 (viking://)</Text>
        </Group>
        <Group gap={4}>
          <Tooltip label="全部折叠">
            <Button size="compact-xs" variant="subtle" color="gray" onClick={handleCollapseAll}>
              全部折叠
            </Button>
          </Tooltip>
          <ActionIcon variant="subtle" color="gray" size="sm" onClick={loadRoot} loading={loadingRoot} title="刷新根目录">
            <IconRefresh size={14} />
          </ActionIcon>
        </Group>
      </Group>

      <Group gap="xs" mb="sm">
        <TextInput
          size="xs"
          placeholder="语义检索知识库/排障经验..."
          style={{ flex: 1 }}
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void handleSearch()
          }}
        />
        <Button size="xs" variant="light" color="indigo" onClick={handleSearch} loading={searching}>
          <IconSearch size={14} />
        </Button>
      </Group>

      {/* 搜索结果展示 */}
      {searchResults.length > 0 && (
        <Paper withBorder p="xs" radius="md" mb="sm" style={{ background: 'var(--astr-surface-muted)' }}>
          <Group justify="space-between" mb={4}>
            <Text size="xs" fw={600} c="indigo">语义命中结果 ({searchResults.length})</Text>
            <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSearchResults([])}>清空</Button>
          </Group>
          <Stack gap={4}>
            {searchResults.map((r) => (
              <Paper
                key={r.uri}
                p={6}
                radius="sm"
                withBorder
                style={{ cursor: 'pointer' }}
                onClick={() => handleSelectFile(r.uri)}
              >
                <Group justify="space-between">
                  <Text size="xs" fw={500} truncate style={{ maxWidth: 220 }}>{r.uri.split('/').pop()}</Text>
                  <Badge size="xs" variant="outline">匹配 {(r.score * 100).toFixed(0)}%</Badge>
                </Group>
                {r.abstract && <Text size="xs" c="dimmed" lineClamp={2} mt={2}>{r.abstract}</Text>}
              </Paper>
            ))}
          </Stack>
        </Paper>
      )}

      {/* 虚拟文件树（按层懒加载） */}
      <Group justify="space-between" mb={4}>
        <Text size="xs" c="dimmed">虚拟目录层级（点击展开按层加载）：</Text>
      </Group>
      <ScrollArea style={{ flex: 1 }}>
        <Stack gap={1}>
          {loadingRoot ? (
            <Text size="xs" c="dimmed" ta="center" py="md">
              正在加载知识库根目录...
            </Text>
          ) : sortedRootNodes.length === 0 ? (
            <Text size="xs" c="dimmed" ta="center" py="md">
              暂无虚拟文件节点
            </Text>
          ) : (
            sortedRootNodes.map((child) => (
              <TreeItemRow
                key={child.uri}
                node={child}
                depth={0}
                expanded={expanded}
                onToggle={handleToggle}
                onSelectFile={handleSelectFile}
              />
            ))
          )}
        </Stack>
      </ScrollArea>

      {/* 详情抽屉 Drawer */}
      <Drawer
        opened={Boolean(selectedUri)}
        onClose={() => setSelectedUri(null)}
        position="right"
        size="md"
        title={
          <Group gap="xs">
            <IconFileText size={16} color="var(--astr-teal)" />
            <Text size="sm" fw={600} truncate style={{ maxWidth: 260 }}>{selectedUri?.split('/').pop()}</Text>
          </Group>
        }
      >
        <Text size="xs" c="dimmed" mb="sm">{selectedUri}</Text>
        <ScrollArea h="calc(100vh - 120px)">
          {loadingContent ? (
            <Text size="xs" c="dimmed">正在读取内容...</Text>
          ) : (
            <MarkdownContent value={content} />
          )}
        </ScrollArea>
      </Drawer>
    </div>
  )
}
