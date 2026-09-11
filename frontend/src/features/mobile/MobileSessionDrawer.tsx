import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent, type TouchEvent as ReactTouchEvent } from 'react'
import { createPortal } from 'react-dom'
import { IconChevronDown, IconChevronRight, IconDotsVertical, IconPin, IconPinned, IconPlus, IconTrash } from '@tabler/icons-react'
import type { Session } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { displaySessionTitle, formatRelativeTime, sessionActivityStatus, type ProjectGroup } from '../../components/sessionRailModel'
import { ProjectGlyph, type ProjectAppearanceMap } from '../../components/projectAppearance'
import { useAstrorderStore } from '../../state/store'
import { useShallow } from 'zustand/react/shallow'
import './mobileMessageMenu.css'

type SessionMenu = { session: Session; x: number; y: number }
type ProjectMenu = { project: ProjectGroup; x: number; y: number }

export function MobileSessionDrawer({ groups, pins, selectedKey, appearance, onSelect, onPin, onCreate, onDeleteProject, onDeleteSession }: {
  groups: ProjectGroup[]; pins: Record<string, boolean>; selectedKey: string; appearance: ProjectAppearanceMap
  onSelect: (session: Session) => void; onPin: (session: Session) => void
  onCreate?: (project: ProjectGroup) => void
  onDeleteProject?: (project: ProjectGroup, event?: { clientX: number; clientY: number }) => void
  onDeleteSession?: (session: Session, event?: { clientX: number; clientY: number }) => void
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [sessionMenu, setSessionMenu] = useState<SessionMenu | null>(null)
  const [projectMenu, setProjectMenu] = useState<ProjectMenu | null>(null)
  const suppressSelect = useRef(false)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  const commands = useAstrorderStore(useShallow((state) => state.commands))
  useAstrorderStore((state) => state.messages)
  useAstrorderStore((state) => state.tasks)
  const currentProjectKey = groups.find((group) => group.sessions.some((session) => scopeKey(session.agent_id, session.id) === selectedKey))?.key
  const isCollapsed = (key: string) => collapsed[key] ?? key !== currentProjectKey
  const pinned = groups.flatMap((group) => group.sessions).filter((session) => pins[scopeKey(session.agent_id, session.id)])
  const cancelHold = () => { if (hold.current) clearTimeout(hold.current); hold.current = null }
  useEffect(() => () => cancelHold(), [])
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
    navigator.vibrate?.(20)
  }

  const row = (session: Session) => {
    const key = scopeKey(session.agent_id, session.id)
    const title = displaySessionTitle(session)
    const isRunning = sessionActivityStatus(session, commands) === 'running'
    const projectColor = appearance[session.project_id || '']?.color
    return <div className={`m-session-row ${key === selectedKey ? 'selected' : ''} ${isRunning ? 'is-running' : ''}`} key={key}>
      <span
        aria-hidden="true"
        className="arc-border arc-row session-running-arc"
        style={{
          '--arc-c1': projectColor || (session.agent_id.includes('codex') ? 'var(--astr-teal, #12b886)' : 'var(--astr-indigo, #6366f1)'),
          '--arc-radius': '7px',
        } as React.CSSProperties}
      />
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
          const point = event.touches[0]
          hold.current = setTimeout(() => openSessionMenu(session, point.clientX, point.clientY), 500)
        }}
        onTouchMove={cancelHold}
        onTouchEnd={cancelHold}
        onTouchCancel={cancelHold}
      >
        <span>{title}</span>
        <small>{formatRelativeTime(session.updated_at)}</small>
      </button>
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
            onClick={() => setCollapsed((prev) => ({ ...prev, [project.key]: !collapsedNow }))}
          >
            {collapsedNow ? <IconChevronRight size={15} /> : <IconChevronDown size={15} />}
            <ProjectGlyph iconName={appearance[project.key]?.icon} colorName={appearance[project.key]?.color} size={16} />
            <b>{project.label}</b><small>{project.remoteLabel}</small><span>{project.sessionCount}</span>
          </button>
          <button
            className="m-project-more"
            aria-label={`项目操作 ${project.label}`}
            onClick={(event) => {
              event.stopPropagation()
              setSessionMenu(null)
              setProjectMenu({ project, x: event.clientX, y: event.clientY })
            }}
          >
            <IconDotsVertical size={16} />
          </button>
        </div>
        {!collapsedNow && <div className="m-project-sessions">
          {visible.map(row)}
          {regular.length > 5 && <button className="m-disclosure" onClick={() => setExpanded((prev) => ({ ...prev, [project.key]: !prev[project.key] }))}>{showAll ? '收起' : `展开显示 ${regular.length - 5}`}</button>}
        </div>}
      </section>
    })}
    {sessionMenu && createPortal(
      <div role="menu" aria-label="会话操作" className="m-message-popover" style={{ left: Math.max(12, sessionMenu.x - 114), top: Math.max(12, sessionMenu.y - 64) }}>
        <button role="menuitem" onClick={() => { onPin(sessionMenu.session); setSessionMenu(null) }}>
          {pins[scopeKey(sessionMenu.session.agent_id, sessionMenu.session.id)] ? <IconPinned size={17} /> : <IconPin size={17} />}
          {pins[scopeKey(sessionMenu.session.agent_id, sessionMenu.session.id)] ? '取消置顶' : '置顶'}
        </button>
        {onDeleteSession && <button role="menuitem" onClick={() => { onDeleteSession(sessionMenu.session, { clientX: sessionMenu.x, clientY: sessionMenu.y }); setSessionMenu(null) }}>
          <IconTrash size={17} />删除
        </button>}
      </div>,
      document.body,
    )}
    {projectMenu && createPortal(
      <div role="menu" aria-label={`项目操作 ${projectMenu.project.label}`} className="m-message-popover" style={{ left: Math.max(12, projectMenu.x - 114), top: Math.max(12, projectMenu.y + 8) }}>
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
