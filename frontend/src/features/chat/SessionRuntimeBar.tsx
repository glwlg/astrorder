import { IconChevronDown, IconChevronUp, IconListCheck, IconPlayerPlay, IconUsersGroup } from '@tabler/icons-react'
import { Badge, Button, Group, Paper, SimpleGrid, Stack, Text } from '@mantine/core'
import { useState } from 'react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import type { Command, Task } from '../../domain/types'

const taskStatusLabels: Record<Task['status'], string> = {
  pending: '等待中', running: '运行中', waiting_approval: '待审批',
  completed: '已完成', failed: '失败', cancelled: '已取消', unknown: '状态未知',
}

function SummaryItem({ icon, label, value, onClick }: { icon: React.ReactNode; label: string; value: string | null; onClick?: () => void }) {
  if (value === null) return null
  const content = <>
    <Group gap={6} wrap="nowrap"><span className="runtime-summary-icon" aria-hidden="true">{icon}</span><Text size="xs" c="dimmed">{label}</Text></Group>
    <Text size="sm" fw={600} mt={4} truncate title={value || '未报告'}>{value || '未报告'}</Text>
  </>
  return (
    onClick ? <button type="button" className="runtime-summary-item runtime-summary-item-button" onClick={onClick} aria-label={`查看${label}${value ? `：${value}` : ''}`}>{content}</button> : <div className="runtime-summary-item">{content}</div>
  )
}

export function SessionRuntimeBar({ commands, tasks = [], onTaskOpen }: { commands: Command[]; tasks?: Task[]; onTaskOpen?: (task: Task) => void }) {
  const reducedMotion = useReducedMotion()
  const backgroundTasks = tasks.filter((task) => task.kind === 'background' && task.status === 'running')
  const todos = tasks.filter((task) => task.kind === 'todo' && task.status !== 'cancelled')
  const subagents = tasks.filter((task) => task.kind === 'subagent')
  const details = [...backgroundTasks, ...todos, ...subagents]
  const otherDetails = [...backgroundTasks, ...todos]
  const summary = {
    backgroundTasks: backgroundTasks.length ? `${backgroundTasks.length} 个运行中` : null,
    todo: todos.length ? `${todos.filter((task) => task.status === 'completed').length} / ${todos.length}` : null,
    subagents: subagents.length ? `${subagents.filter((task) => ['running', 'pending', 'waiting_approval'].includes(task.status)).length} 个活动 / ${subagents.length} 个` : null,
  }
  const queued = commands.filter((command) => command.state === 'queued').length
  const [expanded, setExpanded] = useState(false)
  if (!summary.backgroundTasks && !summary.todo && !summary.subagents && queued === 0) return null
  return (
    <Paper className="runtime-summary-bar" withBorder radius="lg" p="sm" aria-label="运行摘要">
      <Group justify="space-between" align="flex-start" gap="sm" wrap="wrap">
        <SimpleGrid className="runtime-summary-grid" cols={{ base: 2, sm: 3 }} spacing="sm">
          <SummaryItem icon={<IconPlayerPlay size={15} />} label="后台任务" value={summary.backgroundTasks} onClick={() => setExpanded(true)} />
          <SummaryItem icon={<IconListCheck size={15} />} label="待办" value={summary.todo} onClick={() => setExpanded(true)} />
          <SummaryItem icon={<IconUsersGroup size={15} />} label="子代理" value={summary.subagents} onClick={() => setExpanded(true)} />
        </SimpleGrid>
        <Group gap="xs" wrap="nowrap">
          {queued > 0 && <Badge color="yellow" variant="light">{queued} 个排队</Badge>}
          {details.length > 0 && <Button
            size="compact-sm"
            variant="subtle"
            rightSection={expanded ? <IconChevronUp size={15} /> : <IconChevronDown size={15} />}
            aria-expanded={expanded}
            aria-controls="runtime-summary-details"
            onClick={() => setExpanded((value) => !value)}
          >
            {expanded ? '收起运行详情' : '展开运行详情'}
          </Button>}
        </Group>
      </Group>
      <AnimatePresence initial={false}>
        {expanded && details.length > 0 && <motion.div
          id="runtime-summary-details"
          className="runtime-summary-details"
          aria-label="运行任务列表"
          initial={reducedMotion ? false : { opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: 'auto' }}
          exit={{ opacity: 0, height: 0 }}
          transition={{ duration: 0.3, ease: [0.2, 0.8, 0.2, 1] }}
        >
          <Stack gap="xs" mt="sm">
            {otherDetails.map((task) => (
              <Group key={task.id} className="runtime-summary-detail" justify="space-between" gap="sm" wrap="wrap">
                <Badge variant="outline" color="blue">{taskStatusLabels[task.status]}</Badge>
                {onTaskOpen ? <Button variant="subtle" size="compact-sm" onClick={() => onTaskOpen(task)}>{task.title}</Button> : <Text size="xs">{task.title}</Text>}
              </Group>
            ))}
            {subagents.length > 0 && <section className="subagent-flow" aria-label="子代理分支">
              <div className="subagent-root"><IconUsersGroup size={15} /><Text size="xs" fw={600}>主会话</Text></div>
              <div className="subagent-branches">
                <motion.span className="subagent-trunk" initial={reducedMotion ? false : { scaleY: 0 }} animate={{ scaleY: 1 }} transition={{ duration: 0.4 }} />
                {subagents.map((task, index) => {
                  const active = ['running', 'pending', 'waiting_approval'].includes(task.status)
                  return <motion.div
                    className={`subagent-node ${active ? 'is-active' : ''}`}
                    key={task.id}
                    initial={reducedMotion ? false : { opacity: 0, x: -10 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ delay: reducedMotion ? 0 : 0.12 + index * 0.06 }}
                  >
                    <span className="subagent-node-dot" aria-hidden="true" />
                    {onTaskOpen ? <button type="button" onClick={() => onTaskOpen(task)}>{task.title}</button> : <Text size="xs">{task.title}</Text>}
                    <Badge size="xs" variant="light" color={task.status === 'failed' ? 'red' : active ? 'blue' : 'gray'}>{taskStatusLabels[task.status]}</Badge>
                  </motion.div>
                })}
              </div>
            </section>}
          </Stack>
        </motion.div>}
      </AnimatePresence>
    </Paper>
  )
}
