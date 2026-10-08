import { useState } from 'react'
import { ActionIcon, Button, Group, Loader, Paper, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconChecklist, IconTrash, IconX } from '@tabler/icons-react'
import { useBackgroundTasks } from '../state/backgroundTasks'
import './backgroundTasks.css'

export function BackgroundTasks() {
  const [opened, setOpened] = useState(false)
  const taskMap = useBackgroundTasks((state) => state.tasks)
  const tasks = Object.values(taskMap).sort((a, b) => b.createdAt - a.createdAt)
  const dismiss = useBackgroundTasks((state) => state.dismiss)
  const clearFinished = useBackgroundTasks((state) => state.clearFinished)
  const cancel = useBackgroundTasks((state) => state.cancel)
  if (!tasks.length) return null

  const running = tasks.filter((task) => task.status === 'running').length
  const finishedCount = tasks.length - running

  return <div className="background-tasks">
    {opened && <Paper className="background-tasks-panel" withBorder shadow="lg" radius="lg" role="dialog" aria-label="后台任务">
      <Group justify="space-between" px="md" py="sm">
        <Text fw={600}>后台任务</Text>
        <Group gap={6}>
          {finishedCount > 0 && (
            <Tooltip label="一键清空已结束的历史记录" withArrow>
              <ActionIcon variant="subtle" size="sm" color="gray" aria-label="清空已完成记录" onClick={() => clearFinished()}>
                <IconTrash size={15} />
              </ActionIcon>
            </Tooltip>
          )}
          <ActionIcon variant="subtle" color="gray" aria-label="收起后台任务" onClick={() => setOpened(false)}><IconX size={17} /></ActionIcon>
        </Group>
      </Group>
      <Stack className="background-tasks-list" gap={0}>
        {tasks.map((task) => <div
          className="background-task"
          key={task.id}
          data-status={task.status}
          style={{ cursor: task.action ? 'pointer' : undefined }}
          onClick={() => {
            if (task.action) {
              task.action()
              setOpened(false)
            }
          }}
        >
          <div className="background-task-icon" aria-hidden="true">
            {task.status === 'running' ? <Loader size={18} /> : task.status === 'completed' ? <IconCheck size={19} /> : <IconAlertCircle size={19} />}
          </div>
          <div className="background-task-copy">
            <Text size="sm" fw={600}>{task.title}</Text>
            <Text size="xs" c="dimmed">{task.detail}</Text>
          </div>
          <div className="background-task-actions" onClick={(e) => e.stopPropagation()}>
            {task.status === 'running'
              ? <Button size="compact-xs" variant="subtle" color="red" onClick={() => cancel(task.id)}>取消</Button>
              : <>
                {task.action && <Button size="compact-xs" variant="light" onClick={() => { task.action?.(); setOpened(false) }}>{task.actionLabel || '查看'}</Button>}
                <ActionIcon size="sm" variant="subtle" color="gray" aria-label={`移除${task.title}`} onClick={() => dismiss(task.id)}><IconX size={14} /></ActionIcon>
              </>}
          </div>
        </div>)}
      </Stack>
    </Paper>}
    <UnstyledButton className="background-tasks-trigger" onClick={() => setOpened((value) => !value)} aria-expanded={opened} aria-label="后台任务">
      {running ? <Loader size={17} color="white" /> : <IconChecklist size={18} />}
      <span>后台任务</span>
      <span className="background-tasks-count">{tasks.length}</span>
    </UnstyledButton>
  </div>
}
