import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent as ReactMouseEvent, type TouchEvent as ReactTouchEvent } from 'react'
import { createPortal } from 'react-dom'
import { IconChevronDown, IconChevronRight, IconCopy, IconDotsVertical, IconEdit, IconPin, IconPinned, IconPlus, IconTrash, IconTransfer } from '@tabler/icons-react'
import type { Agent, Session } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { displaySessionTitle, formatRelativeTime, sessionActivityStatus, type ProjectGroup } from '../../components/sessionRailModel'
import { ProjectGlyph, PROJECT_APPEARANCE_COLORS, type ProjectAppearanceMap } from '../../components/projectAppearance'
import { useAstrorderStore } from '../../state/store'
import { useShallow } from 'zustand/react/shallow'
import './mobileMessageMenu.css'
import { SessionActivityBorder } from '../../components/AnimatedStatus'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { StatusDot } from '../../components/Status'
import { hapticFeedback } from './mobileGestures'

type SessionMenu = { session: Session; x: number; y: number }
type ProjectMenu = { project: ProjectGroup; x: number; y: number }

export function MobileSessionDrawer({ groups, pins, pinnedProjects, selectedKey, appearance, onSelect, onPin, onPinProject, onCreate, onDeleteProject, onDeleteSession, agents, onHandoffSession, onRenameSession, onCopySessionId, isSearching }: {
  groups: ProjectGroup[]; pins: Record<string, boolean>; selectedKey: string; appearance: ProjectAppearanceMap
  onSelect: (session: Session) => void; onPin: (session: Session) => void
  pinnedProjects: string[]; onPinProject: (project: ProjectGroup) => void
  onCreate?: (project: ProjectGroup) => void
  onDeleteProject?: (project: ProjectGroup, event?: { clientX: number; clientY: number }) => void
  onDeleteSession?: (session: Session, event?: { clientX: number; clientY: number }) => void
  agents?: Record<string, Agent>
  onHandoffSession?: (session: Session) => void
  onRenameSession?: (session: Session) => void
  onCopySessionId?: (session: Session) => void
  isSearching?: boolean
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem('astrorder:mobile-collapsed-projects') || '{}')
    } catch {
      return {}
    }
  })
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [sessionMenu, setSessionMenu] = useState<SessionMenu | null>(null)
  const [projectMenu, setProjectMenu] = useState<ProjectMenu | null>(null)
  const suppressSelect = useRef(false)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const commands = useAstrorderStore(useShallow((state) => state.commands))
  useAstrorderStore((state) => state.messages)
  useAstrorderStore((state) => state.tasks)
  useAstrorderStore((state) => state.liveActivityAt)
  const currentProjectKey = groups.find((group) => group.sessions.some((session) => scopeKey(session.agent_id, session.id) === selectedKey))?.key
  const isCollapsed = (key: string) => {
    if (isSearching) return false
    return collapsed[key] ?? key !== currentProjectKey
  }
  const toggleCollapsed = (key: string) => {
    const nextState = !isCollapsed(key)
    setCollapsed((prev) => {
      const next = { ...prev, [key]: nextState }
      try {
        localStorage.setItem('astrorder:mobile-collapsed-projects', JSON.stringify(next))
      } catch {}
      return next
    })
  }
  const pinned = groups.flatMap((group) => group.sessions).filter((session) => pins[scopeKey(session.agent_id, session.id)])
  const cancelHold = () => { if (hold.current) clearTimeout(hold.current); hold.current = null }
  useEffect(() => () => cancelHold(), [])
  useLayoutEffect(() => {
    const anchor = sessionMenu || projectMenu
    if (!anchor) return
    const update = () => {
      const menu = menuRef.current
      if (!menu) return
      const viewport = window.visualViewport
      const height = menu.getBoundingClientRect().height
      const topEdge = (viewport?.offsetTop || 0) + 12
      const top = sessionMenu ? anchor.y - height - 8 : anchor.y + 8
      menu.style.top = `${Math.max(topEdge, Math.min(top, topEdge + (viewport?.height || window.innerHeight) - height - 24))}px`
    }
    update()
    window.addEventListener('resize', update)
    window.visualViewport?.addEventListener('resize', update)
    return () => { window.removeEventListener('resize', update); window.visualViewport?.removeEventListener('resize', update) }
  }, [sessionMenu, projectMenu])
  useEffect(() => {
    if (!sessionMenu && !projectMenu) return
    const close = (event: PointerEvent) => {
      const target = event.target as HTMLElement | null
      if (target?.closest('[role="menu"]')) return
      setSessionMenu(null)
      setProjectMenu(null)
    }
    document.addEventListener('pointerdown', close, true)
    return () => document.removeEventListener('pointerdown', close, true)
  }, [sessionMenu, projectMenu])

  const openSessionMenu = (session: Session, x: number, y: number) => {
    cancelHold()
    suppressSelect.current = true
    setProjectMenu(null)
    setSessionMenu({ session, x, y })
    hapticFeedback(20)
  }

  const handoffTargets = (session: Session) => {
    if (!agents) return []
    const source = agents[session.agent_id]
    if (!source || source.connection_id != null) return []
    return Object.values(agents).filter((agent) =>
      agent.kind !== source.kind && agent.connection_id == null && agent.status === 'ready',
    )
  }

  const row = (session: Session) => {
    const key = scopeKey(session.agent_id, session.id)
    const title = displaySessionTitle(session)
    const activityStatus = sessionActivityStatus(session, commands)
    const isRunning = activityStatus === 'running'
    const parentGroup = groups.find(g => g.sessions.some(s => s.id === session.id && s.agent_id === session.agent_id))
    const projectColor = (parentGroup && appearance[parentGroup.key]?.color) ||
      appearance[session.project_id || '']?.color ||
      (session.project_id ? appearance[`project:${session.project_id}`]?.color : undefined)
    const resolvedAccent = (projectColor && (PROJECT_APPEARANCE_COLORS[projectColor] || projectColor)) || undefined
    return <div
      className={`m-session-row m-hold ${key === selectedKey ? 'selected' : ''} ${isRunning ? 'is-running' : ''}`}
      key={key}
      style={{
        '--session-running-color': resolvedAccent || (session.agent_id.includes('codex') ? 'var(--astr-teal, #12b886)' : 'var(--astr-indigo, #5b6cff)'),
      } as CSSProperties}
    >
      <SessionActivityBorder status={activityStatus} />
      <button
        aria-label={title}
        onClick={() => {
          if (suppressSelect.current) { suppressSelect.current = false; return }
          onSelect(session)
        }}
        onContextMenu={(event: ReactMouseEvent<HTMLButtonElement>) => {
          event.preventDefault()
          openSessionMenu(session, event.clientX, event.clientY)
        }}
        onTouchStart={(event: ReactTouchEvent<HTMLButtonElement>) => {
          cancelHold()
          suppressSelect.current = false
          const point = event.touches[0]
          hold.current = setTimeout(() => {
            openSessionMenu(session, point.clientX, point.clientY)
            hapticFeedback(20)
          }, 500)
        }}
        onTouchMove={cancelHold}
        onTouchEnd={cancelHold}
        onTouchCancel={cancelHold}
      >
        {agents?.[session.agent_id] && (
          <span className="m-session-icon" aria-hidden="true">
            <AgentBrandIcon kind={agents[session.agent_id].kind} size={15} />
          </span>
        )}
        {parentGroup && appearance[parentGroup.key]?.icon && (
          <ProjectGlyph
            iconName={appearance[parentGroup.key]?.icon}
            colorName={appearance[parentGroup.key]?.color}
            size={13}
            style={{ opacity: 0.85 }}
          />
        )}
        <span>{title}</span>
        <small>{formatRelativeTime(session.updated_at)}</small>
        {activityStatus !== 'idle' && <StatusDot status={activityStatus} />}
      </button>
      <button className="m-session-more" aria-label={`会话操作 ${title}`} onClick={event => {
        event.stopPropagation()
        cancelHold()
        suppressSelect.current = false
        setProjectMenu(null)
        const rect = event.currentTarget.getBoundingClientRect()
        setSessionMenu({ session, x: rect.right, y: rect.bottom })
      }}><IconDotsVertical size={16} /></button>
    </div>
  }

  return <div className="m-project-list">
    {!!pinned.length && <section className="m-pinned-sessions" aria-label="置顶会话"><h3><IconPinned size={16} />置顶会话</h3>{pinned.map(row)}</section>}
    {groups.map((project) => {
      const regular = project.sessions.filter((session) => !pins[scopeKey(session.agent_id, session.id)])
      const collapsedNow = isCollapsed(project.key)
      const selectedIndex = regular.findIndex((session) => scopeKey(session.agent_id, session.id) === selectedKey)
      const showAll = expanded[project.key] || selectedIndex >= 5
      const visible = showAll ? regular : regular.slice(0, 5)
      return <section key={project.key}>
        <div className="m-project">
          <button
            className="m-project-toggle"
            aria-expanded={!collapsedNow}
            aria-label={project.label}
            onClick={() => toggleCollapsed(project.key)}
          >
            {collapsedNow ? <IconChevronRight size={15} /> : <IconChevronDown size={15} />}
            <ProjectGlyph iconName={appearance[project.key]?.icon} colorName={appearance[project.key]?.color} size={16} />
            <b>{project.label}</b><small>{project.remoteLabel}</small><span>{project.sessionCount}</span>
            {pinnedProjects.includes(project.key) && <IconPinned size={14} aria-label="已置顶" />}
          </button>
          <button
            className="m-project-more"
            aria-label={`项目操作 ${project.label}`}
            onClick={(event) => {
              event.stopPropagation()
              setSessionMenu(null)
              const rect = event.currentTarget.getBoundingClientRect()
              setProjectMenu({ project, x: rect.right, y: rect.bottom })
            }}
          >
            <IconDotsVertical size={16} />
          </button>
        </div>
        {!collapsedNow && <div className="m-project-sessions">
          {visible.map(row)}
          {regular.length > 5 && (
            <button
              className="m-disclosure-chip"
              onClick={() => setExpanded((prev) => ({ ...prev, [project.key]: !prev[project.key] }))}
              aria-label={showAll ? '收起更多会话' : `展开剩余 ${regular.length - 5} 个会话`}
            >
              <IconChevronDown
                size={14}
                style={{
                  transform: showAll ? 'rotate(180deg)' : 'none',
                  transition: 'transform 0.2s cubic-bezier(0.16, 1, 0.3, 1)',
                }}
              />
              <span>{showAll ? '收起会话' : `展开更多 (${regular.length - 5})`}</span>
            </button>
          )}
        </div>}
      </section>
    })}
    {sessionMenu && createPortal(
      <div ref={menuRef} role="menu" aria-label="会话操作" className="m-message-popover m-drawer-menu" style={{ '--menu-x': `${sessionMenu.x - 114}px` } as CSSProperties}>
        <button role="menuitem" onClick={() => { onPin(sessionMenu.session); setSessionMenu(null) }}>
          {pins[scopeKey(sessionMenu.session.agent_id, sessionMenu.session.id)] ? <IconPinned size={17} /> : <IconPin size={17} />}
          {pins[scopeKey(sessionMenu.session.agent_id, sessionMenu.session.id)] ? '取消置顶' : '置顶'}
        </button>
        {onRenameSession && (
          <button role="menuitem" onClick={() => { onRenameSession(sessionMenu.session); setSessionMenu(null) }}>
            <IconEdit size={17} />重命名
          </button>
        )}
        <button role="menuitem" onClick={() => { void (onCopySessionId ? onCopySessionId(sessionMenu.session) : navigator.clipboard?.writeText(sessionMenu.session.id)); setSessionMenu(null) }}>
          <IconCopy size={17} />复制 ID
        </button>
        {onHandoffSession && handoffTargets(sessionMenu.session).length > 0 && (
          <button
            role="menuitem"
            disabled={sessionMenu.session.status !== 'idle'}
            onClick={() => {
              onHandoffSession(sessionMenu.session)
              setSessionMenu(null)
            }}
          >
            <IconTransfer size={17} />转交
          </button>
        )}
        {onDeleteSession && <button role="menuitem" onClick={() => { onDeleteSession(sessionMenu.session, { clientX: sessionMenu.x, clientY: sessionMenu.y }); setSessionMenu(null) }}>
          <IconTrash size={17} />删除
        </button>}
      </div>,
      document.body,
    )}
    {projectMenu && createPortal(
      <div ref={menuRef} role="menu" aria-label={`项目操作 ${projectMenu.project.label}`} className="m-message-popover m-drawer-menu" style={{ '--menu-x': `${projectMenu.x - 114}px` } as CSSProperties}>
        <button role="menuitem" onClick={() => { onPinProject(projectMenu.project); setProjectMenu(null) }}>
          {pinnedProjects.includes(projectMenu.project.key) ? <IconPinned size={17} /> : <IconPin size={17} />}
          {pinnedProjects.includes(projectMenu.project.key) ? '取消置顶项目' : '置顶项目'}
        </button>
        {onCreate && <button role="menuitem" onClick={() => { onCreate(projectMenu.project); setProjectMenu(null) }}><IconPlus size={17} />新建会话</button>}
        {onDeleteProject && !projectMenu.project.key.startsWith('unmarked:') && (
          <button role="menuitem" onClick={(event) => { onDeleteProject(projectMenu.project, event); setProjectMenu(null) }}>
            <IconTrash size={17} />删除项目
          </button>
        )}
      </div>,
      document.body,
    )}
  </div>
}
