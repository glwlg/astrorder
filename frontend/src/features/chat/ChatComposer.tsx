import { IconMicrophone, IconPaperclip, IconPlus, IconPlayerStop, IconArrowUp, IconX } from '@tabler/icons-react'
import { Alert, Button, Group, Paper, Stack, Text, Textarea } from '@mantine/core'
import { useQueryClient } from '@tanstack/react-query'
import { type ChangeEvent, type ClipboardEvent, type KeyboardEvent, useEffect, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { isDraftSendable, newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Command, CommandAction, DraftAttachment, DraftState, Message, Session } from '../../domain/types'
import { selectCommands, useAstrorderStore } from '../../state/store'
import { submitBrowserCommand } from './commandActions'
import { VoiceInputSheet } from './VoiceInputSheet'
import { SessionModelControl } from './SessionModelControl'
import { GitStatusBar } from './GitStatusBar'
import { clipboardFiles } from './composerMedia'
import { notifySessionSubmitted } from '../../hooks/useSessionOrder'
import '../agents/agentsLayout.css'
import { ApprovalModeControl } from './ApprovalModeControl'

const EMPTY_DRAFT: DraftState = { text: '', attachments: [] }
const allowedFiles = 'image/*,audio/*,.pdf,.txt,.md,.json,.csv,.log,.webp'

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
  onHeightChange,
}: {
  session: Session
  agent?: Agent
  onHeightChange?: (height: number) => void
}) {
  const queryClient = useQueryClient()
  const draftKey = scopeKey(session.agent_id, session.id)
  const draft = useAstrorderStore((state) => state.drafts[draftKey] || EMPTY_DRAFT)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const [submitting, setSubmitting] = useState(false)
  const [voiceOpened, setVoiceOpened] = useState(false)
  const submittingRef = useRef(false)
  const [error, setError] = useState<string | null>(null)
  const [submittedCommandId, setSubmittedCommandId] = useState<string | null>(null)
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
  const runningCommand = commands.find((command) => (command.action === 'send' || command.action === 'enqueue') && (command.state === 'running' || command.state === 'accepted'))
  const busy = submitting || session.status === 'running' || session.status === 'waiting_approval' || Boolean(runningCommand)
  const canStop = hasCapability(agent, 'stop') && busy
  const hasDraft = isDraftSendable(draft.text, draft.attachments)
  // 当且仅当系统处于执行中且输入框完全为空时，按钮才作为紧急打断的“停止”按钮；若有草稿输入，则作为发送/转向
  const isStopAction = busy && !hasDraft
  const submittedCommand = submittedCommandId ? commands.find((item) => item.id === submittedCommandId) : undefined
  const submittedCommandError = submittedCommand && (submittedCommand.state === 'failed' || submittedCommand.state === 'unknown')
    ? submittedCommand.error || (submittedCommand.state === 'failed' ? '原生命令执行失败。' : '原生命令执行结果未确认。')
    : null
  const visibleError = error || submittedCommandError

  const updateDraft = (next: DraftState) => useAstrorderStore.getState().setDraft(session.agent_id, session.id, next)
  const setText = (text: string) => updateDraft({ ...draft, text })

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

  const submit = async (action: CommandAction, targetId: string | null = null) => {
    const files = draft.attachments.map((item) => item.file)
    if (action === 'send' || action === 'enqueue') {
      if (!isDraftSendable(draft.text, files)) return
      if (!canChat) {
        setError('当前 Agent 不支持聊天发送。')
        return
      }
    }
    if (submitting || submittingRef.current) return

    const commandId = newCommandId()
    const command = localCommand(session, commandId, action, action === 'send' || action === 'enqueue' ? draft.text : '')
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')

    // 乐观更新：在用户点击发送瞬间，立即在聊天框呈现用户消息，彻底消除等待迟滞
    if ((action === 'send' || action === 'enqueue') && command.text) {
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
    try {
      const result = await submitBrowserCommand({
        commandId,
        session,
        text: command.text,
        files: action === 'send' || action === 'enqueue' ? files : [],
        action,
        targetId,
        uploadAttachment: api.uploadAttachment,
        createCommand: api.createCommand,
      })
      store.updateOutboxAttachments(session.agent_id, session.id, commandId, result.attachments)
      store.mergeCommands([result.command])
      if (action === 'send' || action === 'enqueue') {
        if (result.command.state !== 'failed' && result.command.state !== 'unknown') {
          store.setDraft(session.agent_id, session.id, EMPTY_DRAFT)
          notifySessionSubmitted(session)
        }
      }
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', session.agent_id, session.id] })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages', session.agent_id, session.id] })
    } catch (nextError) {
      const status = nextError instanceof ApiError && nextError.status >= 400 && nextError.status < 500 ? 'failed' : 'unknown'
      store.markOutboxError(session.agent_id, session.id, commandId, errorMessage(nextError), status)
      setError(errorMessage(nextError))
      // Draft and File objects intentionally remain in Zustand after a failed send.
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
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
      <Paper ref={cardRef} className="composer-card" withBorder radius="lg" p="sm">
        <GitStatusBar session={session} />
        <Stack gap="xs">
        {visibleError && <Alert color="red" variant="light" icon={<IconX size={17} />} aria-live="assertive">{visibleError}</Alert>}
        {draft.attachments.length > 0 && (
          <div className="draft-attachments" aria-label="待发送附件">
            {draft.attachments.map((item) => (
              <span className="draft-attachment" key={item.key}>
                <IconPaperclip size={14} aria-hidden="true" />
                <span>{item.file.name}</span>
                <button type="button" className="draft-remove" onClick={() => removeAttachment(item.key)} aria-label={`移除 ${item.file.name}`}>
                  <IconX size={13} />
                </button>
              </span>
            ))}
          </div>
        )}
        {!canChat && <Text size="xs" c="dimmed">此 Agent 未报告 chat 能力，发送控件已禁用。</Text>}
        <Textarea className="composer-input" aria-label="消息内容" placeholder="随心输入，可粘贴图片" variant="unstyled" autosize minRows={2} maxRows={8} value={draft.text} onChange={(event) => setText(event.currentTarget.value)} onPaste={handlePaste} onKeyDown={handleKeyDown} disabled={!canChat || submitting} />
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
            <input ref={fileInputRef} hidden type="file" multiple accept={allowedFiles} onChange={addFiles} />
          </Button>
          <ApprovalModeControl session={session} />
          <span className="composer-toolbar-spacer" />
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

          <Button
            className="send-button"
            color={isStopAction ? 'red' : 'indigo'}
            radius="xl"
            disabled={submitting || (isStopAction ? !canStop : !canChat || !hasDraft)}
            onClick={() => void submit(isStopAction ? 'stop' : 'send', isStopAction ? runningCommand?.id || null : null)}
            aria-label={isStopAction ? '停止' : '发送'}
            aria-busy={submitting}
          >
            {isStopAction ? <IconPlayerStop size={18} /> : <IconArrowUp size={18} />}
          </Button>
        </Group>

        </Stack>
      </Paper>
      <VoiceInputSheet opened={voiceOpened} onClose={() => setVoiceOpened(false)} onCommit={commitVoice} />
    </>
  )
}
