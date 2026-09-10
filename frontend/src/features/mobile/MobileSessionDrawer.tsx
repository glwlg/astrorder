import { useState } from 'react'
import { IconChevronDown, IconChevronRight, IconPin, IconPinned, IconPlus, IconTrash } from '@tabler/icons-react'
import type { Session } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { formatRelativeTime, type ProjectGroup } from '../../components/sessionRailModel'
import { ProjectGlyph, type ProjectAppearanceMap } from '../../components/projectAppearance'

export function MobileSessionDrawer({ groups, pins, selectedKey, appearance, onSelect, onPin, onCreate, onDeleteProject, onDeleteSession }: {
  groups: ProjectGroup[]; pins: Record<string, boolean>; selectedKey: string; appearance: ProjectAppearanceMap
  onSelect: (session: Session) => void; onPin: (session: Session) => void
  onCreate?: (project: ProjectGroup) => void
  onDeleteProject?: (project: ProjectGroup) => void
  onDeleteSession?: (session: Session) => void
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const pinned = groups.flatMap(group => group.sessions).filter(s => pins[scopeKey(s.agent_id, s.id)])
  const row = (session: Session) => {
    const key = scopeKey(session.agent_id, session.id)
    return <div className={`m-session-row ${key === selectedKey ? 'selected' : ''}`} key={key}>
      <button onClick={() => onSelect(session)}><span>{session.title || session.id}</span><small>{formatRelativeTime(session.updated_at)}</small></button>
      <button aria-label={`${pins[key] ? '取消置顶' : '置顶'} ${session.title}`} className={pins[key] ? 'm-pin-active' : ''} onClick={() => onPin(session)}>{pins[key] ? <IconPinned size={17} /> : <IconPin size={17} />}</button>
      {onDeleteSession && (
        <button
          aria-label={`删除会话 ${session.title || session.id}`}
          className="m-session-delete"
          onClick={(e) => {
            e.stopPropagation()
            onDeleteSession(session)
          }}
        >
          <IconTrash size={16} />
        </button>
      )}
    </div>
  }
  return <div className="m-project-list">
    {!!pinned.length && <section className="m-pinned-sessions" aria-label="置顶会话"><h3><IconPinned size={16} />置顶会话</h3>{pinned.map(row)}</section>}
    {groups.map(project => {
      const regular = project.sessions.filter(s => !pins[scopeKey(s.agent_id, s.id)])
      return <section key={project.key}>
        <button className="m-project" onClick={() => setCollapsed(prev => ({ ...prev, [project.key]: !prev[project.key] }))}>
          {collapsed[project.key] ? <IconChevronRight size={15} /> : <IconChevronDown size={15} />}
          <ProjectGlyph iconName={appearance[project.key]?.icon} colorName={appearance[project.key]?.color} size={16} />
          <b>{project.label}</b><small>{project.remoteLabel}</small><span>{project.sessionCount}</span>
        </button>
        {!collapsed[project.key] && <div className="m-project-sessions">
          <div className="m-project-actions">
            {onCreate && <button className="m-disclosure" onClick={() => onCreate(project)}><IconPlus size={15} />新建会话</button>}
            {onDeleteProject && !project.key.startsWith('unmarked:') && (
              <button
                className="m-disclosure m-disclosure-danger"
                onClick={() => onDeleteProject(project)}
                aria-label={`删除项目 ${project.label}`}
              >
                <IconTrash size={15} />删除项目
              </button>
            )}
          </div>
          {(expanded[project.key] ? regular : regular.slice(0, 5)).map(row)}
          {regular.length > 5 && <button className="m-disclosure" onClick={() => setExpanded(prev => ({ ...prev, [project.key]: !prev[project.key] }))}>{expanded[project.key] ? '收起' : `展开显示 ${regular.length - 5}`}</button>}
        </div>}
      </section>
    })}
  </div>
}
