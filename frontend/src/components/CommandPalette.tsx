import { useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type KeyboardEvent } from 'react'
import { useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowRight,
  IconBolt,
  IconFolder,
  IconHome,
  IconInbox,
  IconLayoutSidebarLeftCollapse,
  IconMessageSearch,
  IconMoon,
  IconSearch,
  IconSend,
  IconSettings,
  IconSun,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { api, type MessageSearchHit } from '../api/client'
import { newCommandId, scopeKey } from '../domain/semantics'
import type { Session } from '../domain/types'
import { selectProjects, selectSessions, useAstrorderStore } from '../state/store'
import { useActionInbox } from '../features/inbox/useActionInbox'
import { registerBuiltinWorkspacePages } from '../features/extensions/builtinWorkspacePages'
import { extensionRegistry, useExtensionRegistryVersion } from '../features/extensions/extensionRegistry'
import { resolveExtensionPagePath, type ExtensionPageCommandSpec } from '../features/extensions/types'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import { useIsOfficeProfile } from '../features/office/appProfile'
import { AgentBrandIcon } from './AgentBrandIcon'
import { displaySessionTitle, formatRelativeTime, resolveSessionDisplayTitle, sessionActivityStatus } from './sessionRailModel'
import './commandPalette.css'

registerBuiltinWorkspacePages()

type PaletteCategory = 'session' | 'message' | 'route' | 'action' | 'project'

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
  icon?: ComponentType<{ size?: number; color?: string; className?: string }>
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
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const isOfficeProfile = useIsOfficeProfile()

  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [dispatchTarget, setDispatchTarget] = useState<Session | null>(null)
  const [dispatchText, setDispatchText] = useState('')
  const [dispatching, setDispatching] = useState(false)
  const [messageHits, setMessageHits] = useState<MessageSearchHit[]>([])
  const [searchingMessages, setSearchingMessages] = useState(false)
  const { totalCount: inboxCount } = useActionInbox()

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
    setMessageHits([])
    setSearchingMessages(false)
    keyboardNavigatingRef.current = false
    requestAnimationFrame(() => inputRef.current?.focus())
  }, [opened])

  useEffect(() => {
    const trimmed = query.trim()
    if (!opened || trimmed.length < 2) {
      setMessageHits([])
      setSearchingMessages(false)
      return
    }
    let cancelled = false
    setSearchingMessages(true)
    const timer = window.setTimeout(() => {
      void api
        .searchMessages(trimmed, 10)
        .then((res) => {
          if (!cancelled) setMessageHits(res.items || [])
        })
        .catch(() => {
          if (!cancelled) setMessageHits([])
        })
        .finally(() => {
          if (!cancelled) setSearchingMessages(false)
        })
    }, 200)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [opened, query])

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

    // 2. Routes (core + extensionRegistry active pages)
    void registryVersion
    const activePages = extensionRegistry.getActivePages({
      profile: isOfficeProfile ? 'office' : 'developer',
      enabledPlugins,
    })
    const activeCommands = extensionRegistry.getActiveCommands({
      profile: isOfficeProfile ? 'office' : 'developer',
      enabledPlugins,
    })
    const monitorEnabled = activePages.some((page) => page.id === 'monitor')

    const routes: Array<{
      id: string
      title: string
      subtitle: string
      to: string
      keywords: string
      badge?: string
      icon?: ComponentType<{ size?: number; color?: string; className?: string }>
    }> = [
      ...(!isOfficeProfile ? [{ id: 'route:bots', title: 'Bot 工作台', subtitle: '/bots', to: '/bots', keywords: 'bot bots 机器人 agents skill 孵化 派单' }] : []),
      { id: 'route:inbox', title: isOfficeProfile ? '待办清单' : '待我决策（行动收件箱）', subtitle: '/inbox', to: '/inbox', keywords: 'inbox action todo 待办 清单 决策 审批 收件箱', badge: inboxCount > 0 ? `${inboxCount} 项` : undefined },
      { id: 'route:welcome', title: isOfficeProfile ? '首页工作台' : '星序指挥台（欢迎页）', subtitle: '/chat', to: '/chat', keywords: 'welcome home 工作台 指挥台 欢迎页 首页' },
    ]
    for (const page of activePages) {
      if (page.command === false) continue
      const pagePath = resolveExtensionPagePath(page)
      const specs: ExtensionPageCommandSpec[] = page.command
        ? (Array.isArray(page.command) ? page.command : [page.command])
        : [{
            id: `ext-page:${page.pluginId}:${page.id}`,
            title: page.title,
            subtitle: pagePath,
            to: pagePath,
            keywords: `${page.title} ${page.navLabel || ''} ${page.pluginId} ${page.id}`,
            icon: page.mobileIcon || page.icon,
          }]
      for (const spec of specs) {
        if (isOfficeProfile && spec.hideInOfficeMode) continue
        const target = spec.to || pagePath
        routes.push({
          id: spec.id || `ext-page:${page.pluginId}:${page.id}`,
          title: spec.title || page.title,
          subtitle: spec.subtitle || target,
          to: target,
          keywords: spec.keywords || `${page.title} ${page.navLabel || ''} ${page.pluginId} ${page.id}`,
          icon: spec.icon || page.mobileIcon || page.icon,
        })
      }
    }
    for (const r of routes) {
      items.push({
        id: r.id,
        category: 'route',
        title: r.title,
        subtitle: r.subtitle,
        keywords: `${r.title} ${r.keywords}`,
        badge: r.badge,
        icon: r.icon,
        onRun: () => {
          onClose()
          navigate(r.to, r.to === '/chat' ? { state: { welcome: true } } : undefined)
        },
      })
    }

    // 3. Actions (core + extensionRegistry active commands)
    items.push(
      {
        id: 'action:sidebar',
        category: 'action',
        title: isOfficeProfile ? '展开 / 收起左侧栏' : '展开 / 收起左侧会话栏',
        subtitle: 'Ctrl+B · 切换侧栏展开或收起',
        keywords: 'sidebar collapse expand 侧栏 收起 展开',
        onRun: () => {
          onClose()
          onToggleSidebar()
        },
      },
      {
        id: 'action:theme',
        category: 'action',
        title: computedScheme === 'dark' ? '切换至浅色外观' : '切换至深色外观',
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
        subtitle: isOfficeProfile ? '外观、AI 供应商、常用网站、快捷键' : '连接、模型、快捷键、插件',
        keywords: 'settings preferences 设置 连接 快捷键 供应商',
        onRun: () => {
          onClose()
          onOpenSettings()
        },
      },
    )
    for (const cmd of activeCommands) {
      items.push({
        id: `ext-cmd:${cmd.pluginId}:${cmd.id}`,
        category: cmd.category || 'action',
        title: cmd.title,
        subtitle: cmd.subtitle,
        keywords: `${cmd.title} ${cmd.subtitle || ''} ${cmd.keywords || ''} ${cmd.pluginId}`,
        badge: cmd.badge,
        icon: cmd.icon,
        onRun: () => {
          onClose()
          void cmd.run()
        },
      })
    }

    // 4. Projects / Workspaces
    const seenProjects = new Set<string>()
    for (const project of projects) {
      const name = (project.project_name || project.workspace?.split(/[/\\]/).filter(Boolean).pop() || '').trim()
      if (!name || seenProjects.has(name)) continue
      seenProjects.add(name)
      const latestSession = sortedSessions.find(
        (s) =>
          (project.workspace && s.workspace === project.workspace) ||
          (project.project_id && s.project_id === project.project_id) ||
          s.project_name === name,
      )
      if (isOfficeProfile || !monitorEnabled) {
        if (!latestSession) continue
        items.push({
          id: `project:${name}`,
          category: 'project',
          title: `进入工作空间：${name}`,
          subtitle: project.workspace || undefined,
          keywords: `project workspace 工作空间 项目 ${name} ${project.workspace || ''}`,
          onRun: () => {
            onClose()
            navigate(`/chat/${encodeURIComponent(latestSession.id)}?agent_id=${encodeURIComponent(latestSession.agent_id)}`)
          },
        })
      } else {
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
    }

    return items
  }, [agents, computedScheme, enabledPlugins, inboxCount, isOfficeProfile, navigate, onClose, onOpenSettings, onToggleSidebar, projects, registryVersion, sessions, setColorScheme])

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
    const localMatches = allItems
      .filter((item) => {
        const hay = item.keywords.toLowerCase()
        return tokens.every((tok) => hay.includes(tok))
      })
      .slice(0, 16)
    const sessionsByKey = useAstrorderStore.getState().sessions
    const remoteMessages: PaletteItem[] = messageHits.map((hit) => {
      const title = resolveSessionDisplayTitle(sessionsByKey, hit.agent_id, hit.session_id, {
        title: hit.session_title,
        workspace: hit.workspace,
      })
      const timeText = formatRelativeTime(hit.created_at)
      const roleTag = hit.role === 'user' ? '我' : '回复'
      return {
        id: `msg:${hit.agent_id}:${hit.session_id}:${hit.id}`,
        category: 'message',
        title: `${title}${timeText ? ` · ${timeText}` : ''}`,
        subtitle: `${roleTag}：${hit.snippet}`,
        keywords: hit.snippet,
        agentKind: hit.agent_kind || agents[hit.agent_id]?.kind,
        onRun: () => {
          onClose()
          navigate(`/chat/${encodeURIComponent(hit.session_id)}?agent_id=${encodeURIComponent(hit.agent_id)}&message_id=${encodeURIComponent(hit.id)}`)
        },
      }
    })
    return [...localMatches, ...remoteMessages]
  }, [agents, allItems, messageHits, navigate, onClose, query])

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

  const categoryLabel: Record<PaletteCategory, string> = isOfficeProfile
    ? {
        session: '对话',
        message: '消息',
        route: '页面',
        action: '操作',
        project: '工作空间',
      }
    : {
        session: '会话',
        message: '消息',
        route: '视图',
        action: '动作',
        project: '项目',
      }

  const renderIcon = (item: PaletteItem) => {
    if (item.category === 'session') return <AgentBrandIcon kind={item.agentKind} size={15} />
    if (item.category === 'message') return item.agentKind ? <AgentBrandIcon kind={item.agentKind} size={15} /> : <IconMessageSearch size={15} color="var(--astr-indigo)" />
    if (item.category === 'project') return <IconFolder size={15} color="var(--astr-indigo)" />
    if (item.icon) {
      const CustomIcon = item.icon
      return <CustomIcon size={15} color="var(--astr-indigo)" />
    }
    if (item.id === 'route:inbox') return <IconInbox size={15} color="var(--astr-indigo)" />
    if (item.id === 'route:welcome') return <IconHome size={15} color="var(--astr-indigo)" />
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
        aria-label={isOfficeProfile ? '全局搜索' : '星序全能星令面板'}
      >
        {dispatchTarget ? (
          <div className="command-palette-dispatch">
            <div className="command-palette-dispatch-head">
              <AgentBrandIcon kind={agents[dispatchTarget.agent_id]?.kind} size={15} />
              <strong>{isOfficeProfile ? '快速发送消息' : '快发后台指令'} → {displaySessionTitle(dispatchTarget)}</strong>
              <button type="button" className="command-palette-back" onClick={() => setDispatchTarget(null)}>
                返回 (Esc)
              </button>
            </div>
            <div className="command-palette-input-row">
              <IconSend size={16} color="var(--astr-indigo)" aria-hidden="true" />
              <input
                ref={dispatchRef}
                className="command-palette-input"
                aria-label={isOfficeProfile ? '输入要发送的消息内容' : '输入后台指令内容'}
                placeholder={isOfficeProfile ? '输入要发送给该对话的消息，按 Enter 立即发送…' : '输入要直接派发给该会话的指令，按 Enter 立即发送…'}
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
                aria-label={isOfficeProfile ? '搜索对话、历史记录、工作空间或页面' : '搜索会话、历史消息、工作空间、视图或快捷命令'}
                placeholder={isOfficeProfile ? '搜索对话、历史记录、工作空间或页面…' : '搜索会话、历史消息、项目或快捷命令…（选中会话按 Tab 可快发指令）'}
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
                <div className="command-palette-empty">
                  {searchingMessages
                    ? (isOfficeProfile ? '正在检索历史对话记录…' : '正在检索跨会话历史消息…')
                    : (isOfficeProfile ? '未找到匹配的对话、文档或页面' : '未找到匹配的记录或快捷命令')}
                </div>
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
                          title={isOfficeProfile ? '不切换页面直接发消息 (Tab)' : '不切换页面直接发指令 (Tab)'}
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
              <span><kbd>Enter</kbd> {isOfficeProfile ? '打开' : '执行'}</span>
              <span><kbd>Tab</kbd> {isOfficeProfile ? '原地快发消息' : '原地快发指令'}</span>
              <span><kbd>Ctrl+K</kbd> 唤起</span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
