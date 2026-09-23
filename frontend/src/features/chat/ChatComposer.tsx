import { IconBrain, IconMicrophone, IconPaperclip, IconPlus, IconPlayerStop, IconArrowUp, IconX, IconCheck } from '@tabler/icons-react'
import { Alert, Badge, Button, Group, Paper, Stack, Text, Textarea } from '@mantine/core'
import { useQueryClient } from '@tanstack/react-query'
import { type ChangeEvent, type ClipboardEvent, type DragEvent, type KeyboardEvent, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { isDraftSendable, newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Command, CommandAction, DraftAttachment, DraftState, Message, Session, SessionRef, Task } from '../../domain/types'
import { selectCommands, useAstrorderStore } from '../../state/store'
import { submitBrowserCommand } from './commandActions'
import { VoiceInputSheet } from './VoiceInputSheet'
import { SessionModelControl } from './SessionModelControl'
import { Magnet } from '../../components/animations/Magnet'
import { ClickSpark } from '../../components/animations/ClickSpark'
import { GitStatusBar } from './GitStatusBar'
import { clipboardFiles } from './composerMedia'
import { notifySessionSubmitted } from '../../hooks/useSessionOrder'
import '../agents/agentsLayout.css'
import { ApprovalModeControl } from './ApprovalModeControl'
import { MobileOutbox, type OutboxEntry } from '../mobile/mobileOutbox'
import { mobileOutboxStorage } from '../mobile/mobileOutboxStorage'
import { AgentCommandMenu, AgentMentionMenu, buildCommandMenuItems, type CommandMenuItem, filterAgentMentions, formatAgentMention, useAgentCommands, useAgentMentions, useFileMentions, useGitBranches } from './AgentCommandMenu'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { expandSystemMentions } from './systemMentions'
import { usePersistentDraft } from './draftStorage'


export interface InterpretedGatewayError {
  type: 'quota' | 'disconnect' | 'auth' | 'context' | 'generic'
  title: string
  description: string
  isKnownGatewayIssue: boolean
  recommendedModel?: { provider: string; model: string; label: string }
}

export function interpretGatewayError(rawError?: string | null): InterpretedGatewayError | null {
  if (!rawError || typeof rawError !== 'string') return null
  const lower = rawError.toLowerCase()
  if (
    lower.includes('quota') ||
    lower.includes('rate limit') ||
    lower.includes('rate_limit') ||
    lower.includes('429') ||
    lower.includes('insufficient_quota') ||
    lower.includes('额度') ||
    lower.includes('超出限额')
  ) {
    return {
      type: 'quota',
      title: '网关模型配额已耗尽',
      description: '当前模型上游配额已耗尽或触发速率限制，建议切换至其他充裕模型继续对话。',
      isKnownGatewayIssue: true,
      recommendedModel: { provider: 'google-antigravity', model: 'gemini-3.8-flash', label: 'gemini-3.8-flash' },
    }
  }
  if (
    lower.includes('server disconnected without sending a response') ||
    lower.includes('remoteprotocolerror') ||
    lower.includes('apiconnectionerror') ||
    lower.includes('connection error') ||
    lower.includes('econnrefused') ||
    lower.includes('etimedout') ||
    lower.includes('502 bad gateway') ||
    lower.includes('网关连接')
  ) {
    return {
      type: 'disconnect',
      title: '网关服务断开或连接超时',
      description: '上游 LLM 服务断开连接或未返回响应，可能是网络抖动或网关暂时脱机。',
      isKnownGatewayIssue: true,
    }
  }
  if (
    lower.includes('account is paused') ||
    lower.includes('account paused') ||
    lower.includes('suspended') ||
    lower.includes('deactivated') ||
    lower.includes('401') ||
    lower.includes('unauthorized') ||
    lower.includes('账号已暂停')
  ) {
    return {
      type: 'auth',
      title: '网关上游账号已暂停或凭据失效',
      description: '当前模型所绑定的上游服务账号已被暂停或失效，请在网关后台检查账号健康状态。',
      isKnownGatewayIssue: true,
    }
  }
  if (
    lower.includes('context_length_exceeded') ||
    lower.includes('maximum context length') ||
    lower.includes('context window') ||
    lower.includes('上下文长度超出')
  ) {
    return {
      type: 'context',
      title: '上下文超出模型窗口限制',
      description: '输入及历史消息已达到当前模型的上下文上限，建议发送 /compact 压缩会话或切换至长上下文模型。',
      isKnownGatewayIssue: true,
    }
  }
  return {
    type: 'generic',
    title: '命令执行异常',
    description: rawError,
    isKnownGatewayIssue: false,
  }
}

const EMPTY_DRAFT: DraftState = { text: '', attachments: [], sessionRefs: [] }

function fileToDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

function parseSessionDrag(event: DragEvent): SessionRef | null {
  const raw = event.dataTransfer?.getData('application/x-astrorder-session')
  if (!raw) return null
  try {
    const item = JSON.parse(raw)
    const agentId = item.agent_id
    const id = item.id
    if (!agentId || !id) return null
    return { key: item.key || `${agentId}::${id}`, agent_id: agentId, id, title: item.title || '未命名会话' }
  } catch {
    return null
  }
}

function composeOutgoingText(text: string, refs: SessionRef[], sessionKey: string): string {
  const processed = expandSystemMentions(text, sessionKey)

  if (!refs.length) return processed
  const lines = refs.map((ref) => `- ${ref.title || '未命名会话'} (${ref.key})`)
  const block = `星序会话引用：\n${lines.join('\n')}\n需要这些会话的内容时，调用 Astrorder MCP 工具 sessions_read，参数 key 为上列会话键。`
  return processed.trim() ? `${block}\n\n${processed}` : block
}
function hasCapability(agent: Agent | undefined, capability: string): boolean {
  if (!agent) return capability === 'chat' // 默认允许聊天，不设无谓门槛
  return Boolean(agent.capabilities?.includes(capability) || capability === 'chat')
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '提交失败，请检查服务连接。'
}

function localCommand(session: Session, id: string, action: CommandAction, text: string): Command {
  return {
    id,
    session_id: session.id,
    agent_id: session.agent_id,
    action,
    state: 'received',
    text,
    attachments: [],
    created_at: new Date().toISOString(),
    error: null,
    target_id: null,
  }
}

export function ChatComposer({
  session,
  agent,
  tasks = [],
  usage,
  onHeightChange,
  onPreviewImage,
}: {
  session: Session
  agent?: Agent
  tasks?: Task[]
  usage?: { last_input_tokens: number; context_window: number }
  onHeightChange?: (height: number) => void
  onPreviewImage?: (images: string[], index: number) => void
}) {
  const queryClient = useQueryClient()
  const draftKey = scopeKey(session.agent_id, session.id)
  const { draft, setDraft: updateDraft } = usePersistentDraft(session.agent_id, session.id)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const [submitting, setSubmitting] = useState(false)
  const [voiceOpened, setVoiceOpened] = useState(false)
  const submittingRef = useRef(false)
  const [error, setError] = useState<string | null>(null)
  const [submittedCommandId, setSubmittedCommandId] = useState<string | null>(null)
  const [commandIndex, setCommandIndex] = useState(0)
  const [dismissedMenuText, setDismissedMenuText] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [sessionDragOver, setSessionDragOver] = useState(false)
  const [fileDragOver, setFileDragOver] = useState(false)
  const [outbox] = useState(() => new MobileOutbox(mobileOutboxStorage, {
    upload: api.uploadAttachment,
    send: async payload => {
      const result = await api.createCommand(payload)
      useAstrorderStore.getState().mergeCommands([result])
      return result
    },
  }))
  const queue = useSyncExternalStore(outbox.subscribe, outbox.snapshot)
  const [outboxReady, setOutboxReady] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentKeyRef = useRef(0)
  const cardRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = cardRef.current
    if (!el) return
    let lastHeight = -1
    const update = () => {
      const rect = el.getBoundingClientRect()
      const height = Math.ceil(rect.height || el.offsetHeight || 124)
      if (height > 0 && height !== lastHeight) {
        lastHeight = height
        el.parentElement?.style.setProperty('--composer-height', `${height}px`)
        onHeightChange?.(height)
      }
    }
    update()
    if (typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(update)
      observer.observe(el)
      return () => {
        observer.disconnect()
        el.parentElement?.style.removeProperty('--composer-height')
      }
    }
    return () => {
      el.parentElement?.style.removeProperty('--composer-height')
    }
  }, [onHeightChange])

  const canChat = hasCapability(agent, 'chat')
  const canAttach = hasCapability(agent, 'attachments')
  const nativeBusy = session.status === 'running' || session.status === 'waiting_approval'
  const busy = submitting || nativeBusy
  const pending = queue.filter(row => row.payload.agent_id === session.agent_id && row.payload.session_id === session.id && !['accepted', 'running', 'completed'].includes(row.state))
  const canStop = hasCapability(agent, 'stop') && busy
  const sessionRefs = draft.sessionRefs || []
  const hasDraft = isDraftSendable(draft.text, draft.attachments, sessionRefs)
  const commandMenuOpen = (/^\/[^\s]*$/.test(draft.text) || /^\/(?:review|审查)(?:\s.*)?$/i.test(draft.text)) && dismissedMenuText !== draft.text
  const resourcesReady = !agent || agent.status === 'ready'
  const commandQuery = useAgentCommands(session, resourcesReady)
  const gitBranchesQuery = useGitBranches(session, commandMenuOpen && resourcesReady)
  const agentCommands = commandMenuOpen
    ? buildCommandMenuItems(commandQuery.data?.items || [], gitBranchesQuery.data || [], draft.text)
    : []
  const mentionMatch = draft.text.match(/(?:^|\s)@[^\s@]*$/)
  const mentionMenuOpen = Boolean(mentionMatch) && dismissedMenuText !== draft.text
  const mentionText = mentionMatch?.[0].trim().slice(1) || ''
  const mentionQuery = useAgentMentions(session, resourcesReady)
  const fileMentionQuery = useFileMentions(session, mentionText, mentionMenuOpen && resourcesReady)
  const agentMentions = mentionMenuOpen
    ? filterAgentMentions(
        [...(mentionQuery.data?.items || []), ...(fileMentionQuery.data || [])],
        draft.text,
      )
    : []
  // 执行中且输入为空时停止；有草稿时先排队，由用户显式选择是否立即引导。
  const isStopAction = busy && !hasDraft
  const submittedCommand = submittedCommandId ? commands.find((item) => item.id === submittedCommandId) : undefined
  const submittedCommandError = submittedCommand && (submittedCommand.state === 'failed' || submittedCommand.state === 'unknown')
    ? submittedCommand.error || (submittedCommand.state === 'failed' ? '原生命令执行失败。' : '原生命令执行结果未确认。')
    : null
  const visibleError = error || submittedCommandError

  useEffect(() => {
    let mounted = true
    void outbox.load().then(() => { if (mounted) setOutboxReady(true) }).catch(nextError => setError(errorMessage(nextError)))
    return () => { mounted = false }
  }, [outbox])

  useEffect(() => {
    if (outboxReady) void outbox.reconcile(commands, [session]).catch(nextError => setError(errorMessage(nextError)))
  }, [commands, outbox, outboxReady, session])

  useEffect(() => {
    if (outboxReady && !nativeBusy && agent?.status === 'ready' && pending.some(row => row.state === 'queued')) {
      void outbox.flush(session.agent_id, session.id).catch(nextError => setError(errorMessage(nextError)))
    }
  }, [agent?.status, nativeBusy, outbox, outboxReady, pending, session.agent_id, session.id])

  const setText = (text: string) => updateDraft({ ...draft, text })
  const addSessionRef = (ref: SessionRef) => {
    const selfKey = scopeKey(session.agent_id, session.id)
    if (ref.key === selfKey || (ref.agent_id === session.agent_id && ref.id === session.id)) return
    if (sessionRefs.some((item) => item.key === ref.key)) return
    updateDraft({ ...draft, sessionRefs: [...sessionRefs, ref] })
  }
  const removeSessionRef = (key: string) => {
    updateDraft({ ...draft, sessionRefs: sessionRefs.filter((item) => item.key !== key) })
  }
  const handleSessionDragOver = (event: DragEvent<HTMLDivElement>) => {
    const types = Array.from(event.dataTransfer?.types || [])
    if (types.includes('application/x-astrorder-session')) {
      event.preventDefault()
      event.dataTransfer.dropEffect = 'copy'
      setSessionDragOver(true)
      return
    }
    if (canAttach && (types.includes('Files') || event.dataTransfer?.items?.length)) {
      event.preventDefault()
      event.dataTransfer.dropEffect = 'copy'
      setFileDragOver(true)
    }
  }
  const handleSessionDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    setSessionDragOver(false)
    setFileDragOver(false)
    const ref = parseSessionDrag(event)
    if (ref) {
      addSessionRef(ref)
      return
    }
    if (canAttach && event.dataTransfer?.files?.length) {
      addDroppedFiles([...Array.from(event.dataTransfer.files)])
    }
  }

  const selectAgentMention = (item: import('../../domain/types').AgentMention) => {
    setText(draft.text.replace(/@[^\s@]*$/, formatAgentMention(item)))
    setCommandIndex(0)
  }

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    addDroppedFiles([...(event.currentTarget.files || [])])
    event.currentTarget.value = ''
  }

  const addDroppedFiles = (files: File[]) => {
    if (!files.length || !canAttach) return
    const additions: DraftAttachment[] = files.map((file) => {
      attachmentKeyRef.current += 1
      return { key: `draft-attachment-${attachmentKeyRef.current}`, file }
    })
    updateDraft({ ...draft, attachments: [...draft.attachments, ...additions] })
  }

  const handlePaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = clipboardFiles(event)
    if (!files.length || !canAttach) return
    event.preventDefault()
    addDroppedFiles(files)
  }

  const removeAttachment = (key: string) => {
    updateDraft({ ...draft, attachments: draft.attachments.filter((item) => item.key !== key) })
  }
  const previewDraftImage = (key: string) => {
    const images = draft.attachments.filter((item) => item.file.type.startsWith('image/'))
    const index = Math.max(0, images.findIndex((item) => item.key === key))
    void Promise.all(images.map((item) => fileToDataUrl(item.file))).then((urls) => {
      if (!urls.length) return
      if (onPreviewImage) {
        onPreviewImage(urls, index)
        return
      }
      const desktop = (window as unknown as { astrorderDesktop?: { openPreview?: (payload: { images: string[]; index: number }) => Promise<void> } }).astrorderDesktop
      if (desktop?.openPreview) void desktop.openPreview({ images: urls, index })
    })
  }

  const submitDirectMessage = async (customText: string) => {
    if (submitting || submittingRef.current || !canChat) return
    const commandId = newCommandId()
    const command = localCommand(session, commandId, 'send', customText)
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')

    const optimisticMessage: Message = {
      id: `optimistic-${commandId}`,
      session_id: session.id,
      agent_id: session.agent_id,
      role: 'user',
      kind: 'message',
      text: customText,
      attachments: [],
      created_at: new Date().toISOString(),
      command_id: commandId,
      tool: null,
    }
    store.mergeMessages(session.agent_id, session.id, [optimisticMessage])
    store.setDraft(session.agent_id, session.id, EMPTY_DRAFT)

    submittingRef.current = true
    setSubmitting(true)
    setError(null)
    setSubmittedCommandId(commandId)
    try {
      const result = await submitBrowserCommand({
        commandId,
        session,
        text: customText,
        files: [],
        action: 'send',
        targetId: null,
        uploadAttachment: api.uploadAttachment,
        createCommand: api.createCommand,
      })
      store.updateOutboxAttachments(session.agent_id, session.id, commandId, result.attachments)
      store.mergeCommands([result.command])
      notifySessionSubmitted(session)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', session.agent_id, session.id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', session.agent_id, session.id] })
    } catch (nextError) {
      const status = nextError instanceof ApiError && nextError.status >= 400 && nextError.status < 500 ? 'failed' : 'unknown'
      store.markOutboxError(session.agent_id, session.id, commandId, errorMessage(nextError), status)
      setError(errorMessage(nextError))
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  const selectAgentCommand = (item: CommandMenuItem | import('../../domain/types').AgentCommand) => {
    if ('kind' in item) {
      if (item.kind === 'header') return
      if (item.kind === 'review_uncommitted') {
        useSidecarStore.getState().openGitDiff(session.id, session.agent_id, session.workspace || undefined, session.connection_id || undefined)
        updateDraft(EMPTY_DRAFT)
        setDismissedMenuText(null)
        void submitDirectMessage('请检查我未提交的更改')
        return
      }
      if (item.kind === 'review_branch') {
        useSidecarStore.getState().openGitDiff(session.id, session.agent_id, session.workspace || undefined, session.connection_id || undefined, item.branch)
        updateDraft(EMPTY_DRAFT)
        setDismissedMenuText(null)
        void submitDirectMessage(`请对照 ${item.branch} 审查我的更改`)
        return
      }
      if (item.kind === 'command') {
        if (item.command.name === 'review') {
          setText('/review ')
          setDismissedMenuText(null)
          setCommandIndex(0)
          return
        }
        const text = `/${item.command.name}${item.command.input_hint ? ' ' : ''}`
        setText(text)
        setDismissedMenuText(text)
        setCommandIndex(0)
        return
      }
    }
    const text = `/${item.name}${item.input_hint ? ' ' : ''}`
    setText(text)
    setDismissedMenuText(text)
    setCommandIndex(0)
  }

  const submit = async (action: CommandAction, targetId: string | null = null) => {
    const files = draft.attachments.map((item) => item.file)
    const outgoing = composeOutgoingText(draft.text, sessionRefs, `${session.agent_id}::${session.id}`)
    if (action === 'send') {
      if (!isDraftSendable(draft.text, files, sessionRefs)) return
      if (!canChat) {
        setError('当前 Agent 不支持聊天发送。')
        return
      }
    }
    if (submitting || submittingRef.current) return

    if (action === 'send' && nativeBusy) {
      submittingRef.current = true
      setSubmitting(true)
      setError(null)
      try {
        if (!outboxReady) await outbox.load()
        await outbox.enqueue({
          id: newCommandId(),
          agent_id: session.agent_id,
          session_id: session.id,
          action: 'send',
          text: outgoing,
          attachment_ids: [],
          target_id: null,
        }, files)
        updateDraft(EMPTY_DRAFT)
        notifySessionSubmitted(session)
      } catch (nextError) {
        setError(errorMessage(nextError))
      } finally {
        submittingRef.current = false
        setSubmitting(false)
      }
      return
    }

    const commandId = newCommandId()
    const command = localCommand(session, commandId, action, action === 'send' ? outgoing : '')
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')

    // 乐观更新：在用户点击发送瞬间，立即在聊天框呈现用户消息，彻底消除等待迟滞
    if (action === 'send' && command.text) {
      const optimisticMessage: Message = {
        id: `optimistic-${commandId}`,
        session_id: session.id,
        agent_id: session.agent_id,
        role: 'user',
        kind: 'message',
        text: command.text,
        attachments: [],
        created_at: new Date().toISOString(),
        command_id: commandId,
        tool: null,
      }
      store.mergeMessages(session.agent_id, session.id, [optimisticMessage])
    }

    submittingRef.current = true
    setSubmitting(true)
    setError(null)
    setSubmittedCommandId(commandId)
    if (action === 'send') {
      store.setDraft(session.agent_id, session.id, EMPTY_DRAFT)
      // 乐观更新会话状态为 running，防止 Hermes 或原生轮询延迟导致的“无动静”体感
      store.updateSession(session.agent_id, session.id, { status: 'running' })
    }
    try {
      const result = await submitBrowserCommand({
        commandId,
        session,
        text: command.text,
        files: action === 'send' ? files : [],
        action,
        targetId,
        uploadAttachment: api.uploadAttachment,
        createCommand: api.createCommand,
      })
      store.updateOutboxAttachments(session.agent_id, session.id, commandId, result.attachments)
      store.mergeCommands([result.command])
      if (action === 'send') {
        if (result.command.state !== 'failed' && result.command.state !== 'unknown') {
          notifySessionSubmitted(session)
        }
      }
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', session.agent_id, session.id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', session.agent_id, session.id] })
    } catch (nextError) {
      // 发送失败则撤回 running 乐观状态
      store.updateSession(session.agent_id, session.id, { status: session.status || 'idle' })
      const status = nextError instanceof ApiError && nextError.status >= 400 && nextError.status < 500 ? 'failed' : 'unknown'
      store.markOutboxError(session.agent_id, session.id, commandId, errorMessage(nextError), status)
      setError(errorMessage(nextError))
      try {
        if (!outboxReady) await outbox.load()
        await outbox.enqueue({
          id: commandId,
          agent_id: session.agent_id,
          session_id: session.id,
          action: 'send',
          text: command.text,
          attachment_ids: [],
          target_id: null,
        }, files)
      } catch {
        // Fall back to keeping in store
      }
      // Draft and File objects intentionally remain in Zustand after a failed send.
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  const steerQueued = async (commandId: string) => {
    if (submitting || submittingRef.current) return
    submittingRef.current = true
    setSubmitting(true)
    setError(null)
    try {
      await outbox.flush(session.agent_id, session.id, commandId)
      const failed = outbox.snapshot().find(row => row.payload.id === commandId && (row.state === 'failed' || row.state === 'unknown'))
      if (failed?.error) setError(failed.error)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', session.agent_id, session.id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', session.agent_id, session.id] })
    } catch (nextError) {
      setError(errorMessage(nextError))
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  const deleteQueued = async (commandId: string) => {
    try {
      await outbox.remove(session.agent_id, session.id, commandId)
      useAstrorderStore.getState().removeOutbox(session.agent_id, session.id, commandId)
    } catch (err) {
      console.error('移除排队消息失败', err)
    }
  }

  const editQueued = async (entry: OutboxEntry) => {
    try {
      await outbox.remove(session.agent_id, session.id, entry.payload.id)
      useAstrorderStore.getState().removeOutbox(session.agent_id, session.id, entry.payload.id)
      const nextText = entry.payload.text
        ? (draft.text ? `${entry.payload.text}
${draft.text}` : entry.payload.text)
        : draft.text
      const restoredAttachments: DraftAttachment[] = (entry.files || []).map((file) => {
        attachmentKeyRef.current += 1
        return { key: `draft-attachment-${attachmentKeyRef.current}`, file }
      })
      updateDraft({
        text: nextText,
        attachments: [...restoredAttachments, ...draft.attachments],
      })
      setTimeout(() => {
        textareaRef.current?.focus()
      }, 50)
    } catch (err) {
      console.error('编辑排队消息失败', err)
    }
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const choices = mentionMenuOpen ? agentMentions : agentCommands
    if (choices.length && ['ArrowDown', 'ArrowUp'].includes(event.key)) {
      event.preventDefault()
      const dir = event.key === 'ArrowDown' ? 1 : -1
      setCommandIndex(index => {
        let next = (index + dir + choices.length) % choices.length
        if ('kind' in choices[next] && (choices[next] as any).kind === 'header') {
          next = (next + dir + choices.length) % choices.length
        }
        return next
      })
      return
    }
    if (choices.length && (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey))) {
      event.preventDefault()
      if (mentionMenuOpen) selectAgentMention(agentMentions[Math.min(commandIndex, agentMentions.length - 1)])
      else selectAgentCommand(agentCommands[Math.min(commandIndex, agentCommands.length - 1)])
      return
    }
    if (event.key === 'Escape' && commandMenuOpen) {
      event.preventDefault()
      setDismissedMenuText(draft.text)
      return
    }
    if (event.key === 'Escape' && mentionMenuOpen) {
      event.preventDefault()
      setDismissedMenuText(draft.text)
      return
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      if (!submitting && hasDraft) void submit('send')
    }
  }

  const commitVoice = (file: File, transcript: string) => {
    attachmentKeyRef.current += 1
    const nextText = transcript ? `${draft.text.trim()}${draft.text.trim() ? '\n' : ''}${transcript}` : draft.text
    updateDraft({
      text: nextText,
      attachments: [...draft.attachments, { key: `draft-attachment-${attachmentKeyRef.current}`, file }],
    })
  }

  return (
    <>
      <Paper
        ref={cardRef}
        className={`composer-card${sessionDragOver ? ' is-session-drag-over' : ''}${fileDragOver ? ' is-file-drag-over' : ''}`}
        withBorder
        radius="lg"
        p="sm"
        onDragOver={handleSessionDragOver}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node)) {
            setSessionDragOver(false)
            setFileDragOver(false)
          }
        }}
        onDrop={handleSessionDrop}
      >
        {(() => {
          const currentGoal = tasks.find(t => t.id === 'codex:goal:current' && t.status !== 'cancelled')
          if (!currentGoal) return null
          const isRunning = currentGoal.status === 'running' || currentGoal.status === 'pending'
          const isCompleted = currentGoal.status === 'completed'
          const rawTitle = currentGoal.title.replace(/^目标:\s*/, '')
          return (
            <div className={`composer-goal-pill ${isRunning ? 'is-running' : isCompleted ? 'is-completed' : 'is-failed'}`}>
              <span className="goal-pill-status">
                {isRunning ? (
                  <span className="goal-status-dot is-running" aria-hidden="true" />
                ) : isCompleted ? (
                  <IconCheck size={13} color="var(--astr-teal, #12b886)" />
                ) : (
                  <IconX size={13} color="var(--astr-red, #ef4444)" />
                )}
                <span className="goal-status-text">
                  {isRunning ? '进行中的目标' : isCompleted ? '已完成目标' : '目标受阻'}
                </span>
              </span>
              <span className="goal-pill-title" title={rawTitle}>{rawTitle}</span>
            </div>
          )
        })()}
        <GitStatusBar session={session} />
        <Stack gap="xs">
        {pending.map(entry => (
          <Paper key={entry.payload.id} withBorder radius="md" p="xs" aria-label="排队消息">
            <Group justify="space-between" gap="xs" wrap="nowrap">
              <Group gap="xs" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                <Text size="sm" lineClamp={1} style={{ wordBreak: 'break-all' }}>
                  {entry.payload.text || '附件消息'}
                </Text>
                {((entry.files && entry.files.length > 0) || (entry.attachments && entry.attachments.length > 0)) && (
                  <Badge size="xs" variant="light" color="gray" style={{ flexShrink: 0 }}>
                    {(entry.files?.length || 0) + (entry.attachments?.length || 0)} 个附件
                  </Badge>
                )}
              </Group>
              <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
                {entry.state === 'queued' && (
                  <>
                    <Button size="compact-xs" variant="light" color="indigo" onClick={() => void steerQueued(entry.payload.id)}>
                      立即引导
                    </Button>
                    <Button size="compact-xs" variant="subtle" color="blue" onClick={() => void editQueued(entry)}>
                      编辑
                    </Button>
                    <Button size="compact-xs" variant="subtle" color="red" onClick={() => void deleteQueued(entry.payload.id)}>
                      删除
                    </Button>
                  </>
                )}
                {entry.state !== 'queued' && (
                  <>
                    <Text size="xs" c={entry.state === 'failed' || entry.state === 'unknown' ? 'red' : 'dimmed'}>
                      {entry.state === 'submitting' ? '发送中…' : entry.state === 'failed' || entry.state === 'unknown' ? '未确认/失败' : '已提交'}
                    </Text>
                    {(entry.state === 'failed' || entry.state === 'unknown') && (
                      <Button size="compact-xs" variant="light" color="yellow" onClick={() => void steerQueued(entry.payload.id)}>
                        重试
                      </Button>
                    )}
                    <Button size="compact-xs" variant="subtle" color="blue" onClick={() => void editQueued(entry)}>
                      编辑
                    </Button>
                    <Button size="compact-xs" variant="subtle" color="red" onClick={() => void deleteQueued(entry.payload.id)}>
                      删除
                    </Button>
                  </>
                )}
              </Group>
            </Group>
          </Paper>
        ))}
        {visibleError && (() => {
          const parsed = interpretGatewayError(visibleError)
          return (
            <Alert
              color={parsed?.type === 'quota' ? 'orange' : parsed?.type === 'disconnect' ? 'yellow' : 'red'}
              variant="light"
              title={parsed?.isKnownGatewayIssue ? parsed.title : undefined}
              withCloseButton
              onClose={() => { setError(null); setSubmittedCommandId(null) }}
              aria-live="assertive"
            >
              <div>{parsed?.description || visibleError}</div>
              {parsed?.type === 'quota' && parsed.recommendedModel && (
                <Group gap="xs" mt={6}>
                  <Text size="xs" c="dimmed">推荐切换至高可用模型：</Text>
                  <Button
                    size="compact-xs"
                    variant="outline"
                    color="orange"
                    onClick={() => {
                      const rec = parsed.recommendedModel
                      if (!rec) return
                      void api.setSessionModel(session.id, session.agent_id, rec.provider, rec.model)
                        .then(() => setError(null))
                        .catch((e) => setError(errorMessage(e)))
                    }}
                  >
                    切换至 {parsed.recommendedModel.label}
                  </Button>
                </Group>
              )}
            </Alert>
          )
        })()}
        {draft.attachments.length > 0 && (
          <div className="draft-attachments" aria-label="待发送附件">
            {draft.attachments.map((item) => (
              <span
                className="draft-attachment"
                key={item.key}
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Delete' || e.key === 'Backspace') {
                    e.preventDefault()
                    removeAttachment(item.key)
                  }
                }}
                title={`${item.file.name} (可按 Delete/Backspace 移除)`}
              >
                {item.file.type.startsWith('image/') ? (
                  <img
                    className="draft-thumb"
                    src={URL.createObjectURL(item.file)}
                    alt={item.file.name}
                    onClick={(e) => {
                      e.stopPropagation()
                      previewDraftImage(item.key)
                    }}
                    style={{ cursor: 'zoom-in' }}
                    title="查看大图"
                  />
                ) : (
                  <IconPaperclip size={14} aria-hidden="true" />
                )}
                <span className="draft-name">{item.file.name}</span>
                <button type="button" className="draft-remove" onClick={() => removeAttachment(item.key)} aria-label={`移除 ${item.file.name}`}>
                  <IconX size={13} />
                </button>
              </span>
            ))}
          </div>
        )}
        {!canChat && <Text size="xs" c="dimmed">此 Agent 未报告 chat 能力，发送控件已禁用。</Text>}
        {sessionRefs.length > 0 && (
          <div className="composer-session-refs" aria-label="引用的会话">
            {sessionRefs.map((ref) => (
              <span className="composer-session-ref" key={ref.key}>
                <span className="composer-session-ref-title">{ref.title || '未命名会话'}</span>
                <button type="button" className="draft-remove" onClick={() => removeSessionRef(ref.key)} aria-label={`移除引用 ${ref.title || '未命名会话'}`}>
                  <IconX size={13} />
                </button>
              </span>
            ))}
          </div>
        )}
        <div style={{ position: 'relative' }}>
          <AgentCommandMenu agent={agent} items={agentCommands} activeIndex={commandIndex} onSelect={selectAgentCommand} />
          <AgentMentionMenu agent={agent} items={agentMentions} activeIndex={commandIndex} onSelect={selectAgentMention} />
          <Textarea ref={textareaRef} className="composer-input" aria-label="消息内容" placeholder="随心输入，可粘贴图片" variant="unstyled" autosize minRows={2} maxRows={8} value={draft.text} onChange={(event) => { setCommandIndex(0); setDismissedMenuText(null); setText(event.currentTarget.value) }} onPaste={handlePaste} onKeyDown={handleKeyDown} disabled={!canChat || submitting} />
        </div>
        <Group className="composer-row" align="center" gap="xs" wrap="nowrap">
          <Button
            className="attachment-button"
            component="label"
            variant="subtle"
            color="gray"
            disabled={!canAttach || !canChat || submitting}
            aria-label={canAttach ? '添加附件' : '附件能力未提供'}
          >
            <IconPlus size={20} />
            <input ref={fileInputRef} hidden type="file" multiple onChange={addFiles} />
          </Button>
          <ApprovalModeControl session={session} />
          <span className="composer-toolbar-spacer" />
          {usage && (
            <Badge
              variant="light"
              color="gray"
              size="sm"
              leftSection={<IconBrain size={13} />}
              title="当前上下文用量"
            >
              {(usage.last_input_tokens / 1000).toFixed(1)}k / {(usage.context_window / 1000).toFixed(0)}k
            </Badge>
          )}
          <SessionModelControl key={`model:${draftKey}`} session={session} />
          <Button
            className="attachment-button"
            variant="subtle"
            color="gray"
            disabled={!canChat || submitting}
            aria-label="语音输入"
            onClick={() => setVoiceOpened(true)}
          >
            <IconMicrophone size={19} />
          </Button>

          <Magnet padding={20} magnetStrength={3.5} disabled={submitting}>
            <ClickSpark sparkColor={isStopAction ? '#ef4444' : '#3b82f6'} sparkSize={12} sparkRadius={36} sparkCount={8}>
              <Button
                className="send-button"
                color={isStopAction ? 'red' : 'indigo'}
                radius="xl"
                disabled={submitting || (isStopAction ? !canStop : !canChat || !hasDraft)}
                onClick={() => void submit(isStopAction ? 'stop' : 'send', isStopAction ? session.id : null)}
                aria-label={isStopAction ? '停止' : nativeBusy ? '加入队列' : '发送'}
                aria-busy={submitting}
              >
                {isStopAction ? <IconPlayerStop size={18} /> : <IconArrowUp size={18} />}
              </Button>
            </ClickSpark>
          </Magnet>
        </Group>

        </Stack>
      </Paper>
      <VoiceInputSheet opened={voiceOpened} onClose={() => setVoiceOpened(false)} onCommit={commitVoice} />
    </>
  )
}
