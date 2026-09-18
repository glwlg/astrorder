import { useState } from 'react'
import { Button, Group, Modal, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { useAstrorderStore } from '../state/store'

function computeDefaultWorktreePath(workspace: string, branchName: string): string {
  const normalized = workspace.trim().replace(/\\/g, '/')
  const lastSlash = normalized.lastIndexOf('/')
  const baseName = lastSlash >= 0 ? normalized.slice(lastSlash + 1) : normalized
  const parent = lastSlash >= 0 ? normalized.slice(0, lastSlash) : ''
  const prefix = parent ? `${parent}/` : ''
  return `${prefix}${baseName}-worktrees/${branchName}`
}

export function ForkWorktreeDialog({
  session,
  onClose,
  onCreated,
}: {
  session: Session
  onClose: () => void
  onCreated: (newSession: Session) => void
}) {
  const initialBranch = 'branch-' + new Date().toISOString().slice(0, 10).replace(/-/g, '') + '-' + Math.random().toString(36).slice(2, 6)
  const [branchName, setBranchName] = useState(initialBranch)
  const initialPath = session.workspace ? computeDefaultWorktreePath(session.workspace, initialBranch) : ''
  const [worktreePath, setWorktreePath] = useState(initialPath)
  const [pathCustomized, setPathCustomized] = useState(false)
  const [busy, setBusy] = useState(false)

  const handleBranchChange = (value: string) => {
    setBranchName(value)
    if (!pathCustomized && session.workspace) {
      setWorktreePath(computeDefaultWorktreePath(session.workspace, value.trim() || 'branch'))
    }
  }

  const handlePathChange = (value: string) => {
    setPathCustomized(true)
    setWorktreePath(value)
  }

  const submit = async () => {
    if (busy || !branchName.trim()) return
    setBusy(true)
    try {
      const created = await api.forkSession(session.id, {
        agent_id: session.agent_id,
        worktree: true,
        branch_name: branchName.trim(),
        worktree_path: worktreePath.trim() || undefined,
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))
      notifications.show({ color: 'teal', message: '已在新工作树中创建聊天分支' })
      onCreated(created)
      onClose()
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '创建工作树分支失败，请重试',
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      opened
      onClose={() => { if (!busy) onClose() }}
      title="在新工作树中创建聊天分支"
      centered
      size="md"
      zIndex={400}
    >
      <Stack gap="sm">
        {session.workspace && (
          <Text size="xs" c="dimmed">
            源工作区：{session.workspace}
          </Text>
        )}
        <TextInput
          label="分支名称"
          description="将在仓库中新建的 Git 分支"
          value={branchName}
          onChange={(e) => handleBranchChange(e.currentTarget.value)}
          placeholder="如 feature/my-branch"
          disabled={busy}
          required
        />
        <TextInput
          label="工作树目录"
          description="隔离存放该分支独立代码的目录路径"
          value={worktreePath}
          onChange={(e) => handlePathChange(e.currentTarget.value)}
          placeholder="自动生成或手动指定目录"
          disabled={busy}
        />
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button onClick={() => void submit()} loading={busy} disabled={!branchName.trim()}>
            创建分支
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
