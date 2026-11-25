import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Badge,
  Checkbox,
  Group,
  Loader,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { IconCheck, IconFolder, IconGitBranch, IconSparkles } from '@tabler/icons-react'
import { api } from '../api/client'
import type { GitWorktreesResponse, Session, SessionWorktreePayload } from '../domain/types'
import { sessionActivityStatus } from './sessionRailModel'

export interface WorktreeSelectionState {
  mode: 'none' | 'create' | 'existing'
  branch: string
  baseRef: string
  createBranch: boolean
  path: string
  pathCustomized: boolean
  layout: 'sibling' | 'inside'
  copyFiles: string[]
  existingPath: string
}

export function computeWorktreePath(
  workspace: string,
  branchName: string,
  layout: 'sibling' | 'inside' = 'inside',
  bases?: { sibling?: string; inside?: string },
): string {
  const leaf = (branchName.trim() || 'branch')
    .replace(/\\/g, '/')
    .replace(/[:*?"<>|]+/g, '-')
    .replace(/^\/+|\/+$/g, '') || 'branch'

  if (layout === 'inside' && bases?.inside) {
    const cleanBase = bases.inside.replace(/[\\/]+$/, '').replace(/\\/g, '/')
    return `${cleanBase}/${leaf}`
  }
  if (layout === 'sibling' && bases?.sibling) {
    const cleanBase = bases.sibling.replace(/[\\/]+$/, '').replace(/\\/g, '/')
    return `${cleanBase}/${leaf}`
  }
  const normalized = workspace.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  if (!normalized) return ''
  if (layout === 'inside') {
    return `${normalized}/.worktrees/${leaf}`
  }
  const lastSlash = normalized.lastIndexOf('/')
  const baseName = lastSlash >= 0 ? normalized.slice(lastSlash + 1) : normalized
  const parent = lastSlash >= 0 ? normalized.slice(0, lastSlash) : ''
  const prefix = parent ? `${parent}/` : ''
  return `${prefix}${baseName}-worktrees/${leaf}`
}

export function defaultGeneratedBranchName(prefix = 'feat'): string {
  const date = new Date().toISOString().slice(0, 10).replace(/-/g, '')
  const rand = Math.random().toString(36).slice(2, 6)
  return `${prefix}/wt-${date}-${rand}`
}

export function slugifyBranchFromTitle(title: string): string {
  const ascii = title
    .toLowerCase()
    .replace(/[^a-z0-9\s/_-]+/g, ' ')
    .trim()
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^[-/]+|[-/]+$/g, '')
    .slice(0, 40)
  return ascii
}

export function createInitialWorktreeState(
  workspace: string,
  initialMode: 'none' | 'create' | 'existing' = 'none',
  initialBranch?: string,
  initialExistingPath?: string,
): WorktreeSelectionState {
  const branch = initialBranch || defaultGeneratedBranchName()
  return {
    mode: initialMode,
    branch,
    baseRef: '',
    createBranch: true,
    path: workspace ? computeWorktreePath(workspace, branch, 'inside') : '',
    pathCustomized: false,
    layout: 'inside',
    copyFiles: [],
    existingPath: initialExistingPath || '',
  }
}

export function buildWorktreePayload(state: WorktreeSelectionState): SessionWorktreePayload | undefined {
  if (state.mode === 'none') return undefined
  if (state.mode === 'existing') {
    if (!state.existingPath.trim()) return undefined
    return {
      mode: 'existing',
      path: state.existingPath.trim(),
    }
  }
  return {
    mode: 'create',
    branch: state.branch.trim() || undefined,
    base_ref: state.createBranch && state.baseRef.trim() ? state.baseRef.trim() : undefined,
    create_branch: state.createBranch,
    path: state.path.trim() || undefined,
    layout: state.layout,
    copy_files: state.copyFiles,
  }
}

interface StoredWorktreePrefs {
  layout?: 'sibling' | 'inside'
  copyFiles?: string[]
}

function prefsKey(mainWorkspace: string): string {
  return `astrorder:wt-prefs-v2:${mainWorkspace.replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()}`
}

function loadWorktreePrefs(mainWorkspace: string): StoredWorktreePrefs {
  try {
    const raw = localStorage.getItem(prefsKey(mainWorkspace))
    if (!raw) return {}
    return JSON.parse(raw) as StoredWorktreePrefs
  } catch {
    return {}
  }
}

export function saveWorktreePrefs(mainWorkspace: string | undefined, state: WorktreeSelectionState): void {
  if (!mainWorkspace) return
  try {
    localStorage.setItem(prefsKey(mainWorkspace), JSON.stringify({
      layout: state.layout,
      copyFiles: state.copyFiles,
    } satisfies StoredWorktreePrefs))
  } catch {
    // Ignore storage quota errors.
  }
}

function normalizeCompare(value: string | null | undefined): string {
  if (!value) return ''
  const s = value.trim().replace(/\\/g, '/').replace(/\/+$/, '')
  return /^[a-zA-Z]:\//.test(s) ? s.toLowerCase() : s
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

const BRANCH_PREFIXES = ['feat/', 'fix/', 'refactor/', 'exp/'] as const

export function WorktreeConfigSection({
  workspace,
  connectionId,
  sessionId,
  value,
  onChange,
  allowNone = true,
  allowExisting = true,
  suggestedTitle,
  disabled = false,
  sessions = [],
  onRepoLoaded,
}: {
  workspace: string
  connectionId?: string | null
  sessionId?: string | null
  value: WorktreeSelectionState
  onChange: (next: WorktreeSelectionState) => void
  allowNone?: boolean
  allowExisting?: boolean
  suggestedTitle?: string
  disabled?: boolean
  sessions?: Session[]
  onRepoLoaded?: (info: GitWorktreesResponse | null) => void
}) {
  const [info, setInfo] = useState<GitWorktreesResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [probeError, setProbeError] = useState<string | null>(null)
  const hydratedRepoRef = useRef<string>('')
  const latestValueRef = useRef(value)
  latestValueRef.current = value

  useEffect(() => {
    const trimmed = workspace.trim()
    if (!trimmed) {
      setInfo(null)
      setProbeError(null)
      onRepoLoaded?.(null)
      return
    }
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setLoading(true)
      setProbeError(null)
      api.listGitWorktrees(
        {
          workspace: trimmed,
          connection_id: connectionId || undefined,
          session_id: sessionId || undefined,
          include_statuses: true,
        },
        controller.signal,
      ).then((res) => {
        if (controller.signal.aborted) return
        setInfo(res)
        onRepoLoaded?.(res)
        if (res.is_git_repo && res.main_workspace) {
          const repoKey = `${connectionId || 'local'}::${normalizeCompare(res.main_workspace)}`
          if (hydratedRepoRef.current !== repoKey) {
            hydratedRepoRef.current = repoKey
            const prefs = loadWorktreePrefs(res.main_workspace)
            const preferredLayout: 'sibling' | 'inside' =
              prefs.layout === 'inside' || prefs.layout === 'sibling'
                ? prefs.layout
                : res.repo_config?.default_layout === 'sibling'
                  ? 'sibling'
                  : 'inside'
            const availableCopyPaths = new Set((res.copyable_files || []).map((f) => f.path))
            const defaultCopyFiles = prefs.copyFiles
              ? prefs.copyFiles.filter((p) => availableCopyPaths.has(p))
              : (res.copyable_files || []).map((f) => f.path)
            const cur = latestValueRef.current
            const nextBase = cur.baseRef || res.current_branch || res.default_branch || ''
            const nextPath = cur.pathCustomized
              ? cur.path
              : computeWorktreePath(res.main_workspace, cur.branch, preferredLayout, {
                  sibling: res.default_sibling_base,
                  inside: res.default_inside_base,
                })
            onChange({
              ...cur,
              layout: preferredLayout,
              baseRef: nextBase,
              path: nextPath,
              copyFiles: defaultCopyFiles,
            })
          }
        }
      }).catch((err) => {
        if (controller.signal.aborted) return
        setInfo(null)
        onRepoLoaded?.(null)
        setProbeError(err instanceof Error ? err.message : '无法探测 Git 仓库')
      }).finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    }, 180)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspace, connectionId, sessionId])

  const linkedWorktrees = useMemo(
    () => (info?.worktrees || []).filter((w) => !w.is_main && !w.bare && !w.prunable),
    [info?.worktrees],
  )

  const sessionStatsByPath = useMemo(() => {
    const map = new Map<string, { total: number; running: number }>()
    for (const s of sessions) {
      if ((s.connection_id || '') !== (connectionId || '')) continue
      const key = normalizeCompare(s.workspace)
      if (!key) continue
      const prev = map.get(key) || { total: 0, running: 0 }
      prev.total += 1
      if (sessionActivityStatus(s) === 'running') prev.running += 1
      map.set(key, prev)
    }
    return map
  }, [sessions, connectionId])

  const bases = useMemo(
    () => ({
      sibling: info?.default_sibling_base,
      inside: info?.default_inside_base,
    }),
    [info?.default_inside_base, info?.default_sibling_base],
  )

  const effectiveRoot = info?.main_workspace || workspace

  const updateBranch = (branch: string, createBranch = value.createBranch) => {
    const nextPath = value.pathCustomized
      ? value.path
      : computeWorktreePath(effectiveRoot, branch, value.layout, bases)
    onChange({ ...value, branch, createBranch, path: nextPath })
  }

  const updateLayout = (layout: 'sibling' | 'inside') => {
    const nextPath = value.pathCustomized
      ? value.path
      : computeWorktreePath(effectiveRoot, value.branch, layout, bases)
    onChange({ ...value, layout, path: nextPath })
  }

  const applyPrefix = (prefix: string) => {
    const stripped = value.branch.replace(/^(feat|fix|refactor|exp|chore)\//, '')
    updateBranch(`${prefix}${stripped || 'task'}`)
  }

  const applyTitleSlug = () => {
    if (!suggestedTitle) return
    const slug = slugifyBranchFromTitle(suggestedTitle)
    if (!slug) return
    const currentPrefix = value.branch.match(/^(feat|fix|refactor|exp|chore)\//)?.[0] || 'feat/'
    updateBranch(`${currentPrefix}${slug}`)
  }

  const toggleCopyFile = (filePath: string, checked: boolean) => {
    const next = checked
      ? [...new Set([...value.copyFiles, filePath])]
      : value.copyFiles.filter((p) => p !== filePath)
    onChange({ ...value, copyFiles: next })
  }

  if (loading && !info) {
    return (
      <Group gap="xs" py={2}>
        <Loader size={12} />
        <Text size="xs" c="dimmed">正在检测 Git Worktree 支持…</Text>
      </Group>
    )
  }

  if (allowNone && (!info || !info.is_git_repo)) {
    return null
  }

  if (!allowNone && probeError && !info) {
    return <Text size="xs" c="red">{probeError}</Text>
  }

  if (!allowNone && info && !info.is_git_repo) {
    return <Text size="xs" c="orange">当前工作区不是 Git 仓库，无法创建独立 Worktree。</Text>
  }

  const branchOptions = (info?.branches || []).map((b) => ({
    value: b.name,
    label: b.occupied_by_path ? `${b.name}（已被 Worktree 占用）` : b.is_current ? `${b.name}（当前）` : b.name,
    disabled: !value.createBranch && Boolean(b.occupied_by_path),
  }))

  const occupiedWarning = value.branch.trim()
    ? info?.branches?.find((b) => b.name === value.branch.trim() && b.occupied_by_path)
    : undefined

  const modeOptions = [
    ...(allowNone ? [{ value: 'none', label: '当前工作区' }] : []),
    { value: 'create', label: '新建 Worktree' },
    ...(allowExisting && linkedWorktrees.length > 0
      ? [{ value: 'existing', label: `复用已有 (${linkedWorktrees.length})` }]
      : []),
  ]

  return (
    <Paper withBorder radius="md" p="sm" style={{ background: 'var(--astr-surface-subtle, rgba(0, 0, 0, 0.02))' }}>
      <Stack gap="xs">
        <Group justify="space-between" align="center" gap="xs">
          <Group gap={6}>
            <IconGitBranch size={15} color="var(--astr-blue, #3b82f6)" />
            <Text size="xs" fw={600}>Git Worktree 工作区</Text>
          </Group>
          {info?.current_branch && (
            <Badge size="xs" variant="light" color="gray">
              当前分支：{info.current_branch}
            </Badge>
          )}
        </Group>

        {modeOptions.length > 1 && (
          <SegmentedControl
            size="xs"
            fullWidth
            disabled={disabled}
            value={value.mode}
            onChange={(next) => {
              const mode = next as 'none' | 'create' | 'existing'
              if (mode === 'existing' && !value.existingPath && linkedWorktrees.length > 0) {
                onChange({ ...value, mode, existingPath: linkedWorktrees[0].path })
              } else {
                onChange({ ...value, mode })
              }
            }}
            data={modeOptions}
          />
        )}

        {value.mode === 'create' && (
          <Stack gap="xs" pt={2}>
            <Group justify="space-between" align="center" gap="xs">
              <SegmentedControl
                size="xs"
                disabled={disabled}
                value={value.createBranch ? 'new' : 'existing-branch'}
                onChange={(v) => {
                  const createBranch = v === 'new'
                  if (!createBranch) {
                    const firstFree = info?.branches?.find((b) => !b.occupied_by_path)?.name || ''
                    updateBranch(firstFree, false)
                  } else {
                    const nextBranch = value.branch && !info?.branches?.some((b) => b.name === value.branch)
                      ? value.branch
                      : defaultGeneratedBranchName()
                    updateBranch(nextBranch, true)
                  }
                }}
                data={[
                  { value: 'new', label: '新建分支' },
                  { value: 'existing-branch', label: '检出已有分支' },
                ]}
              />
              {value.createBranch && suggestedTitle && slugifyBranchFromTitle(suggestedTitle) && (
                <Tooltip label="根据会话标题生成分支名">
                  <UnstyledButton
                    onClick={applyTitleSlug}
                    disabled={disabled}
                    style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 11, color: 'var(--astr-blue, #3b82f6)' }}
                  >
                    <IconSparkles size={12} />
                    <span>按标题命名</span>
                  </UnstyledButton>
                </Tooltip>
              )}
            </Group>

            {value.createBranch ? (
              <>
                <TextInput
                  size="xs"
                  label="新分支名称"
                  placeholder="例如 feat/login-flow"
                  value={value.branch}
                  onChange={(e) => updateBranch(e.currentTarget.value, true)}
                  disabled={disabled}
                  error={occupiedWarning ? `分支已被 ${occupiedWarning.occupied_by_path} 占用` : undefined}
                  required
                />
                <Group gap={4}>
                  {BRANCH_PREFIXES.map((prefix) => (
                    <Badge
                      key={prefix}
                      size="xs"
                      variant={value.branch.startsWith(prefix) ? 'filled' : 'light'}
                      color="blue"
                      style={{ cursor: disabled ? 'default' : 'pointer', textTransform: 'none' }}
                      onClick={() => { if (!disabled) applyPrefix(prefix) }}
                    >
                      {prefix}
                    </Badge>
                  ))}
                </Group>
                {branchOptions.length > 0 && (
                  <Select
                    size="xs"
                    label="基于分支（基线）"
                    placeholder="默认当前 HEAD"
                    value={value.baseRef || null}
                    onChange={(v) => onChange({ ...value, baseRef: v || '' })}
                    data={info!.branches.map((b) => ({
                      value: b.name,
                      label: b.name === info?.default_branch ? `${b.name}（默认分支）` : b.is_current ? `${b.name}（当前）` : b.name,
                    }))}
                    searchable
                    clearable
                    disabled={disabled}
                  />
                )}
              </>
            ) : (
              <Select
                size="xs"
                label="选择要检出的本地分支"
                placeholder="选择未被占用的本地分支"
                value={value.branch || null}
                onChange={(v) => updateBranch(v || '', false)}
                data={branchOptions}
                searchable
                disabled={disabled}
                nothingFoundMessage="没有可用的未占用本地分支"
              />
            )}

            <Group justify="space-between" align="center" gap="xs">
              <Text size="xs" fw={500}>目录策略</Text>
              <SegmentedControl
                size="xs"
                disabled={disabled}
                value={value.layout}
                onChange={(v) => updateLayout(v as 'sibling' | 'inside')}
                data={[
                  { value: 'inside', label: '仓内 .worktrees/' },
                  { value: 'sibling', label: '同级 -worktrees/' },
                ]}
              />
            </Group>

            <TextInput
              size="xs"
              label="Worktree 目录"
              description={value.layout === 'inside'
                ? '将自动写入本地 .git/info/exclude，不会污染 git status'
                : '独立存放在主仓库同级目录，与主工作区代码完全隔离'}
              value={value.path}
              onChange={(e) => onChange({ ...value, path: e.currentTarget.value, pathCustomized: true })}
              placeholder="自动生成或手动指定路径"
              disabled={disabled}
            />

            {(info?.copyable_files?.length || 0) > 0 && (
              <Stack gap={4} pt={2}>
                <Group justify="space-between" align="center">
                  <Text size="xs" fw={500}>同步本地忽略配置（环境初始化辅助）</Text>
                  <Text size="xs" c="dimmed">已选 {value.copyFiles.length}/{info!.copyable_files.length}</Text>
                </Group>
                <Stack gap={4}>
                  {info!.copyable_files.map((file) => (
                    <Checkbox
                      key={file.path}
                      size="xs"
                      disabled={disabled}
                      checked={value.copyFiles.includes(file.path)}
                      onChange={(e) => toggleCopyFile(file.path, e.currentTarget.checked)}
                      label={
                        <Group gap={6}>
                          <Text size="xs" ff="monospace">{file.path}</Text>
                          <Text size="xs" c="dimmed">{formatFileSize(file.size)}</Text>
                          {file.source === 'worktreeinclude' && (
                            <Badge size="xs" variant="dot" color="violet">.worktreeinclude</Badge>
                          )}
                        </Group>
                      }
                    />
                  ))}
                </Stack>
              </Stack>
            )}
          </Stack>
        )}

        {value.mode === 'existing' && (
          <Stack gap={6} pt={2}>
            {linkedWorktrees.map((wt) => {
              const selected = normalizeCompare(wt.path) === normalizeCompare(value.existingPath)
              const stats = sessionStatsByPath.get(normalizeCompare(wt.path))
              return (
                <UnstyledButton
                  key={wt.path}
                  disabled={disabled}
                  onClick={() => onChange({ ...value, existingPath: wt.path })}
                  style={{
                    border: `1px solid ${selected ? 'var(--astr-blue, #3b82f6)' : 'var(--astr-border, rgba(0,0,0,0.1))'}`,
                    background: selected ? 'rgba(59, 130, 246, 0.08)' : 'transparent',
                    borderRadius: 8,
                    padding: '8px 10px',
                    width: '100%',
                    textAlign: 'left',
                  }}
                >
                  <Group justify="space-between" wrap="nowrap" gap="xs">
                    <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                      <IconFolder size={14} color={selected ? 'var(--astr-blue, #3b82f6)' : 'gray'} style={{ flexShrink: 0 }} />
                      <Text size="xs" fw={600} truncate>{wt.branch || '(detached)'}</Text>
                    </Group>
                    <Group gap={4} wrap="nowrap">
                      {wt.status_loaded && (
                        <Badge size="xs" variant="light" color={wt.dirty ? 'orange' : 'teal'}>
                          {wt.dirty ? `${wt.changed_files} 处修改` : '干净'}
                        </Badge>
                      )}
                      {stats && stats.total > 0 && (
                        <Badge size="xs" variant="light" color={stats.running > 0 ? 'blue' : 'gray'}>
                          {stats.running > 0 ? `${stats.running} 运行中` : `${stats.total} 会话`}
                        </Badge>
                      )}
                      {selected && <IconCheck size={14} color="var(--astr-blue, #3b82f6)" />}
                    </Group>
                  </Group>
                  <Text size="xs" c="dimmed" truncate mt={2} ff="monospace">
                    {wt.path}
                  </Text>
                </UnstyledButton>
              )
            })}
          </Stack>
        )}
      </Stack>
    </Paper>
  )
}
