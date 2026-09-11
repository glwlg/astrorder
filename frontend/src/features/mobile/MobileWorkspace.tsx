import { type TouchEvent, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Menu, useMantineColorScheme } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { IconSun, IconMoon, IconDeviceDesktop, IconBell, IconRefresh, IconPlayerPlay, IconInfoCircle, IconNotes, IconPlayerStop, IconSend, IconMessageCircle, IconCheck, IconX, IconMicrophone, IconPlus, IconDotsVertical, IconCpu, IconLoader2, IconPlugConnected, IconFilter } from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { api } from '../../api/client'
import type { Approval, Session, Task } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { requestNotificationPermission } from '../../domain/notifications'
import { selectApprovals, selectCommands, selectProjects, selectSessions, selectTasks, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { useSessionModel } from '../../hooks/useSessionModel'
import { useSessionOrder, notifySessionSubmitted } from '../../hooks/useSessionOrder'
import { buildProjectGroups, displaySessionTitle } from '../../components/sessionRailModel'
import type { ProjectGroup } from '../../components/sessionRailModel'
import { NewSessionDialog } from '../../components/NewSessionDialog'
import { ConfirmPopover } from '../../components/ConfirmPopover'
import { confirmationCoordinatesFromEvent, type ConfirmationCoordinates } from '../../components/confirmationPosition'
import { AgentSessionFilter, matchesAgent } from '../../components/AgentSessionFilter'
import { adjacentOpenSession, isSessionOpen } from '../../components/sessionVisibility'
import { clipboardFiles, REASONING_EFFORTS } from '../chat/composerMedia'
import { PROJECT_ORDER_KEY, readProjectOrder, reconcileProjectOrder } from '../../components/projectOrder'
import { loadPinnedProjects, loadProjectAppearance, purgeProjectPreferences } from '../../components/projectAppearance'
import { MobileSessionDrawer } from './MobileSessionDrawer'
import { MobileSessionDeck, type SessionCardCut } from './MobileSessionDeck'
import { VoiceInputSheet } from '../chat/VoiceInputSheet'
import { MobileTranscript, type MessageActionAnchor } from './MobileTranscript'
import { MobileMessageMenu } from './MobileMessageMenu'
import { EnvironmentConnections } from '../agents/EnvironmentConnections'
import { MobileApprovals } from './MobileApprovals'
import { AgentKindBadge, SessionRuntimeFacts } from '../../components/SessionRuntimeFacts'
import { MobileAttachmentPreview } from './MobileAttachmentPreview'
import { MobileOutbox } from './mobileOutbox'
import { NativeObservationPanel } from '../../components/NativeObservationPanel'
import { ApprovalModeControl } from '../chat/ApprovalModeControl'
import { mobileOutboxStorage } from './mobileOutboxStorage'
import { closesSessionDrawerFromSwipe, opensSessionDrawerFromEdge, startsAtSessionDrawerEdge, type SessionCardPose, type SessionSwipeGesture } from './mobileGestures'
import './mobile.css'
import './mobilePolish.css'

const messageError = (error: unknown) => error instanceof Error ? error.message : '操作未确认，请检查连接。'
const queueLabels = { queued: '排队待发', submitting: '发送中', received: '等待原生确认', accepted: '已接受，等待本轮结束', running: '执行中', unknown: '结果未确认，未自动重发', failed: '未发送成功，内容已保留', cancelled: '已取消', completed: '已完成' }
type MobileConfirmation =
  | { kind: 'delete-session'; session: Session; coords: ConfirmationCoordinates }
  | { kind: 'delete-project'; project: ProjectGroup; coords: ConfirmationCoordinates }
  | { kind: 'stop-task'; session: Session; task: Task; coords: ConfirmationCoordinates }
  | { kind: 'stop-all'; session: Session; coords: ConfirmationCoordinates }

export function MobileWorkspace() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const rawSessions = useAstrorderStore(useShallow(selectSessions))
  const sessions = useSessionOrder(rawSessions)
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agents = useAstrorderStore(state => state.agents)
  const connection = useAstrorderStore(state => state.connection)
  const openState = useQuery({ queryKey: ['astrorder', 'open-sessions'], queryFn: api.getOpenSessions, staleTime: 10000, retry: false })
  const navigableSessions = useMemo(() => {
    const openKeys = new Set(openState.data?.items.map(s => scopeKey(s.agent_id, s.id)))
    const liveKeys = new Set((openState.data?.live || []).map(s => scopeKey(s.agent_id, s.id)))
    return sessions.map(s => {
      const key = scopeKey(s.agent_id, s.id)
      const known = openState.data?.known_agent_ids.includes(s.agent_id)
      return {
        ...s,
        is_open: known ? openKeys.has(key) : s.is_open,
        live: liveKeys.has(key) || s.live,
      }
    })
  }, [sessions, openState.data])
  const route = new URLSearchParams(location.search)
  const routeId = location.pathname.match(/\/chat\/([^/]+)$/)?.[1]
  const selected = navigableSessions.find(s => s.id === (routeId ? decodeURIComponent(routeId) : '') && s.agent_id === route.get('agent_id')) || null
  const [sheet, setSheet] = useState<'sessions' | 'status' | 'task' | 'models' | 'queue' | 'connections' | null>(null)
  const [sessionTransition, setSessionTransition] = useState<SessionCardCut>('next-down')
  const [sessionDrag, setSessionDrag] = useState<SessionCardPose | null>(null)
  const edgeTouch = useRef<{ x: number; y: number } | null>(null)
  const drawerTouch = useRef<{ x: number; y: number; currentX: number; currentY: number } | null>(null)
  const drawerSheet = useRef<HTMLElement | null>(null)
  const drawerSettleTimer = useRef<number | null>(null)
  const [drawerOffset, setDrawerOffset] = useState<number | null>(null)
  const [drawerSettling, setDrawerSettling] = useState(false)
  const select = (session: Session, transition?: SessionCardCut) => {
    const currentIndex = navigableSessions.findIndex(item => item.id === selected?.id && item.agent_id === selected?.agent_id)
    const nextIndex = navigableSessions.findIndex(item => item.id === session.id && item.agent_id === session.agent_id)
    const inferred: SessionCardCut = nextIndex >= 0 && currentIndex >= 0 && nextIndex < currentIndex ? 'previous-up' : 'next-down'
    setSessionDrag(null)
    setSessionTransition(transition || inferred)
    navigate(`/mobile/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`, { replace: true })
    setSheet(null)
  }
  useEffect(() => { if (!routeId && sessions.length) navigate(`/mobile/chat/${encodeURIComponent(sessions[0].id)}?agent_id=${encodeURIComponent(sessions[0].agent_id)}`, { replace: true }) }, [routeId, sessions, navigate])
  const resources = useSessionResources(selected, true)
  const sessionModel = useSessionModel(selected)
  const messages = resources.visibleMessages
  const tasks = useAstrorderStore(useShallow(state => selected ? selectTasks(state, selected.agent_id, selected.id) : []))
  const commands = useAstrorderStore(useShallow(state => selected ? selectCommands(state, selected.agent_id, selected.id) : []))
  const activeTasks = tasks.filter(task => task.status === 'running' || task.status === 'waiting_approval')
  const approvals = useAstrorderStore(useShallow(state => selected ? selectApprovals(state, selected.agent_id, selected.id) : []))
  const nativeBusy = selected?.status === 'running' || selected?.status === 'waiting_approval' || activeTasks.length > 0
  const [messageAction, setMessageAction] = useState<(MessageActionAnchor & { sessionKey: string }) | null>(null)
  const closeMessageMenu = useCallback(() => setMessageAction(null), [])
  const [task, setTask] = useState<Task | null>(null)
  const [voice, setVoice] = useState(false)
  const [image, setImage] = useState<string | null>(null)
  const [drafts, setDrafts] = useState<Record<string, string>>(() => { try { return JSON.parse(localStorage.getItem('astrorder:mobile-drafts') || '{}') } catch { return {} } })
  const key = selected ? scopeKey(selected.agent_id, selected.id) : ''
  useEffect(closeMessageMenu, [key, closeMessageMenu])
  const text = drafts[key] || ''
  const setText = (value: string) => setDrafts(prev => { const next = { ...prev, [key]: value }; try { localStorage.setItem('astrorder:mobile-drafts', JSON.stringify(next)) } catch {} return next })
  const [filesBySession, setFiles] = useState<Record<string, File[]>>({})
  const files = filesBySession[key] || []
  const updateFiles = (value: File[]) => setFiles(prev => ({ ...prev, [key]: value }))
  const [quotes, setQuotes] = useState<Record<string, string>>({})
  const quote = quotes[key] || ''
  const setQuote = (value: string) => setQuotes(prev => ({ ...prev, [key]: value }))
  const [outbox] = useState(() => new MobileOutbox(mobileOutboxStorage, {
    upload: api.uploadAttachment,
    send: async payload => { const result = await api.createCommand(payload); useAstrorderStore.getState().mergeCommands([result]); return result },
  }))
  const queue = useSyncExternalStore(outbox.subscribe, outbox.snapshot)
  const [outboxReady, setOutboxReady] = useState(false)
  useEffect(() => { let mounted = true; void outbox.load().then(() => { if (mounted) setOutboxReady(true) }).catch(error => notifications.show({ message: messageError(error), color: 'red' })); return () => { mounted = false } }, [outbox])
  const pending = queue.filter(row => row.payload.agent_id === selected?.agent_id && row.payload.session_id === selected?.id)
  const busy = nativeBusy || pending.some(row => ['submitting', 'accepted', 'running'].includes(row.state))
  const canDispatch = !nativeBusy && connection === 'connected' && agents[selected?.agent_id || '']?.status === 'ready' && resources.commands.isSuccess
  useEffect(() => { if (outboxReady) void outbox.reconcile(commands, sessions).catch(error => notifications.show({ message: messageError(error), color: 'red' })) }, [outbox, outboxReady, commands, sessions])
  useEffect(() => {
    if (outboxReady && canDispatch && selected && queue.some(row => row.state === 'queued' && row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id)) {
      void outbox.flush(selected.agent_id, selected.id).then(() => {
        const failed = outbox.snapshot().find(row => row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id && (row.state === 'failed' || row.state === 'unknown') && row.error)
        if (failed?.error) notify(failed.error, 'red')
      }).catch(error => notifications.show({ message: messageError(error), color: 'red' }))
    }
  }, [outbox, outboxReady, canDispatch, selected, queue])
  const sending = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const [filter, setFilter] = useState('all')
  const [filtersOpen, setFiltersOpen] = useState(false)
  const [agentFilter, setAgentFilter] = useState(() => {
    try { return localStorage.getItem('astrorder:mobile-agent-filter') || 'all' } catch { return 'all' }
  })
  const updateAgentFilter = (value: string) => {
    setAgentFilter(value)
    try { localStorage.setItem('astrorder:mobile-agent-filter', value) } catch {}
  }
  const [createOpened, setCreateOpened] = useState(false)
  const [createProject, setCreateProject] = useState<ProjectGroup | null>(null)
  const [confirmation, setConfirmation] = useState<MobileConfirmation | null>(null)
  const [confirmationLoading, setConfirmationLoading] = useState(false)
  const [search, setSearch] = useState('')

  const [pins, setPins] = useState<Record<string, boolean>>(() => { try { return JSON.parse(localStorage.getItem('astrorder_pinned_sessions') || '{}') } catch { return {} } })
  const appearance = useMemo(loadProjectAppearance, [])
  const [order, setOrder] = useState(readProjectOrder)
  const [pinnedProjects] = useState<string[]>(() => loadPinnedProjects())
  const allGroups = useMemo(() => buildProjectGroups(sessions, agents, projects), [sessions, agents, projects])
  const stableOrder = useMemo(() => reconcileProjectOrder(order, allGroups.map(p => p.key)), [order, allGroups])
  useEffect(() => { if (stableOrder.length !== order.length) { setOrder(stableOrder); localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(stableOrder)) } }, [stableOrder, order])
  const visible = navigableSessions.filter(s => {
    if (!matchesAgent(s, agents, agentFilter)) return false
    if (filter === 'pinned' && !pins[scopeKey(s.agent_id, s.id)]) return false
    if (filter === 'open' && !isSessionOpen(s)) return false
    if (filter === 'recent' && Date.now() - Date.parse(s.updated_at) > 86400000) return false
    if (filter === 'unread') return (s as Session & { unread?: boolean }).unread === true
    return true
  })
  const grouped = buildProjectGroups(visible, agents, projects, search.trim().toLowerCase()).filter(project => (filter === 'all' && agentFilter === 'all') || project.sessions.length > 0)
  const groups = useMemo(() => {
    const pinnedSet = new Set(pinnedProjects)
    const ordered = stableOrder.flatMap(id => grouped.filter(p => p.key === id))
    const pinned = pinnedProjects.flatMap(id => grouped.filter(p => p.key === id))
    const unpinned = ordered.filter(p => !pinnedSet.has(p.key))
    return [...pinned, ...unpinned]
  }, [stableOrder, grouped, pinnedProjects])
  const notify = (message: string, color = 'blue') => notifications.show({ message, color })
  const [modelSearch, setModelSearch] = useState('')
  const [modelChoices, setModelChoices] = useState<{ provider: string; model: string; label: string }[]>([])
  const [modelLoading, setModelLoading] = useState(false)

  const [modelSelection, setModelSelection] = useState<{ provider: string; model: string; label: string } | null>(null)
  const openModels = async () => {
    if (!selected) return
    setSheet('models'); setModelLoading(true); setModelChoices([]); setModelSearch(''); setModelSelection(null)
    try { setModelChoices((await api.getSessionModels(selected.id, selected.agent_id)).items) }
    catch (error) { notify(messageError(error), 'red') }
    finally { setModelLoading(false) }
  }
  const chooseModel = async (provider: string, model: string) => {
    if (!selected || modelLoading) return
    setModelLoading(true)
    try { await sessionModel.change(provider, model); setSheet(null) }
    catch (error) { notify(messageError(error), 'red') }
    finally { setModelLoading(false) }
  }
  const refresh = async () => { await queryClient.invalidateQueries({ queryKey: ['astrorder'] }); notify('进度已对齐最新状态') }
  const handleApproval = async (approval: Approval, action: 'approve' | 'cancel') => {
    if (!selected || !approval.target_id) return
    try {
      const result = await api.createCommand({
        id: crypto.randomUUID(),
        agent_id: selected.agent_id,
        session_id: selected.id,
        action,
        target_id: approval.target_id,
        text: '',
        attachment_ids: [],
      })
      useAstrorderStore.getState().mergeCommands([result])
      if (result.state === 'failed' || result.state === 'unknown') throw new Error(result.error || '原生授权尚未确认')
    } catch (error) {
      notify(error instanceof Error ? error.message : '授权请求失败', 'red')
    }
  }
  useEffect(() => {
    const resume = () => { if (document.visibilityState === 'visible') void queryClient.invalidateQueries({ queryKey: ['astrorder'] }) }
    window.addEventListener('online', resume); document.addEventListener('visibilitychange', resume)
    return () => { window.removeEventListener('online', resume); document.removeEventListener('visibilitychange', resume) }
  }, [queryClient])
  const send = async (quickText?: string) => {
    if (!selected || sending.current) return
    const body = quickText ?? (quote ? quote.split('\n').map(line => `> ${line}`).join('\n') + '\n\n' + text : text)
    const chosenFiles = quickText ? [] : files
    if (!body.trim() && !chosenFiles.length) return
    sending.current = true; setSubmitting(true)
    try {
      if (!outboxReady) await outbox.load()
      await outbox.enqueue({ id: crypto.randomUUID(), agent_id: selected.agent_id, session_id: selected.id, action: 'send', text: body, attachment_ids: [], target_id: null }, chosenFiles)
      notifySessionSubmitted(selected)
      if (!quickText) { setText(''); updateFiles([]); setQuote('') }
      if (!canDispatch) notify('已保存到待发队列')
    } catch (error) {
      notify(messageError(error), 'red')
    } finally { sending.current = false; setSubmitting(false) }
  }
  const flush = async () => {
    if (!selected) return
    try {
      const result = await api.getCommands(selected.id, selected.agent_id)
      useAstrorderStore.getState().mergeCommands(result.items)
      await outbox.reconcile(result.items, sessions)
      if (canDispatch) {
        await outbox.flush(selected.agent_id, selected.id)
        const failed = outbox.snapshot().find(row => row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id && (row.state === 'failed' || row.state === 'unknown') && row.error)
        if (failed?.error) notify(failed.error, 'red')
      }
      else notify('会话正在运行或连接未就绪，队列已保留')
    } catch (error) { notify(messageError(error), 'red') }
  }
  const executeStop = async (session: Session) => {
    const targetId = session.id
    try {
      const result = await api.createCommand({ id: crypto.randomUUID(), agent_id: session.agent_id, session_id: session.id, action: 'stop', text: '', attachment_ids: [], target_id: targetId })
      useAstrorderStore.getState().mergeCommands([result])
      if (result.state === 'failed' || result.state === 'unknown') throw new Error(result.error || '停止操作未确认')
    } catch (error) {
      notify(messageError(error), 'red')
    }
  }
  const stop = async () => {
    if (!selected) return
    await executeStop(selected)
  }
  const requestStopTask = (task: Task, event?: { clientX: number; clientY: number }) => {
    if (!selected) return
    setConfirmation({ kind: 'stop-task', session: selected, task, coords: confirmationCoordinatesFromEvent(event) })
  }
  const requestDeleteProject = (project: ProjectGroup, event?: { clientX: number; clientY: number }) => {
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

      purgeProjectPreferences(project.key)

      if (selected && project.sessions.some((s) => s.id === selected.id && s.agent_id === selected.agent_id)) {
        const remainingSessions = sessions.filter(
          (s) => !project.sessions.some((ps) => ps.id === s.id && ps.agent_id === s.agent_id),
        )
        if (remainingSessions.length > 0) {
          select(remainingSessions[0])
        } else {
          setSheet('sessions')
        }
      }
      notify(`项目“${project.label}”已删除`)
    } catch (err) {
      console.error('删除项目失败', err)
      notify('删除项目失败，请重试', 'red')
    }
  }
  const requestStopAll = (event?: { clientX: number; clientY: number }) => {
    if (!selected) return
    setConfirmation({ kind: 'stop-all', session: selected, coords: confirmationCoordinatesFromEvent(event) })
  }
  const switchSession = (gesture: SessionSwipeGesture) => {
    const next = adjacentOpenSession(navigableSessions, selected, gesture.direction)
    if (!next) return
    select(next, gesture.cut)
  }
  const drawerWidth = () => drawerSheet.current?.getBoundingClientRect().width || Math.min(window.innerWidth * 0.88, 380)
  const clearDrawerSettleTimer = () => { if (drawerSettleTimer.current !== null) { window.clearTimeout(drawerSettleTimer.current); drawerSettleTimer.current = null } }
  useEffect(() => () => clearDrawerSettleTimer(), [])
  const handleDrawerTouchStart = (event: TouchEvent<Element>) => {
    if (sheet === 'sessions' && (event.target as Element | null)?.closest('.m-session-sheet') && event.touches[0]) {
      clearDrawerSettleTimer()
      drawerTouch.current = { x: event.touches[0].clientX, y: event.touches[0].clientY, currentX: event.touches[0].clientX, currentY: event.touches[0].clientY }
    }
  }
  const handleDrawerTouchMove = (event: TouchEvent<Element>) => {
    if (sheet === 'sessions' && event.touches[0] && drawerTouch.current) {
      drawerTouch.current = { ...drawerTouch.current, currentX: event.touches[0].clientX, currentY: event.touches[0].clientY }
      const dx = event.touches[0].clientX - drawerTouch.current.x
      const dy = event.touches[0].clientY - drawerTouch.current.y
      if (dx >= -80 || Math.abs(dx) < Math.abs(dy) * 1.6) {
        if (drawerOffset !== null) { setDrawerSettling(true); setDrawerOffset(0) }
        return
      }
      setDrawerSettling(false)
      setDrawerOffset(Math.max(-drawerWidth(), Math.min(0, dx)))
    }
  }
  const handleDrawerTouchEnd = () => {
    if (sheet !== 'sessions' || !drawerTouch.current) return
    const start = drawerTouch.current
    drawerTouch.current = null
    const endX = start.currentX
    const endY = start.currentY
    const shouldClose = closesSessionDrawerFromSwipe(start.x, start.y, endX, endY)
    const targetOffset = shouldClose ? -drawerWidth() : 0
    setDrawerSettling(true)
    setDrawerOffset(targetOffset)
    clearDrawerSettleTimer()
    drawerSettleTimer.current = window.setTimeout(() => {
      drawerSettleTimer.current = null
      if (shouldClose) setSheet(null)
      setDrawerOffset(null)
      setDrawerSettling(false)
    }, 220)
  }
  const handleDrawerTouchCancel = () => {
    if (sheet !== 'sessions' || !drawerTouch.current) return
    drawerTouch.current = null
    setDrawerSettling(true)
    setDrawerOffset(0)
    clearDrawerSettleTimer()
    drawerSettleTimer.current = window.setTimeout(() => { drawerSettleTimer.current = null; setDrawerOffset(null); setDrawerSettling(false) }, 220)
  }
  const handleWorkspaceTouchStart = (event: TouchEvent<HTMLDivElement>) => {
    const point = event.touches[0]
    if (!point || sheet) return
    if (!startsAtSessionDrawerEdge(point.clientX)) { edgeTouch.current = null; return }
    edgeTouch.current = { x: point.clientX, y: point.clientY }
  }
  const handleWorkspaceTouchMove = (event: TouchEvent<HTMLDivElement>) => {
    if (!edgeTouch.current || !event.touches[0]) return
    if (Math.abs(event.touches[0].clientX - edgeTouch.current.x) < 8) return
    if (event.cancelable) event.preventDefault()
  }
  const handleWorkspaceTouchEnd = (event: TouchEvent<HTMLDivElement>) => {
    const start = edgeTouch.current
    edgeTouch.current = null
    if (!start || sheet) return
    const point = event.changedTouches[0]
    if (!point || !opensSessionDrawerFromEdge(start.x, start.y, point.clientX, point.clientY)) return
    setSheet('sessions')
    void openState.refetch()
  }
  const copy = async (value: string) => { try { await navigator.clipboard.writeText(value); notify('已复制') } catch { notify('浏览器未允许复制，请使用选择文字') } }
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
      notify('会话已删除')
    } catch (err) {
      notify(err instanceof Error ? err.message : '删除会话失败', 'red')
    }
  }
  const confirmMobileAction = async () => {
    if (!confirmation || confirmationLoading) return
    setConfirmationLoading(true)
    try {
      if (confirmation.kind === 'delete-session') await deleteSession(confirmation.session)
      else if (confirmation.kind === 'delete-project') await deleteProject(confirmation.project)
      else await executeStop(confirmation.session)
    } finally {
      setConfirmationLoading(false)
      setConfirmation(null)
    }
  }

  return <div className="mobile-workspace" data-mobile-shell="independent" onTouchStartCapture={handleWorkspaceTouchStart} onTouchMoveCapture={handleWorkspaceTouchMove} onTouchEndCapture={handleWorkspaceTouchEnd} onTouchCancelCapture={() => { edgeTouch.current = null }}>
    {createOpened && <NewSessionDialog agents={agents} project={createProject} initialAgentId={agentFilter !== 'all' ? agentFilter : undefined} onClose={() => setCreateOpened(false)} onCreated={session => { setSearch(''); select(session) }} />}
    <header className="m-header"><div className="m-brand"><button className="m-session-nav" aria-label="打开会话列表" onClick={() => { setSheet('sessions'); void openState.refetch() }}><IconMessageCircle size={21} /></button><img className="m-brand-mark" src="/pwa-192.png" alt="" /><strong>星序</strong><span className={`m-dot ${connection === 'connected' ? 'online' : ''}`} aria-label={connection === 'connected' ? '已连接' : '连接中'} /></div><div className="m-header-actions">
      <button aria-label="新建会话" onClick={() => { setCreateProject(null); setCreateOpened(true) }}><IconPlus size={21} /></button>
      <Menu position="bottom-end" width={210} withinPortal>
        <Menu.Target><button aria-label="应用设置"><IconDotsVertical size={21} /></button></Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>星序</Menu.Label>
          <Menu.Item leftSection={<IconPlugConnected size={17} />} onClick={() => setSheet('connections')}>连接管理</Menu.Item>
          <Menu.Item leftSection={colorScheme === 'auto' ? <IconDeviceDesktop size={17} /> : colorScheme === 'light' ? <IconSun size={17} /> : <IconMoon size={17} />} onClick={() => setColorScheme(colorScheme === 'auto' ? 'light' : colorScheme === 'light' ? 'dark' : 'auto')}>切换主题</Menu.Item>
          <Menu.Item leftSection={<IconBell size={17} />} onClick={async () => notify(`通知权限：${await requestNotificationPermission()}`)}>通知设置</Menu.Item>
          <Menu.Item leftSection={<IconRefresh size={17} />} onClick={() => void refresh()}>刷新</Menu.Item>
        </Menu.Dropdown>
      </Menu>
    </div></header>
    <main className="m-main">{selected ? <MobileSessionDeck sessionKey={key} cut={sessionTransition} drag={sessionDrag}>
      <div className="m-card-head"><div><h1>{displaySessionTitle(selected)}</h1><small><AgentKindBadge agent={agents[selected.agent_id]} /> {agents[selected.agent_id]?.name || selected.agent_id}</small></div><button aria-label="会话信息" onClick={() => setSheet('status')}><IconDotsVertical size={20} /></button></div>
      <MobileTranscript messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} busy={busy} loadOlder={() => resources.messages.fetchNextPage()} hasOlder={!!resources.messages.hasNextPage} loadingOlder={resources.messages.isFetchingNextPage} onMessageAction={anchor => setMessageAction({ ...anchor, sessionKey: key })} onImage={setImage} onSwipe={switchSession} onSwipePreview={setSessionDrag} />
      {!!pending.length && <button className="m-queue-banner" onClick={() => setSheet('queue')}>消息队列 ({pending.length}) · {queueLabels[pending[0].state]}{pending[0].error ? ` · ${pending[0].error}` : ''}</button>}
    </MobileSessionDeck> : <div className="m-empty">从左上角选择会话，或新建会话</div>}</main>
    <section className="m-composer-float">

      {!!activeTasks.length && <div className="m-active-tasks">{activeTasks.map(item => <button key={item.id} onClick={() => { setTask(item); setSheet('task') }}><span className="m-dot online" />{item.title}</button>)}</div>}
      {!!approvals.length && <button className="m-queue-banner" onClick={() => setSheet('status')}>等待授权 · {approvals.length} 项</button>}
      {quote && <div className="m-quote"><span>{quote}</span><button aria-label="取消引用" onClick={() => setQuote('')}><IconX size={16} /></button></div>}
      {!!files.length && <div className="m-attachments">{files.map((file, i) => <MobileAttachmentPreview key={`${file.name}-${i}`} file={file} onOpen={setImage} onRemove={() => updateFiles(files.filter((_, n) => n !== i))} />)}</div>}
      <div className="m-composer"><input hidden ref={fileInput} type="file" multiple accept="image/*,audio/*,.pdf,.txt,.md,.json,.csv,.log" onChange={e => { updateFiles([...files, ...Array.from(e.target.files || [])]); e.target.value = '' }} />
        <textarea ref={textarea} aria-label="消息内容" placeholder="输入消息，可粘贴图片…" rows={1} value={text} onPaste={event => { const pasted = clipboardFiles(event); if (pasted.length) { event.preventDefault(); updateFiles([...files, ...pasted]) } }} onChange={e => { setText(e.target.value); e.target.style.height = '35px'; e.target.style.height = `${Math.min(140, Math.max(35, e.target.scrollHeight))}px` }} />
        <div className="m-composer-tools"><button aria-label="添加附件" onClick={() => fileInput.current?.click()}><IconPlus size={21} /></button>
        {selected && <ApprovalModeControl session={selected} compact />}
        <button className="m-model-trigger" aria-label="选择会话模型" title={sessionModel.label} disabled={!selected} onClick={() => void openModels()}><IconCpu size={15} /><span>{sessionModel.label}</span></button>
        <Menu position="top-end" width={230} withinPortal>
          <Menu.Target><button aria-label="会话操作" disabled={!selected}><IconDotsVertical size={20} /></button></Menu.Target>
          <Menu.Dropdown>
            <Menu.Item leftSection={<IconPlayerPlay size={17} />} onClick={() => void send('继续')}>继续</Menu.Item>
            <Menu.Item leftSection={<IconInfoCircle size={17} />} onClick={() => setSheet('status')}>运行状态</Menu.Item>
            <Menu.Item leftSection={<IconNotes size={17} />} onClick={() => void send('帮我总结当前会话的最新进展与遗留事项')}>总结进展</Menu.Item>
            <Menu.Label>内容区斜滑切换开放会话</Menu.Label>
            <Menu.Divider />
            <Menu.Item color="red" leftSection={<IconPlayerStop size={17} />} onClick={(event) => requestStopAll(event)}>终止全部任务</Menu.Item>
          </Menu.Dropdown>
        </Menu>
        {text && <button aria-label="清空输入内容" onClick={() => { setText(''); if (textarea.current) textarea.current.style.height = '35px' }}><IconX size={18} /></button>}
        <button aria-label="语音消息" onClick={() => setVoice(true)}><IconMicrophone size={21} /></button><button className="m-send" data-stop={busy && !text && !files.length} aria-label={busy && !text && !files.length ? '停止' : '发送'} disabled={submitting || !selected} onClick={() => busy && !text && !files.length ? void stop() : void send()}>{submitting ? <IconLoader2 className="m-spin" size={20} /> : busy && !text && !files.length ? <IconPlayerStop size={19} /> : <IconSend size={20} />}</button>
        </div>
      </div>
    </section>
    {messageAction?.sessionKey === key && <MobileMessageMenu anchor={messageAction} onClose={closeMessageMenu} onCopy={() => void copy(messageAction.text)} onQuote={() => { setQuote(messageAction.text); textarea.current?.focus({ preventScroll: true }) }} />}
    {sheet && <div className={`m-backdrop ${sheet === 'sessions' ? 'm-session-backdrop' : ''}`} onClick={() => setSheet(null)} onTouchStartCapture={handleDrawerTouchStart} onTouchMoveCapture={handleDrawerTouchMove} onTouchEndCapture={handleDrawerTouchEnd} onTouchCancelCapture={handleDrawerTouchCancel}><section ref={node => { drawerSheet.current = node }} style={sheet === 'sessions' && drawerOffset !== null ? { transform: `translate3d(${drawerOffset}px, 0, 0)`, transition: drawerSettling ? 'transform .22s cubic-bezier(.2,.8,.2,1)' : 'none', animation: 'none' } : undefined} className={`m-sheet ${sheet === 'sessions' ? 'm-session-sheet' : ''}`} role="dialog" aria-label={sheet === 'sessions' ? '会话列表' : '详情'} onClick={e => e.stopPropagation()} onTouchStart={handleDrawerTouchStart} onTouchMove={handleDrawerTouchMove} onTouchEnd={handleDrawerTouchEnd} onTouchCancel={handleDrawerTouchCancel}><div className="m-handle" /><header><h2>{({ sessions: '会话', status: '运行状态', task: '任务详情', models: '选择模型', queue: '消息队列', connections: '连接管理' })[sheet]}</h2><div className="m-sheet-header-actions">{sheet === 'sessions' && <><button aria-label="筛选" aria-pressed={filtersOpen} onClick={() => setFiltersOpen(open => !open)}><IconFilter size={18} /></button><button aria-label="新建会话" onClick={() => { setCreateProject(null); setCreateOpened(true) }}><IconPlus size={20} /></button></>}<button aria-label="关闭面板" onClick={() => setSheet(null)}><IconX size={20} /></button></div></header>
      {sheet === 'connections' && <div className="m-sheet-body"><EnvironmentConnections embedded /></div>}
      {sheet === 'sessions' && filtersOpen && <><div className="m-drawer-filter-row"><AgentSessionFilter agents={agents} value={agentFilter} onChange={updateAgentFilter} /></div><nav className="m-filters">{[['all','全部'],['unread','未读'],['open','开放中'],['pinned','置顶'],['recent','24小时']].map(([id,label]) => <button className={filter === id ? 'active' : ''} key={id} onClick={() => setFilter(id)}>{label}</button>)}</nav></>}
      {sheet === 'sessions' && <><input className="m-search" aria-label="搜索会话" placeholder="搜索会话" value={search} onChange={e => setSearch(e.target.value)} /><MobileSessionDrawer groups={groups} pins={pins} selectedKey={key} appearance={appearance} onSelect={select} onCreate={project => { setCreateProject(project); setCreateOpened(true) }} onDeleteProject={requestDeleteProject} onDeleteSession={requestDeleteSession} onPin={s => { const next = { ...pins, [scopeKey(s.agent_id,s.id)]: !pins[scopeKey(s.agent_id,s.id)] }; setPins(next); localStorage.setItem('astrorder_pinned_sessions', JSON.stringify(next)) }} /></>}
      {sheet === 'status' && selected && <div className="m-sheet-body"><MobileApprovals session={selected} approvals={approvals} /><SessionRuntimeFacts session={selected} agent={agents[selected.agent_id]} /><button onClick={() => void copy(selected.id)}>复制会话 ID</button>{agents[selected.agent_id]?.kind==='codex' && <NativeObservationPanel agentId={selected.agent_id} sessionId={selected.id} />}</div>}
      {sheet === 'task' && task && <div className="m-sheet-body"><p>{task.title}</p><p>{task.status}</p><pre>{task.command}</pre><button onClick={() => void copy(task.logs.map(log => log.text).join('\n'))}>复制日志</button><button onClick={e => { const pre=e.currentTarget.parentElement?.querySelector('.m-task-log'); if(pre) pre.scrollTop=pre.scrollHeight }}>跳到底部</button><pre className="m-task-log">{task.logs.map(log => log.text).join('\n')}</pre><button onClick={(event) => requestStopTask(task, event)}>停止任务</button></div>}
      {sheet === 'models' && <div className="m-sheet-body m-model-panel"><div className="m-effort-row">{REASONING_EFFORTS.map(item => <button key={item.value} aria-pressed={sessionModel.effort === item.value} aria-label={`思考强度 ${item.label}`} disabled={modelLoading} onClick={() => void sessionModel.changeEffort(item.value).catch(error => notify(messageError(error), 'red'))}>{item.label}</button>)}</div><input className="m-search" aria-label="搜索模型" placeholder="搜索模型或提供商" value={modelSearch} onChange={e => setModelSearch(e.target.value)} />{modelLoading && <p>正在处理原生模型请求…</p>}<div className="m-model-list">{modelChoices.filter(choice => choice.label.toLowerCase().includes(modelSearch.toLowerCase())).map(choice => <button className="m-model-choice" aria-pressed={modelSelection?.provider === choice.provider && modelSelection?.model === choice.model} key={`${choice.provider}/${choice.model}`} disabled={modelLoading} onClick={() => setModelSelection(choice)}><IconCpu size={17} /><span>{choice.label}</span>{modelSelection?.provider === choice.provider && modelSelection?.model === choice.model && <IconCheck size={17} />}</button>)}</div>{!modelLoading && !modelChoices.length && <p>原生运行时未返回可用模型。</p>}{modelSelection && <div className="m-model-confirm"><small>{modelSelection.label}</small><button aria-label="确认切换模型" disabled={modelLoading} onClick={() => void chooseModel(modelSelection.provider, modelSelection.model)}>{modelLoading ? '切换中…' : '确认切换模型'}</button></div>}</div>}
      {sheet === 'queue' && <div className="m-sheet-body">{pending.map(entry => <article className="m-outbox-entry" key={entry.payload.id} data-command-id={entry.payload.id}><p>{entry.payload.text || '附件消息'}</p><small>{queueLabels[entry.state]}{entry.error ? ` · ${entry.error}` : ''}</small><div>{[...entry.attachments, ...entry.files].map((file, i) => <span key={i}>{file.name} </span>)}</div>{['queued', 'failed', 'cancelled'].includes(entry.state) && <button onClick={() => void outbox.remove(entry.payload.agent_id, entry.payload.session_id, entry.payload.id).catch(error => notify(messageError(error), 'red'))}>移除待发消息</button>}{entry.state === 'failed' && <button onClick={() => void outbox.retry(entry.payload.agent_id, entry.payload.session_id, entry.payload.id).catch(error => notify(messageError(error), 'red'))}>重新发送</button>}</article>)}<button onClick={() => void flush()}>核对结果 / 发送下一条</button></div>}
    </section></div>}
    {image && <div className="m-lightbox" role="dialog" aria-label="图片预览" onClick={() => setImage(null)}><button aria-label="关闭图片"><IconX size={22} /></button><img src={image} alt="预览" /></div>}
    <VoiceInputSheet opened={voice} onClose={() => setVoice(false)} onCommit={(file, transcript) => { updateFiles([...files, file]); if (transcript) setText(text + (text ? '\n' : '') + transcript) }} />
    <ConfirmPopover
      opened={confirmation !== null}
      coords={confirmation?.coords}
      title={confirmation?.kind === 'stop-all' ? '终止全部任务？' : confirmation?.kind === 'stop-task' ? '停止任务？' : confirmation?.kind === 'delete-project' ? '删除项目？' : '删除会话？'}
      message={confirmation?.kind === 'stop-all'
        ? '终止当前会话的全部运行任务？'
        : confirmation?.kind === 'stop-task'
          ? `确认停止任务“${confirmation.task.title}”？这也会停止本轮其他活动。`
          : confirmation?.kind === 'delete-project'
            ? ((confirmation.project.sessionCount || confirmation.project.sessions.length) > 0
                ? `确定删除项目“${confirmation.project.label}”吗？\n此操作将同时删除该项目及其包含的 ${confirmation.project.sessionCount || confirmation.project.sessions.length} 个会话。`
                : `确定删除项目“${confirmation.project.label}”吗？`)
            : confirmation ? `确定删除会话“${displaySessionTitle(confirmation.session)}”吗？` : ''}
      confirmLabel={confirmation?.kind === 'stop-all' ? '终止全部' : confirmation?.kind === 'stop-task' ? '停止任务' : '删除'}
      loading={confirmationLoading}
      onConfirm={confirmMobileAction}
      onCancel={() => {
        if (!confirmationLoading) setConfirmation(null)
      }}
    />
  </div>
}
