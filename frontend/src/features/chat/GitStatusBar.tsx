import { useEffect, useState } from 'react'
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
import {
  IconCheck,
  IconGitBranch,
  IconPlus,
} from '@tabler/icons-react'
import type { Session } from '../../domain/types'
import { useSidecarStore } from '../sidecar/sidecarStore'

interface GitStatusBarProps {
  session: Session
}

export function GitStatusBar({ session }: GitStatusBarProps) {
  const [branch, setBranch] = useState('master')
  const [branches, setBranches] = useState<string[]>(['master'])
  const [insertions, setInsertions] = useState(0)
  const [deletions, setDeletions] = useState(0)
  const [changedFiles, setChangedFiles] = useState(0)
  const [createModalOpened, setCreateModalOpened] = useState(false)
  const [newBranchName, setNewBranchName] = useState('')
  const [switching, setSwitching] = useState(false)

  const openGitDiff = useSidecarStore((s) => s.openGitDiff)

  const fetchStatus = async () => {
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
      if (session.workspace) params.set('workspace', session.workspace)
      if (session.id) params.set('session_id', session.id)
      if (session.connection_id) params.set('connection_id', session.connection_id)

      const resp = await fetch(`/api/v1/git/status?${params.toString()}`, { headers })
      if (resp.ok) {
        const data = await resp.json()
        setBranch(data.branch || 'master')
        setBranches(data.branches || ['master'])
        setInsertions(data.insertions || 0)
        setDeletions(data.deletions || 0)
        setChangedFiles(data.changed_files || 0)
      }
    } catch {
      // 静默降级
    }
  }

  useEffect(() => {
    void fetchStatus()
    const timer = setInterval(() => {
      void fetchStatus()
    }, 15000)
    return () => clearInterval(timer)
  }, [session.id, session.workspace, session.connection_id])

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
        setBranch(targetBranch)
        void fetchStatus()
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
        setBranch(cleanName)
        setCreateModalOpened(false)
        setNewBranchName('')
        void fetchStatus()
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
    openGitDiff(session.id, session.agent_id, session.workspace || undefined, session.connection_id || undefined)
  }

  return (
    <>
      <div
        className="composer-git-status-bar"
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '4px 10px 6px',
          borderBottom: '1px solid var(--astr-border)',
          marginBottom: '6px',
          fontSize: '12px',
        }}
      >
        {/* 左侧：分支选择器（点击切换/新建分支） */}
        <Menu shadow="md" width={220} position="bottom-start">
          <Menu.Target>
            <UnstyledButton
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '5px',
                color: 'var(--astr-text, #374151)',
                cursor: 'pointer',
                borderRadius: '4px',
                padding: '2px 6px',
                transition: 'background 0.1s ease',
              }}
              styles={{
                root: {
                  '&:hover': {
                    background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.05))',
                  },
                },
              }}
              title="点击切换或创建分支"
            >
              <IconGitBranch size={15} color="var(--astr-green, #10b981)" />
              <Text size="xs" fw={600} c="dimmed" style={{ color: 'var(--astr-text)' }}>
                {branch}
              </Text>
            </UnstyledButton>
          </Menu.Target>

          <Menu.Dropdown>
            <Menu.Label>本地分支列表</Menu.Label>
            {branches.map((b) => (
              <Menu.Item
                key={b}
                leftSection={
                  b === branch ? (
                    <IconCheck size={14} color="var(--astr-green, #10b981)" />
                  ) : (
                    <span style={{ width: 14 }} />
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
              leftSection={<IconPlus size={14} color="var(--astr-blue, #3b82f6)" />}
              onClick={() => setCreateModalOpened(true)}
            >
              <Text size="xs" c="blue">
                基于当前创建新分支...
              </Text>
            </Menu.Item>
          </Menu.Dropdown>
        </Menu>

        {/* 右侧：代码变更数统计（点击在侧边栏展开 git diff 文件浏览器） */}
        <Tooltip label="点击在侧边栏查看所有修改的文件列表与 Diff">
          <UnstyledButton
            onClick={handleOpenDiffTree}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: '6px',
              cursor: 'pointer',
              borderRadius: '4px',
              padding: '2px 6px',
              transition: 'background 0.1s ease',
            }}
            styles={{
              root: {
                '&:hover': {
                  background: 'var(--astr-card-hover, rgba(0, 0, 0, 0.05))',
                },
              },
            }}
          >
            {changedFiles > 0 ? (
              <>
                <span style={{ fontSize: '11px', color: 'var(--astr-muted)' }}>
                  {changedFiles} 个文件变更
                </span>
                <span
                  style={{
                    color: 'var(--astr-green, #10b981)',
                    fontWeight: 700,
                    fontFamily: 'monospace',
                    fontSize: '12px',
                  }}
                >
                  +{insertions}
                </span>
                <span
                  style={{
                    color: 'var(--astr-red, #ef4444)',
                    fontWeight: 700,
                    fontFamily: 'monospace',
                    fontSize: '12px',
                  }}
                >
                  -{deletions}
                </span>
              </>
            ) : (
              <span style={{ fontSize: '11px', color: 'var(--astr-muted)' }}>工作区无修改</span>
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
