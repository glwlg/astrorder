import {
  IconBrain,
  IconChecklist,
  IconChalkboard,
  IconFolder,
  IconGitFork,
  IconInfoCircle,
  IconLayoutSidebarRightExpand,
  IconSparkles,
  IconTransfer,
  IconX,
} from '@tabler/icons-react'
import { ActionIcon, Button, Drawer, Group, Modal, Select, Stack, Switch, Text, TextInput, Title, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useDisclosure, useMediaQuery } from '@mantine/hooks'
import { useQueryClient, useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useShallow } from 'zustand/react/shallow'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../../api/client'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Approval, Command, Message, Session, Task } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { SessionStatusLabel } from '../../components/Status'
import { WelcomeView } from './WelcomeView'
import { sessionActivityStatus } from '../../components/sessionRailModel'
import { AgentKindBadge } from '../../components/SessionRuntimeFacts'
import { selectApprovals, selectCommands, selectOutbox, selectSessions, selectTasks, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { ChatComposer } from './ChatComposer'
import { QuickOpen } from './QuickOpen'
import { appendReviewOnce, buildUncommittedReviewCommand, extractNativeReviewComments, isRecoverableReviewRun, REVIEW_TIMEOUT_ERROR, reviewOutputPending, reviewRunTimedOut } from './reviewRelay'
import { REASONING_EFFORTS } from './composerMedia'


import { SessionDetails } from './SessionDetails'
import { SessionRuntimeBar } from './SessionRuntimeBar'
import { TaskDetails } from './TaskDetails'
import { Transcript } from './Transcript'
import { SidecarHost } from '../sidecar/SidecarHost'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { BlurText } from '../../components/animations/BlurText'
import { ErrorBoundary } from '../../components/ErrorBoundary'
import { BotGroupChatPage } from './BotGroupChatPage'


const NO_COMMANDS: Command[] = []
const NO_OUTBOX: import('../../domain/types').OutboxEntry[] = []
const NO_APPROVALS: Approval[] = []
const NO_TASKS: Task[] = []

function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '会话数据读取失败。'
}

function resolveSession(sessions: Session[], sessionId: string | undefined, agentId: string | null): Session | null {
  if (!sessionId) return sessions.length === 1 ? sessions[0] : null
  const decodedId = decodeURIComponent(sessionId)
  const candidates = sessions.filter((session) => session.id === decodedId || session.id === sessionId)
  if (agentId) return candidates.find((session) => session.agent_id === agentId) || null
  return candidates.length === 1 ? candidates[0] : null
}

export function ChatPage() {
  const { sessionId = '' } = useParams()
  const [searchParams] = useSearchParams()
  const routeKey = scopeKey(searchParams.get('agent_id') || '', sessionId)
  const [bodyKey, setBodyKey] = useState(routeKey)

  useEffect(() => {
    if (bodyKey === routeKey) return
    const frame = window.requestAnimationFrame(() => setBodyKey(routeKey))
    return () => window.cancelAnimationFrame(frame)
  }, [bodyKey, routeKey])

  if (bodyKey !== routeKey) {
    return (
      <div className="route-page chat-page chat-page-switching" aria-live="polite">
        <div className="chat-layout">
          <section className="chat-column">
            <div className="transcript">
              <div className="transcript-inner">
                <p className="transcript-status">正在打开会话…</p>
              </div>
            </div>
          </section>
        </div>
      </div>
    )
  }

  return <ChatPageBody key={routeKey} />
}

function ChatPageBody() {
  const { sessionId } = useParams()
  const [searchParams] = useSearchParams()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const selected = useMemo(
    () => resolveSession(sessions, sessionId ? decodeURIComponent(sessionId) : undefined, searchParams.get('agent_id')),
    [searchParams, sessionId, sessions],
  )

  const sessionUsage = useQuery({ queryKey: ['astrorder', 'session_usage', selected?.id, selected?.agent_id], queryFn: () => selected ? api.getSessionUsage(selected.id, selected.agent_id) : Promise.reject(), enabled: Boolean(selected), retry: false, staleTime: 0, refetchOnWindowFocus: false })

  const [activeBotGroup, setActiveBotGroup] = useState<import('../../domain/types').BotGroup | null>(null)
  useEffect(() => {
    if (sessionId && sessionId.startsWith('group-')) {
      let mounted = true
      void api.getBotGroup(sessionId).then(res => {
        if (mounted) setActiveBotGroup(res.group)
      }).catch(() => {
        if (mounted) setActiveBotGroup(null)
      })
      return () => { mounted = false }
    } else {
      setActiveBotGroup(null)
    }
  }, [sessionId])
  const [forkModalOpen, setForkModalOpen] = useState(false)
  const [forkTargetMessage, setForkTargetMessage] = useState<{ message: Message; turnIndex: number } | null>(null)
  const [forkTitle, setForkTitle] = useState('')
  const [forkWorktree, setForkWorktree] = useState(false)
  const [forkBranchName, setForkBranchName] = useState('')
  const [forkLoading, setForkLoading] = useState(false)
  const [reviewModalOpen, setReviewModalOpen] = useState(false)
  const [reviewPeerKey, setReviewPeerKey] = useState<string | null>(null)
  const [reviewCreateNew, setReviewCreateNew] = useState(false)
  const [reviewAgentId, setReviewAgentId] = useState<string | null>(null)
  const [reviewModelKey, setReviewModelKey] = useState<string | null>(null)
  const [reviewEffort, setReviewEffort] = useState<string | null>(null)
  const [reviewBusy, setReviewBusy] = useState(false)
  const reviewAgents = useMemo(() => Object.values(agents).filter((candidate) => (
    candidate.kind === 'codex' && candidate.status === 'ready'
    && (candidate.connection_id || 'local') === (selected?.connection_id || agents[selected?.agent_id || '']?.connection_id || 'local')
  )), [agents, selected])
  const reviewProbe = sessions.find((candidate) => candidate.agent_id === reviewAgentId)
  const reviewModels = useQuery({
    queryKey: ['astrorder', 'review-models', reviewAgentId, reviewProbe?.id],
    queryFn: () => api.getSessionModels(reviewProbe!.id, reviewAgentId!),
    enabled: reviewModalOpen && reviewCreateNew && Boolean(reviewProbe),
    retry: false,
  })
  const [reviewRun, setReviewRun] = useState<{ runId: string; commandId: string; peer: Session; baselineIds: Set<string>; startedAt: number; completedWithoutCommentsAt?: number; status: 'reviewing' | 'draft_ready' | 'empty' | 'failed'; error?: string } | null>(null)
  const reviewPeers = useMemo(() => sessions.filter((candidate) => (
    candidate.id !== selected?.id
    && candidate.workspace
    && candidate.workspace === selected?.workspace
    && (candidate.connection_id || agents[candidate.agent_id]?.connection_id || 'local') === (selected?.connection_id || agents[selected?.agent_id || '']?.connection_id || 'local')
    && agents[candidate.agent_id]?.kind === 'codex'
  )), [agents, selected, sessions])

  useEffect(() => {
    if (!selected || reviewRun) return
    let cancelled = false
    void api.getReviewRelayRuns(selected.agent_id, selected.id).then(({ items }) => {
      if (cancelled || !items.length) return
      const persisted = items[0]
      if (!isRecoverableReviewRun(persisted)) return
      const peer = reviewPeers.find((candidate) => (
        candidate.agent_id === persisted.review_agent_id && candidate.id === persisted.review_session_id
      ))
      if (!peer || persisted.status === 'validating') return
      setReviewRun({
        runId: persisted.id,
        commandId: persisted.command_id,
        peer,
        baselineIds: new Set(persisted.baseline_ids),
        startedAt: Date.parse(persisted.created_at) || Date.now(),
        status: 'reviewing',
      })
    }).catch(() => undefined)
    return () => { cancelled = true }
  }, [reviewPeers, reviewRun, selected])

  useEffect(() => {
    if (!reviewRun || reviewRun.status !== 'reviewing') return
    let stopped = false
    let polling = false
    const poll = async () => {
      if (polling) return
      polling = true
      try {
        const [commandsResult, messagesResult] = await Promise.all([
          api.getCommands(reviewRun.peer.id, reviewRun.peer.agent_id),
          api.getMessages(reviewRun.peer.id, reviewRun.peer.agent_id, undefined, 100),
        ])
        if (stopped) return
        const command = commandsResult.items.find((item) => item.id === reviewRun.commandId)
        const freshText = messagesResult.items
          .filter((message) => !reviewRun.baselineIds.has(message.id))
          .map((message) => message.text)
          .join('\\n')
        const extracted = extractNativeReviewComments(freshText)
        const terminal = command?.state === 'completed' || command?.state === 'failed' || command?.state === 'cancelled'
        if (!terminal) {
          if (reviewRunTimedOut(command?.state, reviewRun.startedAt, Date.now())) {
            if (reviewRun.runId) void api.updateReviewRelayRun(reviewRun.runId, { status: 'failed', error: REVIEW_TIMEOUT_ERROR })
            setReviewRun((current) => current ? { ...current, status: 'failed', error: REVIEW_TIMEOUT_ERROR } : current)
          }
          return
        }
        if (command.state !== 'completed') {
          if (reviewRun.runId) void api.updateReviewRelayRun(reviewRun.runId, { status: 'failed', error: command.error || 'Codex 审查未完成。' })
          setReviewRun((current) => current ? { ...current, status: 'failed', error: command.error || 'Codex 审查未完成。' } : current)
          return
        }
        if (!extracted.rawText) {
          if (!reviewRun.completedWithoutCommentsAt) {
            setReviewRun((current) => current?.runId === reviewRun.runId ? { ...current, completedWithoutCommentsAt: Date.now() } : current)
            return
          }
          if (reviewOutputPending(reviewRun.completedWithoutCommentsAt, Date.now())) return
          if (reviewRun.runId) void api.updateReviewRelayRun(reviewRun.runId, { status: 'empty' })
          setReviewRun((current) => current?.runId === reviewRun.runId ? { ...current, status: 'empty' } : current)
          return
        }
        const latest = await api.getReviewRelayRuns(selected!.agent_id, selected!.id)
        if (stopped || latest.items[0]?.id !== reviewRun.runId) return
        const currentDraft = useAstrorderStore.getState().drafts[scopeKey(selected?.agent_id || '', selected?.id || '')] || { text: '', attachments: [], sessionRefs: [] }
        if (selected && currentDraft) {
          useAstrorderStore.getState().setDraft(selected.agent_id, selected.id, appendReviewOnce(currentDraft, extracted.rawText))
        }
        if (reviewRun.runId) void api.updateReviewRelayRun(reviewRun.runId, { status: 'draft_ready', comment_text: extracted.rawText })
        setReviewRun((current) => current ? { ...current, status: 'draft_ready' } : current)
      } catch (error) {
        if (!stopped) {
          if (reviewRun.runId) void api.updateReviewRelayRun(reviewRun.runId, { status: 'failed', error: errorText(error) })
          setReviewRun((current) => current ? { ...current, status: 'failed', error: errorText(error) } : current)
        }
      } finally {
        polling = false
      }
    }
    void poll()
    const timer = window.setInterval(() => void poll(), 2000)
    return () => { stopped = true; window.clearInterval(timer) }
  }, [reviewRun, selected])

  const startReview = async (peerOverride: Session | string | null = reviewPeerKey) => {
    if (!selected || !peerOverride) return
    const peer = typeof peerOverride === 'string'
      ? reviewPeers.find((candidate) => scopeKey(candidate.agent_id, candidate.id) === peerOverride)
      : peerOverride
    if (!peer || peer.workspace !== selected.workspace) {
      notifications.show({ color: 'red', message: '开发会话与审查会话不在同一个工作区。' })
      return
    }
    try {
      await api.validateReviewRelayWorkspaces(selected, peer)
      await api.saveReviewRelayBinding(selected.agent_id, selected.id, {
        review_agent_id: peer.agent_id,
        review_session_id: peer.id,
        workspace: selected.workspace || '',
        enabled: true,
      })
      if (selected.last_user_at) localStorage.setItem(`astrorder:review-triggered:${scopeKey(selected.agent_id, selected.id)}`, selected.last_user_at)
      const baseline = await api.getMessages(peer.id, peer.agent_id, undefined, 100)
      const command = buildUncommittedReviewCommand({ id: newCommandId(), agent_id: peer.agent_id, session_id: peer.id })
      const run = await api.createReviewRelayRun({
        source_agent_id: selected.agent_id,
        source_session_id: selected.id,
        review_agent_id: peer.agent_id,
        review_session_id: peer.id,
        command_id: command.id,
        baseline_ids: baseline.items.map((message) => message.id),
        status: 'validating',
      })
      try {
        await api.createCommand(command)
        await api.updateReviewRelayRun(run.id, { status: 'reviewing' })
      } catch (error) {
        await api.updateReviewRelayRun(run.id, { status: 'failed', error: errorText(error) }).catch(() => undefined)
        throw error
      }
      setReviewRun({ runId: run.id, commandId: command.id, peer, baselineIds: new Set(baseline.items.map((message) => message.id)), startedAt: Date.now(), status: 'reviewing' })
      setReviewModalOpen(false)
      notifications.show({ color: 'blue', message: '已按审查会话中的「审查未提交的更改」发送请求。' })
    } catch (error) {
      notifications.show({ color: 'red', message: `启动审查失败：${errorText(error)}` })
    }
  }
  const createReviewPeer = async () => {
    if (!selected?.workspace || !reviewAgentId || !reviewModelKey || !reviewEffort || reviewBusy) return
    setReviewBusy(true)
    try {
      const [provider, model] = JSON.parse(reviewModelKey) as [string, string]
      const peer = await api.createSession({ agent_id: reviewAgentId, workspace: selected.workspace, title: `Codex 审查 · ${selected.title || '开发会话'}` })
      useAstrorderStore.setState((state) => ({ sessions: { ...state.sessions, [scopeKey(peer.agent_id, peer.id)]: peer } }))
      await api.setSessionModel(peer.id, peer.agent_id, provider, model)
      await api.setSessionReasoning(peer.id, peer.agent_id, reviewEffort)
      setReviewPeerKey(scopeKey(peer.agent_id, peer.id))
      await startReview(peer)
    } catch (error) {
      notifications.show({ color: 'red', message: `创建审查会话失败：${errorText(error)}` })
    } finally {
      setReviewBusy(false)
    }
  }
  useEffect(() => {
    if (!selected || selected.status !== 'idle' || reviewRun || !selected.last_user_at) return
    let cancelled = false
    const revision = selected.last_user_at
    const markerKey = `astrorder:review-triggered:${scopeKey(selected.agent_id, selected.id)}`
    void api.getReviewRelayBinding(selected.agent_id, selected.id).then((binding) => {
      if (cancelled || !binding?.enabled) return
      const bindingKey = scopeKey(binding.review_agent_id, binding.review_session_id)
      if (localStorage.getItem(markerKey) === revision) return
      if (!reviewPeers.some((peer) => scopeKey(peer.agent_id, peer.id) === bindingKey)) return
      localStorage.setItem(markerKey, revision)
      void startReview(bindingKey)
    }).catch(() => undefined)
    return () => { cancelled = true }
  }, [reviewPeers, reviewRun, selected])
  const handoffPeer = useMemo(() => {
    if (!selected) return null
    if (selected.handoff_from_agent_id && selected.handoff_from_session_id) {
      const source = sessions.find((session) =>
        session.agent_id === selected.handoff_from_agent_id
        && session.id === selected.handoff_from_session_id,
      )
      return source ? { session: source, label: `来自 ${agents[source.agent_id]?.kind === 'codex' ? 'Codex' : 'Hermes'}` } : null
    }
    const target = sessions.find((session) =>
      session.handoff_from_agent_id === selected.agent_id
      && session.handoff_from_session_id === selected.id,
    )
    return target ? { session: target, label: `已转交至 ${agents[target.agent_id]?.kind === 'codex' ? 'Codex' : 'Hermes'}` } : null
  }, [agents, selected, sessions])
  const [detailsOpened, { open: openDetails, close: closeDetails }] = useDisclosure(false)
  const [detailsPinned, setDetailsPinned] = useState(false)
  const layoutRef = useRef<HTMLDivElement>(null)
  const sidecarOpen = useSidecarStore((state) => state.isOpen)
  const setSidecarOpen = useSidecarStore((state) => state.setIsOpen)
  const switchSession = useSidecarStore((state) => state.switchSession)
  const sidecarWidth = useSidecarStore((state) => state.sidecarWidth)
  const setSidecarWidth = useSidecarStore((state) => state.setSidecarWidth)
  const [isResizing, setIsResizing] = useState(false)

  // 当会话/项目发生切换时，自动保存并切换侧边栏记忆状态，保持每个会话 Tab 隔离且不丢失
  useEffect(() => {
    if (selected) {
      const sessionKey = `${selected.agent_id}:${selected.id}`
      switchSession(sessionKey)
    }
  }, [selected?.id, selected?.agent_id, switchSession])

  // 拖动调宽逻辑：基于 chat-layout 容器的实际几何位置与宽度进行精确计算
  useEffect(() => {
    if (!isResizing) return
    const handleMouseMove = (e: MouseEvent) => {
      if (!layoutRef.current) return
      const rect = layoutRef.current.getBoundingClientRect()
      const layoutWidth = rect.width
      if (layoutWidth <= 0) return
      // 鼠标相对于 chat-layout 右边界的距离
      const rightPx = rect.right - e.clientX
      const pct = (rightPx / layoutWidth) * 100
      setSidecarWidth(pct)
    }
    const handleMouseUp = () => {
      setIsResizing(false)
    }
    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
    return () => {
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }
  }, [isResizing, setSidecarWidth])

  // 全局快捷键监听（对齐 Codex：Ctrl+P 打开文件树, Ctrl+Alt+S 侧边聊天, Ctrl+T 浏览器, Ctrl+` 终端）
  // 必须在 capture 阶段或尽早拦截并 preventDefault，屏蔽浏览器默认行为（如 Ctrl+P 打印, Ctrl+T 新标签页）
  useEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (!selected) return
      const isCtrlOrMeta = e.ctrlKey || e.metaKey

      // 1. 侧边聊天快捷键：Ctrl + Alt + S
      if (isCtrlOrMeta && e.altKey && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openSideChat(
            selected.id,
            selected.agent_id,
            selected.title || undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 决策状态机快捷键：Ctrl + Alt + G
      if (isCtrlOrMeta && e.altKey && (e.key === 'g' || e.key === 'G')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openAgentGraph(
            selected.id,
            selected.agent_id,
            selected.title || undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 知识记忆快捷键：Ctrl + Alt + M
      if (isCtrlOrMeta && e.altKey && (e.key === 'm' || e.key === 'M')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore.getState().openMemory(selected.id)
        return
      }

      // 黑板快捷键：Ctrl + Alt + B
      if (isCtrlOrMeta && e.altKey && (e.key === 'b' || e.key === 'B')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openBlackboard(
            selected.id,
            selected.agent_id,
            selected.connection_id || undefined,
          )
        return
      }

      // 2. Quick Open 快捷键：Ctrl + P (屏蔽浏览器原生打印，模糊搜文件)
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === 'p' || e.key === 'P')) {
        e.preventDefault()
        e.stopPropagation()
        setQuickOpenOpened(true)
        return
      }

      // 文件树快捷键：Ctrl + Shift + E
      if (isCtrlOrMeta && !e.altKey && e.shiftKey && (e.key === 'e' || e.key === 'E')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openFileTree(
            selected.id,
            selected.agent_id,
            selected.workspace || undefined,
            selected.project_name || selected.title,
            selected.connection_id || undefined,
          )
        return
      }

      // 3. 内置浏览器快捷键：Ctrl + T (屏蔽浏览器打开新标签页)
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === 't' || e.key === 'T')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openBrowser(
            selected.id,
            selected.agent_id,
            undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 4. 内置终端快捷键：Ctrl + `
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === '`' || e.key === '~')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openTerminal(
            selected.id,
            selected.agent_id,
            selected.project_name || selected.title,
            selected.connection_id || undefined,
          )
        return
      }
    }

    window.addEventListener('keydown', handleGlobalKeyDown, { capture: true })
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown, { capture: true })
    }
  }, [selected])
  const [taskDetailsOpened, { open: openTaskDetails, close: closeTaskDetails }] = useDisclosure(false)
  const [taskSelection, setTaskSelection] = useState<Pick<Task, 'id' | 'agent_id' | 'session_id'> | null>(null)
  const [gallery, setGallery] = useState<{ images: string[]; index: number } | null>(null)
  const openGallery = (images: string[], index: number) => {
    const desktop = (window as unknown as { astrorderDesktop?: { openPreview?: (payload: { images: string[]; index: number }) => Promise<void> } }).astrorderDesktop
    if (desktop?.openPreview) {
      const abs = images.map((src) => {
        try { return new URL(src, window.location.origin).href } catch { return src }
      })
      void desktop.openPreview({ images: abs, index })
      return
    }
    setGallery({ images, index })
  }
  const [quickOpenOpened, setQuickOpenOpened] = useState(false)
  const [composerHeight, setComposerHeight] = useState<number>(124)
  const mobileTaskSheet = useMediaQuery('(max-width: 767px)')
  const resources = useSessionResources(selected, true)
  const messages = resources.visibleMessages
  const commands = useAstrorderStore(useShallow((state) => selected ? selectCommands(state, selected.agent_id, selected.id) : NO_COMMANDS))
  const outbox = useAstrorderStore(useShallow((state) => selected ? selectOutbox(state, selected.agent_id, selected.id) : NO_OUTBOX))
  const approvals = useAstrorderStore(useShallow((state) => selected ? selectApprovals(state, selected.agent_id, selected.id) : NO_APPROVALS))
  const tasks = useAstrorderStore(useShallow((state) => selected ? selectTasks(state, selected.agent_id, selected.id) : NO_TASKS))
  const selectedTask = tasks.find((task) => task.id === taskSelection?.id && task.agent_id === taskSelection.agent_id && task.session_id === taskSelection.session_id) ?? null
  const agent = useMemo(() => {
    if (!selected) return undefined
    // 优先精确匹配
    if (agents[selected.agent_id]) return agents[selected.agent_id]
    // 跨别名匹配：local-hermes-default 与 local-hermes-hermes 互相兼容
    if (['local-hermes-default', 'local-hermes-hermes'].includes(selected.agent_id)) {
      const localAgent = Object.values(agents).find((a) => ['local-hermes-default', 'local-hermes-hermes'].includes(a.id))
      if (localAgent) return localAgent
    }
    return undefined
  }, [agents, selected])

  const handleApproval = async (approval: Approval, action: 'approve' | 'cancel') => {
    if (!selected || !agent?.capabilities.includes('approvals')) return
    const id = newCommandId()
    const command: Command = {
      id,
      session_id: selected.id,
      agent_id: selected.agent_id,
      action,
      state: 'received',
      text: '',
      attachments: [],
      created_at: new Date().toISOString(),
      error: null,
      target_id: approval.target_id || approval.id,
    }
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')
    try {
      const result = await api.createCommand({
        id,
        agent_id: selected.agent_id,
        session_id: selected.id,
        action,
        text: '',
        attachment_ids: [],
        target_id: approval.target_id || approval.id,
      })
      store.mergeCommands([result])
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] })
    } catch (error) {
      store.markOutboxError(
        selected.agent_id,
        selected.id,
        id,
        errorText(error),
        error instanceof ApiError && error.status < 500 ? 'failed' : 'unknown',
      )
    }
  }

  const handleStopTask = async (task: Task) => {
    if (!selected || !agent?.capabilities.includes('stop') || !task.target_id) return
    const id = newCommandId()
    const command: Command = { id, session_id: selected.id, agent_id: selected.agent_id, action: 'stop', state: 'received', text: '', attachments: [], created_at: new Date().toISOString(), error: null, target_id: task.target_id }
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')
    try {
      const result = await api.createCommand({ id, agent_id: selected.agent_id, session_id: selected.id, action: 'stop', text: '', attachment_ids: [], target_id: task.target_id })
      store.mergeCommands([result])
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] })
    } catch (error) {
      store.markOutboxError(selected.agent_id, selected.id, id, errorText(error), error instanceof ApiError && error.status < 500 ? 'failed' : 'unknown')
    }
  }

  if (activeBotGroup) {
    return (
      <div className="route-page chat-page">
        <BotGroupChatPage group={activeBotGroup} agents={agents} />
      </div>
    )
  }

  if (sessions.length === 0) {
    return <EmptyState icon={<IconSparkles />} title="还没有可用会话" description="认证成功，但服务端没有返回 Agent 会话。连接真实 Agent 后，会话会出现在这里。" />
  }

  if (!selected) {
    return (
      <div className="route-page chat-page">
        <WelcomeView sessions={sessions} agents={agents} />
      </div>
    )
  }

  const resourceError = resources.messages.error || resources.commands.error || resources.tasks.error
  const showRightPanel = detailsPinned || sidecarOpen

  const handleOpenFork = (message: Message, turnIndex: number) => {
    if (!selected) return
    setForkTargetMessage({ message, turnIndex })
    const baseTitle = selected.title || '新会话'
    setForkTitle(`${baseTitle} (分叉)`)
    setForkBranchName(`fork-${Date.now().toString(36)}`)
    setForkWorktree(false)
    setForkModalOpen(true)
  }

  const handleConfirmFork = async () => {
    if (!selected || !forkTargetMessage) return
    setForkLoading(true)
    try {
      const result = await api.forkSession(selected.id, {
        agent_id: selected.agent_id,
        title: forkTitle.trim() || undefined,
        worktree: forkWorktree,
        branch_name: forkWorktree && forkBranchName.trim() ? forkBranchName.trim() : undefined,
        target_message_id: forkTargetMessage.message.id,
        turn_index: forkTargetMessage.turnIndex,
      })
      notifications.show({
        color: 'teal',
        title: '会话已分叉',
        message: `成功从此回复分叉新会话：${result.title || result.id}`,
      })
      setForkModalOpen(false)
      navigate(`/chat/${encodeURIComponent(result.id)}?agent_id=${encodeURIComponent(result.agent_id)}`)
    } catch (err) {
      notifications.show({
        color: 'red',
        title: '分叉失败',
        message: err instanceof Error ? err.message : '分叉会话出现未知错误',
      })
    } finally {
      setForkLoading(false)
    }
  }

  return (
    <div className="route-page chat-page">
      <div
        ref={layoutRef}
        className={`chat-layout${showRightPanel ? ' has-details' : ''}`}
        style={{
          gridTemplateColumns: showRightPanel ? `minmax(0, 1fr) ${sidecarWidth}%` : undefined,
        }}
      >
        <section className="chat-column">
          <div className="chat-heading">
            <Group justify="space-between" align="center" wrap="nowrap">
              <div className="chat-title-block">
                <Group gap={8} wrap="nowrap" align="center">
                  <IconFolder size={17} className="chat-title-icon" />
                  <Title order={2} size="h4" className="chat-title-text">
                    <BlurText text={selected.title || '未命名会话'} />
                  </Title>
                </Group>
                {handoffPeer && <Button
                  variant="subtle"
                  size="compact-xs"
                  leftSection={<IconTransfer size={13} />}
                  onClick={() => navigate(`/chat/${encodeURIComponent(handoffPeer.session.id)}?agent_id=${encodeURIComponent(handoffPeer.session.agent_id)}`)}
                >
                  {handoffPeer.label}
                </Button>}
              </div>
              <Group gap="xs" wrap="nowrap" className="chat-heading-actions">
                <AgentKindBadge agent={agent} />
                <SessionStatusLabel status={sessionActivityStatus(selected, commands)} />
                <Tooltip label={reviewRun?.status === 'reviewing' ? 'Codex 审查中…' : '请求 Codex 审查'} position="bottom">
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    color="teal"
                    aria-label="请求 Codex 审查"
                    loading={reviewRun?.status === 'reviewing'}
                    disabled={!reviewAgents.length}
                    onClick={() => {
                      void api.getReviewRelayBinding(selected.agent_id, selected.id).then((binding) => {
                        setReviewPeerKey(binding?.enabled ? scopeKey(binding.review_agent_id, binding.review_session_id) : null)
                      }).catch(() => setReviewPeerKey(null))
                      setReviewCreateNew(false)
                      setReviewModalOpen(true)
                    }}
                  >
                    <IconChecklist size={16} />
                  </ActionIcon>
                </Tooltip>
                {reviewRun?.status === 'draft_ready' && <Text size="xs" c="teal">审查结果已回填</Text>}
                {reviewRun?.status === 'empty' && <Text size="xs" c="dimmed">本轮没有 code-comment</Text>}
                {reviewRun?.status === 'failed' && <Text size="xs" c="red">审查失败</Text>}

                {!!approvals.length && <Button size="compact-sm" color="yellow" variant="light" onClick={openDetails}>等待授权 · {approvals.length}</Button>}
                <Tooltip label="知识记忆看板 (Ctrl+Alt+M)" position="bottom">
                  <ActionIcon
                    variant="subtle"
                    size="sm"
                    color="indigo"
                    onClick={() => useSidecarStore.getState().openMemory(selected.id)}
                    aria-label="知识记忆看板"
                  >
                    <IconBrain size={16} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="作战黑板 (Ctrl+Alt+B)" position="bottom">
                  <ActionIcon
                    variant="subtle"
                    size="sm"
                    color="violet"
                    onClick={() => useSidecarStore.getState().openBlackboard(selected.id, selected.agent_id, selected.connection_id || undefined)}
                    aria-label="作战黑板"
                  >
                    <IconChalkboard size={16} />
                  </ActionIcon>
                </Tooltip>
                <Tooltip label="会话详情" position="bottom">
                  <ActionIcon className="chat-details-button" variant="subtle" size="sm" onClick={openDetails} aria-label="打开会话详情">
                    <IconInfoCircle size={16} />
                  </ActionIcon>
                </Tooltip>
                {!showRightPanel && (
                  <Tooltip label="展开工作台侧边栏" position="bottom-end">
                    <ActionIcon
                      variant="subtle"
                      size="sm"
                      color="gray"
                      onClick={() => setSidecarOpen(true)}
                      aria-label="展开工作台侧边栏"
                    >
                      <IconLayoutSidebarRightExpand size={16} />
                    </ActionIcon>
                  </Tooltip>
                )}
              </Group>
            </Group>
          </div>
          <SessionRuntimeBar key={scopeKey(selected.agent_id, selected.id)} commands={commands} tasks={tasks} onTaskOpen={(task) => { setTaskSelection({ id: task.id, agent_id: task.agent_id, session_id: task.session_id }); openTaskDetails() }} />
          <Transcript
            key={`transcript:${scopeKey(selected.agent_id, selected.id)}`}
            session={selected}
            messages={messages}
            outbox={outbox}
            approvals={approvals}
            canApprove={agent?.capabilities.includes('approvals') === true}
            onApproval={(approval, action) => void handleApproval(approval, action)}
            composerHeight={composerHeight}
            loading={resources.messages.isLoading || resources.commands.isLoading}
            hasMoreHistory={Boolean(resources.messages.hasNextPage)}
            loadingOlder={resources.messages.isFetchingNextPage}
            onLoadOlder={() => resources.messages.fetchNextPage()}
            onImageClick={(url) => openGallery([url], 0)}
            onGalleryClick={(url, allImages) => {
              const idx = allImages.indexOf(url)
              openGallery(allImages, idx >= 0 ? idx : 0)
            }}
            error={resourceError ? errorText(resourceError) : undefined}
           onRetry={() => {
             void resources.messages.refetch()
             void resources.commands.refetch()
           }}
           commands={commands}
           onForkAtMessage={handleOpenFork}
           onEditLastUserMessage={(text) => {
              useAstrorderStore.getState().setDraft(selected.agent_id, selected.id, { text, attachments: [] })
              const el = document.querySelector<HTMLTextAreaElement>('.composer-input textarea')
              if (el) {
                el.focus()
                el.setSelectionRange(el.value.length, el.value.length)
              }
            }}
         />
          <ChatComposer
            key={`composer:${scopeKey(selected.agent_id, selected.id)}`}
            session={selected}
            agent={agent}
            tasks={tasks}
            usage={sessionUsage.data}
            onHeightChange={setComposerHeight}
            onPreviewImage={openGallery}
          />
        </section>
        <aside
          className="desktop-details"
          style={{ position: 'relative', width: '100%', padding: 0, display: showRightPanel ? 'block' : 'none' }}
        >
            {/* 左右分界自由拖拽条 */}
            <div
              className="sidecar-resizer"
              data-resizing={isResizing}
              onMouseDown={(e) => {
                e.preventDefault()
                setIsResizing(true)
              }}
              onDoubleClick={() => setSidecarWidth(50)}
              title="拖动调整对话与侧边栏宽度比例，双击复位为 50%"
            />
            {/* 拖拽过程中覆盖遮罩，防止 iframe（如 drawio/html）吃掉鼠标 mousemove 事件 */}
            {isResizing && (
              <div
                style={{
                  position: 'fixed',
                  top: 0,
                  left: 0,
                  right: 0,
                  bottom: 0,
                  zIndex: 9999,
                  cursor: 'col-resize',
                }}
              />
            )}
            <div style={{ display: sidecarOpen ? 'block' : 'none', height: '100%' }}>
              <ErrorBoundary fallbackTitle="侧边栏加载异常">
                <SidecarHost
                  session={selected}
                  agent={agent}
                  commands={commands}
                  messages={messages}
                  approvals={approvals}
                  onApproval={(approval, action) => void handleApproval(approval, action)}
                  onCloseSidecar={() => setSidecarOpen(false)}
                />
              </ErrorBoundary>
            </div>
            {!sidecarOpen && detailsPinned && (
              <div style={{ padding: '16px 0 0 18px' }}>
                <Button variant="subtle" size="compact-sm" onClick={() => setDetailsPinned(false)}>收起详情侧栏</Button>
                <SessionDetails session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
              </div>
            )}
          </aside>
      </div>
      <DrawerDetails opened={detailsOpened} onClose={closeDetails} onPin={() => { setDetailsPinned(true); closeDetails() }} session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
      <Drawer opened={taskDetailsOpened && selectedTask !== null} onClose={() => { closeTaskDetails(); setTaskSelection(null) }} title="任务详情" position={mobileTaskSheet ? 'bottom' : 'right'} size={mobileTaskSheet ? 'min(88vh, 620px)' : 'min(92vw, 520px)'}>
        {selectedTask && <TaskDetails task={selectedTask} canStop={Boolean(agent?.capabilities.includes('stop') && selectedTask.target_id && ['pending', 'running', 'waiting_approval'].includes(selectedTask.status))} onStop={(task) => void handleStopTask(task)} onJumpToLatest={() => window.scrollTo({ top: document.body.scrollHeight, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })} onClose={() => { closeTaskDetails(); setTaskSelection(null) }} />}
      </Drawer>
      {gallery && gallery.images.length > 0 &&
        typeof document !== 'undefined' &&
        createPortal(
          <div
            className="desktop-lightbox"
            role="dialog"
            aria-label="图片预览"
            onClick={() => setGallery(null)}
          >
            <button
              type="button"
              className="desktop-lightbox-close"
              aria-label="关闭图片"
              onClick={() => setGallery(null)}
            >
              <IconX size={24} />
            </button>
            {gallery.images.length > 1 && (
              <>
                <button
                  type="button"
                  className="desktop-lightbox-nav prev"
                  aria-label="上一张图片"
                  disabled={gallery.index <= 0}
                  onClick={(e) => {
                    e.stopPropagation()
                    setGallery(g => g ? { ...g, index: Math.max(0, g.index - 1) } : null)
                  }}
                >
                  ‹
                </button>
                <button
                  type="button"
                  className="desktop-lightbox-nav next"
                  aria-label="下一张图片"
                  disabled={gallery.index >= gallery.images.length - 1}
                  onClick={(e) => {
                    e.stopPropagation()
                    setGallery(g => g ? { ...g, index: Math.min(g.images.length - 1, g.index + 1) } : null)
                  }}
                >
                  ›
                </button>
                <div className="desktop-lightbox-counter" onClick={(e) => e.stopPropagation()}>
                  {gallery.index + 1} / {gallery.images.length}
                </div>
              </>
            )}
            <img
              className="desktop-lightbox-image"
              src={gallery.images[gallery.index]}
              alt="放大预览"
              onClick={(e) => e.stopPropagation()}
            />
          </div>,
          document.body,
        )}
      {selected && <QuickOpen session={selected} opened={quickOpenOpened} onClose={() => setQuickOpenOpened(false)} />}

      {/* 从任意回复分叉会话弹窗 */}
      <Modal
        opened={forkModalOpen}
        onClose={() => setForkModalOpen(false)}
        title={
          <Group gap={8}>
            <IconGitFork size={18} style={{ color: 'var(--astr-indigo)' }} />
            <span style={{ fontWeight: 600, fontSize: '14px' }}>从该回复分叉新会话</span>
          </Group>
        }
        centered
        radius="md"
      >
        <Stack gap="md">
          <Text size="xs" c="dimmed">
            将在当前回复节点创建全新分支，继承此前的完整对话上下文，后续尝试不会影响原会话。
          </Text>
          <TextInput
            label="新会话标题"
            placeholder="输入分叉会话标题"
            value={forkTitle}
            onChange={(e) => setForkTitle(e.currentTarget.value)}
          />
          <Switch
            label="创建独立 Git Worktree 分支工作区"
            description="在独立目录签出新 Git 分支，防止两个分支的代码修改互相干扰"
            checked={forkWorktree}
            onChange={(e) => setForkWorktree(e.currentTarget.checked)}
          />
          {forkWorktree && (
            <TextInput
              label="Git 分支名"
              placeholder="例如：feature-experimental"
              value={forkBranchName}
              onChange={(e) => setForkBranchName(e.currentTarget.value)}
            />
          )}
          <Group justify="flex-end" mt="sm">
            <Button variant="default" onClick={() => setForkModalOpen(false)} disabled={forkLoading}>
              取消
            </Button>
            <Button
              color="indigo"
              onClick={() => void handleConfirmFork()}
              loading={forkLoading}
              leftSection={<IconGitFork size={15} />}
            >
              立即分叉
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Modal opened={reviewModalOpen} onClose={() => setReviewModalOpen(false)} title="请求 Codex 原生审查" centered>
        <Stack gap="md">
          <Text size="sm">选择同一工作区的独立 Codex 审查会话。星序只会向它发送原生 <code>/review</code>，不会注入当前会话内容。</Text>
          <Group gap="xs">
            <Button size="compact-sm" variant={reviewCreateNew ? 'subtle' : 'light'} onClick={() => setReviewCreateNew(false)}>选择现有</Button>
            <Button size="compact-sm" variant={reviewCreateNew ? 'light' : 'subtle'} onClick={() => { setReviewCreateNew(true); setReviewAgentId(reviewAgents.length === 1 ? reviewAgents[0].id : null) }}>新建审查会话</Button>
          </Group>
          {reviewCreateNew ? <>
            <Select label="Codex Agent" placeholder="选择同环境的 Codex" data={reviewAgents.map((item) => ({ value: item.id, label: item.name || item.id }))} value={reviewAgentId} onChange={(value) => { setReviewAgentId(value); setReviewModelKey(null) }} />
            <Select label="模型" placeholder={reviewModels.isLoading ? '正在读取模型…' : '选择模型'} data={(reviewModels.data?.items || []).map((item) => ({ value: JSON.stringify([item.provider, item.model]), label: item.label }))} value={reviewModelKey} onChange={setReviewModelKey} disabled={!reviewProbe || reviewModels.isLoading} searchable />
            {!reviewProbe && reviewAgentId && <Text size="xs" c="dimmed">该 Codex Agent 尚无会话，无法读取可用模型。</Text>}
            {reviewModels.isError && <Text size="xs" c="red">模型列表读取失败：{errorText(reviewModels.error)}</Text>}
            <Select label="思考程度" placeholder="选择思考程度" data={[...REASONING_EFFORTS]} value={reviewEffort} onChange={setReviewEffort} />
          </> : <Select
            label="审查会话"
            placeholder={reviewPeers.length ? '选择审查会话' : '没有找到同工作区的 Codex 会话'}
            data={reviewPeers.map((peer) => ({ value: scopeKey(peer.agent_id, peer.id), label: peer.title || peer.id }))}
            value={reviewPeerKey}
            onChange={setReviewPeerKey}
            searchable
            nothingFoundMessage="没有匹配的审查会话"
          />}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setReviewModalOpen(false)} disabled={reviewBusy}>取消</Button>
            <Button color="teal" loading={reviewBusy} disabled={reviewCreateNew ? !reviewAgentId || !reviewModelKey || !reviewEffort : !reviewPeerKey} onClick={() => void (reviewCreateNew ? createReviewPeer() : startReview())}>{reviewCreateNew ? '创建并发送 /review' : '发送 /review'}</Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}

function DrawerDetails({
  opened,
  onClose,
  onPin,
  ...props
}: {
  opened: boolean
  onClose: () => void
  onPin: () => void
  session: Session
  agent?: import('../../domain/types').Agent
  commands: Command[]
  messages: import('../../domain/types').Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
}) {
  return (
    <Drawer opened={opened} onClose={onClose} title="会话详情" position="right" size="min(92vw, 380px)">
      <Button visibleFrom="md" variant="subtle" size="compact-sm" onClick={onPin}>固定详情侧栏</Button>
      <SessionDetails {...props} />
    </Drawer>
  )
}
