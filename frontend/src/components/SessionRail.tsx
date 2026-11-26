import {
  IconBriefcase,
  IconChecklist,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconCode,
  IconCopy,
  IconDatabase,
  IconDotsVertical,
  IconEdit,
  IconFolderShare,
  IconPalette,
  IconPin,
  IconPinned,
  IconPlus,
  IconRefresh,
  IconRobot,
  IconSearch,
  IconTrash,
  IconTransfer,
  IconGitFork,
  IconDeviceDesktop,
  IconGitBranch,
} from '@tabler/icons-react'
import {
  ActionIcon,
  Button,
  Checkbox,
  Collapse,
  Group,
  Loader,
  Menu,
  Modal,
  Select,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { motion } from 'motion/react'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useSessionOrder } from '../hooks/useSessionOrder'
import { useSessionUnread } from '../hooks/useSessionUnread'
import { AgentSessionFilter, matchesAgent } from './AgentSessionFilter'
import { NewSessionDialog } from './NewSessionDialog'
import { HandoffDialog } from './HandoffDialog'
import { sessionHandoffTargets } from '../domain/handoff'
import { ForkWorktreeDialog } from './ForkWorktreeDialog'
import { ProjectDatabaseModal } from '../features/dbxExec/ProjectDatabaseModal'
import { buildWorkspaceScopeKey, useProjectDbStore } from '../features/dbxExec/projectDbStore'
import { useDbxDirectExecEnabled } from '../features/dbxExec/useInlineSqlExecutor'
import { WORKFLOW_GIT_WORKTREE_PLUGIN_ID } from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import {
  isProjectInOfficeMode,
  isSessionInOfficeMode,
  setProjectOfficeMode,
  useOfficeModeRegistry,
} from '../features/office/officeWorkspaceMode'
import { useIsOfficeProfile } from '../features/office/appProfile'
import {
  findBotForWorkspace,
  storePendingDistillIntent,
  storePendingHandoffIntent,
  useBotsOptional,
} from '../features/bots/useBots'
import { moveProject, reconcileProjectOrder } from './projectOrder'
import { useWorkspacePreferences } from '../hooks/useWorkspacePreferences'
import type { Agent, Project, Session, SessionStatus, Task } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { SessionRowMeta } from './SessionRowMeta'
import { ConfirmPopover } from './ConfirmPopover'
import { confirmationCoordinatesFromEvent, type ConfirmationCoordinates } from './confirmationPosition'
import { AgentKindBadge } from './SessionRuntimeFacts'
import { agentKindLabel } from './AgentBrandIcon'
import { buildProjectGroups, displaySessionTitle, type ProjectGroup, type RailFilter } from './sessionRailModel'
import { api } from '../api/client'
import './sessionPins.css'
const COLLAPSED_PROJECTS_STORAGE_KEY = 'astrorder:collapsed-projects'
import { useAstrorderStore } from '../state/store'
import { AddProjectModal } from './AddProjectModal'
import {
  ProjectAppearanceModal,
  ProjectGlyph,
  projectAppearanceColor,
  type ProjectAppearanceEntry,
} from './projectAppearance'
import { SessionActivityBorder } from './AnimatedStatus'
import { PlusIcon } from './workbenchIcons'
import { SessionQuickFilters } from './SessionQuickFilters'

type PendingConfirmation =
  | { kind: 'delete-session'; session: Session; coords: ConfirmationCoordinates }
  | { kind: 'delete-project'; project: ProjectGroup; coords: ConfirmationCoordinates }
  | { kind: 'batch-delete-sessions'; count: number; sessions: Session[]; coords?: ConfirmationCoordinates }

function sessionRunningStyle(appearance?: ProjectAppearanceEntry): CSSProperties {
  return {
    '--session-running-color': projectAppearanceColor(appearance) || 'var(--astr-text)',
  } as CSSProperties
}

export function SessionRail({
  sessions: incomingSessions,
  agents,
  projects,
  activeSessionKey,
  onSelect,
}: {
  sessions: Session[]
  agents: Record<string, Agent>
  projects?: Project[]
  activeSessionKey?: string
  onSelect: (session: Session) => void
}) {
  const [filter, setFilter] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const sessions = useSessionOrder(incomingSessions)
  const tasks = useAstrorderStore((state) => state.tasks)
  const liveActivityAt = useAstrorderStore((state) => state.liveActivityAt)
  const [statusFilter, setStatusFilter] = useState<RailFilter>('all')
  const [agentFilter, setAgentFilter] = useState(() => {
    try {
      return localStorage.getItem('astrorder:agent-filter') || localStorage.getItem('astrorder:desktop-agent-filter') || 'all'
    } catch {
      return 'all'
    }
  })

  const updateAgentFilter = (value: string) => {
    setAgentFilter(value)
    try {
      localStorage.setItem('astrorder:agent-filter', value)
      localStorage.setItem('astrorder:desktop-agent-filter', value)
    } catch {
      // ignore
    }
  }
  const [createProject, setCreateProject] = useState<ProjectGroup | null>(null)
  const [createOpened, setCreateOpened] = useState(false)
  const [addProjectOpened, setAddProjectOpened] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem(COLLAPSED_PROJECTS_STORAGE_KEY) || '{}')
    } catch {
      return {}
    }
  })

  const updateCollapsed = (updater: (prev: Record<string, boolean>) => Record<string, boolean>) => {
    setCollapsed((prev) => {
      const next = updater(prev)
      try {
        localStorage.setItem(COLLAPSED_PROJECTS_STORAGE_KEY, JSON.stringify(next))
      } catch {
        // ignore
      }
      return next
    })
  }

  const { preferences, updatePreferences, removeProjectPreferences } = useWorkspacePreferences()
  const { pinned_projects: pinnedProjects, appearance: projectAppearance, session_pins: pinnedSessions, project_order: projectOrder } = preferences
  const [appearanceTarget, setAppearanceTarget] = useState<ProjectGroup | null>(null)
  const [databaseTarget, setDatabaseTarget] = useState<ProjectGroup | null>(null)
  const dbxDirectExecEnabled = useDbxDirectExecEnabled()
  const projectDbBindings = useProjectDbStore((s) => s.bindings)
  const [railMenu, setRailMenu] = useState<{ kind: 'project' | 'session'; key: string } | null>(null)
  const openRailMenu = (event: React.MouseEvent, kind: 'project' | 'session', key: string) => {
    event.preventDefault()
    event.stopPropagation()
    setRailMenu({ kind, key })
  }
  const updateRailMenu = (opened: boolean, kind: 'project' | 'session', key: string) => {
    setRailMenu((current) => opened ? { kind, key } : current?.kind === kind && current.key === key ? null : current)
  }

  // 项目下会话默认只显示4个，展开更多状态（记录展开显示的数量）
  const [expandedCountMap, setExpandedCountMap] = useState<Record<string, number>>({})

  const handleExpandMore = (key: string, totalCount: number) => {
    setExpandedCountMap((prev) => {
      const current = prev[key] ?? 4
      return { ...prev, [key]: Math.min(current + 4, totalCount) }
    })
  }

  const handleCollapseSessions = (key: string) => {
    setExpandedCountMap((prev) => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  // 重命名弹窗状态
  const [renameTarget, setRenameTarget] = useState<Session | null>(null)
  const [renameTitle, setRenameTitle] = useState('')
  const [renameLoading, setRenameLoading] = useState(false)

  // 移动到其他项目弹窗状态（Antigravity 原生项目迁移）
  const [moveTarget, setMoveTarget] = useState<Session | null>(null)
  const [moveWorkspace, setMoveWorkspace] = useState('')
  const [moveUseCustomPath, setMoveUseCustomPath] = useState(false)
  const [moveLoading, setMoveLoading] = useState(false)
  const [confirmation, setConfirmation] = useState<PendingConfirmation | null>(null)
  const [confirmationLoading, setConfirmationLoading] = useState(false)
  const [handoffTarget, setHandoffTarget] = useState<Session | null>(null)
  const [forkWorktreeTarget, setForkWorktreeTarget] = useState<Session | null>(null)
  const [forkLoading, setForkLoading] = useState(false)

  const handleForkChatBranch = async (session: Session) => {
    if (forkLoading) return
    setForkLoading(true)
    try {
      const created = await api.forkSession(session.id, {
        agent_id: session.agent_id,
        worktree: false,
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))
      notifications.show({ color: 'teal', message: '已创建聊天分支' })
      onSelect(created)
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '创建聊天分支失败，请重试',
      })
    } finally {
      setForkLoading(false)
    }
  }

  const handleContextBranch = async (session: Session) => {
    if (forkLoading) return
    setForkLoading(true)
    try {
      const created = await api.branchSessionWithContext(session.id, {
        agent_id: session.agent_id,
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))
      notifications.show({ color: 'teal', message: '已基于当前会话摘要创建交接会话' })
      onSelect(created)
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '创建交接会话失败，请重试',
      })
    } finally {
      setForkLoading(false)
    }
  }

  const worktreeEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKFLOW_GIT_WORKTREE_PLUGIN_ID))
  const officeModeRegistry = useOfficeModeRegistry()
  const isOfficeProfile = useIsOfficeProfile()
  const registeredBots = useBotsOptional()
  const [hideBotProjects, setHideBotProjects] = useState<boolean>(() => {
    try {
      return localStorage.getItem('astrorder:rail-hide-bot-projects') === '1'
    } catch {
      return false
    }
  })
  const toggleHideBotProjects = () => {
    setHideBotProjects((prev) => {
      const next = !prev
      try {
        localStorage.setItem('astrorder:rail-hide-bot-projects', next ? '1' : '0')
      } catch {}
      return next
    })
  }

  const isSessionOffice = (session: Session) =>
    isOfficeProfile || isSessionInOfficeMode(officeModeRegistry, session)

  const forkMenuItems = (session: Session) => {
    const kind = agents[session.agent_id]?.kind
    const officeSess = isSessionOffice(session)
    if (kind === 'antigravity' || kind === 'antigravity-ssh') {
      return (
        <>
          <Menu.Item
            leftSection={<IconGitFork size={14} />}
            onClick={() => void handleContextBranch(session)}
          >
            {officeSess ? '基于摘要新建续写对话' : '基于摘要新建交接会话'}
          </Menu.Item>
          {!officeSess && worktreeEnabled && session.workspace && (
            <Menu.Item
              leftSection={<IconGitBranch size={14} />}
              onClick={() => setForkWorktreeTarget(session)}
            >
              在新工作树中新建交接会话
            </Menu.Item>
          )}
        </>
      )
    }
    if (kind === 'grok') return null
    if (officeSess) {
      return (
        <Menu.Item
          leftSection={<IconCopy size={14} />}
          onClick={() => void handleForkChatBranch(session)}
        >
          复制为新对话
        </Menu.Item>
      )
    }
    if (!worktreeEnabled) {
      return (
        <Menu.Item
          leftSection={<IconGitFork size={14} />}
          onClick={() => void handleForkChatBranch(session)}
        >
          分叉（创建聊天分支）
        </Menu.Item>
      )
    }
    return (
      <Menu.Sub>
        <Menu.Sub.Target>
          <Menu.Sub.Item leftSection={<IconGitFork size={14} />}>
            分叉
          </Menu.Sub.Item>
        </Menu.Sub.Target>
        <Menu.Sub.Dropdown onClick={(e) => e.stopPropagation()}>
          <Menu.Item
            leftSection={<IconDeviceDesktop size={14} />}
            onClick={() => void handleForkChatBranch(session)}
          >
            创建聊天分支
          </Menu.Item>
          <Menu.Item
            leftSection={<IconGitBranch size={14} />}
            onClick={() => setForkWorktreeTarget(session)}
          >
            在新工作树中创建聊天分支
          </Menu.Item>
        </Menu.Sub.Dropdown>
      </Menu.Sub>
    )
  }
  const [batchMode, setBatchMode] = useState(false)
  const [selectedSessionKeys, setSelectedSessionKeys] = useState<Set<string>>(new Set())

  // 新建会话加载中
  const creatingForProject = createOpened ? createProject?.key : null

  const liveStatuses = useMemo(() => {
    const result = new Map<string, SessionStatus>()
    for (const task of Object.values(tasks) as Task[]) {
      if (task.kind !== 'subagent' || task.progress?.blocking === false) continue
      const key = scopeKey(task.agent_id, task.session_id)
      if (task.status === 'waiting_approval') result.set(key, 'waiting_approval')
      else if (!result.has(key) && (task.status === 'running' || task.status === 'pending')) result.set(key, 'running')
    }
    const now = Date.now()
    for (const [key, at] of Object.entries(liveActivityAt)) {
      if (!result.has(key) && now - at >= 0 && now - at < 15_000) result.set(key, 'running')
    }
    return result
  }, [tasks, liveActivityAt])

  const activityStatusFor = useCallback((session: Session): SessionStatus => (
    session.status === 'error'
      ? 'idle'
      : session.status === 'running' || session.status === 'waiting_approval'
      ? session.status
      : liveStatuses.get(scopeKey(session.agent_id, session.id)) || session.status
  ), [liveStatuses])

  const { isUnread, markRead: markSessionRead } = useSessionUnread(activeSessionKey)
  const decoratedSessions = useMemo(() => sessions.map(session => {
    const key = scopeKey(session.agent_id, session.id)
    const status = activityStatusFor(session)
    return {
      ...session,
      pinned: pinnedSessions[key] === true,
      unread: status !== 'running' && status !== 'waiting_approval' && isUnread(key),
    }
  }), [sessions, pinnedSessions, activityStatusFor, isUnread])

  const [draggedProject, setDraggedProject] = useState<string | null>(null)
  const allGroups = useMemo(() => buildProjectGroups(decoratedSessions, agents, projects), [decoratedSessions, agents, projects])
  const stableOrder = useMemo(() => reconcileProjectOrder(projectOrder, allGroups.map(project => project.key)), [projectOrder, allGroups])
  const reorderProject = (source: string, target: string) => {
    void updatePreferences(value => pinnedProjects.includes(source) && pinnedProjects.includes(target)
      ? { pinned_projects: moveProject(value.pinned_projects, source, target) }
      : { project_order: moveProject(reconcileProjectOrder(value.project_order, allGroups.map(project => project.key)), source, target) })
  }
  const groups = useMemo(() => {
    const visible = buildProjectGroups(decoratedSessions.filter(s => matchesAgent(s, agents, agentFilter)), agents, projects, filter.trim().toLocaleLowerCase(), statusFilter).filter(p => agentFilter === 'all' || p.sessions.length > 0)
    const byKey = new Map(visible.map(project => [project.key, project]))
    const ordered = stableOrder.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
    const pinnedSet = new Set(pinnedProjects)
    const pinned = pinnedProjects.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
    const unpinned = ordered.filter(p => !pinnedSet.has(p.key))
    return [...pinned, ...unpinned]
  }, [agents, decoratedSessions, filter, projects, statusFilter, stableOrder, agentFilter, pinnedProjects])

  const toggle = (key: string) => updateCollapsed((current) => ({ ...current, [key]: !current[key] }))

  const togglePinProject = (projectKey: string, event?: React.MouseEvent) => {
    event?.stopPropagation()
    void updatePreferences(value => ({ pinned_projects: value.pinned_projects.includes(projectKey)
      ? value.pinned_projects.filter(key => key !== projectKey) : [projectKey, ...value.pinned_projects] }))
  }

  const handleSaveAppearance = (projectKey: string, entry: ProjectAppearanceEntry) => {
    return updatePreferences({ appearance: { [projectKey]: entry } })
  }

  const togglePin = (session: Session, event: React.MouseEvent) => {
    event.stopPropagation()
    const key = scopeKey(session.agent_id, session.id)
    void updatePreferences(value => ({ session_pins: { [key]: !value.session_pins[key] } }))
  }

  const handleCreateSession = async (project: ProjectGroup, event: React.MouseEvent) => {
    event.stopPropagation()
    setCreateProject(project)
    setCreateOpened(true)
  }

  const openRenameModal = (session: Session) => {
    setRenameTarget(session)
    setRenameTitle(session.title || '')
  }

  const normalizeWorkspaceForCompare = (value?: string | null): string => {
    const raw = (value || '').trim()
    if (!raw) return ''
    const slashed = raw.replaceAll('\\', '/').replace(/\/+$/, '') || '/'
    const isWin = /^[a-zA-Z]:\//.test(slashed) || slashed.startsWith('//')
    return isWin ? slashed.toLowerCase() : slashed
  }

  const canMoveSessionProject = (session: Session): boolean => {
    const kind = agents[session.agent_id]?.kind
    return (
      kind === 'antigravity'
      || kind === 'antigravity-ssh'
      || kind === 'codex'
      || kind === 'codex-ssh'
      || kind === 'hermes'
      || kind === 'ssh'
      || kind === 'grok'
      || kind === 'grok-ssh'
    )
  }

  const moveCandidateProjects = useMemo(() => {
    if (!moveTarget) return [] as Array<{ value: string; label: string; workspace: string; projectKey?: string }>
    const targetConn = moveTarget.connection_id || agents[moveTarget.agent_id]?.connection_id || ''
    const currentNorm = normalizeWorkspaceForCompare(moveTarget.workspace)
    const seen = new Set<string>()
    const out: Array<{ value: string; label: string; workspace: string; projectKey?: string }> = []
    const addCandidate = (workspace: string | null | undefined, label: string | null | undefined, conn: string | null | undefined, projectKey?: string) => {
      const ws = (workspace || '').trim()
      if (!ws) return
      const normConn = conn === 'local' ? '' : (conn || '')
      const targetNormConn = targetConn === 'local' ? '' : targetConn
      if (normConn !== targetNormConn) return
      const normWs = normalizeWorkspaceForCompare(ws)
      if (!normWs || normWs === currentNorm || seen.has(normWs)) return
      seen.add(normWs)
      const displayName = (label || '').trim() || ws.replaceAll('\\', '/').split('/').filter(Boolean).pop() || ws
      out.push({
        value: ws,
        label: displayName === ws ? ws : `${displayName} (${ws})`,
        workspace: ws,
        projectKey,
      })
    }

    for (const p of projects ?? []) {
      if (p.project_id === '__no_project__') continue
      const conn = p.connection_id || (p.agent_id ? agents[p.agent_id]?.connection_id : undefined) || ''
      addCandidate(p.workspace, p.project_name, conn)
    }
    for (const g of groups) {
      const sample = g.sessions[0]
      const ownerAgent = g.agentId ? agents[g.agentId] : undefined
      const conn = sample?.connection_id || ownerAgent?.connection_id || ''
      addCandidate(g.workspace, g.label, conn, g.key)
    }
    for (const s of sessions) {
      const conn = s.connection_id || agents[s.agent_id]?.connection_id || ''
      addCandidate(s.worktree_root_workspace || s.workspace, s.project_name, conn)
    }
    return out.sort((a, b) => a.label.localeCompare(b.label, 'zh-CN', { sensitivity: 'base', numeric: true }))
  }, [agents, groups, moveTarget, projects, sessions])

  const openMoveProjectModal = (session: Session) => {
    setMoveTarget(session)
    setMoveWorkspace('')
    setMoveUseCustomPath(false)
  }

  const saveMoveProject = async () => {
    if (!moveTarget || !moveWorkspace.trim() || moveLoading) return
    const nextWS = moveWorkspace.trim()
    setMoveLoading(true)
    try {
      const updated = await api.updateSession(moveTarget.id, {
        agent_id: moveTarget.agent_id,
        workspace: nextWS,
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(updated.agent_id, updated.id)]: updated },
      }))
      const matchedCandidate = moveCandidateProjects.find(
        (item) => normalizeWorkspaceForCompare(item.workspace) === normalizeWorkspaceForCompare(updated.workspace || nextWS),
      )
      if (matchedCandidate?.projectKey) {
        updateCollapsed((current) => ({ ...current, [`project:${matchedCandidate.projectKey}`]: false }))
      }
      const targetName = updated.project_name || nextWS.replaceAll('\\', '/').split('/').filter(Boolean).pop() || nextWS
      notifications.show({ color: 'teal', message: `已将「${displaySessionTitle(updated)}」移动到 ${targetName}` })
      setMoveTarget(null)
    } catch (err) {
      const msg = err instanceof Error ? err.message : '移动会话项目失败'
      notifications.show({ color: 'red', message: msg })
    } finally {
      setMoveLoading(false)
    }
  }

  const moveMenuItem = (session: Session) => {
    if (!canMoveSessionProject(session)) return null
    const status = activityStatusFor(session)
    const busy = status === 'running' || status === 'waiting_approval'
    return (
      <Menu.Item
        leftSection={<IconFolderShare size={14} />}
        disabled={busy}
        title={busy ? '会话空闲后可移动项目' : undefined}
        onClick={() => openMoveProjectModal(session)}
      >
        移动到其他项目
      </Menu.Item>
    )
  }

  const distillToBotMenuItem = (session: Session) => (
    <Menu.Item
      leftSection={<IconRobot size={14} />}
      onClick={() => {
        storePendingDistillIntent({
          sessionKey: scopeKey(session.agent_id, session.id),
          sessionTitle: displaySessionTitle(session),
          workspace: session.workspace || undefined,
          connectionId: session.connection_id || undefined,
          agentId: session.agent_id,
        })
        window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/bots' } }))
      }}
    >
      沉淀为 Bot
    </Menu.Item>
  )

  const handoffToBotMenuItem = (session: Session) => {
    if (registeredBots.length === 0) return null
    return (
      <Menu.Item
        leftSection={<IconTransfer size={14} />}
        onClick={() => {
          const matched = findBotForWorkspace(
            registeredBots,
            session.workspace,
            session.connection_id || agents[session.agent_id]?.connection_id,
          )
          storePendingHandoffIntent({
            callerSessionKey: scopeKey(session.agent_id, session.id),
            callerTitle: displaySessionTitle(session),
            callerWorkspace: session.workspace || undefined,
            callerConnectionId: session.connection_id || agents[session.agent_id]?.connection_id || undefined,
            sourceBotId: matched?.id,
          })
          window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/bots' } }))
        }}
      >
        接力派给 Bot
      </Menu.Item>
    )
  }

  const saveRename = async () => {
    if (!renameTarget || !renameTitle.trim() || renameLoading) return
    setRenameLoading(true)
    try {
      const updated = await api.updateSession(renameTarget.id, {
        agent_id: renameTarget.agent_id,
        title: renameTitle.trim(),
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(updated.agent_id, updated.id)]: updated },
      }))
      setRenameTarget(null)
    } catch (err) {
      console.error('重命名失败', err)
    } finally {
      setRenameLoading(false)
    }
  }

  const [deleteRemoveWorktree, setDeleteRemoveWorktree] = useState(false)
  const [deleteWorktreeBranch, setDeleteWorktreeBranch] = useState(true)
  const [deleteForceWorktree, setDeleteForceWorktree] = useState(false)
  const [deleteWorktreeStatus, setDeleteWorktreeStatus] = useState<{ loading: boolean; dirty: boolean; changedFiles: number; otherSessions: number } | null>(null)

  const isLinkedWorktreeSession = (session: Session): boolean =>
    Boolean(
      session.worktree_root_workspace
      && session.workspace
      && session.worktree_root_workspace.replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
        !== session.workspace.replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase(),
    )

  const requestDeleteSession = (session: Session, event?: { clientX: number; clientY: number }) => {
    setDeleteRemoveWorktree(false)
    setDeleteWorktreeBranch(true)
    setDeleteForceWorktree(false)
    if (isLinkedWorktreeSession(session)) {
      const norm = (v?: string | null) => (v || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
      const otherSessions = sessions.filter(
        (s) => !(s.agent_id === session.agent_id && s.id === session.id)
          && (s.connection_id || '') === (session.connection_id || '')
          && norm(s.workspace) === norm(session.workspace),
      ).length
      setDeleteWorktreeStatus({ loading: otherSessions === 0, dirty: false, changedFiles: 0, otherSessions })
    } else {
      setDeleteWorktreeStatus(null)
    }
    setConfirmation({ kind: 'delete-session', session, coords: confirmationCoordinatesFromEvent(event) })
  }

  useEffect(() => {
    if (confirmation?.kind !== 'delete-session') return
    const target = confirmation.session
    if (!isLinkedWorktreeSession(target)) return
    if (!deleteWorktreeStatus || !deleteWorktreeStatus.loading) return
    const controller = new AbortController()
    api.listGitWorktrees(
      {
        workspace: target.worktree_root_workspace || target.workspace || undefined,
        connection_id: target.connection_id || undefined,
        include_statuses: true,
      },
      controller.signal,
    ).then((res) => {
      if (controller.signal.aborted) return
      const norm = (v?: string | null) => (v || '').replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
      const matched = res.worktrees?.find((w) => norm(w.path) === norm(target.workspace))
      setDeleteWorktreeStatus((prev) => prev ? {
        ...prev,
        loading: false,
        dirty: Boolean(matched?.dirty),
        changedFiles: matched?.changed_files || 0,
      } : null)
    }).catch(() => {
      if (!controller.signal.aborted) {
        setDeleteWorktreeStatus((prev) => prev ? { ...prev, loading: false } : null)
      }
    })
    return () => controller.abort()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [confirmation])

  const deleteSession = async (session: Session) => {
    try {
      const wantRemoveWT = isLinkedWorktreeSession(session) && deleteRemoveWorktree && (deleteWorktreeStatus?.otherSessions ?? 0) === 0
      const res = await api.deleteSession(
        session.id,
        session.agent_id,
        wantRemoveWT
          ? {
              remove_worktree: true,
              delete_worktree_branch: deleteWorktreeBranch,
              force_remove_worktree: deleteForceWorktree,
            }
          : undefined,
      )
      useAstrorderStore.setState((state) => {
        const nextSessions = { ...state.sessions }
        delete nextSessions[scopeKey(session.agent_id, session.id)]
        return { sessions: nextSessions }
      })
      if (wantRemoveWT) {
        if (res.worktree_removed) {
          notifications.show({
            color: 'teal',
            message: res.worktree_branch_deleted
              ? '已删除会话并清理对应 Worktree 与本地分支'
              : res.worktree_error
                ? `已清理 Worktree 目录（${res.worktree_error}）`
                : '已删除会话并清理对应 Worktree 目录',
          })
        } else if (res.worktree_error) {
          notifications.show({ color: 'yellow', message: `会话已删除，但 Worktree 未移除：${res.worktree_error}` })
        }
      }
    } catch (err) {
      console.error('删除会话失败', err)
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '删除会话失败，请重试' })
    }
  }

  const handoffTargets = (session: Session) => sessionHandoffTargets(session, agents)

  const handoffItems = (session: Session) => {
    if (isSessionOffice(session)) return null
    const source = agents[session.agent_id]
    if (!source || !['codex', 'hermes'].includes(source.kind)) return null
    const available = handoffTargets(session).length > 0
    return <>
      <Menu.Divider />
      <Menu.Item
        leftSection={<IconTransfer size={14} />}
        disabled={session.status !== 'idle' || !available}
        title={!available ? '同一环境暂无可用的目标 Agent' : session.status !== 'idle' ? '会话空闲后可转交' : undefined}
        onClick={() => setHandoffTarget(session)}
      >
        转交
      </Menu.Item>
    </>
  }
  const requestDeleteProject = (project: ProjectGroup, event?: { clientX: number; clientY: number }) => {
    const sessionCount = project.sessionCount || project.sessions.length
    if (project.key.startsWith('unmarked:') && sessionCount === 0) return
    setConfirmation({ kind: 'delete-project', project, coords: confirmationCoordinatesFromEvent(event) })
  }

  const deleteProject = async (project: ProjectGroup) => {
    try {
      let projectId: string | undefined
      let sourceId: string | undefined
      if (project.key.startsWith('project:')) {
        const parts = project.key.replace('project:', '').split('\u0000', 2)
        sourceId = parts[0]
        projectId = parts[1]
      }

      const sessionKeys = project.sessions.map((s) => ({ agent_id: s.agent_id, id: s.id }))
      const res = await api.deleteProject({
        project_key: project.key,
        project_id: projectId,
        source_id: sourceId,
        workspace: project.workspace,
        session_keys: sessionKeys,
        delete_sessions: true,
      })

      useAstrorderStore.setState((state) => {
        const nextSessions = { ...state.sessions }
        const nextProjects = { ...state.projects }

        const deletedSet = new Set(
          (res.deleted_sessions || sessionKeys).map((s) => scopeKey(s.agent_id, s.id)),
        )
        for (const sKey of deletedSet) {
          delete nextSessions[sKey]
        }

        for (const [pKey, p] of Object.entries(nextProjects)) {
          if (
            pKey === project.key ||
            p.id === project.key ||
            (projectId && p.project_id === projectId) ||
            (project.workspace && p.workspace === project.workspace)
          ) {
            delete nextProjects[pKey]
          }
        }

        return { sessions: nextSessions, projects: nextProjects }
      })

      await removeProjectPreferences(project.key)
      if (dbxDirectExecEnabled) {
        void useProjectDbStore.getState().deleteBinding(project.key).catch(() => {})
      }

      if (activeSessionKey && project.sessions.some((s) => scopeKey(s.agent_id, s.id) === activeSessionKey)) {
        const remainingSessions = incomingSessions.filter(
          (s) => !project.sessions.some((ps) => ps.id === s.id && ps.agent_id === s.agent_id),
        )
        if (remainingSessions.length > 0) {
          onSelect(remainingSessions[0])
        }
      }
    } catch (err) {
      console.error('删除项目失败', err)
      notifications.show({ color: 'red', message: '删除项目失败，请重试' })
    }
  }

  const confirmationTitle = confirmation?.kind === 'delete-project'
    ? (confirmation.project.key.startsWith('unmarked:') ? '清空未标记会话？' : '删除项目？')
    : confirmation?.kind === 'batch-delete-sessions'
      ? '批量删除会话？'
      : '删除会话？'
  const confirmationMessage = confirmation?.kind === 'delete-project'
    ? (() => {
        const sessionCount = confirmation.project.sessionCount || confirmation.project.sessions.length
        return confirmation.project.key.startsWith('unmarked:')
          ? `确定删除未标记项目中的全部会话吗？\n此操作将删除其中的 ${sessionCount} 个会话。`
              : (sessionCount > 0
                  ? `确定删除项目“${confirmation.project.label}”吗？\n此操作将同时删除该项目及其包含的 ${sessionCount} 个会话。`
                  : `确定删除项目“${confirmation.project.label}”吗？`)
          })()
    : confirmation?.kind === 'batch-delete-sessions'
      ? `确定删除选中的 ${confirmation.count} 个会话吗？\n此操作将永久移除这些会话且无法撤销。`
      : confirmation?.kind === 'delete-session'
        ? `确定删除会话“${confirmation.session.title || confirmation.session.id}”吗？`
        : ''

  const confirmDeletion = async () => {
    if (!confirmation || confirmationLoading) return
    setConfirmationLoading(true)
    try {
      if (confirmation.kind === 'delete-project') await deleteProject(confirmation.project)
      else if (confirmation.kind === 'batch-delete-sessions') await executeBatchDelete(confirmation.sessions)
      else await deleteSession(confirmation.session)
    } finally {
      setConfirmationLoading(false)
      setConfirmation(null)
    }
  }

  const allFilteredSessions = useMemo(() => {
    return groups.flatMap((p) => p.sessions)
  }, [groups])

  const [lastSelectedKey, setLastSelectedKey] = useState<string | null>(null)

  const toggleSelectSession = (key: string, event?: React.MouseEvent) => {
    const isShift = Boolean(event && event.shiftKey)
    if (isShift && lastSelectedKey) {
      const prevIdx = allFilteredSessions.findIndex(
        (s) => scopeKey(s.agent_id, s.id) === lastSelectedKey,
      )
      const curIdx = allFilteredSessions.findIndex(
        (s) => scopeKey(s.agent_id, s.id) === key,
      )
      if (prevIdx !== -1 && curIdx !== -1) {
        const start = Math.min(prevIdx, curIdx)
        const end = Math.max(prevIdx, curIdx)
        const range = allFilteredSessions.slice(start, end + 1)
        setSelectedSessionKeys((prev) => {
          const next = new Set(prev)
          for (const s of range) {
            next.add(scopeKey(s.agent_id, s.id))
          }
          return next
        })
        setLastSelectedKey(key)
        return
      }
    }

    setSelectedSessionKeys((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
    setLastSelectedKey(key)
  }

  const isAllSelected =
    allFilteredSessions.length > 0 &&
    allFilteredSessions.every((s) => selectedSessionKeys.has(scopeKey(s.agent_id, s.id)))

  const toggleSelectAll = () => {
    if (isAllSelected) {
      setSelectedSessionKeys(new Set())
    } else {
      setSelectedSessionKeys(new Set(allFilteredSessions.map((s) => scopeKey(s.agent_id, s.id))))
    }
  }

  const requestBatchDelete = (event?: { clientX: number; clientY: number }) => {
    const selected = allFilteredSessions.filter((s) =>
      selectedSessionKeys.has(scopeKey(s.agent_id, s.id)),
    )
    if (!selected.length) return
    setConfirmation({
      kind: 'batch-delete-sessions',
      count: selected.length,
      sessions: selected,
      coords: confirmationCoordinatesFromEvent(event),
    })
  }

  const executeBatchDelete = async (targetSessions: Session[]) => {
    try {
      const res = await api.batchDeleteSessions(
        targetSessions.map((s) => ({ agent_id: s.agent_id, id: s.id })),
      )
      const deletedSet = new Set(res.deleted.map((d) => scopeKey(d.agent_id, d.id)))
      useAstrorderStore.setState((state) => {
        const nextSessions = { ...state.sessions }
        for (const key of deletedSet) {
          delete nextSessions[key]
        }
        return { sessions: nextSessions }
      })
      if (activeSessionKey && deletedSet.has(activeSessionKey)) {
        const remaining = sessions.filter((s) => !deletedSet.has(scopeKey(s.agent_id, s.id)))
        if (remaining.length > 0) {
          onSelect(remaining[0])
        }
      }
      notifications.show({
        color: 'teal',
        message: '已成功删除 ' + res.deleted.length + ' 个会话',
      })
      setBatchMode(false)
      setSelectedSessionKeys(new Set())
    } catch (err) {
      console.error('批量删除会话失败', err)
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '批量删除会话失败，请重试',
      })
    }
  }

  const copySessionId = (session: Session) => {
    void navigator.clipboard.writeText(session.id)
  }

  // 手动刷新：一次批量读取各 Agent 的原生最后活动时间，结果经 session.upsert 事件回到列表。
  const [refreshingActivity, setRefreshingActivity] = useState(false)
  const refreshActivity = async () => {
    if (refreshingActivity) return
    setRefreshingActivity(true)
    try {
      const result = await api.refreshSessionActivity()
      const failed = result.agents.filter(agent => agent.error)
      if (failed.length > 0) {
        notifications.show({
          color: 'orange',
          message: `已更新 ${result.updated} 个会话；${failed.map(agent => `${agent.agent_id}: ${agent.error}`).join('；')}`,
        })
      } else {
        notifications.show({ color: 'teal', message: result.updated > 0 ? `已更新 ${result.updated} 个会话的最后活动时间` : '最后活动时间已是最新' })
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '刷新最后活动时间失败，请重试' })
    } finally {
      setRefreshingActivity(false)
    }
  }

  return (
    <Stack className="session-rail" gap="sm">
      <div className="session-rail-controls">
      <div className="session-search-row">
        <div className="session-search-field">
          <IconSearch size={21} stroke={1.8} aria-hidden="true" />
          <input ref={searchRef} className="session-search" aria-label={isOfficeProfile ? '搜索对话' : '搜索会话'} placeholder={isOfficeProfile ? '搜索对话' : '搜索会话'} value={filter} onChange={event => setFilter(event.currentTarget.value)} />
          <kbd
            role="button"
            tabIndex={0}
            title={isOfficeProfile ? '打开快捷搜索面板 (Ctrl+K)' : '打开全能星令面板 (Ctrl+K)'}
            style={{ cursor: 'pointer' }}
            onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-command-palette'))}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                window.dispatchEvent(new CustomEvent('astrorder:open-command-palette'))
              }
            }}
          >
            Ctrl K
          </kbd>
        </div>
        <Tooltip label={isOfficeProfile ? '新建办公空间' : '新增项目'} position="bottom">
          <ActionIcon className="session-add-project" variant="subtle" size={30} aria-label={isOfficeProfile ? '新建办公空间' : '新增项目'} onClick={() => setAddProjectOpened(true)}>
            <PlusIcon size={18} aria-hidden="true" />
          </ActionIcon>
        </Tooltip>
      </div>
      {(!isOfficeProfile || Object.values(agents).length > 1) && (
        <AgentSessionFilter agents={agents} sessions={sessions} value={agentFilter} onChange={updateAgentFilter} showLabel />
      )}
      <SessionQuickFilters value={statusFilter} onChange={setStatusFilter}>
      <Menu position="bottom-end" width={210} withinPortal>
        <Menu.Target>
          <button type="button" className="session-rail-more" aria-label="会话列表操作">
            <IconDotsVertical size={16} aria-hidden="true" />
          </button>
        </Menu.Target>
        <Menu.Dropdown>
        <Menu.Item leftSection={<IconRefresh size={17} />} disabled={refreshingActivity} onClick={() => void refreshActivity()}>
          {refreshingActivity ? '正在刷新活动时间…' : '刷新最后活动时间'}
        </Menu.Item>
        <Menu.Item leftSection={<IconChecklist size={17} />} onClick={() => {
          setBatchMode(prev => !prev)
          setSelectedSessionKeys(new Set())
        }}>{batchMode ? '退出批量管理' : '批量管理会话'}</Menu.Item>
        <Menu.Item leftSection={<IconPlus size={17} />} onClick={() => setAddProjectOpened(true)}>新增项目</Menu.Item>
        </Menu.Dropdown>
      </Menu>
      </SessionQuickFilters>
      {addProjectOpened && (
        <AddProjectModal
          opened={addProjectOpened}
          onClose={() => setAddProjectOpened(false)}
          agents={agents}
        />
      )}
      {createOpened && (
        <NewSessionDialog
          agents={agents}
          project={createProject}
          initialAgentId={agentFilter}
          officeMode={
            isOfficeProfile ||
            Boolean(createProject && isProjectInOfficeMode(officeModeRegistry, createProject.key, createProject.workspace))
          }
          onClose={() => setCreateOpened(false)}
          onCreated={session => {
            onSelect(session)
            if (createProject) updateCollapsed(previous => ({ ...previous, [`project:${createProject.key}`]: false }))
          }}
        />
      )}
      {batchMode && (
        <div className="session-batch-toolbar" style={{ padding: '6px 8px', background: 'var(--astr-subtle, rgba(0,0,0,0.04))', border: '1px solid var(--mantine-color-default-border)', borderRadius: 8, marginTop: 4 }}>
          <Group justify="space-between" align="center" gap="xs" wrap="nowrap">
            <Group gap={6} wrap="nowrap">
              <Checkbox
                size="xs"
                checked={isAllSelected && allFilteredSessions.length > 0}
                indeterminate={selectedSessionKeys.size > 0 && !isAllSelected}
                onChange={toggleSelectAll}
                label={`全选 (${selectedSessionKeys.size}/${allFilteredSessions.length})`}
              />
            </Group>
            <Group gap={6} wrap="nowrap">
              <Button
                size="compact-xs"
                color="red"
                variant="light"
                leftSection={<IconTrash size={13} />}
                disabled={selectedSessionKeys.size === 0}
                onClick={(e) => requestBatchDelete(e)}
              >
                {`删除 (${selectedSessionKeys.size})`}
              </Button>
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                onClick={() => {
                  setBatchMode(false)
                  setSelectedSessionKeys(new Set())
                }}
              >
                退出
              </Button>
            </Group>
          </Group>
        </div>
      )}
      </div>
      <div className="session-rail-list">
      {groups.length === 0 ? (
        <Text className="rail-empty" size="sm" c="dimmed">
          {filter || statusFilter !== 'all' ? '没有符合条件的项目或会话' : '尚无会话'}
        </Text>
      ) : (
        <>
        {groups.some(project => project.sessions.some(s => pinnedSessions[scopeKey(s.agent_id, s.id)])) && <section className="session-pinned-section" aria-label="置顶会话">
          <div className="session-pinned-heading">置顶</div>
          <>
          {groups.flatMap(project => project.sessions).filter(s => pinnedSessions[scopeKey(s.agent_id, s.id)]).map(session => {
            const key = scopeKey(session.agent_id, session.id)
            const activityStatus = activityStatusFor(session)
            const isRunning = activityStatus === 'running'
            const parentProject = groups.find(p => p.sessions.some(s => s.id === session.id && s.agent_id === session.agent_id))
            const projectCustom = (parentProject && projectAppearance[parentProject.key]) ||
              (session.project_id ? projectAppearance[`project:${session.project_id}`] : undefined)
            return (
              <motion.div
                initial={false}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.95 }}
                transition={{ type: 'spring', stiffness: 450, damping: 35 }}
                className={`session-row-wrapper ${isRunning ? 'is-running' : ''}`}
                key={key}
                style={sessionRunningStyle(projectCustom)}
                onContextMenu={(event) => { if (!batchMode) openRailMenu(event, 'session', key) }}
              >
                {batchMode && (
                  <Checkbox
                    className="session-batch-checkbox"
                    size="xs"
                    checked={selectedSessionKeys.has(key)}
                    onChange={(e) => toggleSelectSession(key, e.nativeEvent as unknown as React.MouseEvent)}
                    onClick={(e) => e.stopPropagation()}
                  />
                )}
                <SessionActivityBorder status={activityStatus} />
                <UnstyledButton
                  className={`session-row is-pinned ${key === activeSessionKey ? 'is-active' : ''} ${isRunning ? 'is-running' : ''} ${(session as any).unread ? 'is-unread' : ''}`}
                  onClick={(e) => { markSessionRead(key); if (batchMode) toggleSelectSession(key, e); else onSelect(session) }}
                  draggable={!batchMode}
                  onDragStart={(event) => {
                    if (batchMode) return
                    event.dataTransfer.setData('application/x-astrorder-session', JSON.stringify({
                      agent_id: session.agent_id,
                      id: session.id,
                      title: session.title || '未命名会话',
                      key,
                    }))
                    event.dataTransfer.setData('text/plain', key)
                    event.dataTransfer.effectAllowed = 'copy'
                  }}
                >
                  <span className="session-row-glyph"><AgentKindBadge agent={agents[session.agent_id]} iconOnly /></span>
                  <span className="session-row-title" title={session.worktree_branch && isLinkedWorktreeSession(session) ? `${displaySessionTitle(session)} · Worktree: ${session.worktree_branch} (${session.workspace})` : displaySessionTitle(session)}>
                    {projectCustom?.icon && (
                      <ProjectGlyph
                        iconName={projectCustom.icon}
                        colorName={projectCustom.color}
                        size={13}
                        style={{ marginRight: 5, verticalAlign: 'text-bottom', opacity: 0.85 }}
                      />
                    )}
                    {displaySessionTitle(session)}
                    {session.worktree_branch && isLinkedWorktreeSession(session) && (
                      <span
                        title={`Git Worktree 分支：${session.worktree_branch}\n路径：${session.workspace}`}
                        style={{
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: 2,
                          marginLeft: 6,
                          padding: '0 5px',
                          borderRadius: 4,
                          fontSize: 10,
                          lineHeight: '16px',
                          background: 'rgba(14, 165, 233, 0.12)',
                          color: '#0284c7',
                          verticalAlign: 'middle',
                          maxWidth: 110,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        <IconGitBranch size={10} style={{ flexShrink: 0 }} />
                        {session.worktree_branch}
                      </span>
                    )}
                  </span>
                  <SessionRowMeta status={activityStatus} updatedAt={session.updated_at} unread={(session as any).unread} />
                </UnstyledButton>
                {!batchMode && <div className="session-row-actions has-pinned"><button className="session-action-btn is-active" aria-label="取消置顶" onClick={e => togglePin(session, e)}><IconPinned size={14} /></button>
                  <Menu position="bottom-end" withinPortal opened={railMenu?.kind === 'session' && railMenu.key === key} onChange={(opened) => updateRailMenu(opened, 'session', key)}><Menu.Target><button className="session-action-btn" aria-label="更多操作"><IconDotsVertical size={14} /></button></Menu.Target><Menu.Dropdown>
                    {isUnread(key) && <Menu.Item leftSection={<IconCheck size={14} />} onClick={() => markSessionRead(key)}>标为已读</Menu.Item>}
                    <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => openRenameModal(session)}>重命名</Menu.Item>
                    {moveMenuItem(session)}
                    {handoffToBotMenuItem(session)}
                    {distillToBotMenuItem(session)}
                    {!isSessionOffice(session) && <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copySessionId(session)}>复制 ID</Menu.Item>}
                    {forkMenuItems(session)}
                    {handoffItems(session)}
                    <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={(event) => requestDeleteSession(session, event)}>删除会话</Menu.Item>
                  </Menu.Dropdown></Menu>
                </div>}
              </motion.div>
            )
          })}
          </>
        </section>}
        {(() => {
          const botForGroup = (p: ProjectGroup) =>
            findBotForWorkspace(
              registeredBots,
              p.workspace,
              p.sessions[0]?.connection_id || (p.agentId ? agents[p.agentId]?.connection_id : undefined),
            )
          const botProjectCount = groups.filter((p) => Boolean(botForGroup(p))).length
          const displayedGroups = hideBotProjects && botProjectCount > 0
            ? groups.filter((p) => !botForGroup(p))
            : groups
          return (
            <>
              <div className="session-project-section-heading">
                <span>{isOfficeProfile ? '工作空间' : '项目'}</span>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  {botProjectCount > 0 && (
                    <button
                      type="button"
                      onClick={toggleHideBotProjects}
                      style={{
                        border: 'none',
                        background: 'transparent',
                        color: 'var(--astr-muted)',
                        fontSize: 11,
                        cursor: 'pointer',
                        padding: '0 4px',
                      }}
                      title={hideBotProjects ? '显示已注册为 Bot 的项目目录' : '隐藏已注册为 Bot 的项目（可在左侧 Bot 标签查看）'}
                    >
                      {hideBotProjects ? `显示 Bot (${botProjectCount})` : '隐藏 Bot'}
                    </button>
                  )}
                  <span>{displayedGroups.length}</span>
                </span>
              </div>
              {displayedGroups.map((project) => {
          const projectCollapseKey = `project:${project.key}`
          const projectCollapsed = collapsed[projectCollapseKey] === true
          const isProjectPinned = pinnedProjects.includes(project.key)
          const isOfficeProject = isOfficeProfile || isProjectInOfficeMode(officeModeRegistry, project.key, project.workspace)
          const matchedBot = botForGroup(project)
          const projectCustom = projectAppearance[project.key]
          const visibleCount = expandedCountMap[project.key] ?? 4
          const regularSessions = project.sessions.filter(s => !pinnedSessions[scopeKey(s.agent_id, s.id)])
          const visibleSessions = regularSessions.slice(0, visibleCount)
          const remainingSessionsCount = Math.max(0, regularSessions.length - visibleCount)
          const hasMoreSessions = remainingSessionsCount > 0
          const canCollapse = visibleCount > 4

          return (
            <section
              className="session-project-group"
              data-active={project.sessions.some(session => scopeKey(session.agent_id, session.id) === activeSessionKey) || undefined}
              key={project.key}
              aria-label={`${project.label}${project.remoteLabel ? ` ${project.remoteLabel}` : ''}`}
            >
              <div className="session-project-header"
                onContextMenu={(event) => openRailMenu(event, 'project', project.key)}
                onDragOver={(event) => { if (draggedProject) { event.preventDefault(); event.dataTransfer.dropEffect = 'move' } }}
                onDrop={(event) => {
                  event.preventDefault()
                  if (draggedProject) reorderProject(draggedProject, project.key)
                  setDraggedProject(null)
                }}
              >
                <UnstyledButton
                  className="session-project-title"
                  draggable
                  title="点击展开或收起；拖拽调整顺序"
                  onClick={() => toggle(projectCollapseKey)}
                  onDragStart={(event) => {
                    setDraggedProject(project.key)
                    event.dataTransfer.setData('text/plain', project.key)
                    event.dataTransfer.effectAllowed = 'move'
                  }}
                  onDragEnd={() => setDraggedProject(null)}
                  onKeyDown={(event) => {
                    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
                    event.preventDefault()
                    const index = groups.findIndex(item => item.key === project.key)
                    const target = groups[index + (event.key === 'ArrowUp' ? -1 : 1)]
                    if (target) reorderProject(project.key, target.key)
                  }}
                  aria-expanded={!projectCollapsed}
                  aria-label={`${project.label}${project.remoteLabel ? ` ${project.remoteLabel}` : ''}，${project.sessionCount} 个会话；调整 ${project.label} 顺序`}
                  data-project-key={project.key}
                >
                  <ProjectGlyph
                    iconName={projectCustom?.icon}
                    colorName={projectCustom?.color}
                    size={16}
                  />
                  <span className="session-project-label">{project.label}</span>
                  {matchedBot && (
                    <span
                      className="session-project-source"
                      title={`已注册为 Bot：${matchedBot.name}`}
                      style={{
                        background: 'rgba(99, 102, 241, 0.14)',
                        color: '#4f46e5',
                      }}
                    >
                      Bot
                    </span>
                  )}
                  {isOfficeProject && (
                    <span
                      className="session-project-source"
                      title="办公空间模式（已隐藏 Git / 终端 / 数据库等开发工具）"
                      style={{
                        background: 'rgba(14, 165, 233, 0.14)',
                        color: '#0369a1',
                      }}
                    >
                      办公
                    </span>
                  )}
                  {project.remoteLabel && (
                    <span className="session-project-source" title={project.remoteLabel}>
                      {project.remoteLabel}
                    </span>
                  )}
                  <span className="session-count">{project.sessionCount}</span>
                </UnstyledButton>
                <button
                  type="button"
                  className="session-project-order-handle"
                  aria-label={`调整 ${project.label} 顺序`}
                  draggable
                  style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clipPath: 'inset(50%)', whiteSpace: 'nowrap' }}
                  onDragStart={(event) => {
                    setDraggedProject(project.key)
                    event.dataTransfer.setData('text/plain', project.key)
                    event.dataTransfer.effectAllowed = 'move'
                  }}
                  onDragEnd={() => setDraggedProject(null)}
                />
                <div className={`session-project-actions ${isProjectPinned ? 'has-pinned' : ''}`}>
                  {matchedBot && (
                    <Tooltip label={`打开 Bot 工作台：${matchedBot.name}`} withArrow position="top">
                      <button
                        type="button"
                        className="session-action-btn"
                        onClick={(e) => {
                          e.stopPropagation()
                          window.dispatchEvent(
                            new CustomEvent('astrorder:navigate', {
                              detail: { to: `/bots?bot=${encodeURIComponent(matchedBot.id)}` },
                            }),
                          )
                        }}
                        aria-label={`打开 Bot 工作台：${matchedBot.name}`}
                      >
                        <IconRobot size={14} />
                      </button>
                    </Tooltip>
                  )}
                  <Tooltip label={isProjectPinned ? '取消置顶项目' : '置顶项目'} withArrow position="top">
                    <button
                      type="button"
                      className={`session-action-btn ${isProjectPinned ? 'is-active' : ''}`}
                      onClick={(e) => togglePinProject(project.key, e)}
                      aria-label="置顶项目"
                    >
                      {isProjectPinned ? <IconPinned size={14} /> : <IconPin size={14} />}
                    </button>
                  </Tooltip>
                  <Tooltip label={`在 ${project.label} 中新建会话`} withArrow position="top">
                    <button
                      type="button"
                      className="session-action-btn"
                      onClick={(e) => void handleCreateSession(project, e)}
                      aria-label="新建会话"
                      disabled={creatingForProject === project.key}
                    >
                      {creatingForProject === project.key ? <Loader size={12} /> : <IconPlus size={14} />}
                    </button>
                  </Tooltip>
                  <Menu position="bottom-end" shadow="md" width={168} withinPortal opened={railMenu?.kind === 'project' && railMenu.key === project.key} onChange={(opened) => updateRailMenu(opened, 'project', project.key)}>
                    <Menu.Target>
                      <button
                        type="button"
                        className="session-action-btn"
                        onClick={(e) => e.stopPropagation()}
                        aria-label="项目操作"
                      >
                        <IconDotsVertical size={14} />
                      </button>
                    </Menu.Target>
                    <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
                      {matchedBot && (
                        <Menu.Item
                          leftSection={<IconRobot size={14} />}
                          onClick={() => {
                            window.dispatchEvent(
                              new CustomEvent('astrorder:navigate', {
                                detail: { to: `/bots?bot=${encodeURIComponent(matchedBot.id)}` },
                              }),
                            )
                          }}
                        >
                          打开 Bot 工作台
                        </Menu.Item>
                      )}
                      <Menu.Item
                        leftSection={isProjectPinned ? <IconPinned size={14} /> : <IconPin size={14} />}
                        onClick={() => togglePinProject(project.key)}
                      >
                        {isProjectPinned ? '取消置顶' : '置顶项目'}
                      </Menu.Item>
                      <Menu.Item
                        leftSection={<IconPalette size={14} />}
                        onClick={() => setAppearanceTarget(project)}
                      >
                        外观设置
                      </Menu.Item>
                      <Menu.Item
                        leftSection={isOfficeProject ? <IconCode size={14} /> : <IconBriefcase size={14} />}
                        onClick={() => {
                          setProjectOfficeMode(
                            { projectKey: project.key, workspace: project.workspace },
                            !isOfficeProject,
                          )
                          notifications.show({
                            color: 'teal',
                            message: isOfficeProject
                              ? `已将「${project.label}」切换回开发项目`
                              : `已将「${project.label}」切换为办公空间`,
                          })
                        }}
                      >
                        {isOfficeProject ? '切换回开发项目' : '切换为办公空间'}
                      </Menu.Item>
                      {!isOfficeProject && dbxDirectExecEnabled && (
                        <Menu.Item
                          leftSection={<IconDatabase size={14} />}
                          rightSection={(() => {
                            const sample = project.sessions[0]
                            const ownerAgent = project.agentId ? agents[project.agentId] : undefined
                            const wsKey = buildWorkspaceScopeKey(
                              project.workspace,
                              sample?.connection_id || ownerAgent?.connection_id,
                              sample?.source_id || ownerAgent?.source_id,
                              ownerAgent,
                            )
                            const bound =
                              projectDbBindings.find((b) => b.project_key === project.key) ||
                              (wsKey
                                ? projectDbBindings.find((b) => b.workspace_key?.toLowerCase() === wsKey.toLowerCase())
                                : undefined)
                            return bound && bound.targets.length > 0 ? (
                              <Text size="xs" c="dimmed" fw={600}>
                                {bound.targets.length}
                              </Text>
                            ) : undefined
                          })()}
                          onClick={() => setDatabaseTarget(project)}
                        >
                          关联数据库
                        </Menu.Item>
                      )}
                      <Menu.Item
                        leftSection={<IconPlus size={14} />}
                        onClick={(e) => void handleCreateSession(project, e)}
                      >
                        新建会话
                      </Menu.Item>
                      {!isOfficeProject && worktreeEnabled && project.workspace && (
                        <Menu.Item
                          leftSection={<IconGitBranch size={14} />}
                          onClick={() => {
                            const sample = project.sessions[0]
                            const ownerAgent = project.agentId ? agents[project.agentId] : undefined
                            const connId = sample?.connection_id || ownerAgent?.connection_id || undefined
                            if (sample) onSelect(sample)
                            useSidecarStore.getState().openWorktrees(
                              sample?.id || project.key,
                              sample?.agent_id || project.agentId || 'local',
                              project.workspace || undefined,
                              connId,
                            )
                          }}
                        >
                          Worktree 管理
                        </Menu.Item>
                      )}
                      <Menu.Divider />
                      <Menu.Item
                        color="red"
                        leftSection={<IconTrash size={14} />}
                        onClick={(event) => requestDeleteProject(project, event)}
                      >
                        删除项目
                      </Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </div>
              </div>
              <Collapse expanded={!projectCollapsed}>
                <Stack className="session-project-sessions">
                  <>
                  {visibleSessions.map((session) => {
                    const key = scopeKey(session.agent_id, session.id)
                    const isPinned = pinnedSessions[key] === true
                    const activityStatus = activityStatusFor(session)
                    const isRunning = activityStatus === 'running'
                    return (
                      <motion.div
                        initial={{ opacity: 0, y: 4 }}
                        animate={{ opacity: 1, y: 0 }}
                        exit={{ opacity: 0, scale: 0.95 }}
                        transition={{ type: 'spring', stiffness: 450, damping: 35 }}
                        className={`session-row-wrapper ${isRunning ? 'is-running' : ''}`}
                        key={key}
                        style={sessionRunningStyle(projectCustom)}
                        onContextMenu={(event) => { if (!batchMode) openRailMenu(event, 'session', key) }}
                      >
                        {batchMode && (
                          <Checkbox
                            className="session-batch-checkbox"
                            size="xs"
                            checked={selectedSessionKeys.has(key)}
                            onChange={(e) => toggleSelectSession(key, e.nativeEvent as unknown as React.MouseEvent)}
                            onClick={(e) => e.stopPropagation()}
                          />
                        )}
                        <SessionActivityBorder status={activityStatus} />
                        <UnstyledButton
                          className={`session-row ${key === activeSessionKey ? 'is-active' : ''} ${isPinned ? 'is-pinned' : ''} ${isRunning ? 'is-running' : ''} ${(session as any).unread ? 'is-unread' : ''}`}
                          onClick={(e) => { markSessionRead(key); if (batchMode) toggleSelectSession(key, e); else onSelect(session) }}
                          aria-current={key === activeSessionKey ? 'page' : undefined}
                          draggable={!batchMode}
                          onDragStart={(event) => {
                            if (batchMode) return
                            event.dataTransfer.setData('application/x-astrorder-session', JSON.stringify({
                              agent_id: session.agent_id,
                              id: session.id,
                              title: session.title || '未命名会话',
                              key,
                            }))
                            event.dataTransfer.setData('text/plain', key)
                            event.dataTransfer.effectAllowed = 'copy'
                          }}
                        >
                          <span className="session-row-glyph"><AgentKindBadge agent={agents[session.agent_id]} iconOnly /></span>
                          <span className="session-row-title" title={session.worktree_branch && isLinkedWorktreeSession(session) ? `${displaySessionTitle(session)} · Worktree: ${session.worktree_branch} (${session.workspace})` : displaySessionTitle(session)}>
                            {displaySessionTitle(session)}
                            {session.worktree_branch && isLinkedWorktreeSession(session) && (
                              <span
                                title={`Git Worktree 分支：${session.worktree_branch}\n路径：${session.workspace}`}
                                style={{
                                  display: 'inline-flex',
                                  alignItems: 'center',
                                  gap: 2,
                                  marginLeft: 6,
                                  padding: '0 5px',
                                  borderRadius: 4,
                                  fontSize: 10,
                                  lineHeight: '16px',
                                  background: 'rgba(14, 165, 233, 0.12)',
                                  color: '#0284c7',
                                  verticalAlign: 'middle',
                                  maxWidth: 110,
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                  whiteSpace: 'nowrap',
                                }}
                              >
                                <IconGitBranch size={10} style={{ flexShrink: 0 }} />
                                {session.worktree_branch}
                              </span>
                            )}
                          </span>
                          <SessionRowMeta status={activityStatus} updatedAt={session.updated_at} unread={(session as any).unread} />
                        </UnstyledButton>
                        {!batchMode && <div className={`session-row-actions ${isPinned ? 'has-pinned' : ''}`}>
                          <Tooltip label={isPinned ? '取消置顶' : '置顶'} withArrow position="top">
                            <button
                              type="button"
                              className={`session-action-btn ${isPinned ? 'is-active' : ''}`}
                              onClick={(e) => togglePin(session, e)}
                              aria-label={isPinned ? '取消置顶' : '置顶'}
                            >
                              {isPinned ? <IconPinned size={14} /> : <IconPin size={14} />}
                            </button>
                          </Tooltip>
                          <Menu position="bottom-end" shadow="md" width={176} withinPortal opened={railMenu?.kind === 'session' && railMenu.key === key} onChange={(opened) => updateRailMenu(opened, 'session', key)}>
                            <Menu.Target>
                              <button
                                type="button"
                                className="session-action-btn"
                                onClick={(e) => e.stopPropagation()}
                                aria-label="更多操作"
                              >
                                <IconDotsVertical size={14} />
                              </button>
                            </Menu.Target>
                            <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
                              {isUnread(key) && <Menu.Item leftSection={<IconCheck size={14} />} onClick={() => markSessionRead(key)}>
                                标为已读
                              </Menu.Item>}
                              <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => openRenameModal(session)}>
                                重命名
                              </Menu.Item>
                              {moveMenuItem(session)}
                              {handoffToBotMenuItem(session)}
                              {distillToBotMenuItem(session)}
                              <Menu.Item
                                leftSection={isPinned ? <IconPinned size={14} /> : <IconPin size={14} />}
                                onClick={(e) => togglePin(session, e)}
                              >
                                {isPinned ? '取消置顶' : '置顶会话'}
                              </Menu.Item>
                              {!isOfficeProject && (
                                <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copySessionId(session)}>
                                  复制 ID
                                </Menu.Item>
                              )}
                              {forkMenuItems(session)}
                              {handoffItems(session)}
                              <Menu.Divider />
                              <Menu.Item
                                color="red"
                                leftSection={<IconTrash size={14} />}
                                onClick={(event) => requestDeleteSession(session, event)}
                              >
                                删除会话
                              </Menu.Item>
                            </Menu.Dropdown>
                          </Menu>
                        </div>}
                      </motion.div>
                    )
                  })}
                  </> 
                  {(hasMoreSessions || canCollapse) && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {hasMoreSessions && (
                        <UnstyledButton
                          className="session-expand-more-btn"
                          onClick={(e) => {
                            e.stopPropagation()
                            handleExpandMore(project.key, regularSessions.length)
                          }}
                        >
                          <IconChevronDown size={12} />
                          <span>展开更多（{remainingSessionsCount}）</span>
                        </UnstyledButton>
                      )}
                      {canCollapse && (
                        <UnstyledButton
                          className="session-expand-more-btn"
                          style={hasMoreSessions ? { width: 'auto', flexShrink: 0, padding: '4px 10px' } : undefined}
                          onClick={(e) => {
                            e.stopPropagation()
                            handleCollapseSessions(project.key)
                          }}
                        >
                          <IconChevronUp size={12} />
                          <span>收起</span>
                        </UnstyledButton>
                      )}
                    </div>
                  )}
                </Stack>
              </Collapse>
            </section>
          )
        })}
            </>
          )
        })()}
        </>
      )}
      </div>

      {/* 会话重命名模态弹窗 */}
      <Modal
        opened={renameTarget !== null}
        onClose={() => setRenameTarget(null)}
        title="重命名会话"
        centered
        size="sm"
      >
        <Stack gap="md">
          <TextInput
            label="会话名称"
            value={renameTitle}
            onChange={(e) => setRenameTitle(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void saveRename()
            }}
            autoFocus
          />
          <Group justify="flex-end" gap="xs">
            <Button variant="default" size="xs" onClick={() => setRenameTarget(null)}>
              取消
            </Button>
            <Button size="xs" onClick={saveRename} loading={renameLoading} disabled={!renameTitle.trim()}>
              保存
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* 移动会话到其他项目模态弹窗 */}
      <Modal
        opened={moveTarget !== null}
        onClose={() => { if (!moveLoading) setMoveTarget(null) }}
        title="移动会话到其他项目"
        centered
        size="md"
      >
        <Stack gap="md">
          {moveTarget && (
            <Stack gap={4}>
              <Text size="xs" c="dimmed">
                会话：{displaySessionTitle(moveTarget)}
              </Text>
              {moveTarget.workspace && (
                <Text size="xs" c="dimmed" style={{ wordBreak: 'break-all' }}>
                  当前项目：{moveTarget.workspace}
                </Text>
              )}
            </Stack>
          )}

          {moveCandidateProjects.length > 0 && !moveUseCustomPath ? (
            <Select
              label="目标项目"
              placeholder="选择要移入的目标项目"
              searchable
              nothingFoundMessage="未找到匹配项目"
              data={moveCandidateProjects.map((item) => ({ value: item.value, label: item.label }))}
              value={moveWorkspace || null}
              onChange={(val) => setMoveWorkspace(val || '')}
              disabled={moveLoading}
              comboboxProps={{ withinPortal: true }}
            />
          ) : (
            <TextInput
              label="目标项目路径"
              placeholder={moveTarget?.connection_id ? '例如 /home/user/workspace/project' : '输入目标项目绝对路径'}
              value={moveWorkspace}
              onChange={(e) => setMoveWorkspace(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void saveMoveProject()
              }}
              disabled={moveLoading}
              autoFocus
            />
          )}

          {moveCandidateProjects.length > 0 && (
            <Group justify="space-between" align="center">
              <Button
                variant="subtle"
                size="compact-xs"
                onClick={() => {
                  setMoveUseCustomPath((v) => !v)
                  setMoveWorkspace('')
                }}
                disabled={moveLoading}
              >
                {moveUseCustomPath ? '从已有项目中选择' : '手动输入其他目录路径'}
              </Button>
            </Group>
          )}

          <Text size="xs" c="dimmed">
            将同步更新原生工作区归属并释放旧会话连接；下次发送消息时将在目标项目目录下继续执行。
          </Text>

          <Group justify="flex-end" gap="xs">
            <Button variant="default" size="xs" onClick={() => setMoveTarget(null)} disabled={moveLoading}>
              取消
            </Button>
            <Button
              size="xs"
              onClick={saveMoveProject}
              loading={moveLoading}
              disabled={!moveWorkspace.trim() || normalizeWorkspaceForCompare(moveWorkspace) === normalizeWorkspaceForCompare(moveTarget?.workspace)}
            >
              确认移动
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* 项目外观设置模态弹窗 */}
      {appearanceTarget && (
        <ProjectAppearanceModal
          opened={appearanceTarget !== null}
          onClose={() => setAppearanceTarget(null)}
          projectKey={appearanceTarget.key}
          projectLabel={appearanceTarget.label}
          appearance={projectAppearance}
          onSave={handleSaveAppearance}
        />
      )}
      {databaseTarget && (
        <ProjectDatabaseModal
          opened={databaseTarget !== null}
          onClose={() => setDatabaseTarget(null)}
          projectKey={databaseTarget.key}
          workspaceKey={(() => {
            const sample = databaseTarget.sessions[0]
            const ownerAgent = databaseTarget.agentId ? agents[databaseTarget.agentId] : undefined
            return buildWorkspaceScopeKey(
              databaseTarget.workspace,
              sample?.connection_id || ownerAgent?.connection_id,
              sample?.source_id || ownerAgent?.source_id,
              ownerAgent,
            )
          })()}
          projectLabel={databaseTarget.label}
        />
      )}
      {forkWorktreeTarget && (
        <ForkWorktreeDialog
          session={forkWorktreeTarget}
          onClose={() => setForkWorktreeTarget(null)}
          onCreated={(newSession) => {
            onSelect(newSession)
          }}
        />
      )}
      {handoffTarget && <HandoffDialog
        source={handoffTarget}
        targets={handoffTargets(handoffTarget)}
        sessions={sessions}
        onClose={() => setHandoffTarget(null)}
        onTransferred={(created, target) => {
          useAstrorderStore.setState((state) => ({
            sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
          }))
          notifications.show({ color: 'teal', message: `已转交给 ${agentKindLabel(target.kind)}` })
        }}
        onOpenTransferred={onSelect}
      />}      <ConfirmPopover
        opened={confirmation !== null}
        coords={confirmation?.coords}
        title={confirmationTitle}
        message={confirmationMessage}
        extraContent={confirmation?.kind === 'delete-session' && deleteWorktreeStatus ? (
          <Stack gap={6} pt={2}>
            {deleteWorktreeStatus.otherSessions > 0 ? (
              <Text size="xs" c="dimmed">
                该独立 Worktree 仍有其他 {deleteWorktreeStatus.otherSessions} 个会话在使用，将自动保留目录。
              </Text>
            ) : (
              <>
                <Checkbox
                  size="xs"
                  label={`同时移除 Worktree 目录${confirmation.session.worktree_branch ? ` (${confirmation.session.worktree_branch})` : ''}`}
                  checked={deleteRemoveWorktree}
                  onChange={(e) => setDeleteRemoveWorktree(e.currentTarget.checked)}
                  disabled={confirmationLoading}
                />
                {deleteRemoveWorktree && confirmation.session.worktree_branch && (
                  <Checkbox
                    size="xs"
                    ml={18}
                    label={`同时删除本地分支 ${confirmation.session.worktree_branch}`}
                    checked={deleteWorktreeBranch}
                    onChange={(e) => setDeleteWorktreeBranch(e.currentTarget.checked)}
                    disabled={confirmationLoading}
                  />
                )}
                {deleteWorktreeStatus.dirty && (
                  <>
                    <Text size="xs" c="orange">
                      检测到该 Worktree 有 {deleteWorktreeStatus.changedFiles} 处未提交修改。
                    </Text>
                    {deleteRemoveWorktree && (
                      <Checkbox
                        size="xs"
                        ml={18}
                        color="red"
                        label="强制移除（将丢弃未提交更改）"
                        checked={deleteForceWorktree}
                        onChange={(e) => setDeleteForceWorktree(e.currentTarget.checked)}
                        disabled={confirmationLoading}
                      />
                    )}
                  </>
                )}
              </>
            )}
          </Stack>
        ) : undefined}
        confirmLabel="删除"
        loading={confirmationLoading}
        onConfirm={confirmDeletion}
        onCancel={() => {
          if (!confirmationLoading) setConfirmation(null)
        }}
      />
    </Stack>
  )
}
