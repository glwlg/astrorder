import { IconChevronDown, IconChevronUp, IconGitBranch, IconListCheck, IconPlayerPlay, IconUsersGroup } from '@tabler/icons-react'
import { Badge, Button, Group, Paper, SimpleGrid, Stack, Text } from '@mantine/core'
import { useState } from 'react'
import type { Command, Message, Task } from '../../domain/types'

type RuntimePayload = Record<string, unknown>

type RuntimeSummary = {
  branch: string | null
  changes: string | null
  backgroundTasks: string | null
  todo: string | null
  subagents: string | null
}

type RuntimeDetail = {
  id: string
  name: string
  text: string
}

function payloadValue(payload: RuntimePayload, paths: string[]): unknown {
  for (const path of paths) {
    let current: unknown = payload
    for (const segment of path.split('.')) {
      if (!current || typeof current !== 'object' || !(segment in current)) {
        current = undefined
        break
      }
      current = (current as RuntimePayload)[segment]
    }
    if (current !== undefined && current !== null) return current
  }
  return null
}

function reportString(value: unknown): string | null {
  if (typeof value === 'string' && value.trim()) return value.trim()
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  return null
}

function progressString(value: unknown): string | null {
  if (!value || typeof value !== 'object') return reportString(value)
  const record = value as RuntimePayload
  const completed = reportString(record.completed ?? record.done ?? record.running)
  const total = reportString(record.total ?? record.count)
  if (completed && total) return `${completed} / ${total}`
  return reportString(record.label ?? record.status)
}

function changeString(value: unknown): string | null {
  if (Array.isArray(value)) return `${value.length} 个文件`
  const reported = reportString(value)
  return reported ? `${reported} 个文件` : null
}

function taskString(value: unknown): string | null {
  if (Array.isArray(value)) return `${value.length} 个任务`
  const record = progressString(value)
  return record ? record : reportString(value)
}

function toolMessages(messages: Message[]): Message[] {
  return messages.filter((message) => message.kind === 'tool' && message.tool && typeof message.tool === 'object')
}

function latestValue(messages: Message[], paths: string[]): unknown {
  for (const message of [...toolMessages(messages)].reverse()) {
    const value = payloadValue(message.tool as RuntimePayload, paths)
    if (value !== null) return value
  }
  return null
}

function taskSummary(tasks: Task[], kind: Task['kind']): string | null {
  const matching = tasks.filter((task) => task.kind === kind)
  if (matching.length === 0) return null
  const running = matching.filter((task) => task.status === 'running' || task.status === 'pending').length
  const progress = matching.find((task) => task.progress)?.progress
  const reportedProgress = progressString(progress)
  if (reportedProgress) return reportedProgress
  if (running > 0) return `${running} 个活动`
  return `${matching.length} 个任务`
}

function buildSummary(messages: Message[], _commands: Command[], tasks: Task[], nativeBranch?: string | null): RuntimeSummary {
  const branchFromTool = reportString(latestValue(messages, ['branch', 'git_branch', 'git.branch']))


  return {
    branch: nativeBranch === undefined ? branchFromTool : nativeBranch === '' ? '非 Git 工作区' : nativeBranch,
    changes: changeString(latestValue(messages, ['changed_files', 'changes.files', 'git.changed_files'])),
    backgroundTasks: taskSummary(tasks, 'background') || taskString(latestValue(messages, ['background_tasks', 'tasks.background', 'task_queue'])),
    todo: taskSummary(tasks, 'todo') || progressString(latestValue(messages, ['todo', 'todo_progress', 'tasks.todo'])),
    subagents: taskSummary(tasks, 'subagent') || progressString(latestValue(messages, ['subagents', 'sub_agents', 'agents.subagents'])),
  }
}

function buildDetails(messages: Message[]): RuntimeDetail[] {
  return [...toolMessages(messages)].reverse().slice(0, 8).map((message) => {
    const payload = message.tool as RuntimePayload
    const name = reportString(payload.name ?? payload.tool_name) || '公开工具活动'
    return { id: message.id, name, text: message.text || '服务端未提供活动摘要。' }
  })
}

function SummaryItem({ icon, label, value, taskTitle, onClick, showUnknown }: { icon: React.ReactNode; label: string; value: string | null; taskTitle?: string; onClick?: () => void; showUnknown?: boolean }) {
  if (value === null && !showUnknown) return null
  const content = <>
    <Group gap={6} wrap="nowrap"><span className="runtime-summary-icon" aria-hidden="true">{icon}</span><Text size="xs" c="dimmed">{label}</Text></Group>
    <Text size="sm" fw={600} mt={4} truncate title={value || '未报告'}>{value || '未报告'}</Text>
  </>
  return (
    onClick ? <button type="button" className="runtime-summary-item runtime-summary-item-button" onClick={onClick} aria-label={`查看${label}${taskTitle ? `：${taskTitle}` : value ? `：${value}` : ''}`}>{content}</button> : <div className="runtime-summary-item">{content}</div>
  )
}

export function SessionRuntimeBar({ messages, commands, tasks = [], onTaskOpen, nativeBranch }: { messages: Message[]; commands: Command[]; tasks?: Task[]; onTaskOpen?: (task: Task) => void; nativeBranch?: string | null }) {
  const [summary, details] = [buildSummary(messages, commands, tasks, nativeBranch), buildDetails(messages)]
  const firstTask = (kind: Task['kind']) => tasks.find((task) => task.kind === kind)
  const queued = commands.filter((command) => command.state === 'queued').length
  const [expanded, setExpanded] = useState(false)
  return (
    <Paper className="runtime-summary-bar" withBorder radius="lg" p="sm" aria-label="运行摘要">
      <Group justify="space-between" align="flex-start" gap="sm" wrap="wrap">
        <SimpleGrid className="runtime-summary-grid" cols={{ base: 2, sm: 5 }} spacing="sm">
          <SummaryItem showUnknown={expanded} icon={<IconGitBranch size={15} />} label="分支" value={summary.branch} />
          <SummaryItem showUnknown={expanded} icon={<IconListCheck size={15} />} label="改动" value={summary.changes} />
          <SummaryItem showUnknown={expanded} icon={<IconPlayerPlay size={15} />} label="后台任务" value={summary.backgroundTasks} taskTitle={firstTask('background')?.title} onClick={firstTask('background') && onTaskOpen ? () => onTaskOpen(firstTask('background')!) : undefined} />
          <SummaryItem showUnknown={expanded} icon={<IconListCheck size={15} />} label="待办" value={summary.todo} taskTitle={firstTask('todo')?.title} onClick={firstTask('todo') && onTaskOpen ? () => onTaskOpen(firstTask('todo')!) : undefined} />
          <SummaryItem showUnknown={expanded} icon={<IconUsersGroup size={15} />} label="子代理" value={summary.subagents} taskTitle={firstTask('subagent')?.title} onClick={firstTask('subagent') && onTaskOpen ? () => onTaskOpen(firstTask('subagent')!) : undefined} />
        </SimpleGrid>
        <Group gap="xs" wrap="nowrap">
          {queued > 0 && <Badge color="yellow" variant="light">{queued} 个排队</Badge>}
          <Button
            size="compact-sm"
            variant="subtle"
            rightSection={expanded ? <IconChevronUp size={15} /> : <IconChevronDown size={15} />}
            aria-expanded={expanded}
            aria-controls="runtime-summary-details"
            onClick={() => setExpanded((value) => !value)}
          >
            {expanded ? '收起运行详情' : '展开运行详情'}
          </Button>
        </Group>
      </Group>
      {expanded && <Stack id="runtime-summary-details" className="runtime-summary-details" gap="xs" mt="sm" aria-label="公开运行日志">
        {details.length === 0 ? <Text size="xs" c="dimmed">服务端尚未报告公开工具日志。</Text> : details.map((detail) => (
          <Group key={detail.id} className="runtime-summary-detail" justify="space-between" gap="sm" wrap="wrap">
            <Badge variant="outline" color="gray">{detail.name}</Badge>
            <Text size="xs" c="dimmed">{detail.text}</Text>
          </Group>
        ))}
      </Stack>}
    </Paper>
  )
}
