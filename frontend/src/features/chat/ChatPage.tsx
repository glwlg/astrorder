import { IconInfoCircle, IconSparkles } from '@tabler/icons-react'
import { ActionIcon, Drawer, Group, Paper, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useParams, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../../api/client'
import { newCommandId } from '../../domain/semantics'
import type { Approval, Command, Session } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { SessionStatusLabel } from '../../components/Status'
import { selectApprovals, selectCommands, selectMessages, selectOutbox, selectSessions, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { ChatComposer } from './ChatComposer'
import { SessionDetails } from './SessionDetails'
import { Transcript } from './Transcript'

const NO_MESSAGES: import('../../domain/types').Message[] = []
const NO_COMMANDS: Command[] = []
const NO_OUTBOX: import('../../domain/types').OutboxEntry[] = []
const NO_APPROVALS: Approval[] = []

function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '会话数据读取失败。'
}

function resolveSession(sessions: Session[], sessionId: string | undefined, agentId: string | null): Session | null {
  if (!sessionId) return sessions.length === 1 ? sessions[0] : null
  const candidates = sessions.filter((session) => session.id === sessionId)
  if (agentId) return candidates.find((session) => session.agent_id === agentId) || null
  return candidates.length === 1 ? candidates[0] : null
}

export function ChatPage() {
  const { sessionId } = useParams()
  const [searchParams] = useSearchParams()
  const queryClient = useQueryClient()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const selected = useMemo(
    () => resolveSession(sessions, sessionId ? decodeURIComponent(sessionId) : undefined, searchParams.get('agent_id')),
    [searchParams, sessionId, sessions],
  )
  const [detailsOpened, { open: openDetails, close: closeDetails }] = useDisclosure(false)
  const resources = useSessionResources(selected, true)
  const messages = useAstrorderStore(useShallow((state) => selected ? selectMessages(state, selected.agent_id, selected.id) : NO_MESSAGES))
  const commands = useAstrorderStore(useShallow((state) => selected ? selectCommands(state, selected.agent_id, selected.id) : NO_COMMANDS))
  const outbox = useAstrorderStore(useShallow((state) => selected ? selectOutbox(state, selected.agent_id, selected.id) : NO_OUTBOX))
  const approvals = useAstrorderStore(useShallow((state) => selected ? selectApprovals(state, selected.agent_id, selected.id) : NO_APPROVALS))
  const agent = selected ? agents[selected.agent_id] : undefined

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

  if (sessions.length === 0) {
    return <EmptyState icon={<IconSparkles />} title="还没有可用会话" description="认证成功，但服务端没有返回 Agent 会话。连接真实 Agent 后，会话会出现在这里。" />
  }

  if (!selected) {
    return (
      <div className="route-page chat-page">
        <EmptyState icon={<IconInfoCircle />} title="请选择一个会话" description="左侧会话列表为空或存在同名 ID 的不同 Agent，请从明确的 Agent 路由进入。" />
      </div>
    )
  }

  const resourceError = resources.messages.error || resources.commands.error
  return (
    <div className="route-page chat-page">
      <div className="chat-layout">
        <section className="chat-column">
          <Paper className="chat-heading" withBorder radius="lg" p="md">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <div className="chat-title-block">
                <Text size="xs" c="dimmed">{agent?.name || selected.agent_id}</Text>
                <Title order={2} size="h3" mt={2}>{selected.title || '未命名会话'}</Title>
                <Text className="workspace-path" size="xs" c="dimmed">{selected.workspace || '未提供工作区'}</Text>
              </div>
              <Group gap="xs" wrap="nowrap">
                <SessionStatusLabel status={selected.status} />
                <ActionIcon className="mobile-details-button" hiddenFrom="md" variant="light" onClick={openDetails} aria-label="打开会话详情">
                  <IconInfoCircle size={18} />
                </ActionIcon>
              </Group>
            </Group>
          </Paper>
          <Transcript
            messages={messages}
            outbox={outbox}
            loading={resources.messages.isLoading || resources.commands.isLoading}
            hasMoreHistory={Boolean(resources.messages.hasNextPage)}
            loadingOlder={resources.messages.isFetchingNextPage}
            onLoadOlder={() => void resources.messages.fetchNextPage()}
            error={resourceError ? errorText(resourceError) : undefined}
            onRetry={() => {
              void resources.messages.refetch()
              void resources.commands.refetch()
            }}
          />
          <ChatComposer session={selected} agent={agent} />
        </section>
        <aside className="desktop-details">
          <SessionDetails session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
        </aside>
      </div>
      <DrawerDetails opened={detailsOpened} onClose={closeDetails} session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
    </div>
  )
}

function DrawerDetails({
  opened,
  onClose,
  ...props
}: {
  opened: boolean
  onClose: () => void
  session: Session
  agent?: import('../../domain/types').Agent
  commands: Command[]
  messages: import('../../domain/types').Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
}) {
  return (
    <Drawer opened={opened} onClose={onClose} title="会话详情" position="right" size="min(92vw, 380px)">
      <SessionDetails {...props} />
    </Drawer>
  )
}
