import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent as ReactMouseEvent, type TouchEvent as ReactTouchEvent } from 'react'
import { createPortal } from 'react-dom'
import { IconCheck, IconDotsVertical, IconPlus, IconTrash, IconUsers } from '@tabler/icons-react'
import type { Agent, BotGroup } from '../../domain/types'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { StatusDot } from '../../components/Status'
import { hapticFeedback } from './mobileGestures'
import './mobileMessageMenu.css'

type GroupMenu = { group: BotGroup; x: number; y: number }

function isGroupRunning(group: BotGroup): boolean {
  if (group.coordination?.status === 'running') return true
  if (group.active_speaker_agent_id) return true
  if ((group.active_speakers?.length ?? 0) > 0) return true
  return Object.values(group.member_states || {}).some((s) => s.status === 'running')
}

export function MobileGroupDrawer({
  groups,
  selectedGroupId,
  agents,
  loading,
  search,
  onSelect,
  onCreate,
  onDeleteGroup,
}: {
  groups: BotGroup[]
  selectedGroupId: string
  agents: Record<string, Agent>
  loading?: boolean
  search: string
  onSelect: (group: BotGroup) => void
  onCreate: () => void
  onDeleteGroup?: (group: BotGroup, event?: { clientX: number; clientY: number }) => void
}) {
  const [groupMenu, setGroupMenu] = useState<GroupMenu | null>(null)
  const suppressSelect = useRef(false)
  const hold = useRef<ReturnType<typeof setTimeout> | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const cancelHold = () => {
    if (hold.current) clearTimeout(hold.current)
    hold.current = null
  }
  useEffect(() => () => cancelHold(), [])

  useLayoutEffect(() => {
    if (!groupMenu) return
    const update = () => {
      const menu = menuRef.current
      if (!menu) return
      const viewport = window.visualViewport
      const height = menu.getBoundingClientRect().height
      const topEdge = (viewport?.offsetTop || 0) + 12
      const top = groupMenu.y + 8
      menu.style.top = `${Math.max(topEdge, Math.min(top, topEdge + (viewport?.height || window.innerHeight) - height - 24))}px`
    }
    update()
    window.addEventListener('resize', update)
    window.visualViewport?.addEventListener('resize', update)
    return () => {
      window.removeEventListener('resize', update)
      window.visualViewport?.removeEventListener('resize', update)
    }
  }, [groupMenu])

  useEffect(() => {
    if (!groupMenu) return
    const close = (event: PointerEvent) => {
      const target = event.target as HTMLElement | null
      if (target?.closest('[role="menu"]')) return
      setGroupMenu(null)
    }
    document.addEventListener('pointerdown', close, true)
    return () => document.removeEventListener('pointerdown', close, true)
  }, [groupMenu])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return groups
    return groups.filter((group) => {
      if (group.name.toLowerCase().includes(q)) return true
      if (group.description?.toLowerCase().includes(q)) return true
      return group.members.some((member) => {
        const ag = agents[member.agent_id]
        return (
          member.alias?.toLowerCase().includes(q) ||
          member.name?.toLowerCase().includes(q) ||
          ag?.name?.toLowerCase().includes(q)
        )
      })
    })
  }, [groups, search, agents])

  const openGroupMenu = (group: BotGroup, x: number, y: number) => {
    cancelHold()
    suppressSelect.current = true
    setGroupMenu({ group, x, y })
    hapticFeedback(20)
  }

  return (
    <div className="m-project-list m-group-drawer-list">
      {loading && groups.length === 0 ? (
        <div className="m-drawer-empty">正在加载群聊…</div>
      ) : filtered.length === 0 ? (
        <div className="m-drawer-empty">
          <IconUsers size={26} stroke={1.5} aria-hidden="true" />
          <span>{search.trim() ? '未找到匹配的群聊' : '暂无群聊会话'}</span>
          {!search.trim() && (
            <button type="button" className="m-drawer-empty-cta" onClick={onCreate}>
              <IconPlus size={15} />
              <span>新建群聊</span>
            </button>
          )}
        </div>
      ) : (
        filtered.map((group) => {
          const isSelected = group.id === selectedGroupId
          const running = isGroupRunning(group)
          const mutedCount = Object.values(group.member_states || {}).filter((s) => s.muted).length
          return (
            <div
              key={group.id}
              className={`m-group-drawer-row m-hold${isSelected ? ' is-selected' : ''}${running ? ' is-running' : ''}`}
            >
              <button
                type="button"
                className="m-group-drawer-main"
                aria-label={group.name}
                aria-current={isSelected ? 'page' : undefined}
                onClick={() => {
                  if (suppressSelect.current) {
                    suppressSelect.current = false
                    return
                  }
                  onSelect(group)
                }}
                onContextMenu={(event: ReactMouseEvent<HTMLButtonElement>) => {
                  if (!onDeleteGroup) return
                  event.preventDefault()
                  openGroupMenu(group, event.clientX, event.clientY)
                }}
                onTouchStart={(event: ReactTouchEvent<HTMLButtonElement>) => {
                  if (!onDeleteGroup) return
                  cancelHold()
                  suppressSelect.current = false
                  const point = event.touches[0]
                  hold.current = setTimeout(() => {
                    openGroupMenu(group, point.clientX, point.clientY)
                  }, 500)
                }}
                onTouchMove={cancelHold}
                onTouchEnd={cancelHold}
                onTouchCancel={cancelHold}
              >
                <span className="m-group-drawer-icon" aria-hidden="true">
                  <IconUsers size={16} />
                </span>
                <span className="m-group-drawer-copy">
                  <span className="m-group-drawer-title-row">
                    <strong>{group.name}</strong>
                    <span className="m-group-drawer-count">{group.members.length}人</span>
                  </span>
                  <span className="m-group-drawer-meta">
                    <span className="m-group-drawer-avatars" aria-hidden="true">
                      {group.members.slice(0, 4).map((member) => {
                        const ag = agents[member.agent_id]
                        return (
                          <span key={member.id || member.agent_id} className="m-group-drawer-avatar">
                            <AgentBrandIcon kind={ag?.kind} size={13} />
                          </span>
                        )
                      })}
                      {group.members.length > 4 && (
                        <small className="m-group-drawer-more-count">+{group.members.length - 4}</small>
                      )}
                    </span>
                    <small className="m-group-drawer-sub">
                      {running
                        ? '接力进行中'
                        : group.description
                          ? group.description
                          : mutedCount > 0
                            ? `${mutedCount} 禁言`
                            : '多 Agent 群聊'}
                    </small>
                  </span>
                </span>
                {running ? (
                  <StatusDot status="running" />
                ) : (
                  isSelected && <IconCheck size={16} className="m-group-row-check" aria-hidden="true" />
                )}
              </button>
              {onDeleteGroup && (
                <button
                  type="button"
                  className="m-session-more"
                  aria-label={`群聊操作 ${group.name}`}
                  onClick={(event) => {
                    event.stopPropagation()
                    cancelHold()
                    suppressSelect.current = false
                    const rect = event.currentTarget.getBoundingClientRect()
                    setGroupMenu({ group, x: rect.right, y: rect.bottom })
                  }}
                >
                  <IconDotsVertical size={16} />
                </button>
              )}
            </div>
          )
        })
      )}
      {groupMenu && onDeleteGroup && createPortal(
        <div
          ref={menuRef}
          role="menu"
          aria-label={`群聊操作 ${groupMenu.group.name}`}
          className="m-message-popover m-drawer-menu"
          style={{ '--menu-x': `${groupMenu.x - 114}px` } as CSSProperties}
        >
          <button
            type="button"
            role="menuitem"
            onClick={(event) => {
              onDeleteGroup(groupMenu.group, { clientX: event.clientX || groupMenu.x, clientY: event.clientY || groupMenu.y })
              setGroupMenu(null)
            }}
          >
            <IconTrash size={17} />解散群聊
          </button>
        </div>,
        document.body,
      )}
    </div>
  )
}
