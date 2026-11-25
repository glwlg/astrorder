import { useEffect, useState } from 'react'
import { Badge, Button, Checkbox, Group, Modal, SegmentedControl, Select, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, GitWorktreesResponse, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { useAstrorderStore } from '../state/store'
import { computeWorktreePath } from './WorktreeConfigSection'

export function ForkWorktreeDialog({
  session,
  agent: explicitAgent,
  onClose,
  onCreated,
}: {
  session: Session
  agent?: Agent
  onClose: () => void
  onCreated: (newSession: Session) => void
}) {
  const storeAgent = useAstrorderStore((s) => s.agents[session.agent_id])
  const agent = explicitAgent || storeAgent
  const useContextBranch = Boolean(agent?.kind && agent.kind !== 'codex')
  const initialBranch = 'branch-' + new Date().toISOString().slice(0, 10).replace(/-/g, '') + '-' + Math.random().toString(36).slice(2, 6)
  const [branchName, setBranchName] = useState(initialBranch)
  const [layout, setLayout] = useState<'sibling' | 'inside'>('inside')
  const [baseRef, setBaseRef] = useState('')
  const [copyFiles, setCopyFiles] = useState<string[]>([])
  const [repoInfo, setRepoInfo] = useState<GitWorktreesResponse | null>(null)
  const initialPath = session.workspace ? computeWorktreePath(session.workspace, initialBranch, 'inside') : ''
  const [worktreePath, setWorktreePath] = useState(initialPath)
  const [pathCustomized, setPathCustomized] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!session.workspace) return
    const controller = new AbortController()
    api.listGitWorktrees(
      { workspace: session.workspace, session_id: session.id, connection_id: session.connection_id || undefined, include_statuses: false },
      controller.signal,
    ).then((res) => {
      if (controller.signal.aborted || !res.is_git_repo) return
      setRepoInfo(res)
      if (res.current_branch || res.default_branch) {
        setBaseRef((prev) => prev || res.current_branch || res.default_branch || '')
      }
      if (res.copyable_files?.length) {
        setCopyFiles(res.copyable_files.map((f) => f.path))
      }
    }).catch(() => {
      // Optional enrichment; dialog remains usable without probe.
    })
    return () => controller.abort()
  }, [session.workspace, session.id, session.connection_id])

  const effectiveRoot = repoInfo?.main_workspace || session.workspace || ''
  const bases = { sibling: repoInfo?.default_sibling_base, inside: repoInfo?.default_inside_base }

  const handleBranchChange = (value: string) => {
    setBranchName(value)
    if (!pathCustomized && effectiveRoot) {
      setWorktreePath(computeWorktreePath(effectiveRoot, value.trim() || 'branch', layout, bases))
    }
  }

  const handleLayoutChange = (nextLayout: 'sibling' | 'inside') => {
    setLayout(nextLayout)
    if (!pathCustomized && effectiveRoot) {
      setWorktreePath(computeWorktreePath(effectiveRoot, branchName.trim() || 'branch', nextLayout, bases))
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
      const created = useContextBranch
        ? await api.branchSessionWithContext(session.id, {
            agent_id: session.agent_id,
            create_worktree: true,
            worktree_branch: branchName.trim(),
            worktree_path: worktreePath.trim() || undefined,
            worktree_base_ref: baseRef.trim() || undefined,
            worktree_layout: layout,
            worktree_copy_files: copyFiles.length > 0 ? copyFiles : undefined,
          })
        : await api.forkSession(session.id, {
            agent_id: session.agent_id,
            worktree: true,
            branch_name: branchName.trim(),
            worktree_path: worktreePath.trim() || undefined,
            ...(baseRef.trim() ? { worktree_base_ref: baseRef.trim() } : {}),
            ...(layout !== 'inside' ? { worktree_layout: layout } : {}),
            ...(copyFiles.length > 0 ? { worktree_copy_files: copyFiles } : {}),
          })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))
      notifications.show({
        color: 'teal',
        message: useContextBranch ? '已在新工作树中创建交接会话' : '已在新工作树中创建聊天分支',
      })
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
      title={useContextBranch ? '在新工作树中新建交接会话' : '在新工作树中创建聊天分支'}
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
        {useContextBranch && (
          <Text size="xs" c="dimmed">
            将在独立 Git Worktree 中创建新会话，并携带当前会话的本地上下文摘要，不影响原会话与主目录。
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
        {repoInfo?.branches && repoInfo.branches.length > 0 && (
          <Select
            size="xs"
            label="基于分支（基线）"
            placeholder="默认当前 HEAD"
            value={baseRef || null}
            onChange={(v) => setBaseRef(v || '')}
            data={repoInfo.branches.map((b) => ({
              value: b.name,
              label: b.is_current ? `${b.name}（当前）` : b.name,
            }))}
            searchable
            clearable
            disabled={busy}
          />
        )}
        <Group justify="space-between" align="center">
          <Text size="xs" fw={500}>目录策略</Text>
          <SegmentedControl
            size="xs"
            disabled={busy}
            value={layout}
            onChange={(v) => handleLayoutChange(v as 'sibling' | 'inside')}
            data={[
              { value: 'inside', label: '仓内 .worktrees/' },
              { value: 'sibling', label: '同级 -worktrees/' },
            ]}
          />
        </Group>
        <TextInput
          label="工作树目录"
          description="隔离存放该分支独立代码的目录路径"
          value={worktreePath}
          onChange={(e) => handlePathChange(e.currentTarget.value)}
          placeholder="自动生成或手动指定目录"
          disabled={busy}
        />
        {repoInfo?.copyable_files && repoInfo.copyable_files.length > 0 && (
          <Stack gap={4}>
            <Text size="xs" fw={500}>同步本地忽略配置</Text>
            {repoInfo.copyable_files.map((file) => (
              <Checkbox
                key={file.path}
                size="xs"
                disabled={busy}
                checked={copyFiles.includes(file.path)}
                onChange={(e) => {
                  const checked = e.currentTarget.checked
                  setCopyFiles((prev) => checked ? [...new Set([...prev, file.path])] : prev.filter((p) => p !== file.path))
                }}
                label={
                  <Group gap={6}>
                    <Text size="xs" ff="monospace">{file.path}</Text>
                    {file.source === 'worktreeinclude' && <Badge size="xs" variant="dot" color="violet">.worktreeinclude</Badge>}
                  </Group>
                }
              />
            ))}
          </Stack>
        )}
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
