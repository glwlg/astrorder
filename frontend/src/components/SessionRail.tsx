import {
  IconChevronDown,
  IconChevronRight,
  IconChevronUp,
  IconCopy,
  IconDotsVertical,
  IconEdit,
  IconPalette,
  IconPin,
  IconPinned,
  IconPlus,
  IconTrash,
} from '@tabler/icons-react'
import {
  Button,
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
import { useEffect, useMemo, useState } from 'react'
import { useSessionOrder } from '../hooks/useSessionOrder'
import { AgentSessionFilter, matchesAgent } from './AgentSessionFilter'
import { NewSessionDialog } from './NewSessionDialog'
import { moveProject, PROJECT_ORDER_KEY, readProjectOrder, reconcileProjectOrder } from './projectOrder'
import type { Agent, Project, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { StatusDot } from './Status'
import { AgentKindBadge } from './SessionRuntimeFacts'
import { buildProjectGroups, formatRelativeTime, sessionActivityStatus, type ProjectGroup, type RailFilter } from './sessionRailModel'
import { api } from '../api/client'
import './sessionPins.css'
import { useAstrorderStore } from '../state/store'
import {
  loadPinnedProjects,
  savePinnedProjects,
  loadProjectAppearance,
  saveProjectAppearance,
  purgeProjectPreferences,
  ProjectAppearanceModal,
  ProjectGlyph,
  type ProjectAppearanceMap,
  type ProjectAppearanceEntry,
} from './projectAppearance'

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

  // 本地置顶项目与外观状态
  const [pinnedProjects, setPinnedProjects] = useState<string[]>(() => loadPinnedProjects())
  const [projectAppearance, setProjectAppearance] = useState<ProjectAppearanceMap>(() => loadProjectAppearance())
  const [appearanceTarget, setAppearanceTarget] = useState<ProjectGroup | null>(null)

  // 项目下会话默认只显示4个，展开更多状态
  const [expandedSessionsMap, setExpandedSessionsMap] = useState<Record<string, boolean>>({})

  const updateExpandedSessions = (key: string, expanded: boolean) => {
    setExpandedSessionsMap((prev) => {
      const next = { ...prev, [key]: expanded }
      return next
    })
  }

  // 本地置顶会话存储
  const [pinnedSessions, setPinnedSessions] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem('astrorder_pinned_sessions') || '{}')
    } catch {
      return {}
    }
  })

  // 重命名弹窗状态
  const [renameTarget, setRenameTarget] = useState<Session | null>(null)
  const [renameTitle, setRenameTitle] = useState('')
  const [renameLoading, setRenameLoading] = useState(false)

  // 新建会话加载中
  const creatingForProject = createOpened ? createProject?.key : null

  // 增强装饰会话带上 pinned
  const decoratedSessions = useMemo(() => {
    return sessions.map((s) => ({
      ...s,
      pinned: pinnedSessions[scopeKey(s.agent_id, s.id)] === true,
    }))
  }, [sessions, pinnedSessions])

  const [projectOrder, setProjectOrder] = useState(readProjectOrder)
  const [draggedProject, setDraggedProject] = useState<string | null>(null)
  const allGroups = useMemo(() => buildProjectGroups(decoratedSessions, agents, projects), [decoratedSessions, agents, projects])
  const stableOrder = useMemo(() => reconcileProjectOrder(projectOrder, allGroups.map(project => project.key)), [projectOrder, allGroups])
  useEffect(() => {
    if (stableOrder.length === projectOrder.length && stableOrder.every((key, index) => key === projectOrder[index])) return
    setProjectOrder(stableOrder)
    try { localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(stableOrder)) } catch {}
  }, [stableOrder, projectOrder])
  const reorderProject = (source: string, target: string) => {
    const next = moveProject(stableOrder, source, target)
    setProjectOrder(next)
    try { localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(next)) } catch {}
  }
  const groups = useMemo(() => {
    const visible = buildProjectGroups(decoratedSessions.filter(s => matchesAgent(s, agents, agentFilter)), agents, projects, filter.trim().toLocaleLowerCase(), statusFilter).filter(p => agentFilter === 'all' || p.sessions.length > 0)
    const byKey = new Map(visible.map(project => [project.key, project]))
    return stableOrder.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
  }, [agents, decoratedSessions, filter, projects, statusFilter, stableOrder, agentFilter])

  const toggle = (key: string) => setCollapsed((current) => ({ ...current, [key]: !current[key] }))

  const togglePinProject = (projectKey: string, event?: React.MouseEvent) => {
    event?.stopPropagation()
    setPinnedProjects((prev) => {
      const next = prev.includes(projectKey) ? prev.filter((k) => k !== projectKey) : [...prev, projectKey]
      savePinnedProjects(next)
      return next
    })
  }

  const handleSaveAppearance = (projectKey: string, entry: ProjectAppearanceEntry) => {
    setProjectAppearance((prev) => {
      const next = { ...prev, [projectKey]: entry }
      saveProjectAppearance(next)
      return next
    })
  }

  const togglePin = (session: Session, event: React.MouseEvent) => {
    event.stopPropagation()
    const key = scopeKey(session.agent_id, session.id)
    setPinnedSessions((prev) => {
      const next = { ...prev, [key]: !prev[key] }
      try {
        localStorage.setItem('astrorder_pinned_sessions', JSON.stringify(next))
      } catch {}
      return next
    })
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

  const handleDeleteSession = async (session: Session) => {
    if (!window.confirm(`确定删除会话“${session.title || session.id}”吗？`)) return
    try {
      await api.deleteSession(session.id, session.agent_id)
      useAstrorderStore.setState((state) => {
        const nextSessions = { ...state.sessions }
        delete nextSessions[scopeKey(session.agent_id, session.id)]
        return { sessions: nextSessions }
      })
    } catch (err) {
      console.error('删除会话失败', err)
    }
  }

  const handleDeleteProject = async (project: ProjectGroup) => {
    const sessionCount = project.sessionCount || project.sessions.length
    const prompt = sessionCount > 0
      ? `确定删除项目“${project.label}”吗？\n此操作将同时删除该项目及其包含的 ${sessionCount} 个会话。`
      : `确定删除项目“${project.label}”吗？`
    if (!window.confirm(prompt)) return

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

      purgeProjectPreferences(project.key)
      setPinnedProjects((prev) => prev.filter((k) => k !== project.key))
      setProjectOrder((prev) => prev.filter((k) => k !== project.key))
      setProjectAppearance((prev) => {
        const next = { ...prev }
        delete next[project.key]
        return next
      })

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
      window.alert('删除项目失败，请重试')
    }
  }

  const copySessionId = (session: Session) => {
    void navigator.clipboard.writeText(session.id)
  }

  return (
    <Stack className="session-rail" gap="sm">
      <Group gap="xs" wrap="nowrap"><AgentSessionFilter agents={agents} value={agentFilter} onChange={updateAgentFilter} /><Button size="xs" onClick={() => { setCreateProject(null); setCreateOpened(true) }} aria-label="新建会话"><IconPlus size={16} /></Button></Group>
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
        {([
          ['all', '全部'],
          ['running', '运行中'],
          ['unread', '未读'],
          ['pinned', '置顶'],
          ['recent', '24小时'],
        ] as const).map(([value, label]) => (
          <button
            className={`session-filter ${statusFilter === value ? 'is-active' : ''}`}
            key={value}
            type="button"
            role="tab"
            aria-selected={statusFilter === value}
            onClick={() => setStatusFilter(value)}
          >
            {label}
          </button>
        ))}
      </div>
      {groups.length === 0 ? (
        <Text className="rail-empty" size="sm" c="dimmed">
          {filter || statusFilter !== 'all' ? '没有符合条件的项目或会话' : '尚无会话'}
        </Text>
      ) : (
        <>
        {groups.some(project => project.sessions.some(s => pinnedSessions[scopeKey(s.agent_id, s.id)])) && <section className="session-pinned-section" aria-label="置顶会话">
          <div className="session-pinned-heading"><IconPinned size={15} />置顶会话</div>
          {groups.flatMap(project => project.sessions).filter(s => pinnedSessions[scopeKey(s.agent_id, s.id)]).map(session => <div className="session-row-wrapper" key={scopeKey(session.agent_id, session.id)}>
            <UnstyledButton className={`session-row is-pinned ${scopeKey(session.agent_id, session.id) === activeSessionKey ? 'is-active' : ''}`} onClick={() => onSelect(session)}>
              <span className="session-row-title">{session.title || '未命名会话'}</span><div className="session-row-info"><AgentKindBadge agent={agents[session.agent_id]} /><span className="session-row-time">{formatRelativeTime(session.updated_at)}</span><StatusDot status={sessionActivityStatus(session)} /></div>
            </UnstyledButton>
            <div className="session-row-actions has-pinned"><button className="session-action-btn is-active" aria-label="取消置顶" onClick={e => togglePin(session, e)}><IconPinned size={14} /></button>
              <Menu position="bottom-end" withinPortal><Menu.Target><button className="session-action-btn" aria-label="更多操作"><IconDotsVertical size={14} /></button></Menu.Target><Menu.Dropdown>
                <Menu.Item leftSection={<IconEdit size={14} />} onClick={() => openRenameModal(session)}>重命名</Menu.Item>
                <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copySessionId(session)}>复制 ID</Menu.Item>
                <Menu.Item color="red" leftSection={<IconTrash size={14} />} onClick={() => void handleDeleteSession(session)}>删除会话</Menu.Item>
              </Menu.Dropdown></Menu>
            </div>
          </div>)}
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
                <button type="button" draggable
                  className="session-project-drag"
                  aria-label={`调整 ${project.label} 顺序`}
                  title="拖拽调整项目顺序；也可用上下方向键移动"
                  onClick={(event) => event.stopPropagation()}
                  onDragStart={(event) => { setDraggedProject(project.key); event.dataTransfer.setData('text/plain', project.key); event.dataTransfer.effectAllowed = 'move' }}
                  onDragEnd={() => setDraggedProject(null)}
                  onKeyDown={(event) => {
                    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
                    event.preventDefault()
                    const index = groups.findIndex(item => item.key === project.key)
                    const target = groups[index + (event.key === 'ArrowUp' ? -1 : 1)]
                    if (target) reorderProject(project.key, target.key)
                  }}
                >⠿</button>
                <UnstyledButton
                  className="session-project-title"
                  onClick={() => toggle(projectCollapseKey)}
                  aria-expanded={!projectCollapsed}
                  aria-label={`${project.label}${project.remoteLabel ? ` ${project.remoteLabel}` : ''}，${project.sessionCount} 个会话`}
                  data-project-key={project.key}
                >
                  {projectCollapsed ? <IconChevronRight size={15} /> : <IconChevronDown size={15} />}
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
                      {!project.key.startsWith('unmarked:') && (
                        <>
                          <Menu.Divider />
                          <Menu.Item
                            color="red"
                            leftSection={<IconTrash size={14} />}
                            onClick={() => void handleDeleteProject(project)}
                          >
                            删除项目
                          </Menu.Item>
                        </>
                      )}
                    </Menu.Dropdown>
                  </Menu>
                </div>
              </div>
              <Collapse expanded={!projectCollapsed}>
                <Stack className="session-project-sessions" gap={4} mt={8}>
                  {visibleSessions.map((session) => {
                    const key = scopeKey(session.agent_id, session.id)
                    const isPinned = pinnedSessions[key] === true
                    return (
                      <div className="session-row-wrapper" key={key}>
                        <UnstyledButton
                          className={`session-row ${key === activeSessionKey ? 'is-active' : ''} ${isPinned ? 'is-pinned' : ''}`}
                          onClick={() => onSelect(session)}
                          aria-current={key === activeSessionKey ? 'page' : undefined}
                        >
                          <span className="session-row-title" title={session.title || '未命名会话'}>
                            {session.title || '未命名会话'}
                          </span>
                          <div className="session-row-info">
                            <AgentKindBadge agent={agents[session.agent_id]} />
                            <span className="session-row-time">{formatRelativeTime(session.updated_at)}</span>
                            <StatusDot status={sessionActivityStatus(session)} />
                          </div>
                        </UnstyledButton>
                        <div className={`session-row-actions ${isPinned ? 'has-pinned' : ''}`}>
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
                              <Menu.Divider />
                              <Menu.Item
                                color="red"
                                leftSection={<IconTrash size={14} />}
                                onClick={() => void handleDeleteSession(session)}
                              >
                                删除会话
                              </Menu.Item>
                            </Menu.Dropdown>
                          </Menu>
                        </div>
                      </div>
                    )
                  })}
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
    </Stack>
  )
}
