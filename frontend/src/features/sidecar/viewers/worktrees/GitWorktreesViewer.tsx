import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Group,
  Loader,
  Modal,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconCheck,
  IconCopy,
  IconFileDiff,
  IconFolder,
  IconGitBranch,
  IconGitMerge,
  IconLock,
  IconPlus,
  IconRefresh,
  IconSettings,
  IconTrash,
  IconWand,
} from '@tabler/icons-react'
import { api } from '../../../../api/client'
import type { GitWorktreeEntry, GitWorktreeMergeResult, Session } from '../../../../domain/types'
import { useGitWorktrees } from '../../../../hooks/useGitWorktrees'
import { useAstrorderStore } from '../../../../state/store'
import { sessionActivityStatus } from '../../../../components/sessionRailModel'
import { NewSessionDialog } from '../../../../components/NewSessionDialog'
import {
  WorktreeConfigSection,
  createInitialWorktreeState,
  saveWorktreePrefs,
} from '../../../../components/WorktreeConfigSection'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'

function normalizeCompare(value: string | null | undefined): string {
  if (!value) return ''
  const s = value.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  return /^[a-zA-Z]:\//.test(s) ? s.toLowerCase() : s
}

export function GitWorktreesViewer({ artifact, session }: ViewerContext) {
  const navigate = useNavigate()
  const agents = useAstrorderStore((s) => s.agents)
  const sessionsMap = useAstrorderStore((s) => s.sessions)
  const allSessions = useMemo(() => Object.values(sessionsMap), [sessionsMap])
  const openGitDiff = useSidecarStore((s) => s.openGitDiff)
  const openFileTree = useSidecarStore((s) => s.openFileTree)

  const workspace = artifact.path || session?.worktree_root_workspace || session?.workspace || ''
  const connectionId = artifact.connectionId || session?.connection_id || undefined
  const sessionId = artifact.sessionId || session?.id || ''
  const agentId = artifact.agentId || session?.agent_id || ''

  const query = useGitWorktrees({
    workspace,
    connectionId,
    sessionId: sessionId || undefined,
    includeStatuses: true,
    enabled: Boolean(workspace),
  })
  const data = query.data

  const [busyAction, setBusyAction] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [createState, setCreateState] = useState(() => createInitialWorktreeState(workspace, 'create'))
  const [newSessionTarget, setNewSessionTarget] = useState<{ mode: 'none' | 'existing'; path?: string } | null>(null)

  const [removeTarget, setRemoveTarget] = useState<GitWorktreeEntry | null>(null)
  const [removeForce, setRemoveForce] = useState(false)
  const [removeDeleteBranch, setRemoveDeleteBranch] = useState(true)

  const [mergeTarget, setMergeTarget] = useState<GitWorktreeEntry | null>(null)
  const [mergeDestBranch, setMergeDestBranch] = useState('')
  const [mergeStrategy, setMergeStrategy] = useState<'merge' | 'ff_only' | 'squash'>('merge')
  const [mergeCleanupAfter, setMergeCleanupAfter] = useState(false)
  const [mergeConflict, setMergeConflict] = useState<GitWorktreeMergeResult | null>(null)

  const sessionsByPath = useMemo(() => {
    const map = new Map<string, Session[]>()
    for (const s of allSessions) {
      if ((s.connection_id || '') !== (connectionId || '')) continue
      const key = normalizeCompare(s.workspace)
      if (!key) continue
      const list = map.get(key) || []
      list.push(s)
      map.set(key, list)
    }
    return map
  }, [allSessions, connectionId])

  const sortedWorktrees = useMemo(() => {
    const list = [...(data?.worktrees || [])]
    list.sort((a, b) => {
      if (a.is_main !== b.is_main) return a.is_main ? -1 : 1
      if (a.is_current !== b.is_current) return a.is_current ? -1 : 1
      const aSessions = sessionsByPath.get(normalizeCompare(a.path))?.length || 0
      const bSessions = sessionsByPath.get(normalizeCompare(b.path))?.length || 0
      if (aSessions !== bSessions) return bSessions - aSessions
      return (a.branch || a.path).localeCompare(b.branch || b.path)
    })
    return list
  }, [data?.worktrees, sessionsByPath])

  const linkedCount = sortedWorktrees.filter((w) => !w.is_main).length
  const dirtyCount = sortedWorktrees.filter((w) => w.dirty).length
  const prunableCount = sortedWorktrees.filter((w) => w.prunable).length

  const copyPath = async (path: string) => {
    try {
      await navigator.clipboard.writeText(path)
      notifications.show({ color: 'teal', message: '已复制 Worktree 路径' })
    } catch {
      notifications.show({ color: 'red', message: '复制路径失败' })
    }
  }

  const handlePrune = async () => {
    setBusyAction('prune')
    try {
      const res = await api.pruneGitWorktrees({
        workspace: data?.main_workspace || workspace,
        session_id: sessionId || undefined,
        connection_id: connectionId,
      })
      notifications.show({
        color: 'teal',
        message: res.pruned_count > 0 ? `已清理 ${res.pruned_count} 条失效 Worktree 记录` : '没有需要清理的失效 Worktree 记录',
      })
      await query.refetch()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '清理失败' })
    } finally {
      setBusyAction(null)
    }
  }

  const handleCreateStandalone = async () => {
    if (!createState.branch.trim()) return
    setBusyAction('create')
    try {
      const res = await api.createGitWorktree({
        workspace: data?.main_workspace || workspace,
        session_id: sessionId || undefined,
        connection_id: connectionId,
        branch: createState.branch.trim(),
        base_ref: createState.createBranch && createState.baseRef.trim() ? createState.baseRef.trim() : undefined,
        create_branch: createState.createBranch,
        target_path: createState.path.trim() || undefined,
        layout: createState.layout,
        copy_files: createState.copyFiles,
      })
      saveWorktreePrefs(data?.main_workspace || workspace, createState)
      const copiedNote = res.copied_files?.length ? `，已同步 ${res.copied_files.length} 个配置文件` : ''
      notifications.show({ color: 'teal', message: `已创建 Worktree (${res.branch})${copiedNote}` })
      setCreateOpen(false)
      await query.refetch()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '创建 Worktree 失败' })
    } finally {
      setBusyAction(null)
    }
  }

  const handleBootstrap = async (wt: GitWorktreeEntry) => {
    setBusyAction(`bootstrap:${wt.path}`)
    try {
      const res = await api.bootstrapGitWorktree({
        workspace: data?.main_workspace || workspace,
        session_id: sessionId || undefined,
        connection_id: connectionId,
        target_path: wt.path,
      })
      notifications.show({
        color: 'teal',
        message: res.copied_files?.length
          ? `已同步 ${res.copied_files.length} 个配置文件：${res.copied_files.join(', ')}`
          : '未找到可同步的本地忽略配置文件',
      })
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '同步配置失败' })
    } finally {
      setBusyAction(null)
    }
  }

  const handleConfirmRemove = async () => {
    if (!removeTarget) return
    setBusyAction('remove')
    try {
      const res = await api.removeGitWorktree({
        workspace: data?.main_workspace || workspace,
        session_id: sessionId || undefined,
        connection_id: connectionId,
        target_path: removeTarget.path,
        force: removeForce,
        delete_branch: removeDeleteBranch,
      })
      const branchNote = removeDeleteBranch
        ? (res.branch_deleted ? '，并已删除对应本地分支' : res.branch_delete_error ? `（分支未删除：${res.branch_delete_error}）` : '')
        : ''
      notifications.show({ color: 'teal', message: `已移除 Worktree${branchNote}` })
      setRemoveTarget(null)
      await query.refetch()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '移除 Worktree 失败' })
    } finally {
      setBusyAction(null)
    }
  }

  const openMergeModal = (wt: GitWorktreeEntry) => {
    setMergeTarget(wt)
    setMergeDestBranch(data?.default_branch || 'main')
    setMergeStrategy('merge')
    setMergeCleanupAfter(false)
    setMergeConflict(null)
  }

  const handleConfirmMerge = async () => {
    if (!mergeTarget?.branch) return
    setBusyAction('merge')
    setMergeConflict(null)
    try {
      const res = await api.mergeGitWorktree({
        workspace: data?.main_workspace || workspace,
        session_id: sessionId || undefined,
        connection_id: connectionId,
        source_branch: mergeTarget.branch,
        target_branch: mergeDestBranch || data?.default_branch,
        strategy: mergeStrategy,
      })
      if (!res.merged) {
        setMergeConflict(res)
        return
      }
      notifications.show({
        color: 'teal',
        message: `已将 ${res.source_branch} 成功合并到 ${res.target_branch}`,
      })
      if (mergeCleanupAfter) {
        try {
          await api.removeGitWorktree({
            workspace: data?.main_workspace || workspace,
            session_id: sessionId || undefined,
            connection_id: connectionId,
            target_path: mergeTarget.path,
            delete_branch: true,
          })
          notifications.show({ color: 'teal', message: `已清理合并后的 Worktree 与分支 ${mergeTarget.branch}` })
        } catch (cleanupErr) {
          notifications.show({
            color: 'yellow',
            message: `分支已合并，但自动清理 Worktree 失败：${cleanupErr instanceof Error ? cleanupErr.message : '未知错误'}`,
          })
        }
      }
      setMergeTarget(null)
      await query.refetch()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '合并失败' })
    } finally {
      setBusyAction(null)
    }
  }

  const sendConflictToComposer = (conflict: GitWorktreeMergeResult) => {
    if (!session) return
    const filesText = conflict.conflict_files?.length
      ? conflict.conflict_files.map((f) => `- \`${f}\``).join('\n')
      : '（请运行 git status 查看具体冲突文件）'
    const text = `请帮我将分支 \`${conflict.source_branch}\` 合并到 \`${conflict.target_branch}\` 并解决以下冲突（此前自动合并已安全回滚）：\n${filesText}\n`
    window.dispatchEvent(new CustomEvent('astrorder:quote-to-composer', {
      detail: { agentId: session.agent_id, sessionId: session.id, text },
    }))
    notifications.show({ color: 'violet', message: '已将冲突解决提示填入当前会话输入框' })
    setMergeTarget(null)
  }

  const projectForNewSession = useMemo(() => {
    if (!data?.main_workspace) return null
    const baseName = data.main_workspace.replace(/\\/g, '/').split('/').filter(Boolean).pop() || data.main_workspace
    return {
      key: `workspace:${connectionId || 'local'}\u0000${data.main_workspace}`,
      label: session?.project_name || baseName,
      sessions: [],
      sessionCount: 0,
      latestUpdatedAt: '',
      agentId: agentId || undefined,
      workspace: data.main_workspace,
    }
  }, [data?.main_workspace, connectionId, session?.project_name, agentId])

  return (
    <Stack gap="sm" p="sm" style={{ height: '100%', overflow: 'auto' }}>
      <Group justify="space-between" align="center" wrap="nowrap">
        <Group gap={6} wrap="nowrap">
          <IconGitBranch size={16} color="var(--astr-blue, #3b82f6)" />
          <Text size="sm" fw={600}>Git Worktree 工作区</Text>
          {data?.is_git_repo && (
            <Badge size="xs" variant="light" color="blue">
              {linkedCount} 个独立工作树
            </Badge>
          )}
          {dirtyCount > 0 && (
            <Badge size="xs" variant="light" color="orange">
              {dirtyCount} 处有未提交
            </Badge>
          )}
        </Group>
        <Group gap={4} wrap="nowrap">
          {data?.is_git_repo && (
            <>
              <Button
                size="compact-xs"
                leftSection={<IconPlus size={13} />}
                onClick={() => {
                  setCreateState(createInitialWorktreeState(data.main_workspace || workspace, 'create'))
                  setCreateOpen(true)
                }}
              >
                新建
              </Button>
              <Tooltip label={prunableCount > 0 ? `清理 ${prunableCount} 条失效记录 (git worktree prune)` : '清理失效 Worktree 元数据 (git worktree prune)'}>
                <ActionIcon
                  size="sm"
                  variant={prunableCount > 0 ? 'light' : 'subtle'}
                  color={prunableCount > 0 ? 'orange' : 'gray'}
                  aria-label="清理失效 Worktree"
                  loading={busyAction === 'prune'}
                  onClick={() => void handlePrune()}
                >
                  <IconWand size={15} />
                </ActionIcon>
              </Tooltip>
            </>
          )}
          <Tooltip label="刷新 Worktree 列表">
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              aria-label="刷新 Worktree"
              loading={query.isFetching}
              onClick={() => void query.refetch()}
            >
              <IconRefresh size={15} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {query.isPending ? (
        <Group justify="center" py="xl">
          <Loader size="sm" />
          <Text size="xs" c="dimmed">正在扫描仓库 Worktree 状态…</Text>
        </Group>
      ) : query.isError ? (
        <Alert color="red" icon={<IconAlertTriangle size={16} />} title="读取 Worktree 失败">
          {query.error instanceof Error ? query.error.message : '无法读取该工作区的 Git Worktree 状态'}
        </Alert>
      ) : !data?.is_git_repo ? (
        <Paper withBorder p="md" radius="md">
          <Text size="sm" fw={500}>当前工作区不是 Git 仓库</Text>
          <Text size="xs" c="dimmed" mt={4}>
            在 Git 仓库中打开会话即可创建、并行开发并管理多个隔离的 Git Worktree 目录。
          </Text>
        </Paper>
      ) : (
        <>
          <Paper withBorder p="xs" radius="md" style={{ background: 'var(--astr-surface-subtle, rgba(0,0,0,0.02))' }}>
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Stack gap={2} style={{ minWidth: 0 }}>
                <Group gap={6}>
                  <Text size="xs" c="dimmed">主仓库</Text>
                  {data.default_branch && <Badge size="xs" variant="outline" color="gray">默认：{data.default_branch}</Badge>}
                </Group>
                <Text size="xs" ff="monospace" truncate title={data.main_workspace}>
                  {data.main_workspace}
                </Text>
              </Stack>
              {data.main_workspace && (
                <Tooltip label="复制主仓库路径">
                  <ActionIcon size="sm" variant="subtle" color="gray" onClick={() => void copyPath(data.main_workspace!)}>
                    <IconCopy size={14} />
                  </ActionIcon>
                </Tooltip>
              )}
            </Group>
          </Paper>

          <Stack gap="xs">
            {sortedWorktrees.map((wt) => {
              const boundSessions = sessionsByPath.get(normalizeCompare(wt.path)) || []
              const canMerge = !wt.is_main && !wt.prunable && Boolean(wt.branch) && wt.branch !== data.default_branch
              return (
                <Paper
                  key={wt.path}
                  withBorder
                  radius="md"
                  p="sm"
                  style={{
                    borderColor: wt.is_current ? 'var(--astr-blue, #3b82f6)' : undefined,
                  }}
                >
                  <Stack gap={6}>
                    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
                      <Group gap={6} wrap="wrap" style={{ minWidth: 0 }}>
                        <IconGitBranch size={14} color={wt.is_main ? '#64748b' : 'var(--astr-blue, #3b82f6)'} />
                        <Text size="xs" fw={700} truncate>
                          {wt.branch || (wt.detached ? `HEAD @ ${wt.head.slice(0, 7)}` : '(bare)')}
                        </Text>
                        {wt.is_main && <Badge size="xs" variant="light" color="gray">主工作区</Badge>}
                        {wt.is_current && <Badge size="xs" variant="filled" color="blue">当前</Badge>}
                        {wt.locked && (
                          <Tooltip label={wt.lock_reason || '已锁定'}>
                            <Badge size="xs" variant="light" color="yellow" leftSection={<IconLock size={10} />}>已锁定</Badge>
                          </Tooltip>
                        )}
                        {wt.prunable && <Badge size="xs" variant="light" color="red">目录缺失</Badge>}
                      </Group>

                      <Group gap={4} wrap="nowrap">
                        {wt.status_loaded && (
                          <Badge size="xs" variant="light" color={wt.dirty ? 'orange' : 'teal'}>
                            {wt.dirty ? `${wt.changed_files} 处修改` : '干净'}
                          </Badge>
                        )}
                        {(wt.ahead > 0 || wt.behind > 0) && (
                          <Badge size="xs" variant="outline" color="indigo">
                            {wt.ahead > 0 ? `↑${wt.ahead}` : ''}{wt.ahead > 0 && wt.behind > 0 ? ' ' : ''}{wt.behind > 0 ? `↓${wt.behind}` : ''}
                          </Badge>
                        )}
                      </Group>
                    </Group>

                    <Group justify="space-between" wrap="nowrap" gap={4}>
                      <Text size="xs" c="dimmed" ff="monospace" truncate title={wt.path}>
                        {wt.path}
                      </Text>
                      <Tooltip label="复制路径">
                        <ActionIcon size="xs" variant="subtle" color="gray" onClick={() => void copyPath(wt.path)}>
                          <IconCopy size={12} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>

                    {/* 关联会话快捷跳转 */}
                    <Group gap={4} wrap="wrap">
                      {boundSessions.map((s) => {
                        const running = sessionActivityStatus(s) === 'running'
                        return (
                          <Badge
                            key={`${s.agent_id}:${s.id}`}
                            size="xs"
                            variant={running ? 'filled' : 'light'}
                            color={running ? 'teal' : 'gray'}
                            style={{ cursor: 'pointer', textTransform: 'none' }}
                            onClick={() => navigate(`/chat/${encodeURIComponent(s.id)}?agent_id=${encodeURIComponent(s.agent_id)}`)}
                          >
                            {running ? '● ' : ''}{s.title || s.id.slice(0, 8)}
                          </Badge>
                        )
                      })}
                      {!wt.prunable && (
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          leftSection={<IconPlus size={11} />}
                          onClick={() => setNewSessionTarget({
                            mode: wt.is_main ? 'none' : 'existing',
                            path: wt.is_main ? undefined : wt.path,
                          })}
                        >
                          在此新建会话
                        </Button>
                      )}
                    </Group>

                    {/* 辅助功能按钮栏 */}
                    {!wt.prunable && (
                      <Group gap={6} wrap="wrap" pt={2}>
                        {!wt.is_main && data.default_branch && (
                          <Button
                            size="compact-xs"
                            variant="light"
                            color="blue"
                            leftSection={<IconFileDiff size={12} />}
                            onClick={() => openGitDiff(sessionId || 'wt', agentId || 'local', wt.path, connectionId, data.default_branch)}
                          >
                            对比 {data.default_branch}
                          </Button>
                        )}
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          color="gray"
                          leftSection={<IconFolder size={12} />}
                          onClick={() => openFileTree(sessionId || 'wt', agentId || 'local', wt.path, wt.branch || 'Worktree', connectionId)}
                        >
                          文件树
                        </Button>
                        {!wt.is_main && (data.copyable_files?.length || 0) > 0 && (
                          <Tooltip label={`从主仓同步 ${data.copyable_files.map((f) => f.path).join(', ')}`}>
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="violet"
                              leftSection={<IconSettings size={12} />}
                              loading={busyAction === `bootstrap:${wt.path}`}
                              onClick={() => void handleBootstrap(wt)}
                            >
                              同步配置
                            </Button>
                          </Tooltip>
                        )}
                        {canMerge && (
                          <Button
                            size="compact-xs"
                            variant="light"
                            color="teal"
                            leftSection={<IconGitMerge size={12} />}
                            onClick={() => openMergeModal(wt)}
                          >
                            合并
                          </Button>
                        )}
                        {!wt.is_main && (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color="red"
                            leftSection={<IconTrash size={12} />}
                            onClick={() => {
                              setRemoveTarget(wt)
                              setRemoveForce(false)
                              setRemoveDeleteBranch(true)
                            }}
                          >
                            移除
                          </Button>
                        )}
                      </Group>
                    )}
                  </Stack>
                </Paper>
              )
            })}
          </Stack>
        </>
      )}

      {/* 独立创建 Worktree 弹窗 */}
      <Modal
        opened={createOpen}
        onClose={() => { if (!busyAction) setCreateOpen(false) }}
        title="新建 Git Worktree"
        centered
        size="md"
        zIndex={420}
      >
        <Stack gap="sm">
          <WorktreeConfigSection
            workspace={data?.main_workspace || workspace}
            connectionId={connectionId}
            sessionId={sessionId || undefined}
            value={createState}
            onChange={setCreateState}
            allowNone={false}
            allowExisting={false}
            disabled={busyAction === 'create'}
            sessions={allSessions}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" size="xs" onClick={() => setCreateOpen(false)} disabled={busyAction === 'create'}>
              取消
            </Button>
            <Button
              size="xs"
              onClick={() => void handleCreateStandalone()}
              loading={busyAction === 'create'}
              disabled={!createState.branch.trim()}
            >
              创建 Worktree
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* 在指定 Worktree 新建会话 */}
      {newSessionTarget && (
        <NewSessionDialog
          agents={agents}
          project={projectForNewSession}
          initialAgentId={agentId || undefined}
          initialWorkspace={data?.main_workspace || workspace}
          initialWorktreeMode={newSessionTarget.mode}
          initialWorktreePath={newSessionTarget.path}
          onClose={() => setNewSessionTarget(null)}
          onCreated={(created) => {
            navigate(`/chat/${encodeURIComponent(created.id)}?agent_id=${encodeURIComponent(created.agent_id)}`)
          }}
        />
      )}

      {/* 安全移除 Worktree 弹窗 */}
      <Modal
        opened={removeTarget !== null}
        onClose={() => { if (!busyAction) setRemoveTarget(null) }}
        title="移除 Git Worktree"
        centered
        size="sm"
        zIndex={420}
      >
        {removeTarget && (
          <Stack gap="sm">
            <Text size="xs">
              确定移除工作树 <Text span fw={700}>{removeTarget.branch || removeTarget.path}</Text>？
            </Text>
            <Text size="xs" c="dimmed" ff="monospace" style={{ wordBreak: 'break-all' }}>
              {removeTarget.path}
            </Text>
            {(sessionsByPath.get(normalizeCompare(removeTarget.path))?.length || 0) > 0 && (
              <Alert color="yellow" icon={<IconAlertTriangle size={15} />}>
                当前仍有 {sessionsByPath.get(normalizeCompare(removeTarget.path))?.length} 个星序会话指向该目录，移除后这些会话将无法读取工作区文件。
              </Alert>
            )}
            {removeTarget.dirty && (
              <Alert color="red" icon={<IconAlertTriangle size={15} />}>
                检测到 {removeTarget.changed_files} 处未提交修改！若确认放弃，请勾选下方的强制移除。
              </Alert>
            )}
            {removeTarget.branch && removeTarget.branch !== data?.default_branch && (
              <Checkbox
                size="xs"
                label={`同时删除本地分支 ${removeTarget.branch}`}
                checked={removeDeleteBranch}
                onChange={(e) => setRemoveDeleteBranch(e.currentTarget.checked)}
                disabled={busyAction === 'remove'}
              />
            )}
            {removeTarget.dirty && (
              <Checkbox
                size="xs"
                color="red"
                label="强制移除（将永久丢弃未提交更改）"
                checked={removeForce}
                onChange={(e) => setRemoveForce(e.currentTarget.checked)}
                disabled={busyAction === 'remove'}
              />
            )}
            <Group justify="flex-end" mt="xs">
              <Button variant="default" size="xs" onClick={() => setRemoveTarget(null)} disabled={busyAction === 'remove'}>
                取消
              </Button>
              <Button
                color="red"
                size="xs"
                loading={busyAction === 'remove'}
                disabled={removeTarget.dirty && !removeForce}
                onClick={() => void handleConfirmRemove()}
              >
                确认移除
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>

      {/* 安全合并回目标分支弹窗 */}
      <Modal
        opened={mergeTarget !== null}
        onClose={() => { if (!busyAction) setMergeTarget(null) }}
        title="安全合并 Worktree 分支"
        centered
        size="md"
        zIndex={420}
      >
        {mergeTarget && (
          <Stack gap="sm">
            <Text size="xs" c="dimmed">
              将 <Text span fw={700} c="var(--astr-text)">{mergeTarget.branch}</Text> 合并到目标分支。若发生冲突，星序会自动执行 <Text span ff="monospace">git merge --abort</Text> 恢复目标工作区现场。
            </Text>
            <Select
              size="xs"
              label="目标分支"
              value={mergeDestBranch || null}
              onChange={(v) => setMergeDestBranch(v || '')}
              data={(data?.branches || [])
                .filter((b) => b.name !== mergeTarget.branch)
                .map((b) => ({ value: b.name, label: b.name === data?.default_branch ? `${b.name}（默认）` : b.name }))}
              disabled={busyAction === 'merge'}
            />
            <Stack gap={4}>
              <Text size="xs" fw={500}>合并策略</Text>
              <SegmentedControl
                size="xs"
                fullWidth
                disabled={busyAction === 'merge'}
                value={mergeStrategy}
                onChange={(v) => setMergeStrategy(v as 'merge' | 'ff_only' | 'squash')}
                data={[
                  { value: 'merge', label: '标准合并 (--no-ff)' },
                  { value: 'ff_only', label: '快进 (--ff-only)' },
                  { value: 'squash', label: '压缩 (--squash)' },
                ]}
              />
            </Stack>
            <Checkbox
              size="xs"
              label="合并成功后自动移除该 Worktree 目录与本地分支"
              checked={mergeCleanupAfter}
              onChange={(e) => setMergeCleanupAfter(e.currentTarget.checked)}
              disabled={busyAction === 'merge'}
            />

            {mergeConflict && (
              <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={mergeConflict.aborted ? '检测到合并冲突（已自动回滚目标工作区）' : '合并未完成'}>
                <Stack gap={6}>
                  <Text size="xs">{mergeConflict.detail}</Text>
                  {mergeConflict.conflict_files && mergeConflict.conflict_files.length > 0 && (
                    <Stack gap={2}>
                      {mergeConflict.conflict_files.map((f) => (
                        <Text key={f} size="xs" ff="monospace">• {f}</Text>
                      ))}
                    </Stack>
                  )}
                  {session && (
                    <Button
                      size="compact-xs"
                      variant="light"
                      color="violet"
                      leftSection={<IconCheck size={12} />}
                      onClick={() => sendConflictToComposer(mergeConflict)}
                    >
                      将冲突说明填入当前会话让 Agent 协助解决
                    </Button>
                  )}
                </Stack>
              </Alert>
            )}

            <Group justify="flex-end" mt="xs">
              <Button variant="default" size="xs" onClick={() => setMergeTarget(null)} disabled={busyAction === 'merge'}>
                取消
              </Button>
              <Button
                color="teal"
                size="xs"
                leftSection={<IconGitMerge size={14} />}
                loading={busyAction === 'merge'}
                disabled={!mergeDestBranch}
                onClick={() => void handleConfirmMerge()}
              >
                执行合并
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  )
}
