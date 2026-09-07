import { IconPaperclip, IconPlayerPause, IconSend, IconStack2, IconX } from '@tabler/icons-react'
import { Alert, Button, Group, Paper, Stack, Text, Textarea } from '@mantine/core'
import { useQueryClient } from '@tanstack/react-query'
import { type ChangeEvent, type KeyboardEvent, useRef, useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { isDraftSendable, newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Command, CommandAction, DraftAttachment, DraftState, Session } from '../../domain/types'
import { selectCommands, useAstrorderStore } from '../../state/store'
import { submitBrowserCommand } from './commandActions'

const EMPTY_DRAFT: DraftState = { text: '', attachments: [] }
const allowedFiles = 'image/*,audio/*,.pdf,.txt,.md,.json,.csv,.log,.webp'

function hasCapability(agent: Agent | undefined, capability: string): boolean {
  return Boolean(agent?.capabilities.includes(capability))
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

export function ChatComposer({ session, agent }: { session: Session; agent?: Agent }) {
  const queryClient = useQueryClient()
  const draftKey = scopeKey(session.agent_id, session.id)
  const draft = useAstrorderStore((state) => state.drafts[draftKey] || EMPTY_DRAFT)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const [submitting, setSubmitting] = useState(false)
  const submittingRef = useRef(false)
  const [error, setError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const attachmentKeyRef = useRef(0)

  const canChat = hasCapability(agent, 'chat')
  const canAttach = hasCapability(agent, 'attachments')
  const canQueue = hasCapability(agent, 'queue')
  const runningCommand = commands.find((command) => command.state === 'running' || command.state === 'accepted')
  const canStop = hasCapability(agent, 'stop') && Boolean(runningCommand)

  const updateDraft = (next: DraftState) => useAstrorderStore.getState().setDraft(session.agent_id, session.id, next)
  const setText = (text: string) => updateDraft({ ...draft, text })

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const files = [...(event.currentTarget.files || [])]
    if (files.length === 0) return
    const additions: DraftAttachment[] = files.map((file) => {
      attachmentKeyRef.current += 1
      return { key: `draft-attachment-${attachmentKeyRef.current}`, file }
    })
    updateDraft({ ...draft, attachments: [...draft.attachments, ...additions] })
    event.currentTarget.value = ''
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
    submittingRef.current = true
    setSubmitting(true)
    setError(null)
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
      void submit('send')
    }
  }

  return (
    <Paper className="composer-card" withBorder radius="lg" p="sm">
      <Stack gap="xs">
        {error && <Alert color="red" variant="light" icon={<IconX size={17} />} aria-live="assertive">{error}</Alert>}
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
        <Group className="composer-row" align="flex-end" gap="xs" wrap="nowrap">
          <Button
            className="attachment-button"
            component="label"
            variant="subtle"
            color="gray"
            disabled={!canAttach || !canChat || submitting}
            aria-label={canAttach ? '添加附件' : '附件能力未提供'}
          >
            <IconPaperclip size={19} />
            <input ref={fileInputRef} hidden type="file" multiple accept={allowedFiles} onChange={addFiles} />
          </Button>
          <Textarea
            className="composer-input"
            aria-label="消息内容"
            placeholder={canChat ? '向 Agent 发送指令…' : '当前 Agent 不支持发送'}
            minRows={1}
            maxRows={6}
            value={draft.text}
            onChange={(event) => setText(event.currentTarget.value)}
            onKeyDown={handleKeyDown}
            disabled={!canChat || submitting}
          />
          <Button
            className="send-button"
            color="indigo"
            radius="xl"
            loading={submitting}
            disabled={!canChat || !isDraftSendable(draft.text, draft.attachments) || submitting}
            onClick={() => void submit('send')}
            aria-label="发送"
          >
            <IconSend size={18} />
          </Button>
        </Group>
        <Group className="composer-actions" gap="xs" justify="space-between">
          <Group gap="xs">
            <Button
              size="compact-sm"
              variant="light"
              leftSection={<IconStack2 size={15} />}
              disabled={!canQueue || !canChat || !isDraftSendable(draft.text, draft.attachments) || submitting}
              onClick={() => void submit('enqueue')}
            >
              排队发送
            </Button>
            <Button
              size="compact-sm"
              variant="subtle"
              color="red"
              leftSection={<IconPlayerPause size={15} />}
              disabled={!canStop || submitting}
              onClick={() => void submit('stop', runningCommand?.id || null)}
              title={!hasCapability(agent, 'stop') ? 'Agent 未报告 stop 能力' : !runningCommand ? '没有可验证的运行命令' : undefined}
            >
              停止
            </Button>
          </Group>
          <Text size="xs" c="dimmed">Shift + Enter 换行</Text>
        </Group>
      </Stack>
    </Paper>
  )
}
