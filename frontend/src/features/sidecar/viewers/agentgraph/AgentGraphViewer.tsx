import { useMemo, useState } from 'react'
import {
  Badge,
  Button,
  Drawer,
  Group,
  Paper,
  Progress,
  ScrollArea,
  Stack,
  Text,
} from '@mantine/core'
import {
  IconAlertTriangle,
  IconBrain,
  IconCheck,
  IconClock,
  IconGitBranch,
  IconRobot,
  IconTool,
  IconUser,
} from '@tabler/icons-react'
import { useShallow } from 'zustand/react/shallow'
import type { ViewerContext } from '../../types'
import { selectMessages, selectTasks, useAstrorderStore } from '../../../../state/store'
import type { Message, Task } from '../../../../domain/types'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import { parseNodesFromSession, type AgentGraphNode, type StepKind, type StepStatus } from './parseAgentGraph'

const EMPTY_MESSAGES: Message[] = []
const EMPTY_TASKS: Task[] = []

export type { AgentGraphNode, StepKind, StepStatus }

function getNodeIcon(kind: StepKind, status: StepStatus) {
  if (status === 'running') {
    return <IconClock size={16} color="var(--astr-indigo, #6366f1)" className="rotating-icon" />
  }
  if (status === 'failed') {
    return <IconAlertTriangle size={16} color="var(--astr-red, #ef4444)" />
  }

  switch (kind) {
    case 'user_prompt':
      return <IconUser size={16} color="var(--astr-blue, #3b82f6)" />
    case 'thinking':
      return <IconBrain size={16} color="var(--astr-purple, #a855f7)" />
    case 'tool_call':
      return <IconTool size={16} color="var(--astr-cyan, #06b6d4)" />
    case 'subagent':
      return <IconRobot size={16} color="var(--astr-indigo, #6366f1)" />
    case 'planning':
      return <IconGitBranch size={16} color="var(--astr-warning, #f59e0b)" />
    case 'completed':
    default:
      return <IconCheck size={16} color="var(--astr-green, #10b981)" />
  }
}

export function AgentGraphViewer({ artifact }: ViewerContext) {
  const sessionId = artifact.sessionId
  const agentId = artifact.agentId

  const messages = useAstrorderStore(
    useShallow((s) => (sessionId && agentId ? selectMessages(s, agentId, sessionId) : EMPTY_MESSAGES))
  )
  const tasks = useAstrorderStore(
    useShallow((s) => (sessionId && agentId ? selectTasks(s, agentId, sessionId) : EMPTY_TASKS))
  )

  const [selectedNode, setSelectedNode] = useState<AgentGraphNode | null>(null)
  const [filterKind, setFilterKind] = useState<'all' | 'tools' | 'thinking'>('all')

  const rawNodes = useMemo(() => parseNodesFromSession(messages, tasks), [messages, tasks])

  const nodes = useMemo(() => {
    if (filterKind === 'tools') return rawNodes.filter((n) => n.kind === 'tool_call')
    if (filterKind === 'thinking') return rawNodes.filter((n) => n.kind === 'thinking' || n.kind === 'user_prompt')
    return rawNodes
  }, [rawNodes, filterKind])

  // 统计指标
  const completedCount = nodes.filter((n) => n.status === 'completed').length
  const runningCount = nodes.filter((n) => n.status === 'running').length
  const progressPct = nodes.length > 0 ? Math.round((completedCount / nodes.length) * 100) : 0

  return (
    <div
      className="agent-graph-viewer"
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        background: 'var(--astr-surface, #ffffff)',
      }}
    >
      {/* 顶部指示状态面板 */}
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Stack gap={8}>
          <Group justify="space-between" wrap="nowrap">
            <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
              <IconBrain size={18} color="var(--astr-indigo, #6366f1)" />
              <Text size="xs" fw={700} truncate>
                Agent 思考决策流程与状态机
              </Text>
              {runningCount > 0 ? (
                <Badge size="xs" color="indigo" variant="filled">
                  执行活跃中 ({runningCount})
                </Badge>
              ) : (
                <Badge size="xs" color="teal" variant="light">
                  就绪
                </Badge>
              )}
            </Group>
            <Group gap={6} wrap="nowrap">
              <Button.Group>
                <Button
                  size="compact-xs"
                  variant={filterKind === 'all' ? 'filled' : 'subtle'}
                  color="gray"
                  onClick={() => setFilterKind('all')}
                >
                  全部 ({rawNodes.length})
                </Button>
                <Button
                  size="compact-xs"
                  variant={filterKind === 'tools' ? 'filled' : 'subtle'}
                  color="gray"
                  onClick={() => setFilterKind('tools')}
                >
                  工具链
                </Button>
                <Button
                  size="compact-xs"
                  variant={filterKind === 'thinking' ? 'filled' : 'subtle'}
                  color="gray"
                  onClick={() => setFilterKind('thinking')}
                >
                  思考 (CoT)
                </Button>
              </Button.Group>
            </Group>
          </Group>

          {/* 进度流概览条 */}
          <Group gap="xs" align="center" wrap="nowrap">
            <Progress value={progressPct} size="xs" radius="xl" style={{ flex: 1 }} color="indigo" />
            <Text size="xs" c="dimmed" style={{ fontSize: 11, whiteSpace: 'nowrap' }}>
              {completedCount} / {nodes.length} 步完成 ({progressPct}%)
            </Text>
          </Group>
        </Stack>
      </Paper>

      {/* DAG 状态节点拓扑流 */}
      <ScrollArea style={{ flex: 1, padding: '16px 20px' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 0, position: 'relative' }}>
          {nodes.map((node, idx) => {
            const isLast = idx === nodes.length - 1
            const isSelected = selectedNode?.id === node.id

            return (
              <div key={node.id} style={{ display: 'flex', gap: 14, position: 'relative' }}>
                {/* 左侧垂直状态线与节点徽章 */}
                <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', width: 28, flexShrink: 0 }}>
                  <div
                    style={{
                      width: 28,
                      height: 28,
                      borderRadius: '50%',
                      background: isSelected
                        ? 'var(--astr-indigo)'
                        : node.status === 'running'
                        ? 'var(--astr-indigo-subtle, rgba(99, 102, 241, 0.15))'
                        : 'var(--astr-card, #f8f9fa)',
                      border: `2px solid ${
                        node.status === 'running'
                          ? 'var(--astr-indigo, #6366f1)'
                          : node.status === 'completed'
                          ? 'var(--astr-green, #10b981)'
                          : 'var(--astr-border)'
                      }`,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      zIndex: 2,
                      cursor: 'pointer',
                      transition: 'all 0.15s ease',
                      boxShadow: node.status === 'running' ? '0 0 10px rgba(99, 102, 241, 0.4)' : undefined,
                    }}
                    onClick={() => setSelectedNode(node)}
                  >
                    {getNodeIcon(node.kind, node.status)}
                  </div>
                  {!isLast && (
                    <div
                      style={{
                        width: 2,
                        flex: 1,
                        minHeight: 32,
                        background:
                          node.status === 'completed'
                            ? 'var(--astr-green, #10b981)'
                            : 'var(--astr-border, rgba(0, 0, 0, 0.1))',
                        margin: '3px 0',
                      }}
                    />
                  )}
                </div>

                {/* 右侧节点卡片 */}
                <div style={{ flex: 1, paddingBottom: isLast ? 0 : 20, minWidth: 0 }}>
                  <Paper
                    p="xs"
                    radius="md"
                    withBorder
                    style={{
                      cursor: 'pointer',
                      background: isSelected
                        ? 'var(--astr-card-hover, rgba(99, 102, 241, 0.06))'
                        : 'var(--astr-card, #ffffff)',
                      borderColor: isSelected
                        ? 'var(--astr-indigo, #6366f1)'
                        : node.status === 'running'
                        ? 'var(--astr-indigo)'
                        : 'var(--astr-border)',
                      transition: 'border-color 0.12s ease, box-shadow 0.12s ease',
                    }}
                    onClick={() => setSelectedNode(node)}
                  >
                    <Group justify="space-between" wrap="nowrap" mb={4}>
                      <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                        <Text size="xs" fw={600} truncate style={{ color: 'var(--astr-text)' }}>
                          {node.title}
                        </Text>
                        {node.toolName && (
                          <Badge size="xs" variant="light" color="cyan">
                            {node.toolName}
                          </Badge>
                        )}
                      </Group>
                      <Group gap={4} wrap="nowrap">
                        {node.timestamp && (
                          <Text size="xs" c="dimmed" style={{ fontSize: 10 }}>
                            {new Date(node.timestamp).toLocaleTimeString('zh-CN', {
                              hour: '2-digit',
                              minute: '2-digit',
                              second: '2-digit',
                            })}
                          </Text>
                        )}
                      </Group>
                    </Group>

                    {/* 节点摘要预览 */}
                    {node.detail && (
                      <Text
                        size="xs"
                        c="dimmed"
                        lineClamp={2}
                        style={{ fontSize: 12, lineHeight: 1.4, wordBreak: 'break-all' }}
                      >
                        {node.detail}
                      </Text>
                    )}
                  </Paper>
                </div>
              </div>
            )
          })}
        </div>
      </ScrollArea>

      {/* 底部详细抽屉 / 节点深挖面板 */}
      <Drawer
        opened={Boolean(selectedNode)}
        onClose={() => setSelectedNode(null)}
        title={selectedNode?.title || '节点详情'}
        position="right"
        size="md"
      >
        {selectedNode && (
          <Stack gap="md">
            <Group justify="space-between">
              <Badge color="indigo">{selectedNode.kind}</Badge>
              <Badge color={selectedNode.status === 'completed' ? 'teal' : 'blue'}>
                {selectedNode.status}
              </Badge>
            </Group>

            {selectedNode.toolPayload != null && (
              <div>
                <Text size="xs" fw={600} mb={4}>
                  工具参数输入 (Payload)
                </Text>
                <Paper
                  p="xs"
                  withBorder
                  style={{
                    background: 'var(--astr-surface)',
                    fontFamily: 'monospace',
                    fontSize: 12,
                    maxHeight: 240,
                    overflow: 'auto',
                  }}
                >
                  <pre style={{ margin: 0 }}>
                    {JSON.stringify(selectedNode.toolPayload, null, 2)}
                  </pre>
                </Paper>
              </div>
            )}

            {selectedNode.detail && (
              <div>
                <Text size="xs" fw={600} mb={4}>
                  详细思考/执行输出
                </Text>
                <Paper p="xs" withBorder style={{ maxHeight: 360, overflow: 'auto' }}>
                  <MarkdownContent value={selectedNode.detail} />
                </Paper>
              </div>
            )}
          </Stack>
        )}
      </Drawer>
    </div>
  )
}
