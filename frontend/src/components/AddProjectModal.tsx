import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Group,
  Loader,
  Modal,
  NativeSelect,
  Paper,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowUp,
  
  IconFolder,
  IconFolderPlus,
  IconRefresh,
  IconServer,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Agent } from '../domain/types'
// useAstrorderStore

interface DirectoryItem {
  name: string
  path: string
  is_dir: boolean
}

interface TreeResponse {
  root: string
  name: string
  parent?: string | null
  drives?: string[]
  items: DirectoryItem[]
}

export function AddProjectModal({
  opened,
  onClose,
  agents = {},
}: {
  opened: boolean
  onClose: () => void
  agents?: Record<string, Agent>
}) {
  let navigate: ReturnType<typeof useNavigate> | null = null
  try {
    // eslint-disable-next-line react-hooks/rules-of-hooks
    navigate = useNavigate()
  } catch {
    navigate = null
  }
  const queryClient = useQueryClient()

  const [connectionId, setConnectionId] = useState<string>('local')
  const [serverList, setServerList] = useState<Array<{ value: string; label: string }>>([
    { value: 'local', label: '本机 (Local)' },
  ])
  const [loadingServers, setLoadingServers] = useState(false)

  // 目录选择与树形浏览状态
  const [currentPath, setCurrentPath] = useState<string>('')
  const [pathInput, setPathInput] = useState<string>('')
  const [selectedPath, setSelectedPath] = useState<string>('')
  const [parentPath, setParentPath] = useState<string | null>(null)
  const [drives, setDrives] = useState<string[]>([])
  const [dirItems, setDirItems] = useState<DirectoryItem[]>([])
  const [loadingTree, setLoadingTree] = useState(false)
  const [treeError, setTreeError] = useState<string | null>(null)

  // 项目属性
  const [projectName, setProjectName] = useState('')
  const [selectedAgentId, setSelectedAgentId] = useState('')
  const [submitting, setSubmitting] = useState(false)

  // 1. 获取服务器列表 (本机 + 所有已连接或保存的 SSH 服务器)
  useEffect(() => {
    if (!opened) return
    let mounted = true
    setLoadingServers(true)
    void api.getConnections().then((res) => {
      if (!mounted) return
      const list: Array<{ value: string; label: string }> = [{ value: 'local', label: '本机 (Local)' }]
      const sshList = res.ssh || []
      for (const conn of Object.values(sshList || {})) {
        if (conn && conn.id) {
          const name = conn.display_name || conn.host || conn.id
          list.push({
            value: conn.id,
            label: `${name} (${conn.user || 'root'}@${conn.host || 'remote'})`,
          })
        }
      }
      setServerList(list)
    }).catch(() => {
      // 降级使用现有 agents 的 connection_id
      const list: Array<{ value: string; label: string }> = [{ value: 'local', label: '本机 (Local)' }]
      const seen = new Set<string>()
      for (const a of Object.values(agents)) {
        if (a.connection_id && a.connection_id !== 'local' && !seen.has(a.connection_id)) {
          seen.add(a.connection_id)
          list.push({ value: a.connection_id, label: `SSH: ${a.name || a.connection_id}` })
        }
      }
      setServerList(list)
    }).finally(() => {
      if (mounted) setLoadingServers(false)
    })
    return () => { mounted = false }
  }, [opened, agents])

  // 可选 Agent 列表
  const agentOptions = useMemo(() => {
    return Object.values(agents)
      .filter((a) => {
        if (a.status !== 'ready') return false
        if (connectionId === 'local') return !a.connection_id || a.connection_id === 'local'
        return a.connection_id === connectionId
      })
      .map((a) => ({ value: a.id, label: `${a.name} (${a.kind})` }))
  }, [agents, connectionId])

  useEffect(() => {
    if (agentOptions.length > 0 && (!selectedAgentId || !agentOptions.some((opt) => opt.value === selectedAgentId))) {
      setSelectedAgentId(agentOptions[0].value)
    }
  }, [agentOptions, selectedAgentId])

  // 2. 加载选定服务器下的目录树
  const fetchDirectory = useCallback(async (pathQuery: string) => {
    setLoadingTree(true)
    setTreeError(null)
    try {
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = {}
      if (localToken) headers['Authorization'] = `Bearer ${localToken}`

      const params = new URLSearchParams()
      if (pathQuery) params.set('path', pathQuery)
      if (connectionId && connectionId !== 'local') params.set('connection_id', connectionId)
      params.set('depth', '1')

      const res = await fetch(`/api/v1/files/tree?${params.toString()}`, { headers })
      if (!res.ok) throw new Error(`加载目录树失败 (${res.status})`)
      const data: TreeResponse = await res.json()
      const dirs = (data.items || []).filter((item) => item.is_dir)
      setDirItems(dirs)
      setCurrentPath(data.root)
      setPathInput(data.root)
      setParentPath(data.parent || null)
      if (data.drives && data.drives.length > 0) setDrives(data.drives)
      setSelectedPath((prev) => {
        const next = (!prev || pathQuery === data.root) ? data.root : prev
        const norm = next.replace(/\\/g, '/').replace(/\/+$/, '')
        const baseName = norm.split('/').filter(Boolean).pop() || ''
        if (baseName) setProjectName((p) => p || baseName)
        return next
      })
    } catch (err: unknown) {
      setTreeError(err instanceof Error ? err.message : '加载目录树失败')
    } finally {
      setLoadingTree(false)
    }
  }, [connectionId])

  // 切换服务器时重置并重新拉取根目录
  useEffect(() => {
    if (!opened) return
    setCurrentPath('')
    setPathInput('')
    setSelectedPath('')
    setParentPath(null)
    setDirItems([])
    void fetchDirectory('')
  }, [connectionId, opened, fetchDirectory])

  // 选择文件夹
  const handleSelectFolder = (folderPath: string) => {
    setSelectedPath(folderPath)
    setPathInput(folderPath)
    const norm = folderPath.replace(/\\/g, '/').replace(/\/+$/, '')
    const baseName = norm.split('/').filter(Boolean).pop() || ''
    if (baseName) setProjectName(baseName)
  }

  // 双击或点击进入文件夹
  const handleEnterFolder = (folderPath: string) => {
    handleSelectFolder(folderPath)
    void fetchDirectory(folderPath)
  }

  // 返回上一级目录
  const handleGoParent = () => {
    if (parentPath) {
      handleSelectFolder(parentPath)
      void fetchDirectory(parentPath)
    }
  }

  // 3. 提交新建项目
  const handleSubmit = async () => {
    if (!selectedPath.trim() || submitting) return
    setSubmitting(true)
    try {
      const finalName = projectName.trim() || selectedPath.replace(/\\/g, '/').split('/').filter(Boolean).pop() || '未命名项目'
      const res = await api.createProject({
        workspace: selectedPath.trim(),
        name: finalName,
        connection_id: connectionId === 'local' ? null : connectionId,
        agent_id: selectedAgentId || 'codex',
      })

      // 同时自动为该新项目创建一个会话，以便用户直接进入对话
      const sessionRes = await api.createSession({
        agent_id: selectedAgentId || 'codex',
        workspace: selectedPath.trim(),
        title: '新会话',
        project_id: res.project.project_id,
        project_name: finalName,
      })

      // 刷新项目与会话缓存
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'projects'] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'sessions'] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })

      notifications.show({
        color: 'teal',
        message: `项目「${finalName}」已成功创建`,
      })

      onClose()
      if (navigate) {
        navigate(`/chat/${encodeURIComponent(sessionRes.id)}?agent_id=${encodeURIComponent(sessionRes.agent_id)}`)
      } else if (typeof window !== 'undefined') {
        window.location.href = `/chat/${encodeURIComponent(sessionRes.id)}?agent_id=${encodeURIComponent(sessionRes.agent_id)}`
      }
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '创建项目失败',
      })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={() => !submitting && onClose()}
      title={
        <Group gap={8}>
          <IconFolderPlus size={20} color="var(--astr-indigo, #5b6cff)" />
          <Text fw={700} size="md">新增项目</Text>
        </Group>
      }
      size="lg"
      radius="lg"
      centered
    >
      <Stack gap="md">
        {/* Step 1: 先选服务器 */}
        <NativeSelect
          label="1. 选择运行服务器"
          description="指定项目所在的本地环境或远端 SSH 服务器"
          data={serverList}
          value={connectionId}
          onChange={(e) => setConnectionId(e.currentTarget.value)}
          disabled={loadingServers || submitting}
          leftSection={loadingServers ? <Loader size={14} /> : <IconServer size={16} />}
        />

        {/* Step 2: 选择文件夹 (复用文件树组件/浏览) */}
        <div>
          <Text size="sm" fw={500} mb={4}>2. 选择项目文件夹</Text>
          <Text size="xs" c="dimmed" mb={8}>在下方浏览并选择该服务器上的代码根目录</Text>
          
          <Paper withBorder radius="md" p="xs" style={{ background: 'var(--astr-surface-muted, #f8fafc)' }}>
            {/* 路径导航条与上一级按钮 */}
            <Group justify="space-between" mb="xs" wrap="wrap" gap="xs">
              <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  onClick={handleGoParent}
                  disabled={!parentPath || loadingTree}
                  title="返回上一级目录"
                >
                  <IconArrowUp size={14} />
                </ActionIcon>
                <TextInput
                  size="xs"
                  value={pathInput}
                  onChange={(e) => setPathInput(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      void fetchDirectory(pathInput.trim())
                    }
                  }}
                  placeholder="输入路径按回车跳转"
                  style={{ flex: 1, minWidth: 140 }}
                  styles={{ input: { fontFamily: 'monospace', fontSize: 11 } }}
                  disabled={loadingTree}
                  rightSection={
                    <ActionIcon
                      variant="transparent"
                      size="xs"
                      color="gray"
                      onClick={() => void fetchDirectory(pathInput.trim())}
                      title="跳转/刷新"
                    >
                      <IconRefresh size={12} />
                    </ActionIcon>
                  }
                />
              </Group>
              {drives.length > 0 && (
                <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
                  {drives.map((d) => {
                    const isCurrentDrive = currentPath.toUpperCase().startsWith(d.toUpperCase())
                    return (
                      <Button
                        key={d}
                        size="compact-xs"
                        variant={isCurrentDrive ? 'filled' : 'light'}
                        color="indigo"
                        onClick={() => {
                          setPathInput(d)
                          void fetchDirectory(d)
                        }}
                        style={{ fontFamily: 'monospace', fontWeight: 600, fontSize: 11 }}
                      >
                        {d.replace(/\\$/, '')}
                      </Button>
                    )
                  })}
                </Group>
              )}
            </Group>

            {/* 目录列表 */}
            <ScrollArea.Autosize mah={220}>
              {loadingTree ? (
                <Group justify="center" p="md">
                  <Loader size="sm" />
                  <Text size="xs" c="dimmed">正在读取目录树…</Text>
                </Group>
              ) : treeError ? (
                <Text size="xs" c="red" p="xs">{treeError}</Text>
              ) : dirItems.length === 0 ? (
                <Text size="xs" c="dimmed" p="xs" ta="center">当前目录下无子文件夹，可直接选用此目录</Text>
              ) : (
                <Stack gap={2}>
                  {dirItems.map((item) => {
                    const isSelected = selectedPath === item.path
                    return (
                      <UnstyledButton
                        key={item.path}
                        onClick={() => handleSelectFolder(item.path)}
                        onDoubleClick={() => handleEnterFolder(item.path)}
                        p="xs"
                        style={{
                          borderRadius: 6,
                          background: isSelected ? 'color-mix(in srgb, var(--astr-indigo, #5b6cff) 14%, transparent)' : undefined,
                          border: isSelected ? '1px solid color-mix(in srgb, var(--astr-indigo, #5b6cff) 35%, transparent)' : '1px solid transparent',
                        }}
                      >
                        <Group justify="space-between" wrap="nowrap">
                          <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
                            <IconFolder size={16} color={isSelected ? 'var(--astr-indigo, #5b6cff)' : 'var(--astr-muted)'} />
                            <Text size="xs" fw={isSelected ? 600 : 500} truncate style={{ color: 'var(--astr-text)' }}>
                              {item.name}
                            </Text>
                          </Group>
                          <Group gap={4} wrap="nowrap">
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              onClick={(e) => {
                                e.stopPropagation()
                                handleEnterFolder(item.path)
                              }}
                            >
                              进入
                            </Button>
                          </Group>
                        </Group>
                      </UnstyledButton>
                    )
                  })}
                </Stack>
              )}
            </ScrollArea.Autosize>
          </Paper>

          {selectedPath && (
            <Group gap={6} mt={6}>
              <Badge size="xs" color="teal" variant="light">已选目录</Badge>
              <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
                {selectedPath}
              </Text>
            </Group>
          )}
        </div>

        {/* Step 3: 项目名称 */}
        <TextInput
          label="3. 项目展示名称"
          placeholder="例如：my-project"
          value={projectName}
          onChange={(e) => setProjectName(e.currentTarget.value)}
          disabled={submitting}
        />

        {/* Step 4: 默认 Agent */}
        {agentOptions.length > 0 && (
          <NativeSelect
            label="4. 初始智能体"
            data={agentOptions}
            value={selectedAgentId}
            onChange={(e) => setSelectedAgentId(e.currentTarget.value)}
            disabled={submitting}
          />
        )}

        <Group justify="flex-end" gap="sm" mt="md">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button
            color="indigo"
            onClick={() => void handleSubmit()}
            loading={submitting}
            disabled={!selectedPath.trim() || !projectName.trim()}
          >
            确定创建项目
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
