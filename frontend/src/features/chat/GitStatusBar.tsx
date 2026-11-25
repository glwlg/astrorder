import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Badge,
  Button,
  Group,
  Menu,
  Modal,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { CaretUpIcon, CheckIcon, GitBranchIcon, PlusIcon } from '../../components/workbenchIcons'
import type { Session } from '../../domain/types'
import { useGitStatus } from '../../hooks/useGitStatus'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { WORKFLOW_GIT_WORKTREE_PLUGIN_ID } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { NewSessionDialog } from '../../components/NewSessionDialog'
import { useAstrorderStore } from '../../state/store'

function WorktreeNewSessionLauncher({
  session,
  onClose,
}: {
  session: Session
  onClose: () => void
}) {
  const navigate = useNavigate()
  const agents = useAstrorderStore((s) => s.agents)
  const rootWorkspace = session.worktree_root_workspace || session.workspace || ''
  const baseName = rootWorkspace.replace(/\\/g, '/').split('/').filter(Boolean).pop() || rootWorkspace
  const project = rootWorkspace ? {
    key: `workspace:${session.connection_id || 'local'}\u0000${rootWorkspace}`,
    label: session.project_name || baseName,
    sessions: [session],
    sessionCount: 1,
    latestUpdatedAt: session.updated_at,
    agentId: session.agent_id,
    workspace: rootWorkspace,
  } : null
  return (
    <NewSessionDialog
      agents={agents}
      project={project}
      initialAgentId={session.agent_id}
      initialWorkspace={rootWorkspace}
      initialWorktreeMode="create"
      onClose={onClose}
      onCreated={(created) => {
        navigate(`/chat/${encodeURIComponent(created.id)}?agent_id=${encodeURIComponent(created.agent_id)}`)
      }}
    />
  )
}

interface GitStatusBarProps {
  session: Session
}

export function GitStatusBar({ session }: GitStatusBarProps) {
  const [createModalOpened, setCreateModalOpened] = useState(false)
  const [createWorktreeSessionOpened, setCreateWorktreeSessionOpened] = useState(false)
  const [newBranchName, setNewBranchName] = useState('')
  const [switching, setSwitching] = useState(false)
  const [menuOpened, setMenuOpened] = useState(false)
  const git = useGitStatus(session)
  const statusUnavailable = !session.workspace || git.isError
  const statusLoading = !statusUnavailable && git.isPending
  const branch = statusUnavailable ? null : git.data?.branch || null
  const branches = Array.isArray(git.data?.branches) ? [...new Set(git.data.branches)] : []
  const insertions = git.data?.insertions || 0
  const deletions = git.data?.deletions || 0
  const changedFiles = git.data?.changed_files || 0
  const openGitDiff = useSidecarStore((s) => s.openGitDiff)
  const openWorktrees = useSidecarStore((s) => s.openWorktrees)
  const worktreeEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKFLOW_GIT_WORKTREE_PLUGIN_ID))
  const isLinkedWorktree = Boolean(
    session.worktree_root_workspace
    && session.workspace
    && session.worktree_root_workspace.replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
      !== session.workspace.replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase(),
  )

  const handleSwitchBranch = async (targetBranch: string) => {
    if (targetBranch === branch || switching) return
    setSwitching(true)
    try {
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (localToken) {
        headers['Authorization'] = `Bearer ${localToken}`
      }
      const resp = await fetch('/api/v1/git/branch', {
        method: 'POST',
        headers,
        body: JSON.stringify({
          branch: targetBranch,
          create: false,
          workspace: session.workspace,
          session_id: session.id,
          connection_id: session.connection_id,
        }),
      })
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}))
        notifications.show({
          color: 'red',
          message: data?.detail || '切换分支失败',
        })
      } else {
        await git.refetch()
      }
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '网络请求失败',
      })
    } finally {
      setSwitching(false)
    }
  }

  const handleCreateBranch = async () => {
    const cleanName = newBranchName.trim()
    if (!cleanName || switching) return
    setSwitching(true)
    try {
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (localToken) {
        headers['Authorization'] = `Bearer ${localToken}`
      }
      const resp = await fetch('/api/v1/git/branch', {
        method: 'POST',
        headers,
        body: JSON.stringify({
          branch: cleanName,
          create: true,
          workspace: session.workspace,
          session_id: session.id,
          connection_id: session.connection_id,
        }),
      })
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}))
        notifications.show({
          color: 'red',
          message: data?.detail || '创建新分支失败',
        })
      } else {
        setCreateModalOpened(false)
        setNewBranchName('')
        await git.refetch()
      }
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '网络请求失败',
      })
    } finally {
      setSwitching(false)
    }
  }

  const handleOpenDiffTree = () => {
    if (!session.workspace || statusUnavailable) return
    openGitDiff(session.id, session.agent_id, session.workspace, session.connection_id || undefined)
  }

  return (
    <>
      <div
        className="composer-git-status-bar"
      >
        {/* 左侧：分支选择器（点击切换/新建分支） */}
        <Menu opened={menuOpened} onChange={setMenuOpened} shadow="md" width={280} position="top-start" offset={8}
          middlewares={{
            flip: false,
            shift: { padding: 12 },
            size: {
              padding: 12,
              apply: ({ availableHeight, elements }) => {
                elements.floating.style.setProperty('--branch-menu-available-height', `${availableHeight}px`)
              },
            },
          }}>
          <Menu.Target>
            <UnstyledButton
              className="composer-git-branch"
              styles={{
                root: {
                  '&:hover': {
                    background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.05))',
                  },
                },
              }}
              title="点击切换或创建分支"
              aria-label="切换或创建 Git 分支"
              disabled={statusUnavailable || !branch || switching}
            >
              <GitBranchIcon size={20} aria-hidden="true" />
              {isLinkedWorktree && (
                <Badge size="xs" variant="light" color="cyan" px={4} style={{ textTransform: 'none', flexShrink: 0 }}>
                  WT
                </Badge>
              )}
              <Text size="xs" fw={600}>
                {branch || (statusLoading ? '读取分支…' : '分支未知')}
              </Text>
              <CaretUpIcon className="composer-git-chevron" size={16} aria-hidden="true" />
            </UnstyledButton>
          </Menu.Target>

          <Menu.Dropdown className="composer-branch-menu">
            <Menu.Label>本地分支列表</Menu.Label>
            {branches.map((b) => (
              <Menu.Item
                key={b}
                leftSection={
                  b === branch ? (
                    <CheckIcon size={16} color="var(--astr-green)" />
                  ) : (
                    <span style={{ width: 16 }} />
                  )
                }
                onClick={() => void handleSwitchBranch(b)}
              >
                <Text size="xs" fw={b === branch ? 600 : 400}>
                  {b}
                </Text>
              </Menu.Item>
            ))}
            <Menu.Divider />
            <Menu.Item
              leftSection={<PlusIcon size={16} color="var(--astr-blue)" />}
              onClick={() => setCreateModalOpened(true)}
            >
              <Text size="xs" c="blue">
                基于当前创建新分支...
              </Text>
            </Menu.Item>
            {worktreeEnabled && session.workspace && (
              <>
                <Menu.Divider />
                <Menu.Label>Git Worktree 辅助</Menu.Label>
                <Menu.Item
                  leftSection={<PlusIcon size={16} color="#0ea5e9" />}
                  onClick={() => setCreateWorktreeSessionOpened(true)}
                >
                  <Text size="xs">
                    基于当前新建 Worktree 会话...
                  </Text>
                </Menu.Item>
                <Menu.Item
                  leftSection={<GitBranchIcon size={16} color="#0ea5e9" />}
                  onClick={() => openWorktrees(
                    session.id,
                    session.agent_id,
                    session.worktree_root_workspace || session.workspace || undefined,
                    session.connection_id || undefined,
                  )}
                >
                  <Text size="xs">
                    管理 Git Worktree 工作区...
                  </Text>
                </Menu.Item>
              </>
            )}
          </Menu.Dropdown>
        </Menu>

        {/* 右侧：代码变更数统计（点击在侧边栏展开 git diff 文件浏览器） */}
        <Tooltip label="点击在侧边栏查看所有修改的文件列表与 Diff">
          <UnstyledButton
            onClick={handleOpenDiffTree}
            aria-label="查看工作区 Git Diff"
            className="composer-git-changes"
            styles={{
              root: {
                '&:hover': {
                  background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.05))',
                },
              },
            }}
          >
            {statusUnavailable ? (
              <span className="git-changed-files">工作区状态不可用</span>
            ) : statusLoading ? (
              <span className="git-changed-files">读取工作区…</span>
            ) : changedFiles > 0 ? (
              <>
                <span className="git-changed-files">
                  {changedFiles} 个文件变更
                </span>
                {(insertions > 0 || deletions > 0) && <>
                  <span
                    className="git-insertions"
                  >
                    +{insertions}
                  </span>
                  <span
                    className="git-deletions"
                  >
                    -{deletions}
                  </span>
                </>}
              </>
            ) : (
              <span className="git-changed-files">工作区无修改</span>
            )}
          </UnstyledButton>
        </Tooltip>
      </div>

      {/* 创建新分支 Modal 弹窗框 */}
      <Modal
        opened={createModalOpened}
        onClose={() => setCreateModalOpened(false)}
        title="创建并切换到新分支"
        size="sm"
        centered
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
          <TextInput
            label="新分支名称"
            placeholder="例如 feature/git-status-bar"
            value={newBranchName}
            onChange={(e) => setNewBranchName(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                void handleCreateBranch()
              }
            }}
            autoFocus
          />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" size="xs" onClick={() => setCreateModalOpened(false)}>
              取消
            </Button>
            <Button
              size="xs"
              color="blue"
              loading={switching}
              disabled={!newBranchName.trim()}
              onClick={() => void handleCreateBranch()}
            >
              创建并切换
            </Button>
          </Group>
        </div>
      </Modal>
      {createWorktreeSessionOpened && (
        <WorktreeNewSessionLauncher
          session={session}
          onClose={() => setCreateWorktreeSessionOpened(false)}
        />
      )}
    </>
  )
}
