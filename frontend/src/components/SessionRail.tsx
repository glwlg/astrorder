import {
  IconChecklist,
  IconChevronDown,
  IconChevronUp,
  IconCopy,
  IconDotsVertical,
  IconEdit,
  IconPalette,
  IconPin,
  IconPinned,
  IconPlus,
  IconTrash,
  IconTransfer,
} from '@tabler/icons-react'
import {
  Button,
  Checkbox,
  Collapse,
  Group,
  Loader,
  Menu,
  Modal,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useMemo, useState, type CSSProperties } from 'react'
import { AnimatePresence, LayoutGroup, motion } from 'motion/react'
import { VariableProximity } from './animations/VariableProximity'
import { useSessionOrder } from '../hooks/useSessionOrder'
import { AgentSessionFilter, matchesAgent } from './AgentSessionFilter'
import { NewSessionDialog } from './NewSessionDialog'
import { HandoffDialog } from './HandoffDialog'
import { moveProject, reconcileProjectOrder } from './projectOrder'
import { useWorkspacePreferences } from '../hooks/useWorkspacePreferences'
import type { Agent, Project, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { StatusDot } from './Status'
import { ConfirmPopover } from './ConfirmPopover'
import { confirmationCoordinatesFromEvent, type ConfirmationCoordinates } from './confirmationPosition'
import { AgentKindBadge } from './SessionRuntimeFacts'
import { buildProjectGroups, displaySessionTitle, formatRelativeTime, sessionActivityStatus, type ProjectGroup, type RailFilter } from './sessionRailModel'
import { api } from '../api/client'
import './sessionPins.css'
import { useAstrorderStore } from '../state/store'
import { useShallow } from 'zustand/react/shallow'
import {
  ProjectAppearanceModal,
  ProjectGlyph,
  PROJECT_APPEARANCE_COLORS,
  type ProjectAppearanceEntry,
} from './projectAppearance'
import { SessionActivityBorder } from './AnimatedStatus'

type PendingConfirmation =
  | { kind: 'delete-session'; session: Session; coords: ConfirmationCoordinates }
  | { kind: 'delete-project'; project: ProjectGroup; coords: ConfirmationCoordinates }
  | { kind: 'batch-delete-sessions'; count: number; sessions: Session[]; coords?: ConfirmationCoordinates }

function sessionRunningStyle(session: Session, accent?: string): CSSProperties {
  const resolvedAccent = (accent && (PROJECT_APPEARANCE_COLORS[accent] || accent)) || undefined
  return {
    '--session-running-color': resolvedAccent || (session.agent_id.includes('codex') ? 'var(--astr-teal, #12b886)' : 'var(--astr-indigo, #5b6cff)'),
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
  const sessions = useSessionOrder(incomingSessions)
  const commands = useAstrorderStore(useShallow((state) => state.commands))
  useAstrorderStore((state) => state.messages)
  useAstrorderStore((state) => state.tasks)
  useAstrorderStore((state) => state.liveActivityAt)
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
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  const { preferences, updatePreferences, removeProjectPreferences } = useWorkspacePreferences()
  const { pinned_projects: pinnedProjects, appearance: projectAppearance, session_pins: pinnedSessions, project_order: projectOrder } = preferences
  const [appearanceTarget, setAppearanceTarget] = useState<ProjectGroup | null>(null)

  // 项目下会话默认只显示4个，展开更多状态
  const [expandedSessionsMap, setExpandedSessionsMap] = useState<Record<string, boolean>>({})

  const updateExpandedSessions = (key: string, expanded: boolean) => {
    setExpandedSessionsMap((prev) => {
      const next = { ...prev, [key]: expanded }
      return next
    })
  }

  // 重命名弹窗状态
  const [renameTarget, setRenameTarget] = useState<Session | null>(null)
  const [renameTitle, setRenameTitle] = useState('')
  const [renameLoading, setRenameLoading] = useState(false)
  const [confirmation, setConfirmation] = useState<PendingConfirmation | null>(null)
  const [confirmationLoading, setConfirmationLoading] = useState(false)
  const [handoffTarget, setHandoffTarget] = useState<Session | null>(null)
  const [batchMode, setBatchMode] = useState(false)
  const [selectedSessionKeys, setSelectedSessionKeys] = useState<Set<string>>(new Set())

  // 新建会话加载中
  const creatingForProject = createOpened ? createProject?.key : null

  // 增强装饰会话带上 pinned
  const decoratedSessions = useMemo(() => {
    return sessions.map((s) => ({
      ...s,
      pinned: pinnedSessions[scopeKey(s.agent_id, s.id)] === true,
    }))
  }, [sessions, pinnedSessions])

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

  const toggle = (key: string) => setCollapsed((current) => ({ ...current, [key]: !current[key] }))

  const togglePinProject = (projectKey: string, event?: React.MouseEvent) => {
    event?.stopPropagation()
    void updatePreferences(value => ({ pinned_projects: value.pinned_projects.includes(projectKey)
      ? value.pinned_projects.filter(key => key !== projectKey) : [projectKey, ...value.pinned_projects] }))
  }

  const handleSaveAppearance = (projectKey: string, entry: ProjectAppearanceEntry) => {
    void updatePreferences({ appearance: { [projectKey]: entry } })
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

  const requestDeleteSession = (session: Session, event?: { clientX: number; clientY: number }) => {
    setConfirmation({ kind: 'delete-session', session, coords: confirmationCoordinatesFromEvent(event) })
  }

  const deleteSession = async (session: Session) => {
    try {
      await api.deleteSession(session.id, session.agent_id)
      useAstrorderStore.setState((state) => {
        const nextSessions = { ...state.sessions }
        delete nextSessions[scopeKey(session.agent_id, session.id)]
        return { sessions: nextSessions }
      })
    } catch (err) {
      console.error('删除会话失败', err)
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '删除会话失败，请重试' })
    }
  }

  const handoffTargets = (session: Session) => {
    const source = agents[session.agent_id]
    if (!source || source.connection_id != null) return []
    return Object.values(agents).filter((agent) =>
      agent.kind !== source.kind && agent.connection_id == null && agent.status === 'ready',
    )
  }

  const handoffItems = (session: Session) => {
    if (!handoffTargets(session).length) return null
    return <>
      <Menu.Divider />
      <Menu.Item
        leftSection={<IconTransfer size={14} />}
        disabled={session.status !== 'idle'}
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

  return (
    <Stack className="session-rail" gap="sm">
      <div className="session-rail-controls">
      <Group gap="xs" wrap="nowrap">
        <AgentSessionFilter agents={agents} value={agentFilter} onChange={updateAgentFilter} />
        <Tooltip label={batchMode ? '退出批量管理' : '批量管理会话'} withArrow position="bottom">
          <Button
            size="xs"
            variant={batchMode ? 'filled' : 'subtle'}
            color={batchMode ? 'indigo' : 'gray'}
            px={0}
            w={36}
            onClick={() => {
              setBatchMode(prev => !prev)
              setSelectedSessionKeys(new Set())
            }}
            aria-label={batchMode ? '退出批量管理' : '批量管理会话'}
            aria-pressed={batchMode}
          >
            <IconChecklist size={17} />
          </Button>
        </Tooltip>
        <Button size="xs" variant="subtle" color="gray" px={0} w={36} onClick={() => { setCreateProject(null); setCreateOpened(true) }} aria-label="新建会话"><IconPlus size={17} /></Button>
      </Group>
      {createOpened && (
        <NewSessionDialog
          agents={agents}
          project={createProject}
          initialAgentId={agentFilter}
          onClose={() => setCreateOpened(false)}
          onCreated={session => {
            onSelect(session)
            if (createProject) setCollapsed(previous => ({ ...previous, [`project:${createProject.key}`]: false }))
          }}
        />
      )}
      <input
        className="session-search"
        aria-label="搜索会话"
        placeholder="搜索项目或会话"
        value={filter}
        onChange={(event) => setFilter(event.currentTarget.value)}
      />
      <div className="session-rail-filters" role="tablist" aria-label="会话筛选">
        <LayoutGroup id="session-rail-filters-group">
          {([
            ['all', '全部'],
            ['running', '运行中'],
            ['unread', '未读'],
            ['pinned', '置顶'],
            ['recent', '24小时'],
          ] as const).map(([value, label]) => {
            const isActive = statusFilter === value
            return (
              <button
                className={`session-filter ${isActive ? 'is-active' : ''}`}
                key={value}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => setStatusFilter(value)}
                style={{ position: 'relative' }}
              >
                {isActive && (
                  <motion.span
                    layoutId="session-filter-pill-bg"
                    style={{
                      position: 'absolute',
                      inset: 0,
                      borderRadius: 999,
                      background: 'color-mix(in srgb, var(--astr-indigo, #5b6cff) 12%, var(--astr-surface))',
                      border: '1px solid color-mix(in srgb, var(--astr-indigo, #5b6cff) 30%, transparent)',
                      zIndex: 0,
                    }}
                    transition={{ type: 'spring', stiffness: 450, damping: 30 }}
                  />
                )}
                <span style={{ position: 'relative', zIndex: 1 }}>
                  <VariableProximity label={label} active={isActive} />
                </span>
              </button>
            )
          })}
        </LayoutGroup>
      </div>
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
          <div className="session-pinned-heading"><IconPinned size={15} />置顶会话</div>
          <AnimatePresence initial={false}>
          {groups.flatMap(project => project.sessions).filter(s => pinnedSessions[scopeKey(s.agent_id, s.id)]).map(session => {
            const key = scopeKey(session.agent_id, session.id)
            const activityStatus = sessionActivityStatus(session, commands)
            const isRunning = activityStatus === 'running'
            const parentProject = groups.find(p => p.sessions.some(s => s.id === session.id && s.agent_id === session.agent_id))
            const projectCustom = (parentProject && projectAppearance[parentProject.key]) ||
              (session.project_id ? projectAppearance[`project:${session.project_id}`] : undefined)
            const projectColor = projectCustom?.color
            return (
              <motion.div
                initial={{ opacity: 0, y: -4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, scale: 0.95 }}
                transition={{ type: 'spring', stiffness: 450, damping: 35 }}
                className={`session-row-wrapper ${isRunning ? 'is-running' : ''}`}
                key={key}
                style={sessionRunningStyle(session, projectColor)}
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
                  className={`session-row is-pinned ${key === activeSessionKey ? 'is-active' : ''} ${isRunning ? 'is-running' : ''}`}
                  onClick={(e) => batchMode ? toggleSelectSession(key, e) : onSelect(session)}
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
                  <span className="session-row-title">
                    {projectCustom?.icon && (
                      <ProjectGlyph
                        iconName={projectCustom.icon}
                        colorName={projectCustom.color}
                        size={13}
                        style={{ marginRight: 5, verticalAlign: 'text-bottom', opacity: 0.85 }}
                      />
                    )}
                    {displaySessionTitle(session)}
                  </span>
                  <div className="session-row-info"><AgentKindBadge agent={agents[session.agent_id]} iconOnly /><span className="session-row-time">{formatRelativeTime(session.updated_at)}</span><StatusDot status={activityStatus} /></div>
                </UnstyledButton>
                {!batchMode && <div className="session-row-actions has-pinned"><button className="session-action-btn is-active" aria-label="取消置顶" onClick={e => togglePin(session, e)}><IconPinned size={14} /></button>
                  <Menu position="bottom-end" withinPortal><Menu.Target><button className="session-action-btn" aria-label="更多操作"><IconDotsVertical size={14} /></button></Menu.Target><Menu.Dropdown>
                    <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => openRenameModal(session)}>重命名</Menu.Item>
                    <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copySessionId(session)}>复制 ID</Menu.Item>
                    {handoffItems(session)}
                    <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={(event) => requestDeleteSession(session, event)}>删除会话</Menu.Item>
                  </Menu.Dropdown></Menu>
                </div>}
              </motion.div>
            )
          })}
          </AnimatePresence>
        </section>}
        {groups.map((project) => {
          const projectCollapseKey = `project:${project.key}`
          const projectCollapsed = collapsed[projectCollapseKey] === true
          const isProjectPinned = pinnedProjects.includes(project.key)
          const projectCustom = projectAppearance[project.key]
          const isSessionsExpanded = expandedSessionsMap[project.key] === true
          const regularSessions = project.sessions.filter(s => !pinnedSessions[scopeKey(s.agent_id, s.id)])
          const visibleSessions = isSessionsExpanded ? regularSessions : regularSessions.slice(0, 4)
          const hasMoreSessions = regularSessions.length > 4

          return (
            <section
              className="session-project-group"
              key={project.key}
              aria-label={`${project.label}${project.remoteLabel ? ` ${project.remoteLabel}` : ''}`}
            >
              <div className="session-project-header"
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
                  aria-label={`${project.label}${project.remoteLabel ? ` ${project.remoteLabel}` : ''}，${project.sessionCount} 个会话`}
                  data-project-key={project.key}
                >
                  <ProjectGlyph
                    iconName={projectCustom?.icon}
                    colorName={projectCustom?.color}
                    size={16}
                  />
                  <span className="session-project-label">{project.label}</span>
                  {project.remoteLabel && (
                    <span className="session-project-source" title={project.remoteLabel}>
                      {project.remoteLabel}
                    </span>
                  )}
                  <span className="session-count">{project.sessionCount}</span>
                </UnstyledButton>
                <div className={`session-project-actions ${isProjectPinned ? 'has-pinned' : ''}`}>
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
                  <Menu position="bottom-end" shadow="md" width={140} withinPortal>
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
                        leftSection={<IconPlus size={14} />}
                        onClick={(e) => void handleCreateSession(project, e)}
                      >
                        新建会话
                      </Menu.Item>
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
                <Stack className="session-project-sessions" gap={4} mt={8}>
                  <AnimatePresence initial={false}>
                  {visibleSessions.map((session) => {
                    const key = scopeKey(session.agent_id, session.id)
                    const isPinned = pinnedSessions[key] === true
                    const activityStatus = sessionActivityStatus(session, commands)
                    const isRunning = activityStatus === 'running'
                    return (
                      <motion.div
                        initial={{ opacity: 0, y: 4 }}
                        animate={{ opacity: 1, y: 0 }}
                        exit={{ opacity: 0, scale: 0.95 }}
                        transition={{ type: 'spring', stiffness: 450, damping: 35 }}
                        className={`session-row-wrapper ${isRunning ? 'is-running' : ''}`}
                        key={key}
                        style={sessionRunningStyle(session, projectCustom?.color)}
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
                          className={`session-row ${key === activeSessionKey ? 'is-active' : ''} ${isPinned ? 'is-pinned' : ''} ${isRunning ? 'is-running' : ''}`}
                          onClick={(e) => batchMode ? toggleSelectSession(key, e) : onSelect(session)}
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
                          <span className="session-row-title" title={displaySessionTitle(session)}>
                            {displaySessionTitle(session)}
                          </span>
                          <div className="session-row-info">
                            <AgentKindBadge agent={agents[session.agent_id]} iconOnly />
                            <span className="session-row-time">{formatRelativeTime(session.updated_at)}</span>
                            <StatusDot status={activityStatus} />
                          </div>
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
                          <Menu position="bottom-end" shadow="md" width={130} withinPortal>
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
                              <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => openRenameModal(session)}>
                                重命名
                              </Menu.Item>
                              <Menu.Item
                                leftSection={isPinned ? <IconPinned size={14} /> : <IconPin size={14} />}
                                onClick={(e) => togglePin(session, e)}
                              >
                                {isPinned ? '取消置顶' : '置顶会话'}
                              </Menu.Item>
                              <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copySessionId(session)}>
                                复制 ID
                              </Menu.Item>
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
                  </AnimatePresence>
                  {hasMoreSessions && (
                    <UnstyledButton
                      className="session-expand-more-btn"
                      onClick={(e) => {
                        e.stopPropagation()
                        updateExpandedSessions(project.key, !isSessionsExpanded)
                      }}
                    >
                      {isSessionsExpanded ? (
                        <>
                          <IconChevronUp size={12} />
                          <span>收起</span>
                        </>
                      ) : (
                        <>
                          <IconChevronDown size={12} />
                          <span>展开更多（还有 {regularSessions.length - 4} 个会话）</span>
                        </>
                      )}
                    </UnstyledButton>
                  )}
                </Stack>
              </Collapse>
            </section>
          )
        })}
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
      {handoffTarget && <HandoffDialog
        source={handoffTarget}
        targets={handoffTargets(handoffTarget)}
        sessions={sessions}
        onClose={() => setHandoffTarget(null)}
        onTransferred={(created, target) => {
          useAstrorderStore.setState((state) => ({
            sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
          }))
          notifications.show({ color: 'teal', message: `已转交给 ${target.kind === 'codex' ? 'Codex' : 'Hermes'}` })
        }}
        onOpenTransferred={onSelect}
      />}      <ConfirmPopover
        opened={confirmation !== null}
        coords={confirmation?.coords}
        title={confirmationTitle}
        message={confirmationMessage}
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
