import { type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type TouchEvent, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Button, Group, Menu, Modal, TextInput, useMantineColorScheme } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { IconSun, IconMoon, IconDeviceDesktop, IconBell, IconRefresh, IconPlayerPlay, IconInfoCircle, IconNotes, IconPlayerStop, IconSend, IconMessageCircle, IconCheck, IconX, IconMicrophone, IconPlus, IconDotsVertical, IconCpu, IconLoader2, IconPlugConnected, IconCloudDataConnection, IconFilter, IconTransfer, IconEdit, IconCopy, IconTrash, IconChalkboard, IconWorld } from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { useReducedMotion } from '@mantine/hooks'
import { AnimatePresence, motion } from 'motion/react'
import { api } from '../../api/client'
import type { Agent, AgentCommand, AgentMention, Approval, Command, Message, Session, Task } from '../../domain/types'
import { findSessionHandoff, scopeKey } from '../../domain/semantics'
import { requestNotificationPermission } from '../../domain/notifications'
import { selectApprovals, selectCommands, selectMessages, selectProjects, selectSessions, selectTasks, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { useSessionModel } from '../../hooks/useSessionModel'
import { useSessionOrder, notifySessionSubmitted } from '../../hooks/useSessionOrder'
import { buildProjectGroups, displaySessionTitle } from '../../components/sessionRailModel'
import type { ProjectGroup } from '../../components/sessionRailModel'
import { NewSessionDialog } from '../../components/NewSessionDialog'
import { AddProjectModal } from '../../components/AddProjectModal'
import { BrandMark } from '../../components/BrandMark'
import { ConfirmPopover } from '../../components/ConfirmPopover'
import { confirmationCoordinatesFromEvent, type ConfirmationCoordinates } from '../../components/confirmationPosition'
import { AgentSessionFilter, matchesAgent } from '../../components/AgentSessionFilter'
import { adjacentOpenSession, isSessionOpen } from '../../components/sessionVisibility'
import { clipboardFiles, REASONING_EFFORTS } from '../chat/composerMedia'
import { usePersistentDraft } from '../chat/draftStorage'
import { reconcileProjectOrder } from '../../components/projectOrder'
import { useWorkspacePreferences } from '../../hooks/useWorkspacePreferences'
import { MobileSessionDrawer } from './MobileSessionDrawer'
import { MobileSessionDeck, type SessionCardCut } from './MobileSessionDeck'
import { applyVoiceTranscript, useVoiceInput } from '../chat/useVoiceInput'
import { MobileVoiceHold } from './MobileVoiceHold'
import { MobileTranscript, type MessageActionAnchor } from './MobileTranscript'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { MobileMessageMenu } from './MobileMessageMenu'
import { RtkSettingsPage } from '../rtk/RtkSettingsPage'
import { EnvironmentConnections } from '../agents/EnvironmentConnections'
import { UsageGatewaySettingsCard } from '../services/UsageGatewaySettingsCard'
import { isUserInput } from '../../components/UserInputCard'
import { MobileApprovals } from './MobileApprovals'
import { AgentKindBadge, SessionRuntimeFacts } from '../../components/SessionRuntimeFacts'
import { MobileAttachmentPreview } from './MobileAttachmentPreview'
import { sharedServerOutbox } from './serverOutbox'
import { MobileOutbox as LocalOutbox, type OutboxEntry } from './mobileOutbox'
import { NativeObservationPanel } from '../../components/NativeObservationPanel'
import { ApprovalModeControl } from '../chat/ApprovalModeControl'
import { mobileOutboxStorage, directOutboxStorage } from './mobileOutboxStorage'
import { closesSessionDrawerFromSwipe, hapticFeedback, opensSessionDrawerFromEdge, queueSwipeZone, startsAtSessionDrawerEdge, suppressNativeHold, type SessionCardPose, type SessionSwipeGesture } from './mobileGestures'
import { HandoffDialog } from '../../components/HandoffDialog'
import { ForkWorktreeDialog } from '../../components/ForkWorktreeDialog'
import { BackgroundTasks } from '../../components/BackgroundTasks'
import { agentKindLabel } from '../../components/AgentBrandIcon'
import { BrowserMirrorPanel } from '../sidecar/viewers/browser/BrowserMirrorViewer'
import { AgentCommandMenu, AgentMentionMenu, filterAgentCommands, filterAgentMentions, formatAgentMention, useAgentCommands, useAgentMentions, useFileMentions } from '../chat/AgentCommandMenu'
import { ClickSpark } from '../../components/animations/ClickSpark'
import { expandSystemMentions } from '../chat/systemMentions'
import { interpretGatewayError } from '../chat/ChatComposer'
import { SessionComposerGate } from '../chat/SessionComposerGate'
import { GoalManager } from '../chat/GoalManager'
import { useBackgroundProcesses } from '../chat/useBackgroundProcesses'
import { SessionRuntimeBar } from '../chat/SessionRuntimeBar'
import { TaskDetails } from '../chat/TaskDetails'
import { isGoalTask, selectSessionGoal } from '../chat/goalStatus'
import { MobileBlackboardPanel } from './MobileBlackboardPanel'
import { MobileWorkbench } from './MobileWorkbench'
import './mobile.css'
import './mobilePolish.css'
import './mobileParity.css'

const messageError = (error: unknown) => error instanceof Error ? error.message : '操作未确认，请检查连接。'
const queueLabels = { queued: '排队待发', submitting: '发送中', received: '等待原生确认', accepted: '已接受，等待本轮结束', running: '执行中', unknown: '结果未确认，未自动重发', failed: '未发送成功，内容已保留', cancelled: '已取消', completed: '已完成' }

export function cleanServerName(agent?: Agent): string {
  if (!agent) return '本机'
  if (!agent.connection_id) return '本机'
  const name = agent.name.trim().replace(/\s*[·\s]\s*(?:Codex|Hermes|Claude|Grok|OpenCode[xr]|Gemini).*$/i, '').trim()
  return name || agent.connection_id || '远程'
}

export function displayShortModel(label: string): string {
  if (!label || label === '读取模型…' || label === '模型暂不可读') return label
  const [modelPart, ...rest] = label.split(' · ')
  const suffix = rest.length ? ' · ' + rest.join(' · ') : ''
  const parts = modelPart.split('/')
  const realName = parts[parts.length - 1] || modelPart
  return realName + suffix
}

type MobileConfirmation =
  | { kind: 'delete-session'; session: Session; coords: ConfirmationCoordinates }
  | { kind: 'delete-project'; project: ProjectGroup; coords: ConfirmationCoordinates }
  | { kind: 'stop-all'; session: Session; coords: ConfirmationCoordinates }

export function MobileWorkspace() {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
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
  const [sheet, setSheet] = useState<'sessions' | 'status' | 'goal' | 'tasks' | 'task' | 'models' | 'connections' | 'gateway' | 'blackboard' | 'browser' | null>(null)
  const [workbenchMounted, setWorkbenchMounted] = useState(false)
  const [workbenchOpened, setWorkbenchOpened] = useState(false)
  const closeWorkbench = useCallback(() => setWorkbenchOpened(false), [])
  const openWorkbench = () => { setSheet(null); setWorkbenchMounted(true); setWorkbenchOpened(true) }
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
  const sessionUsage = useQuery({ queryKey: ['astrorder', 'session_usage', selected?.id, selected?.agent_id], queryFn: () => api.getSessionUsage(selected!.id, selected!.agent_id), enabled: Boolean(selected), retry: false, staleTime: 5000, refetchInterval: selected?.status === 'running' ? 3000 : false })
  const readOnly = selected?.control_state === 'readonly' || sessionModel.data?.read_only === true
  const messages = resources.visibleMessages
  const tasks = useAstrorderStore(useShallow(state => selected ? selectTasks(state, selected.agent_id, selected.id) : []))
  const commands = useAstrorderStore(useShallow(state => selected ? selectCommands(state, selected.agent_id, selected.id) : []))
  const goal = selectSessionGoal(tasks)
  const goalSupported = ['codex', 'hermes'].includes(agents[selected?.agent_id || '']?.kind)
  const background = useBackgroundProcesses(selected, goalSupported)
  const runtimeTasks = [...tasks.filter(task => task.kind !== 'background'), ...background.items]
  const visibleTasks = runtimeTasks.filter(task => !isGoalTask(task) && task.kind !== 'tool')
  const activeTasks = visibleTasks.filter(task => ['pending', 'running', 'waiting_approval'].includes(task.status))
  const activeSubagents = tasks.filter(task => task.kind === 'subagent' && (task.status === 'running' || task.status === 'waiting_approval' || task.status === 'pending') && task.progress?.blocking !== false)
  const approvals = useAstrorderStore(useShallow(state => selected ? selectApprovals(state, selected.agent_id, selected.id) : []))
  const nativeBusy = selected?.status === 'running' || selected?.status === 'waiting_approval' || activeSubagents.length > 0
  const [messageAction, setMessageAction] = useState<(MessageActionAnchor & { sessionKey: string }) | null>(null)
  const closeMessageMenu = useCallback(() => setMessageAction(null), [])
  const [taskSelection, setTaskSelection] = useState<Pick<Task, 'id' | 'agent_id' | 'session_id'> | null>(null)
  const task = tasks.find(item => item.id === taskSelection?.id && item.agent_id === taskSelection.agent_id && item.session_id === taskSelection.session_id) ?? null

  const [image, setImage] = useState<string | null>(null)
  const openFile = (path: string) => {
    if (!selected) return
    const artifact = resolveArtifactFromPath(path, selected)
    const viewer = artifactViewerRegistry.findViewer(artifact)
    if (!viewer) { notify('该文件暂不支持在工作区中打开', 'yellow'); return }
    useSidecarStore.getState().switchSession(`${selected.agent_id}:${selected.id}`)
    useSidecarStore.getState().openArtifact(artifact, viewer.id)
    openWorkbench()
  }
  const key = selected ? scopeKey(selected.agent_id, selected.id) : ''
  useEffect(closeMessageMenu, [key, closeMessageMenu])
  useEffect(() => { setSessionError(null); setSubmittedCommandId(null) }, [selected?.id, selected?.agent_id])
  const { draft, setDraft } = usePersistentDraft(selected?.agent_id || '', selected?.id || '')
  const text = draft.text
  const voice = useVoiceInput(key, (transcript, event) => {
    if (event.kind === 'final' && event.submit) {
      void send(transcript)
    } else {
      setDraft(current => ({ ...current, text: applyVoiceTranscript(current.text, transcript, event) }))
    }
  })
  const setText = (value: string) => setDraft(current => ({ ...current, text: value }))
  const files = draft.attachments.map(item => item.file)
  const updateFiles = (value: File[]) => setDraft(current => ({ ...current, attachments: value.map(file => current.attachments.find(item => item.file === file) || { key: crypto.randomUUID(), file }) }))
  const [quotes, setQuotes] = useState<Record<string, string>>({})
  const quote = quotes[key] || ''
  const setQuote = (value: string) => setQuotes(prev => ({ ...prev, [key]: value }))
  const [outbox] = useState(() => sharedServerOutbox(mobileOutboxStorage))
  const [directOutbox] = useState(() => new LocalOutbox(directOutboxStorage, {
    upload: api.uploadAttachment,
    send: async payload => { const result = await api.createCommand(payload); useAstrorderStore.getState().mergeCommands([result]); return result },
  }))
  const directQueue = useSyncExternalStore(directOutbox.subscribe, directOutbox.snapshot)
  const serverQueue = useSyncExternalStore(outbox.subscribe, outbox.snapshot)
  const queue = [...serverQueue, ...directQueue.filter(row => !serverQueue.some(other => other.payload.id === row.payload.id && other.payload.agent_id === row.payload.agent_id && other.payload.session_id === row.payload.session_id))]
  const [outboxReady, setOutboxReady] = useState(false)
  useEffect(() => { let mounted = true; void Promise.all([outbox.load(), directOutbox.load()]).then(() => { if (mounted) setOutboxReady(true) }).catch(error => notifications.show({ message: messageError(error), color: 'red' })); return () => { mounted = false } }, [outbox, directOutbox])
  const pending = queue.filter(row => row.payload.agent_id === selected?.agent_id && row.payload.session_id === selected?.id && !['accepted', 'running', 'completed'].includes(row.state))
  const busy = nativeBusy || pending.some(row => ['submitting', 'accepted', 'running'].includes(row.state))
  const canSubmit = !readOnly && !sessionModel.isPending && connection === 'connected' && agents[selected?.agent_id || '']?.status === 'ready' && resources.commands.isSuccess
  const canDispatch = canSubmit && !nativeBusy
  useEffect(() => { if (outboxReady) void Promise.all([outbox.reconcile(commands, sessions), directOutbox.reconcile(commands, sessions)]).catch(error => notifications.show({ message: messageError(error), color: 'red' })) }, [outbox, directOutbox, outboxReady, commands, sessions])
  useEffect(() => {
    if (outboxReady && canDispatch && !sending.current && selected && queue.some(row => row.state === 'queued' && row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id)) {
      void outbox.flush(selected.agent_id, selected.id).then(async result => {
        const failed = outbox.snapshot().find(row => row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id && (row.state === 'failed' || row.state === 'unknown') && row.error)
        if (failed?.error) notify(failed.error, 'red')
        if (result) {
          await Promise.all([
            queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] }),
            queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', selected.agent_id, selected.id] }),
          ])
        }
      }).catch(error => notifications.show({ message: messageError(error), color: 'red' }))
    }
  }, [outbox, outboxReady, canDispatch, selected, queue, queryClient])
  const sending = useRef(false)
  const [submitting, setSubmitting] = useState(false)
  const queueHold = useRef<{ pointerId: number; entry: OutboxEntry; startY: number; armed: boolean } | null>(null)
  const [queueLift, setQueueLift] = useState<{ id: string; text: string; x: number; y: number; zone: 'send' | 'edit' | null } | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const composer = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const panel = composer.current
    const transcript = panel?.closest('.mobile-workspace')?.querySelector<HTMLElement>('.m-transcript')
    if (!panel || !transcript) return
    const reserveSpace = () => transcript.style.setProperty('padding-bottom', `${panel.getBoundingClientRect().height + 24}px`, 'important')
    reserveSpace()
    const observer = new ResizeObserver(reserveSpace)
    observer.observe(panel)
    return () => { observer.disconnect(); transcript.style.removeProperty('padding-bottom') }
  }, [key])
  useLayoutEffect(() => {
    const input = textarea.current
    if (!input) return
    input.style.height = '34px'
    input.style.height = `${Math.min(140, Math.max(34, input.scrollHeight))}px`
  }, [text, key])
  const [sessionError, setSessionError] = useState<string | null>(null)
  const [submittedCommandId, setSubmittedCommandId] = useState<string | null>(null)
  const submittedCommand = submittedCommandId ? commands.find(c => c.id === submittedCommandId) : undefined
  const submittedCommandError = submittedCommand && (submittedCommand.state === 'failed' || submittedCommand.state === 'unknown') ? (submittedCommand.error || '原生命令执行失败。') : null
  const visibleError = sessionError || submittedCommandError
  const parsedGatewayError = visibleError ? interpretGatewayError(visibleError) : null
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
  const [addProjectOpened, setAddProjectOpened] = useState(false)
  const [createProject, setCreateProject] = useState<ProjectGroup | null>(null)
  const [confirmation, setConfirmation] = useState<MobileConfirmation | null>(null)
  const [confirmationLoading, setConfirmationLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [handoffTarget, setHandoffTarget] = useState<Session | null>(null)
  const [forkWorktreeTarget, setForkWorktreeTarget] = useState<Session | null>(null)
  
  const handleForkChatBranch = async (session: Session) => {
    try {
      const created = await api.forkSession(session.id, {
        agent_id: session.agent_id,
        worktree: false,
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))
      notify('已创建聊天分支', 'teal')
      select(created)
      setSheet(null)
    } catch (err) {
      notify(err instanceof Error ? err.message : '创建聊天分支失败', 'red')
    }
  }
  const [renameSession, setRenameSession] = useState<Session | null>(null)
  const [renameTitle, setRenameTitle] = useState('')
  const [renameLoading, setRenameLoading] = useState(false)
  const [commandIndex, setCommandIndex] = useState(0)
  const [dismissedMenuText, setDismissedMenuText] = useState<string | null>(null)

  const commandMenuOpen = /^\/[^\s]*$/.test(text) && dismissedMenuText !== text
  const resourcesReady = !agents[selected?.agent_id || ''] || agents[selected?.agent_id || '']?.status === 'ready'
  const commandQuery = useAgentCommands(selected, resourcesReady && !!selected)
  const agentCommands = commandMenuOpen && selected
    ? filterAgentCommands(commandQuery.data?.items || [], text)
    : []
  const mentionMatch = text.match(/(?:^|\s)@[^\s@]*$/)
  const mentionMenuOpen = Boolean(mentionMatch) && dismissedMenuText !== text
  const mentionText = mentionMatch?.[0].trim().slice(1) || ''
  const mentionQuery = useAgentMentions(selected, resourcesReady && !!selected)
  const fileMentionQuery = useFileMentions(selected, mentionText, mentionMenuOpen && resourcesReady && !!selected)
  const agentMentions = mentionMenuOpen && selected
    ? filterAgentMentions(
        [...(mentionQuery.data?.items || []), ...(fileMentionQuery.data || [])],
        text,
      )
    : []

  const selectAgentCommand = (item: AgentCommand) => {
    const next = `/${item.name}${item.input_hint ? ' ' : ''}`
    setText(next)
    setDismissedMenuText(next)
    setCommandIndex(0)
    textarea.current?.focus()
  }

  const selectAgentMention = (item: AgentMention) => {
    setText(text.replace(/@[^\s@]*$/, formatAgentMention(item)))
    setCommandIndex(0)
    textarea.current?.focus()
  }

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    const choices = mentionMenuOpen ? agentMentions : agentCommands
    if (choices.length && ['ArrowDown', 'ArrowUp'].includes(event.key)) {
      event.preventDefault()
      setCommandIndex(index => (index + (event.key === 'ArrowDown' ? 1 : -1) + choices.length) % choices.length)
      return
    }
    if (choices.length && (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey))) {
      event.preventDefault()
      if (mentionMenuOpen) selectAgentMention(agentMentions[Math.min(commandIndex, agentMentions.length - 1)])
      else selectAgentCommand(agentCommands[Math.min(commandIndex, agentCommands.length - 1)])
      return
    }
    if (event.key === 'Escape' && (commandMenuOpen || mentionMenuOpen)) {
      event.preventDefault()
      setDismissedMenuText(text)
      return
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      if (!submitting) void send()
    }
  }

  const handoffTargets = (session: Session) => {
    const source = agents?.[session.agent_id]
    if (!source) return []
    return Object.values(agents || {}).filter((agent) =>
      agent.kind !== source.kind &&
      (agent.connection_id ?? null) === (source.connection_id ?? null) &&
      agent.status === 'ready',
    )
  }

  const openRename = (s: Session) => {
    setRenameSession(s)
    setRenameTitle(s.title || '')
  }

  const handleRename = async () => {
    if (!renameSession || !renameTitle.trim()) return
    setRenameLoading(true)
    try {
      const updated = await api.updateSession(renameSession.id, {
        agent_id: renameSession.agent_id,
        title: renameTitle.trim(),
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(updated.agent_id, updated.id)]: updated },
      }))
      setRenameSession(null)
      notify('会话已重命名', 'teal')
    } catch (e) {
      notify(e instanceof Error ? e.message : '重命名失败', 'red')
    } finally {
      setRenameLoading(false)
    }
  }

  const copySessionId = (s: Session) => {
    void navigator.clipboard?.writeText(s.id)
    notify('已复制会话 ID', 'teal')
  }

  const { preferences, updatePreferences, removeProjectPreferences } = useWorkspacePreferences()
  const { session_pins: pins, appearance, project_order: order, pinned_projects: pinnedProjects } = preferences
  const allGroups = useMemo(() => buildProjectGroups(sessions, agents, projects), [sessions, agents, projects])
  const stableOrder = useMemo(() => reconcileProjectOrder(order, allGroups.map(p => p.key)), [order, allGroups])
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
  const notify = (message: string, color = 'blue') => notifications.show({ message, color, autoClose: color === 'red' ? 5000 : 2200, withCloseButton: color === 'red' })
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
    if (!selected || readOnly || modelLoading) return
    setModelLoading(true)
    try { await sessionModel.change(provider, model); setSheet(null) }
    catch (error) { notify(messageError(error), 'red') }
    finally { setModelLoading(false) }
  }
  const refresh = async () => { await queryClient.invalidateQueries({ queryKey: ['astrorder'] }); notify('进度已对齐最新状态') }
  const handoffPeer = useMemo(() => {
    const peer = findSessionHandoff(selected, sessions)
    return peer ? { ...peer, label: `${peer.direction === 'from' ? '转交自' : '已转交至'} ${agentKindLabel(agents[peer.session.agent_id]?.kind)}` } : null
  }, [agents, selected, sessions])
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
    const resume = () => {
      if (document.visibilityState === 'visible') {
        void queryClient.invalidateQueries({ queryKey: ['astrorder'] })
        if (selected) {
          void api.getMessages(selected.id, selected.agent_id).then(res => {
            useAstrorderStore.getState().mergeMessages(selected.agent_id, selected.id, res.items)
          }).catch(() => {})
          void api.getCommands(selected.id, selected.agent_id).then(res => {
            useAstrorderStore.getState().mergeCommands(res.items)
          }).catch(() => {})
        }
      }
    }
    window.addEventListener('online', resume); document.addEventListener('visibilitychange', resume)
    return () => { window.removeEventListener('online', resume); document.removeEventListener('visibilitychange', resume) }
  }, [queryClient, selected])
  const send = async (quickText?: string) => {
    if (!selected || readOnly || sending.current) return
    hapticFeedback(12)
    const body = expandSystemMentions(quickText ?? (quote ? quote.split('\n').map(line => `> ${line}`).join('\n') + '\n\n' + text : text), `${selected.agent_id}::${selected.id}`)
    const chosenAttachments = quickText ? [] : draft.attachments.flatMap(item => item.attachment ? [item.attachment] : [])
    const chosenFiles = quickText ? [] : draft.attachments.filter(item => !item.attachment).map(item => item.file)
    if (!body.trim() && !chosenFiles.length && !chosenAttachments.length) return
    const commandId = crypto.randomUUID()
    setSubmittedCommandId(commandId)
    setSessionError(null)
    const direct = canDispatch && pending.length === 0
    sending.current = true
    setSubmitting(true)
    if (!quickText) {
      setDraft({ text: '', attachments: [], sessionRefs: [] })
      setQuote('')
      if (textarea.current) textarea.current.style.height = '35px'
    }
    const store = useAstrorderStore.getState()
    const command: Command = { id: commandId, agent_id: selected.agent_id, session_id: selected.id, action: 'send', state: 'received', text: body, attachments: chosenAttachments, created_at: new Date().toISOString(), error: null, target_id: null }
    const optimisticMessage: Message = { id: `optimistic-${commandId}`, agent_id: selected.agent_id, session_id: selected.id, role: 'user', kind: 'message', text: body, attachments: chosenAttachments, created_at: command.created_at, command_id: commandId, tool: null }
    if (direct) {
      store.addOutbox(command, 'submitting')
      store.mergeMessages(selected.agent_id, selected.id, [optimisticMessage])
    }
    let persisted = false
    try {
      if (!outboxReady) await outbox.load()
      const target = direct ? directOutbox : outbox
      if (direct) await directOutbox.load()
      await target.enqueue({ id: commandId, agent_id: selected.agent_id, session_id: selected.id, action: 'send', text: body, attachment_ids: chosenAttachments.map(item => item.id), target_id: null }, chosenFiles, chosenAttachments)
      persisted = true
      if (direct) {
        const result = await directOutbox.flush(selected.agent_id, selected.id, commandId)
        const entry = directOutbox.snapshot().find(row => row.payload.id === commandId && row.payload.agent_id === selected.agent_id && row.payload.session_id === selected.id)
        const attachments = result?.attachments || entry?.attachments || chosenAttachments
        store.updateOutboxAttachments(selected.agent_id, selected.id, commandId, attachments)
        // Preserve a canonical message if history arrived while the write was pending.
        const currentMessage = selectMessages(useAstrorderStore.getState(), selected.agent_id, selected.id).find(message => message.role === 'user' && message.command_id === commandId)
        if (currentMessage) store.mergeMessages(selected.agent_id, selected.id, [{ ...currentMessage, attachments }])
        if (entry?.state === 'failed' || entry?.state === 'unknown') {
          store.markOutboxError(selected.agent_id, selected.id, commandId, entry.error || '原生命令执行未确认', entry.state)
          setSessionError(entry.error || '原生命令执行未确认')
          notify(entry.error || '原生命令执行未确认', 'red')
          return
        }
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] })
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', selected.agent_id, selected.id] })
      } else {
        notify('已保存到待发队列')
      }
      notifySessionSubmitted(selected)
    } catch (error) {
      if (direct && persisted) store.markOutboxError(selected.agent_id, selected.id, commandId, messageError(error), 'unknown')
      if (direct && !persisted) {
        store.removeOutbox(selected.agent_id, selected.id, commandId)
        useAstrorderStore.setState(current => ({
          messages: { ...current.messages, [key]: Object.fromEntries(Object.entries(current.messages[key] || {}).filter(([id]) => id !== optimisticMessage.id)) },
        }))
      }
      if (!persisted && !quickText) {
        // A rejected local write never dispatched; restore to the original scope.
        setDraft(current => ({
          ...current,
          text: draft.text && current.text ? `${draft.text}\n${current.text}` : draft.text || current.text,
          attachments: [...draft.attachments, ...current.attachments],
          sessionRefs: [...(draft.sessionRefs || []), ...(current.sessionRefs || [])],
        }))
        if (quote) setQuotes(current => ({ ...current, [key]: current[key] ? `${quote}\n${current[key]}` : quote }))
      }
      notify(messageError(error), 'red')
    } finally { sending.current = false; setSubmitting(false) }
  }
  const sendPress = useRef(false)
  const pressSend = (event?: { preventDefault(): void; button?: number }) => {
    if (typeof event?.button === 'number' && event.button !== 0) return
    if (submitting || !selected) return
    if (event) {
      event.preventDefault()
      sendPress.current = true
    } else if (sendPress.current) {
      sendPress.current = false
      return
    }
    if (busy && !text && !files.length) void stop()
    else void send()
  }
  const editQueued = async (entry: OutboxEntry) => {
    if (readOnly || !selected || entry.payload.agent_id !== selected.agent_id || entry.payload.session_id !== selected.id || !['queued', 'failed', 'cancelled'].includes(entry.state)) return
    try {
      await (directOutbox.snapshot().some(row => row === entry) ? directOutbox : outbox).remove(entry.payload.agent_id, entry.payload.session_id, entry.payload.id)
      setDraft(current => ({ ...current,
        text: entry.payload.text ? (current.text ? `${entry.payload.text}\n${current.text}` : entry.payload.text) : current.text,
        attachments: [
          ...entry.attachments.map(attachment => ({ key: crypto.randomUUID(), file: new File([], attachment.name, { type: attachment.media_type }), attachment })),
          ...entry.files.map(file => ({ key: crypto.randomUUID(), file })),
          ...current.attachments,
        ],
      }))
      textarea.current?.focus({ preventScroll: true })
    } catch (error) {
      notify(messageError(error), 'red')
    }
  }
  const sendQueued = async (entry: OutboxEntry) => {
    if (!canSubmit || sending.current || !selected || entry.payload.agent_id !== selected.agent_id || entry.payload.session_id !== selected.id) return
    sending.current = true
    setSubmitting(true)
    try {
      const owner = directOutbox.snapshot().some(row => row === entry) ? directOutbox : outbox
      const commandId = entry.state === 'failed' ? await owner.retry(entry.payload.agent_id, entry.payload.session_id, entry.payload.id) : entry.payload.id
      if (!commandId) return
      await owner.flush(entry.payload.agent_id, entry.payload.session_id, commandId)
      const failed = owner.snapshot().find(row => row.payload.id === commandId && row.payload.agent_id === entry.payload.agent_id && row.payload.session_id === entry.payload.session_id && ['failed', 'unknown'].includes(row.state))
      if (failed?.error) notify(failed.error, 'red')
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', entry.payload.agent_id, entry.payload.session_id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', entry.payload.agent_id, entry.payload.session_id] })
    } catch (error) {
      notify(messageError(error), 'red')
    } finally {
      sending.current = false
      setSubmitting(false)
    }
  }
  const finishQueueDrag = (y: number) => {
    const hold = queueHold.current
    queueHold.current = null
    setQueueLift(null)
    if (!hold?.armed) return
    const zone = queueSwipeZone(hold.startY, y)
    if (zone === 'send' && ['queued', 'failed'].includes(hold.entry.state)) {
      void sendQueued(hold.entry)
    } else if (zone === 'edit') {
      void editQueued(hold.entry)
    }
  }
  const onQueuePointerDown = (entry: OutboxEntry, event: ReactPointerEvent<HTMLElement>) => {
    if (event.button !== 0 || !['queued', 'failed', 'cancelled'].includes(entry.state)) return
    const list = event.currentTarget.closest('.m-queue-list')
    if (event.pointerType === 'touch' && list && list.scrollHeight > list.clientHeight) return
    event.preventDefault()
    event.stopPropagation()
    const pointerId = event.pointerId
    const startY = event.clientY
    queueHold.current = { pointerId, entry, startY, armed: false }
    event.currentTarget.setPointerCapture?.(pointerId)
  }
  const onQueuePointerMove = (event: ReactPointerEvent<HTMLElement>) => {
    const hold = queueHold.current
    if (!hold || hold.pointerId !== event.pointerId) return
    if (!hold.armed) {
      if (Math.abs(event.clientY - hold.startY) < 8) return
      hold.armed = true
      hapticFeedback(12)
      setQueueLift({ id: hold.entry.payload.id, text: hold.entry.payload.text || '附件消息', x: event.clientX, y: event.clientY, zone: queueSwipeZone(hold.startY, event.clientY) })
      return
    }
    event.preventDefault()
    setQueueLift(prev => prev ? { ...prev, x: event.clientX, y: event.clientY, zone: queueSwipeZone(hold.startY, event.clientY) } : prev)
  }
  const onQueuePointerUp = (event: ReactPointerEvent<HTMLElement>) => {
    const hold = queueHold.current
    if (!hold || hold.pointerId !== event.pointerId) return
    event.currentTarget.releasePointerCapture?.(event.pointerId)
    finishQueueDrag(event.clientY)
  }
  const onQueuePointerCancel = (event: ReactPointerEvent<HTMLElement>) => {
    if (queueHold.current?.pointerId !== event.pointerId) return
    queueHold.current = null
    setQueueLift(null)
    event.currentTarget.releasePointerCapture?.(event.pointerId)
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
    if (!selected || readOnly || !agents[selected.agent_id]?.capabilities.includes('stop')) return
    hapticFeedback(12)
    await executeStop(selected)
  }
  const resolveTaskTarget = (item: Task) => {
    if (item.kind === 'subagent') {
      const childId = typeof item.progress?.child_session_id === 'string' ? item.progress.child_session_id : item.target_id
      return sessions.find(session => session.agent_id === item.agent_id && session.id === childId && session.id !== item.session_id) ?? null
    }
    return sessions.find(session => session.agent_id === item.agent_id && session.id === item.session_id) ?? null
  }
  const handleStopTask = async (item: Task) => {
    const current = selectTasks(useAstrorderStore.getState(), item.agent_id, item.session_id).find(candidate => candidate.id === item.id)
    if (!current || !['pending', 'running', 'waiting_approval'].includes(current.status) || !current.target_id) return
    const target = resolveTaskTarget(current)
    if (!target || target.control_state === 'readonly' || (target.agent_id === selected?.agent_id && target.id === selected?.id && readOnly) || !agents[target.agent_id]?.capabilities.includes('stop')) return
    await executeStop(target)
  }
  const openTask = (item: Task) => {
    setTaskSelection({ id: item.id, agent_id: item.agent_id, session_id: item.session_id })
    setSheet('task')
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

      await removeProjectPreferences(project.key)

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
    if (!selected || readOnly) return
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
  useEffect(() => {
    clearDrawerSettleTimer()
    drawerTouch.current = null
    setDrawerOffset(null)
    setDrawerSettling(false)
  }, [sheet])
  const handleDrawerTouchStart = (event: TouchEvent<Element>) => {
    if (sheet === 'sessions' && (event.target as Element | null)?.closest('.m-session-sheet') && event.touches[0]) {
      drawerTouch.current = { x: event.touches[0].clientX, y: event.touches[0].clientY, currentX: event.touches[0].clientX, currentY: event.touches[0].clientY }
    }
  }
  const handleDrawerTouchMove = (event: TouchEvent<Element>) => {
    if (sheet === 'sessions' && event.touches[0] && drawerTouch.current) {
      drawerTouch.current = { ...drawerTouch.current, currentX: event.touches[0].clientX, currentY: event.touches[0].clientY }
      const dx = event.touches[0].clientX - drawerTouch.current.x
      const dy = event.touches[0].clientY - drawerTouch.current.y
      if (dx >= -80 || Math.abs(dx) < Math.abs(dy) * 1.6) {
        if (drawerOffset !== null && drawerOffset !== 0) { setDrawerSettling(true); setDrawerOffset(0) }
        return
      }
      clearDrawerSettleTimer()
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
    // Leave taps and list scrolling untouched so the browser can dispatch click.
    if (!shouldClose && (drawerOffset === null || drawerOffset === 0)) return
    const targetOffset = shouldClose ? -drawerWidth() : 0
    setDrawerSettling(true)
    setDrawerOffset(targetOffset)
    clearDrawerSettleTimer()
    drawerSettleTimer.current = window.setTimeout(() => {
      drawerSettleTimer.current = null
      if (shouldClose) setSheet(null)
      setDrawerOffset(0)
      setDrawerSettling(false)
    }, 220)
  }
  const handleDrawerTouchCancel = () => {
    if (sheet !== 'sessions' || !drawerTouch.current) return
    drawerTouch.current = null
    if (drawerOffset === null || drawerOffset === 0) return
    setDrawerSettling(true)
    setDrawerOffset(0)
    clearDrawerSettleTimer()
    drawerSettleTimer.current = window.setTimeout(() => { drawerSettleTimer.current = null; setDrawerOffset(0); setDrawerSettling(false) }, 220)
  }
  const handleWorkspaceTouchStart = (event: TouchEvent<HTMLDivElement>) => {
    const point = event.touches[0]
    if (!point || sheet || workbenchOpened) return
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
    hapticFeedback(15)
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

  const resolveCurrentProject = () => {
    return (selected ? allGroups.find(g => g.sessions.some(s => s.id === selected.id)) : null)
      || allGroups.find(g => g.workspace?.toLowerCase().includes('astrorder'))
      || allGroups[0]
      || null
  }

  return <div className="mobile-workspace" data-mobile-shell="independent" data-sheet={sheet || undefined} data-queue-drop={queueLift?.zone || undefined} onContextMenuCapture={(event) => suppressNativeHold(event.nativeEvent)} onTouchStartCapture={handleWorkspaceTouchStart} onTouchMoveCapture={handleWorkspaceTouchMove} onTouchEndCapture={handleWorkspaceTouchEnd} onTouchCancelCapture={() => { edgeTouch.current = null }}>
    {addProjectOpened && <AddProjectModal opened={addProjectOpened} onClose={() => setAddProjectOpened(false)} agents={agents} />}
    {createOpened && <NewSessionDialog agents={agents} project={createProject} initialAgentId={agentFilter !== 'all' ? agentFilter : undefined} onClose={() => setCreateOpened(false)} onCreated={session => { setSearch(''); select(session) }} />}
    <header className="m-header"><div className="m-brand"><button className="m-session-nav" aria-label="打开会话列表" onClick={() => { setSheet('sessions'); void openState.refetch() }}><IconMessageCircle size={21} /></button><div className="m-brand-home-btn" onClick={() => navigate('/chat')} role="button" aria-label="返回工作台首页"><BrandMark className="m-brand-mark" size={26} alt="" /><strong>星序</strong></div><span className={`m-dot ${connection === 'connected' ? 'online' : ''}`} aria-label={connection === 'connected' ? '已连接' : '连接中'} /></div><div className="m-header-actions">
      <button aria-label="新建会话" onClick={() => { setCreateProject(resolveCurrentProject()); setCreateOpened(true) }}><IconPlus size={21} /></button>
      <Menu position="bottom-end" width={210} withinPortal>
        <Menu.Target><button aria-label="应用设置"><IconDotsVertical size={21} /></button></Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>星序</Menu.Label>
          <Menu.Item leftSection={<IconPlugConnected size={17} />} onClick={() => setSheet('connections')}>连接管理</Menu.Item>
          <Menu.Item leftSection={<IconCloudDataConnection size={17} />} onClick={() => setSheet('gateway')}>模型网关</Menu.Item>
          <Menu.Item leftSection={colorScheme === 'auto' ? <IconDeviceDesktop size={17} /> : colorScheme === 'light' ? <IconSun size={17} /> : <IconMoon size={17} />} onClick={() => setColorScheme(colorScheme === 'auto' ? 'light' : colorScheme === 'light' ? 'dark' : 'auto')}>切换主题</Menu.Item>
          <Menu.Item leftSection={<IconBell size={17} />} onClick={async () => notify(`通知权限：${await requestNotificationPermission()}`)}>通知设置</Menu.Item>
          <Menu.Item leftSection={<IconRefresh size={17} />} onClick={() => void refresh()}>刷新</Menu.Item>
        </Menu.Dropdown>
      </Menu>
    </div></header>
    <main className="m-main">{selected ? <MobileSessionDeck sessionKey={key} cut={sessionTransition} drag={sessionDrag}>
      <div className="m-card-head">
        <div className="m-card-head-main">
          <h1>{displaySessionTitle(selected)}</h1>
          <div className="m-card-meta">
            <AgentKindBadge agent={agents[selected.agent_id]} iconOnly />
            <span className="m-server-name">{cleanServerName(agents[selected.agent_id])}</span>
            <button className="m-model-chip" aria-label="选择会话模型" title={sessionModel.label} onClick={() => void openModels()}>
              <IconCpu size={13} /><span>{displayShortModel(sessionModel.label)}</span>
            </button>
            <button className="m-usage-chip" aria-label="上下文用量" title={sessionUsage.data?.source === 'native_last_request' ? '原生记录的上次请求输入用量' : '当前上下文用量'} onClick={() => setSheet('status')}>{sessionUsage.data ? sessionUsage.data.context_available === false ? '上下文未取得' : sessionUsage.data.context_window > 0 ? `${(sessionUsage.data.last_input_tokens / 1000).toFixed(1)}k / ${(sessionUsage.data.context_window / 1000).toFixed(0)}k` : `${(sessionUsage.data.last_input_tokens / 1000).toFixed(1)}k` : sessionUsage.isError ? '用量暂不可读' : '读取用量…'}</button>
            {handoffPeer && (
              <button className="m-model-chip" aria-label={handoffPeer.label} title={handoffPeer.session.title} onClick={() => select(handoffPeer.session)}>
                <IconTransfer size={13} /><span>{handoffPeer.label}</span>
              </button>
            )}
          </div>
        </div>
        <Menu position="bottom-end" width={230} withinPortal>
          <Menu.Target><button aria-label="会话操作"><IconDotsVertical size={20} /></button></Menu.Target>
          <Menu.Dropdown>
            <Menu.Item disabled={readOnly} leftSection={<IconPlayerPlay size={17} />} onClick={() => void send('继续')}>继续</Menu.Item>
            <Menu.Item leftSection={<IconInfoCircle size={17} />} onClick={() => setSheet('status')}>运行状态</Menu.Item>
            <Menu.Item leftSection={<IconNotes size={17} />} onClick={() => setSheet('tasks')}>任务与子代理</Menu.Item>
            {goal && <Menu.Item leftSection={<IconNotes size={17} />} onClick={() => setSheet('goal')}>目标管理</Menu.Item>}
            <Menu.Item leftSection={<IconDeviceDesktop size={17} />} onClick={openWorkbench}>工作区</Menu.Item>
            <Menu.Item leftSection={<IconChalkboard size={17} />} onClick={() => setSheet('blackboard')}>黑板</Menu.Item>
            <Menu.Item leftSection={<IconWorld size={17} />} onClick={() => setSheet('browser')}>浏览器镜像</Menu.Item>
            <Menu.Item leftSection={<IconEdit size={17} />} onClick={() => openRename(selected)}>重命名</Menu.Item>
            <Menu.Item leftSection={<IconCopy size={17} />} onClick={() => copySessionId(selected)}>复制 ID</Menu.Item>
            <Menu.Item disabled={readOnly} leftSection={<IconNotes size={17} />} onClick={() => void send('帮我总结当前会话的最新进展与遗留事项')}>总结进展</Menu.Item>
            {selected && handoffTargets(selected).length > 0 && (
              <Menu.Item
                leftSection={<IconTransfer size={17} />}
                disabled={selected.status !== 'idle'}
                onClick={() => setHandoffTarget(selected)}
              >
                转交
              </Menu.Item>
            )}
            <Menu.Label>内容区斜滑切换开放会话</Menu.Label>
            <Menu.Divider />
            <Menu.Item color="red" disabled={readOnly || !agents[selected.agent_id]?.capabilities.includes('stop')} leftSection={<IconPlayerStop size={17} />} onClick={(event) => requestStopAll(event)}>终止全部任务</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </div>
      {goal && <button className={`m-goal-summary is-${goal.tone}`} aria-label="目标管理" title={goalSupported ? '管理原生目标' : '查看原生目标'} onClick={() => setSheet('goal')}><IconNotes size={15} /><span>{`${goal.label} · ${goal.title}`}</span></button>}
      {!!visibleTasks.length && <button className="m-task-summary" aria-label="任务与子代理" onClick={() => setSheet('tasks')}>任务与子代理 · {activeTasks.length} 项活动 / {visibleTasks.length} 项记录</button>}
      <MobileTranscript readOnly={readOnly} messages={messages} commands={commands} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} busy={busy} loadOlder={() => resources.messages.fetchNextPage()} hasOlder={!!resources.messages.hasNextPage} loadingOlder={resources.messages.isFetchingNextPage} onMessageAction={anchor => setMessageAction({ ...anchor, sessionKey: key })} onImage={setImage} onFile={openFile} workspace={selected?.workspace} connectionId={selected?.connection_id} onSwipe={switchSession} onSwipePreview={setSessionDrag} />
    </MobileSessionDeck> : <div className="m-empty">从左上角选择会话，或新建会话</div>}</main>
    <section ref={composer} className="m-composer-float">
      {visibleError && (
        <div className={"m-gateway-alert is-" + (parsedGatewayError?.type || "generic")} role="alert" aria-live="assertive">
          <div className="m-gateway-alert-head">
            <strong>{parsedGatewayError?.isKnownGatewayIssue ? parsedGatewayError.title : "请求处理异常"}</strong>
            <button type="button" aria-label="关闭提示" onClick={() => { setSessionError(null); setSubmittedCommandId(null) }}>
              <IconX size={14} />
            </button>
          </div>
          <div className="m-gateway-alert-body">{parsedGatewayError?.description || visibleError}</div>
          {parsedGatewayError?.type === "quota" && parsedGatewayError.recommendedModel && selected && (
            <div className="m-gateway-alert-actions">
              <span>推荐切换：</span>
              <button
                type="button"
                className="m-gateway-rec-btn"
                disabled={readOnly || modelLoading}
                onClick={() => {
                  if (readOnly || modelLoading) return
                  const rec = parsedGatewayError.recommendedModel!
                  void sessionModel.change(rec.provider, rec.model)
                    .then(() => {
                      setSessionError(null)
                      setSubmittedCommandId(null)
                      notify("已切换至 " + rec.label, "teal")
                    })
                    .catch(e => setSessionError(messageError(e)))
                }}
              >
                切换至 {parsedGatewayError.recommendedModel.label}
              </button>
            </div>
          )}
        </div>
      )}

      {selected && <SessionComposerGate key={`gate:${key}`} session={selected} onFork={() => void handleForkChatBranch(selected)}>
      {!!pending.length && <div className="m-queue-list" role="region" aria-label="待发队列">
      <AnimatePresence initial={false}>
        {pending.map(entry => (
          <motion.article className={queueLift?.id === entry.payload.id ? 'm-queue-item is-lifted' : 'm-queue-item'} key={entry.payload.id} data-command-id={entry.payload.id} aria-label="排队消息" layout={!reducedMotion} initial={reducedMotion ? false : { opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, x: reducedMotion ? 0 : 24 }} transition={{ duration: reducedMotion ? 0 : 0.2 }} onPointerDown={event => onQueuePointerDown(entry, event)} onPointerMove={onQueuePointerMove} onPointerUp={onQueuePointerUp} onPointerCancel={onQueuePointerCancel} onTouchStart={event => event.stopPropagation()}>
            <p>{entry.payload.text || '附件消息'}</p>
            <small>{queueLabels[entry.state]}{entry.attachments.length + entry.files.length > 0 ? ` · ${entry.attachments.length + entry.files.length} 个附件` : ''}</small>
            {entry.error && <small role="alert">{entry.error}</small>}
            <div className="m-queue-actions" onPointerDown={event => event.stopPropagation()}>
              {['queued', 'failed'].includes(entry.state) && <button type="button" disabled={!canSubmit || submitting} aria-label={entry.state === 'failed' ? '重新发送' : nativeBusy ? '立即引导' : '发送待发消息'} onClick={() => void sendQueued(entry)}>{entry.state === 'failed' ? '重新发送' : nativeBusy ? '立即引导' : '发送'}</button>}
              {['queued', 'failed', 'cancelled'].includes(entry.state) && <button type="button" aria-label="编辑待发消息" onClick={() => void editQueued(entry)}><IconEdit size={16} /></button>}
              {entry.state === 'unknown' && <button type="button" aria-label="核对发送结果" onClick={() => void refresh()}>核对结果</button>}
              <button type="button" aria-label="删除" onClick={() => void (directOutbox.snapshot().some(row => row === entry) ? directOutbox : outbox).remove(entry.payload.agent_id, entry.payload.session_id, entry.payload.id).catch(error => notify(messageError(error), 'red'))}><IconTrash size={16} /></button>
            </div>
          </motion.article>
        ))}
      </AnimatePresence>
      </div>}
      {!!activeTasks.length && <div className="m-active-tasks">{activeTasks.map(item => <button key={item.id} onClick={() => item.kind === 'background' ? setSheet('tasks') : openTask(item)}><span className="m-dot online" />{item.title}</button>)}</div>}
      {!!approvals.length && <button className="m-queue-banner" onClick={() => setSheet('status')}>{approvals.some(isUserInput) ? '等待回答' : '等待授权'} · {approvals.length} 项</button>}
      {quote && <div className="m-quote"><span>{quote}</span><button aria-label="取消引用" onClick={() => setQuote('')}><IconX size={16} /></button></div>}
      {!!files.length && <div className="m-attachments">{files.map((file, i) => <MobileAttachmentPreview key={`${file.name}-${i}`} file={file} onOpen={setImage} onRemove={() => updateFiles(files.filter((_, n) => n !== i))} />)}</div>}
      <div style={{ position: 'relative' }}>
      <AgentCommandMenu agent={agents[selected?.agent_id || '']} items={agentCommands} activeIndex={commandIndex} onSelect={selectAgentCommand} />
      <AgentMentionMenu agent={agents[selected?.agent_id || '']} items={agentMentions} activeIndex={commandIndex} onSelect={selectAgentMention} />
      {voice.error && <div className="mobile-voice-status" role="alert">{voice.error}</div>}
      {voice.status === 'transcribing' && <div className="mobile-voice-status" role="status">正在转写…</div>}
      <div className="m-composer">
        <input hidden ref={fileInput} type="file" multiple onChange={e => { updateFiles([...files, ...Array.from(e.target.files || [])]); e.target.value = '' }} />
          <button aria-label="添加附件" onClick={() => fileInput.current?.click()}><IconPlus size={20} /></button>
          <MobileVoiceHold scope={key} voice={voice}><textarea ref={textarea} aria-label="消息内容" placeholder="输入消息，或按住说话…" rows={1} value={text} onPaste={event => { const pasted = clipboardFiles(event); if (pasted.length) { event.preventDefault(); updateFiles([...files, ...pasted]) } }} onKeyDown={handleKeyDown} onChange={e => { setCommandIndex(0); setDismissedMenuText(null); setText(e.target.value) }} /></MobileVoiceHold>
          <button aria-label={voice.status === 'recording' || voice.status === 'requesting' ? '停止录音' : voice.status === 'transcribing' ? '正在转写' : '语音消息'} disabled={voice.status === 'transcribing'} onClick={voice.toggle}>{voice.status === 'recording' || voice.status === 'requesting' ? <IconPlayerStop size={19} color="#ef4444" /> : <IconMicrophone size={19} />}</button>
          <ClickSpark className="m-send-spark" sparkColor={busy && !text && !files.length ? '#ef4444' : '#3b82f6'} sparkSize={10} sparkRadius={28} sparkCount={8}>
            <button type="button" className="m-send" data-stop={busy && !text && !files.length} aria-label={busy && !text && !files.length ? '停止' : '发送'} disabled={submitting || !selected} onPointerDown={pressSend} onClick={() => pressSend()}>{submitting ? <IconLoader2 className="m-spin" size={18} /> : busy && !text && !files.length ? <IconPlayerStop size={17} /> : <IconSend size={18} />}</button>
          </ClickSpark>
        </div>
      </div>
      </SessionComposerGate>}
    </section>
    {messageAction?.sessionKey === key && <MobileMessageMenu anchor={messageAction} onClose={closeMessageMenu} onCopy={() => void copy(messageAction.text)} onQuote={() => { setQuote(messageAction.text); textarea.current?.focus({ preventScroll: true }) }} />}
    {sheet && <div className={`m-backdrop ${sheet === 'sessions' ? 'm-session-backdrop' : ''}`} onClick={() => setSheet(null)} onTouchStartCapture={handleDrawerTouchStart} onTouchMoveCapture={handleDrawerTouchMove} onTouchEndCapture={handleDrawerTouchEnd} onTouchCancelCapture={handleDrawerTouchCancel}><section ref={node => { drawerSheet.current = node }} style={sheet === 'sessions' && drawerOffset !== null ? { transform: `translate3d(${drawerOffset}px, 0, 0)`, transition: drawerSettling ? 'transform .22s cubic-bezier(.2,.8,.2,1)' : 'none', animation: 'none' } : undefined} className={`m-sheet ${sheet === 'sessions' ? 'm-session-sheet' : sheet === 'blackboard' || sheet === 'browser' ? 'm-blackboard-dialog' : ''}`} role="dialog" aria-label={sheet === 'sessions' ? '会话列表' : sheet === 'blackboard' ? '会话黑板' : sheet === 'browser' ? '浏览器镜像' : '详情'} onClick={e => e.stopPropagation()} onTouchStart={handleDrawerTouchStart} onTouchMove={handleDrawerTouchMove} onTouchEnd={handleDrawerTouchEnd} onTouchCancel={handleDrawerTouchCancel}><div className="m-handle" /><header><h2>{({ sessions: '会话', status: '运行状态', goal: '目标管理', tasks: '任务与子代理', task: '任务详情', models: '选择模型', connections: '连接管理', gateway: '模型网关', blackboard: '黑板', browser: '浏览器镜像' })[sheet]}</h2><div className="m-sheet-header-actions">{sheet === 'sessions' && <><button aria-label="筛选" aria-pressed={filtersOpen} onClick={() => setFiltersOpen(open => !open)}><IconFilter size={18} /></button><button aria-label="新建项目" onClick={() => setAddProjectOpened(true)}><IconPlus size={20} /></button></>}<button aria-label="关闭面板" onClick={() => setSheet(null)}><IconX size={20} /></button></div></header>
      {sheet === 'connections' && <div className="m-sheet-body"><EnvironmentConnections embedded /><RtkSettingsPage /></div>}
      {sheet === 'gateway' && <div className="m-sheet-body" style={{ padding: 12 }}><UsageGatewaySettingsCard /></div>}
      {sheet === 'sessions' && filtersOpen && <><div className="m-drawer-filter-row"><AgentSessionFilter agents={agents} value={agentFilter} onChange={updateAgentFilter} /></div><nav className="m-filters">{[['all','全部'],['unread','未读'],['open','开放中'],['pinned','置顶'],['recent','24小时']].map(([id,label]) => <button className={filter === id ? 'active' : ''} key={id} onClick={() => setFilter(id)}>{label}</button>)}</nav></>}
      {sheet === 'sessions' && <><div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}><input className="m-search" aria-label="搜索会话" placeholder="搜索会话" value={search} onChange={e => setSearch(e.target.value)} />{search && <button aria-label="清空搜索" onClick={() => setSearch('')} style={{ position: 'absolute', right: 8, padding: 4, display: 'inline-flex', alignItems: 'center', color: 'var(--m-muted)' }}><IconX size={16} /></button>}</div><MobileSessionDrawer groups={groups} pins={pins} pinnedProjects={pinnedProjects} onPinProject={project => { void updatePreferences(value => ({ pinned_projects: value.pinned_projects.includes(project.key) ? value.pinned_projects.filter(key => key !== project.key) : [project.key, ...value.pinned_projects] })) }} selectedKey={key} appearance={appearance} onSelect={select} onCreate={project => { setCreateProject(project); setCreateOpened(true) }} onDeleteProject={requestDeleteProject} onDeleteSession={requestDeleteSession} onPin={s => { const sessionKey = scopeKey(s.agent_id, s.id); void updatePreferences(value => ({ session_pins: { [sessionKey]: !value.session_pins[sessionKey] } })) }} agents={agents} onHandoffSession={setHandoffTarget} onRenameSession={openRename} onCopySessionId={copySessionId} onForkSession={handleForkChatBranch} onForkWorktreeSession={setForkWorktreeTarget} isSearching={Boolean(search.trim())} /></>}
      {sheet === 'status' && selected && <div className="m-sheet-body"><MobileApprovals session={selected} approvals={approvals} /><SessionRuntimeFacts session={selected} agent={agents[selected.agent_id]} />{agents[selected.agent_id]?.kind==='codex' && <NativeObservationPanel agentId={selected.agent_id} sessionId={selected.id} />}</div>}
      {sheet === 'goal' && selected && <div className="m-sheet-body m-goal-panel"><GoalManager key={key} session={selected} agent={agents[selected.agent_id]} tasks={tasks} disabled={readOnly} /></div>}
      {sheet === 'blackboard' && selected && <div className="m-sheet-body m-blackboard-sheet"><MobileBlackboardPanel namespace={`session:${selected.agent_id}::${selected.id}`} /></div>}
      {sheet === 'browser' && selected && <div className="m-sheet-body m-blackboard-sheet"><BrowserMirrorPanel agentId={selected.agent_id} sessionId={selected.id} /></div>}
      {sheet === 'tasks' && <div className="m-sheet-body m-runtime-panel"><SessionRuntimeBar key={key} commands={commands} tasks={runtimeTasks} backgroundError={background.error} onTaskOpen={openTask} />{!visibleTasks.length && <p>当前会话暂无任务记录。</p>}</div>}
      {sheet === 'task' && task && <div className="m-sheet-body m-task-panel"><TaskDetails key={`${key}:${task.id}`} task={task} canStop={Boolean(task.target_id && resolveTaskTarget(task) && resolveTaskTarget(task)?.control_state !== 'readonly' && !readOnly && agents[task.agent_id]?.capabilities.includes('stop') && ['pending', 'running', 'waiting_approval'].includes(task.status))} onStop={item => void handleStopTask(item)} onOpenSession={(sid, aid) => { const child = sessions.find(session => session.id === sid && session.agent_id === aid); if (child) select(child); else notify('子会话尚未同步，请刷新后重试', 'red') }} onJumpToLatest={() => { const viewport = document.querySelector('.m-task-panel .task-log-scroll [data-radix-scroll-area-viewport]'); if (viewport) viewport.scrollTop = viewport.scrollHeight }} onClose={() => setSheet('tasks')} /></div>}
      {sheet === 'models' && <div className="m-sheet-body m-model-panel">{selected && <ApprovalModeControl key={key} session={selected} variant="panel" disabled={readOnly} />}<div className="m-effort-row">{REASONING_EFFORTS.map(item => <button key={item.value} aria-pressed={sessionModel.effort === item.value} aria-label={`思考强度 ${item.label}`} disabled={readOnly || modelLoading} onClick={() => void sessionModel.changeEffort(item.value).catch(error => notify(messageError(error), 'red'))}>{item.label}</button>)}</div><input className="m-search" aria-label="搜索模型" placeholder="搜索模型或提供商" value={modelSearch} onChange={e => setModelSearch(e.target.value)} />{modelLoading && <p>正在处理原生模型请求…</p>}<div className="m-model-list">{modelChoices.filter(choice => choice.label.toLowerCase().includes(modelSearch.toLowerCase())).map(choice => <button className="m-model-choice" aria-pressed={modelSelection?.provider === choice.provider && modelSelection?.model === choice.model} key={`${choice.provider}/${choice.model}`} disabled={readOnly || modelLoading} onClick={() => setModelSelection(choice)}><IconCpu size={17} /><span>{choice.label}</span>{modelSelection?.provider === choice.provider && modelSelection?.model === choice.model && <IconCheck size={17} />}</button>)}</div>{!modelLoading && !modelChoices.length && <p>原生运行时未返回可用模型。</p>}{modelSelection && <div className="m-model-confirm"><small>{modelSelection.label}</small><button aria-label="确认切换模型" disabled={readOnly || modelLoading} onClick={() => void chooseModel(modelSelection.provider, modelSelection.model)}>{modelLoading ? '切换中…' : '确认切换模型'}</button></div>}</div>}
    </section></div>}
    {workbenchMounted && selected && <MobileWorkbench session={selected} agent={agents[selected.agent_id]} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} opened={workbenchOpened} onClose={closeWorkbench} />}
    {image && <div className="m-lightbox" role="dialog" aria-label="图片预览" onClick={() => setImage(null)}><button aria-label="关闭图片"><IconX size={22} /></button><img src={image} alt="预览" /></div>}

    <ConfirmPopover
      opened={confirmation !== null}
      coords={confirmation?.coords}
      title={confirmation?.kind === 'stop-all' ? '终止全部任务？' : confirmation?.kind === 'delete-project' ? '删除项目？' : '删除会话？'}
      message={confirmation?.kind === 'stop-all'
        ? '终止当前会话的全部运行任务？'
        : confirmation?.kind === 'delete-project'
            ? ((confirmation.project.sessionCount || confirmation.project.sessions.length) > 0
                ? `确定删除项目“${confirmation.project.label}”吗？\n此操作将同时删除该项目及其包含的 ${confirmation.project.sessionCount || confirmation.project.sessions.length} 个会话。`
                : `确定删除项目“${confirmation.project.label}”吗？`)
            : confirmation ? `确定删除会话“${displaySessionTitle(confirmation.session)}”吗？` : ''}
      confirmLabel={confirmation?.kind === 'stop-all' ? '终止全部' : '删除'}
      loading={confirmationLoading}
      onConfirm={confirmMobileAction}
      onCancel={() => {
        if (!confirmationLoading) setConfirmation(null)
      }}
    />
    {forkWorktreeTarget && (
      <ForkWorktreeDialog
        session={forkWorktreeTarget}
        onClose={() => setForkWorktreeTarget(null)}
        onCreated={(newSession) => {
          select(newSession)
          setSheet(null)
        }}
      />
    )}
    {handoffTarget && (
      <HandoffDialog
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
        onOpenTransferred={(session) => select(session)}
      />
    )}
    <Modal
      opened={renameSession !== null}
      onClose={() => setRenameSession(null)}
      title="重命名会话"
      centered
      size="sm"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void handleRename()
        }}
      >
        <TextInput
          label="会话名称"
          data-autofocus
          value={renameTitle}
          onChange={(e) => setRenameTitle(e.currentTarget.value)}
          placeholder="输入新的会话名称"
          mb="md"
        />
        <Group justify="flex-end" gap="xs">
          <Button variant="default" onClick={() => setRenameSession(null)}>取消</Button>
          <Button type="submit" loading={renameLoading} disabled={!renameTitle.trim()}>保存</Button>
        </Group>
      </form>
    </Modal>
    {queueLift && <div className="m-queue-ghost" data-zone={queueLift.zone || undefined} style={{ left: queueLift.x, top: queueLift.y }}>{queueLift.text}</div>}
    <BackgroundTasks />
  </div>
}
