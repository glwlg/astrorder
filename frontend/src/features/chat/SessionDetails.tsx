import { IconAlertTriangle, IconBolt, IconCircleCheck, IconClock, IconShieldCheck } from '@tabler/icons-react'
import { Badge, Divider, Group, Paper, Stack, Text, Title } from '@mantine/core'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'
import { AgentStatusBadge, SessionStatusLabel } from '../../components/Status'
import { MarkdownContent } from '../../components/MarkdownContent'
import { SessionRuntimeFacts } from '../../components/SessionRuntimeFacts'
import { LazyDetails } from '../../components/LazyDetails'
import { NativeObservationPanel } from '../../components/NativeObservationPanel'

const capabilityLabels: Record<string, string> = {
  chat: '聊天',
  stop: '停止',
  queue: '排队',
  attachments: '附件',
  approvals: '审批',
  launch: '启动',
  history: '消息记录',
  events: '事件',
  task_events: '任务事件',
  delete: '原生删除',
}

function commandState(command: Command): { label: string; color: string; icon: React.ReactNode } {
  if (command.state === 'failed') return { label: '失败', color: 'red', icon: <IconAlertTriangle size={14} /> }
  if (command.state === 'unknown') return { label: '未知', color: 'yellow', icon: <IconAlertTriangle size={14} /> }
  if (command.state === 'completed') return { label: '完成', color: 'teal', icon: <IconCircleCheck size={14} /> }
  if (command.state === 'queued') return { label: '排队', color: 'yellow', icon: <IconClock size={14} /> }
  return { label: '活动', color: 'indigo', icon: <IconBolt size={14} /> }
}

export function SessionDetails({
  session,
  agent,
  commands,
  messages,
  approvals,
  onApproval,
}: {
  session: Session
  agent?: Agent
  commands: Command[]
  messages: Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
}) {
  const canApprove = agent?.capabilities.includes('approvals') === true
  const activities = messages.filter((message) => message.kind === 'thinking' || message.kind === 'tool').slice(-8).reverse()
  return (
    <Stack className="session-details" gap="md">
      <div>
        <Text size="xs" c="dimmed">当前会话</Text>
        <Title order={3} size="h4" mt={3}>{session.title || '未命名会话'}</Title>
        <Text size="xs" c="dimmed" className="workspace-path">{session.workspace || '未提供工作区'}</Text>
      </div>
      <Group justify="space-between">
        <SessionStatusLabel status={session.status} />
        {agent ? <AgentStatusBadge status={agent.status} /> : <Badge color="gray" variant="light">Agent 未返回</Badge>}
      </Group>
      <Divider />
      <SessionRuntimeFacts session={session} agent={agent} />
      <Divider />
      <section>
        <Text size="xs" fw={700} c="dimmed" mb="xs">能力</Text>
        <Group gap={6}>
          {(agent?.capabilities || []).map((capability) => (
            <Badge key={capability} variant="outline" color="gray">{capabilityLabels[capability] || capability}</Badge>
          ))}
          {!agent?.capabilities.length && <Text size="xs" c="dimmed">未报告能力，交互控件保持禁用。</Text>}
        </Group>
        {agent?.limitation && <Text size="xs" c="dimmed" mt="xs">限制：{agent.limitation}</Text>}
      </section>
      {approvals.length > 0 && (
        <section className="approval-section" aria-label="待处理审批">
          <Group gap={6} mb="xs"><IconShieldCheck size={16} /><Text size="xs" fw={700}>待处理审批</Text></Group>
          <Stack gap="xs">
            {approvals.map((approval) => (
              <Paper className="approval-card" withBorder p="sm" radius="md" key={approval.id}>
                <Text size="sm" fw={600}>{approval.title}</Text>
                {approval.detail && <Text size="xs" c="dimmed" mt={4}><MarkdownContent value={approval.detail} /></Text>}
                {canApprove ? (
                  <Group mt="xs" gap="xs">
                    <button className="approval-button approval-approve" type="button" onClick={() => onApproval(approval, 'approve')}>允许</button>
                    <button className="approval-button approval-cancel" type="button" onClick={() => onApproval(approval, 'cancel')}>取消</button>
                  </Group>
                ) : (
                  <Text size="xs" c="dimmed" mt="xs">Agent 未报告审批能力，操作已禁用。</Text>
                )}
              </Paper>
            ))}
          </Stack>
        </section>
      )}
      <section>
        {agent?.kind==='codex' && <NativeObservationPanel agentId={agent.id} sessionId={session.id} />}
        <LazyDetails summary="工具与命令活动">
        <Stack gap="xs">
          {activities.length === 0 && commands.length === 0 && <Text size="xs" c="dimmed">暂无活动事件。</Text>}
          {activities.map((message) => (
            <Paper className="activity-card" withBorder p="xs" radius="sm" key={message.id}>
              <Group gap="xs" wrap="nowrap">
                <IconBolt size={14} className="activity-icon" />
                <Text size="xs" lineClamp={2}>{message.text || (message.tool ? '工具调用' : '思考活动')}</Text>
              </Group>
            </Paper>
          ))}
          {commands.slice(-6).reverse().map((command) => {
            const state = commandState(command)
            return (
              <Paper className="activity-card" withBorder p="xs" radius="sm" key={command.id}>
                <Group justify="space-between" gap="xs" wrap="nowrap">
                  <Text size="xs" lineClamp={1}>{command.action === 'enqueue' ? '排队指令' : command.action === 'stop' ? '停止指令' : '浏览器指令'}</Text>
                  <Badge size="xs" color={state.color} variant="light" leftSection={state.icon}>{state.label}</Badge>
                </Group>
              </Paper>
            )
          })}
        </Stack>
        </LazyDetails>
      </section>
    </Stack>
  )
}
