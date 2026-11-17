import { useState } from 'react'
import {
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
import { ComposerGlassMenu, GlassButtonLens } from '../../components/GlassPanel'

interface GitStatusBarProps {
  session: Session
}

export function GitStatusBar({ session }: GitStatusBarProps) {
  const [createModalOpened, setCreateModalOpened] = useState(false)
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
              <GlassButtonLens />
              <GitBranchIcon size={20} aria-hidden="true" />
              <Text size="xs" fw={600}>
                {branch || (statusLoading ? '读取分支…' : '分支未知')}
              </Text>
              <CaretUpIcon className="composer-git-chevron" size={16} aria-hidden="true" />
            </UnstyledButton>
          </Menu.Target>

          <Menu.Dropdown className="composer-branch-menu">
            <ComposerGlassMenu active={menuOpened}>
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
            </ComposerGlassMenu>
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
    </>
  )
}
