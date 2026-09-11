import { IconArrowDown, IconCopy, IconPlayerStop, IconX } from '@tabler/icons-react'
import { Badge, Button, Code, Group, Paper, ScrollArea, Stack, Text, Textarea } from '@mantine/core'
import { useState } from 'react'
import type { Task } from '../../domain/types'
import { ConfirmPopover } from '../../components/ConfirmPopover'
import { confirmationCoordinatesFromEvent, type ConfirmationCoordinates } from '../../components/confirmationPosition'

const statusLabels: Record<Task['status'], string> = {
  pending: '等待中',
  running: '运行中',
  waiting_approval: '待审批',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
  unknown: '未报告',
}

function progressLabel(progress: Task['progress']): string {
  if (!progress) return '未报告'
  const completed = progress.completed ?? progress.done
  const total = progress.total ?? progress.count
  if (completed !== undefined && total !== undefined) return `${completed} / ${total}`
  if (typeof progress.label === 'string' && progress.label.trim()) return progress.label
  return '已报告'
}

export function TaskDetails({
  task,
  canStop,
  onStop,
  onJumpToLatest,
  onClose,
}: {
  task: Task
  canStop: boolean
  onStop?: (task: Task) => void
  onJumpToLatest: () => void
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)
  const [logFilter, setLogFilter] = useState('')
  const [stopConfirmation, setStopConfirmation] = useState<ConfirmationCoordinates | undefined>(undefined)
  const logs = task.logs.filter((log) => !logFilter.trim() || log.text.toLowerCase().includes(logFilter.trim().toLowerCase()))

  const copyCommand = async () => {
    if (!task.command || !navigator.clipboard?.writeText) return
    await navigator.clipboard.writeText(task.command)
    setCopied(true)
  }

  const requestStop = (event?: { clientX: number; clientY: number }) => {
    if (!onStop || !canStop || !task.target_id) return
    setStopConfirmation(confirmationCoordinatesFromEvent(event))
  }

  const confirmStop = () => {
    if (!onStop || !canStop || !task.target_id) return
    onStop(task)
    setStopConfirmation(undefined)
  }

  return (
    <Stack gap="md" className="task-details-panel" aria-label="任务详情">
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={2}>
          <Text fw={700}>{task.title}</Text>
          <Text size="xs" c="dimmed">会话 {task.session_id}</Text>
        </Stack>
        <Badge color={task.status === 'failed' ? 'red' : task.status === 'completed' ? 'green' : 'blue'}>{statusLabels[task.status]}</Badge>
      </Group>

      <Group gap="xs" wrap="wrap">
        <Badge variant="light">{task.kind}</Badge>
        <Text size="sm">进度：{progressLabel(task.progress)}</Text>
      </Group>

      <Stack gap={6}>
        <Group justify="space-between">
          <Text size="sm" fw={600}>命令</Text>
          <Button size="compact-xs" variant="subtle" leftSection={<IconCopy size={14} />} onClick={copyCommand} disabled={!task.command}>
            {copied ? '已复制' : '复制命令'}
          </Button>
        </Group>
        {task.command ? <Code block>{task.command}</Code> : <Text size="sm" c="dimmed">服务端未报告命令。</Text>}
      </Stack>

      <Stack gap={6}>
        <Group justify="space-between">
          <Text size="sm" fw={600}>公开日志</Text>
          <Button size="compact-xs" variant="subtle" leftSection={<IconArrowDown size={14} />} onClick={onJumpToLatest}>跳到最新</Button>
        </Group>
        <Textarea value={logFilter} onChange={(event) => setLogFilter(event.currentTarget.value)} placeholder="筛选日志（不会修改远端任务）" aria-label="筛选任务日志" />
        <ScrollArea h={180} type="auto" className="task-log-scroll">
          <Stack gap="xs">
            {logs.length === 0 ? <Text size="sm" c="dimmed">服务端未提供可用日志。</Text> : logs.map((log) => (
              <Paper key={log.id} withBorder p="xs" radius="sm">
                <Group justify="space-between" gap="xs"><Badge size="xs" variant="light">{log.level}</Badge><Text size="xs" c="dimmed">{log.created_at}</Text></Group>
                <Text size="sm" mt={4}>{log.text}</Text>
              </Paper>
            ))}
          </Stack>
        </ScrollArea>
      </Stack>

      <Group justify="space-between" mt="auto" wrap="wrap">
        <Button variant="subtle" leftSection={<IconX size={16} />} onClick={onClose}>返回</Button>
        {canStop && task.target_id ? <Button color="red" variant="light" leftSection={<IconPlayerStop size={16} />} onClick={(event) => requestStop(event)}>停止任务</Button> : <Text size="xs" c="dimmed">停止不可用：connector 未报告可停止能力。</Text>}
      </Group>
      <ConfirmPopover
        opened={stopConfirmation !== undefined}
        coords={stopConfirmation}
        title="停止任务？"
        message={`确认停止任务“${task.title}”？这会中断该任务正在执行的操作。`}
        confirmLabel="停止任务"
        onConfirm={confirmStop}
        onCancel={() => setStopConfirmation(undefined)}
      />
    </Stack>
  )
}
