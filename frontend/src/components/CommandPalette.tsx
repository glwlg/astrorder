import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowRight,
  IconBolt,
  IconChartBar,
  IconDatabase,
  IconFolder,
  IconHome,
  IconId,
  IconLayoutGrid,
  IconLayoutSidebarLeftCollapse,
  IconMoon,
  IconSearch,
  IconSend,
  IconSettings,
  IconSun,
  IconUsers,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { api } from '../api/client'
import { newCommandId, scopeKey } from '../domain/semantics'
import type { Session } from '../domain/types'
import { selectProjects, selectSessions, useAstrorderStore } from '../state/store'
import { AgentBrandIcon } from './AgentBrandIcon'
import { displaySessionTitle, sessionActivityStatus } from './sessionRailModel'
import './commandPalette.css'

type PaletteCategory = 'session' | 'route' | 'action' | 'project'

interface PaletteItem {
  id: string
  category: PaletteCategory
  title: string
  subtitle?: string
  keywords: string
  badge?: string
  running?: boolean
  session?: Session
  agentKind?: string
  onRun: () => void
}

interface CommandPaletteProps {
  opened: boolean
  onClose: () => void
  onToggleSidebar: () => void
  onOpenSettings: () => void
}

export function CommandPalette({ opened, onClose, onToggleSidebar, onOpenSettings }: CommandPaletteProps) {
  const navigate = useNavigate()
  const { setColorScheme } = useMantineColorScheme()
  const computedScheme = useComputedColorScheme('light')
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agents = useAstrorderStore((state) => state.agents)

  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [dispatchTarget, setDispatchTarget] = useState<Session | null>(null)
  const [dispatchText, setDispatchText] = useState('')
  const [dispatching, setDispatching] = useState(false)

  const inputRef = useRef<HTMLInputElement>(null)
  const dispatchRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const keyboardNavigatingRef = useRef(false)

  useEffect(() => {
    if (!opened) return
    setQuery('')
    setSelectedIndex(0)
    setDispatchTarget(null)
    setDispatchText('')
    keyboardNavigatingRef.current = false
    requestAnimationFrame(() => inputRef.current?.focus())
  }, [opened])

  useEffect(() => {
    if (dispatchTarget) {
      requestAnimationFrame(() => dispatchRef.current?.focus())
    }
  }, [dispatchTarget])

  const allItems = useMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = []

    // 1. Sessions (running floated first)
    const sortedSessions = [...sessions].sort((a, b) => {
      const aRun = sessionActivityStatus(a) === 'running' ? 1 : 0
      const bRun = sessionActivityStatus(b) === 'running' ? 1 : 0
      if (aRun !== bRun) return bRun - aRun
      return (b.updated_at || '').localeCompare(a.updated_at || '')
    })

    for (const session of sortedSessions) {
      const agent = agents[session.agent_id]
      const title = displaySessionTitle(session)
      const running = sessionActivityStatus(session) === 'running'
      items.push({
        id: `session:${scopeKey(session.agent_id, session.id)}`,
        category: 'session',
        title,
        subtitle: [session.project_name, agent?.name || session.agent_id].filter(Boolean).join(' · '),
        keywords: `${title} ${session.project_name || ''} ${session.workspace || ''} ${agent?.name || ''} ${agent?.kind || ''}`,
        badge: running ? '运行中' : undefined,
        running,
        session,
        agentKind: agent?.kind,
        onRun: () => {
          onClose()
          navigate(`/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`)
        },
      })
    }

    // 2. Routes
    const routes: Array<{ id: string; title: string; subtitle: string; to: string; keywords: string }> = [
      { id: 'route:welcome', title: '星序指挥台（欢迎页）', subtitle: '/chat', to: '/chat', keywords: 'welcome home 指挥台 欢迎页 首页' },
      { id: 'route:matrix', title: '矩阵（多会话网格）', subtitle: '/monitor', to: '/monitor', keywords: 'matrix monitor 矩阵 监控室 多窗' },
      { id: 'route:groups', title: '群聊协作', subtitle: '/groups', to: '/groups', keywords: 'groups chat 群聊 接力' },
      { id: 'route:analytics', title: '用量统计', subtitle: '/analytics', to: '/analytics', keywords: 'analytics usage token 统计 网关' },
      { id: 'route:backups', title: '文件与目录备份', subtitle: '/backups', to: '/backups', keywords: 'backups 备份 快照' },
      { id: 'route:accounts', title: 'Agent 账号管理', subtitle: '/agent-accounts', to: '/agent-accounts', keywords: 'accounts quota 账号 额度' },
    ]
    for (const r of routes) {
      items.push({
        id: r.id,
        category: 'route',
        title: r.title,
        subtitle: r.subtitle,
        keywords: `${r.title} ${r.keywords}`,
        onRun: () => {
          onClose()
          navigate(r.to, r.to === '/chat' ? { state: { welcome: true } } : undefined)
        },
      })
    }

    // 3. Actions
    items.push(
      {
        id: 'action:sidebar',
        category: 'action',
        title: '展开 / 收起左侧会话栏',
        subtitle: 'Ctrl+B · Arc 悬浮抽屉模式',
        keywords: 'sidebar collapse expand 侧栏 收起 展开 arc',
        onRun: () => {
          onClose()
          onToggleSidebar()
        },
      },
      {
        id: 'action:theme',
        category: 'action',
        title: computedScheme === 'dark' ? '切换至浅色星雾主题' : '切换至深色星曜主题',
        subtitle: '外观主题',
        keywords: 'theme dark light 主题 深色 浅色',
        onRun: () => {
          onClose()
          setColorScheme(computedScheme === 'dark' ? 'light' : 'dark')
        },
      },
      {
        id: 'action:settings',
        category: 'action',
        title: '打开系统设置',
        subtitle: '连接、模型、快捷键、插件',
        keywords: 'settings preferences 设置 连接 快捷键',
        onRun: () => {
          onClose()
          onOpenSettings()
        },
      },
    )

    // 4. Projects (open Matrix filtered to project)
    const seenProjects = new Set<string>()
    for (const project of projects) {
      const name = (project.project_name || project.workspace?.split(/[/\\]/).filter(Boolean).pop() || '').trim()
      if (!name || seenProjects.has(name)) continue
      seenProjects.add(name)
      items.push({
        id: `project:${name}`,
        category: 'project',
        title: `在矩阵中聚焦项目：${name}`,
        subtitle: project.workspace || undefined,
        keywords: `project matrix 项目 矩阵 ${name} ${project.workspace || ''}`,
        onRun: () => {
          onClose()
          navigate(`/monitor?project=${encodeURIComponent(name)}`)
        },
      })
    }

    return items
  }, [agents, computedScheme, navigate, onClose, onOpenSettings, onToggleSidebar, projects, sessions, setColorScheme])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) {
      // Balanced default shortlist: top 6 sessions + key routes + top actions
      const topSessions = allItems.filter((i) => i.category === 'session').slice(0, 6)
      const topRoutes = allItems.filter((i) => i.category === 'route').slice(0, 4)
      const topActions = allItems.filter((i) => i.category === 'action').slice(0, 3)
      return [...topSessions, ...topRoutes, ...topActions]
    }
    const tokens = q.split(/\s+/).filter(Boolean)
    return allItems
      .filter((item) => {
        const hay = item.keywords.toLowerCase()
        return tokens.every((tok) => hay.includes(tok))
      })
      .slice(0, 24)
  }, [allItems, query])

  const clampedIndex = filtered.length > 0 ? Math.min(selectedIndex, filtered.length - 1) : 0

  useEffect(() => {
    if (!opened || dispatchTarget) return
    const list = listRef.current
    const activeEl = list?.querySelector<HTMLElement>(`[data-palette-index="${clampedIndex}"]`)
    if (!list || !activeEl) return
    if (list.scrollHeight > list.clientHeight) {
      const listRect = list.getBoundingClientRect()
      const itemRect = activeEl.getBoundingClientRect()
      if (itemRect.bottom > listRect.bottom) {
        list.scrollTop += itemRect.bottom - listRect.bottom + 6
      } else if (itemRect.top < listRect.top) {
        list.scrollTop -= listRect.top - itemRect.top + 6
      }
    }
    if (typeof activeEl.scrollIntoView === 'function') {
      activeEl.scrollIntoView({ block: 'nearest' })
    }
  }, [clampedIndex, dispatchTarget, opened])

  const handleQuickDispatch = useCallback(async () => {
    if (!dispatchTarget || !dispatchText.trim() || dispatching) return
    setDispatching(true)
    try {
      const cmd = await api.createCommand({
        id: newCommandId(),
        agent_id: dispatchTarget.agent_id,
        session_id: dispatchTarget.id,
        action: 'send',
        text: dispatchText.trim(),
        attachment_ids: [],
        target_id: null,
      })
      useAstrorderStore.getState().mergeCommands([cmd])
      notifications.show({
        color: 'teal',
        message: `已向「${displaySessionTitle(dispatchTarget)}」发送后台指令`,
      })
      onClose()
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '指令派发失败',
      })
    } finally {
      setDispatching(false)
    }
  }, [dispatchTarget, dispatchText, dispatching, onClose])

  const handleSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
      return
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      keyboardNavigatingRef.current = true
      setSelectedIndex((i) => (filtered.length ? (i + 1) % filtered.length : 0))
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      keyboardNavigatingRef.current = true
      setSelectedIndex((i) => (filtered.length ? (i - 1 + filtered.length) % filtered.length : 0))
      return
    }
    if (event.key === 'Tab') {
      const active = filtered[clampedIndex]
      if (active?.session) {
        event.preventDefault()
        setDispatchTarget(active.session)
      }
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      if (event.nativeEvent.isComposing || event.keyCode === 229) return
      filtered[clampedIndex]?.onRun()
    }
  }

  const handleDispatchKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      setDispatchTarget(null)
      requestAnimationFrame(() => inputRef.current?.focus())
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      if (event.nativeEvent.isComposing || event.keyCode === 229) return
      void handleQuickDispatch()
    }
  }

  if (!opened) return null

  const categoryLabel: Record<PaletteCategory, string> = {
    session: '会话',
    route: '视图',
    action: '动作',
    project: '项目',
  }

  const renderIcon = (item: PaletteItem) => {
    if (item.category === 'session') return <AgentBrandIcon kind={item.agentKind} size={15} />
    if (item.category === 'project') return <IconFolder size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:welcome') return <IconHome size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:matrix') return <IconLayoutGrid size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:groups') return <IconUsers size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:analytics') return <IconChartBar size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:backups') return <IconDatabase size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:accounts') return <IconId size={15} color="var(--astr-indigo)" />
    if (item.id === 'action:sidebar') return <IconLayoutSidebarLeftCollapse size={15} color="var(--astr-indigo)" />
    if (item.id === 'action:theme') return computedScheme === 'dark' ? <IconSun size={15} color="var(--astr-indigo)" /> : <IconMoon size={15} color="var(--astr-indigo)" />
    if (item.id === 'action:settings') return <IconSettings size={15} color="var(--astr-indigo)" />
    return <IconBolt size={15} color="var(--astr-indigo)" />
  }

  return (
    <div
      className="command-palette-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        className="command-palette-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="星序全能星令面板"
        data-liquid-surface="panel"
        data-liquid-snapshot="#astr-shell-sky"
      >
        {dispatchTarget ? (
          <div className="command-palette-dispatch">
            <div className="command-palette-dispatch-head">
              <AgentBrandIcon kind={agents[dispatchTarget.agent_id]?.kind} size={15} />
              <strong>快发后台指令 → {displaySessionTitle(dispatchTarget)}</strong>
              <button type="button" className="command-palette-back" onClick={() => setDispatchTarget(null)}>
                返回 (Esc)
              </button>
            </div>
            <div className="command-palette-input-row">
              <IconSend size={16} color="var(--astr-indigo)" aria-hidden="true" />
              <input
                ref={dispatchRef}
                className="command-palette-input"
                aria-label="输入后台指令内容"
                placeholder="输入要直接派发给该会话的指令，按 Enter 立即发送…"
                value={dispatchText}
                onChange={(e) => setDispatchText(e.target.value)}
                onKeyDown={handleDispatchKeyDown}
                disabled={dispatching}
              />
              <button
                type="button"
                className="command-palette-send-btn"
                disabled={dispatching || !dispatchText.trim()}
                onClick={() => void handleQuickDispatch()}
              >
                {dispatching ? '发送中…' : '发送'}
              </button>
            </div>
          </div>
        ) : (
          <>
            <div className="command-palette-input-row">
              <IconSearch size={16} color="var(--astr-muted)" aria-hidden="true" />
              <input
                ref={inputRef}
                className="command-palette-input"
                role="combobox"
                aria-expanded="true"
                aria-controls="command-palette-listbox"
                aria-activedescendant={filtered.length > 0 ? `command-palette-opt-${clampedIndex}` : undefined}
                aria-label="搜索会话、项目、视图或快捷命令"
                placeholder="搜索会话、项目、视图或动作…（选中会话按 Tab 可不切页快发指令）"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value)
                  setSelectedIndex(0)
                }}
                onKeyDown={handleSearchKeyDown}
              />
              <kbd className="command-palette-kbd">Esc</kbd>
            </div>

            <div
              ref={listRef}
              id="command-palette-listbox"
              className="command-palette-list"
              role="listbox"
              onPointerMove={() => {
                keyboardNavigatingRef.current = false
              }}
            >
              {filtered.length === 0 ? (
                <div className="command-palette-empty">未找到匹配的会话、项目或星令</div>
              ) : (
                filtered.map((item, idx) => {
                  const active = idx === clampedIndex
                  return (
                    <div
                      key={item.id}
                      id={`command-palette-opt-${idx}`}
                      data-palette-index={idx}
                      role="option"
                      aria-selected={active}
                      className={`command-palette-item${active ? ' is-active' : ''}`}
                      onMouseMove={() => {
                        if (!keyboardNavigatingRef.current && selectedIndex !== idx) {
                          setSelectedIndex(idx)
                        }
                      }}
                      onClick={item.onRun}
                    >
                      <span className="command-palette-icon" aria-hidden="true">{renderIcon(item)}</span>
                      <div className="command-palette-copy">
                        <span className="command-palette-title">{item.title}</span>
                        {item.subtitle && <span className="command-palette-sub">{item.subtitle}</span>}
                      </div>
                      {item.running && <span className="command-palette-running">运行中</span>}
                      {item.session && (
                        <button
                          type="button"
                          className="command-palette-quick-send"
                          title="不切换页面直接发指令 (Tab)"
                          onClick={(e) => {
                            e.stopPropagation()
                            setDispatchTarget(item.session!)
                          }}
                        >
                          <IconSend size={11} aria-hidden="true" />
                          <span>快发</span>
                        </button>
                      )}
                      <span className="command-palette-cat">{categoryLabel[item.category]}</span>
                      {active && <IconArrowRight size={13} color="var(--astr-indigo)" aria-hidden="true" />}
                    </div>
                  )
                })
              )}
            </div>

            <div className="command-palette-footer">
              <span><kbd>↑↓</kbd> 选择</span>
              <span><kbd>Enter</kbd> 执行</span>
              <span><kbd>Tab</kbd> 原地快发指令</span>
              <span><kbd>Ctrl+K</kbd> 唤起</span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
