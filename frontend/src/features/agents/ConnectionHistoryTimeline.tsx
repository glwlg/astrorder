import { IconAlertTriangle, IconClock, IconRefresh } from '@tabler/icons-react'
import { Alert, Badge, Button, Group, Paper, Stack, Text } from '@mantine/core'
import type { ConnectionHistoryEntry } from '../../domain/types'

const stageLabels: Record<string, string> = {
  configure: '配置',
  validate: 'SSH 校验',
  deploy: '插件部署',
  handshake: '握手',
  discovery: '目录发现',
  disconnect: '断开',
  connect: '连接',
}

function stateColor(state: string): string {
  if (state === 'failed' || state === 'error') return 'red'
  if (state === 'running' || state === 'connecting') return 'indigo'
  if (state === 'connected' || state === 'completed') return 'teal'
  return 'gray'
}

export function ConnectionHistoryTimeline({
  items,
  isLoading,
  error,
  hasMore,
  onLoadMore,
}: {
  items: ConnectionHistoryEntry[]
  isLoading: boolean
  error: unknown
  hasMore: boolean
  onLoadMore: () => void
}) {
  const errorMessage = error instanceof Error ? error.message : '服务未提供该接口'
  return (
    <Stack gap="sm" className="connection-history" aria-label="连接运行历史">
      <Group justify="space-between"><Text fw={700}>连接运行历史</Text><IconClock size={17} aria-hidden="true" /></Group>
      {Boolean(error) && <Alert color="yellow" icon={<IconAlertTriangle size={17} />}>运行历史不可用：{errorMessage}。</Alert>}
      {!error && !isLoading && items.length === 0 && <Text size="sm" c="dimmed">尚无连接运行历史。</Text>}
      {items.map((item) => <Paper key={item.id} withBorder p="sm" radius="sm">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <div><Text size="sm" fw={600}>{stageLabels[item.stage] || item.stage}</Text><Text size="xs" c="dimmed" mt={3}>{item.detail}</Text></div>
          <Badge color={stateColor(item.state)} variant="light">{item.state}</Badge>
        </Group>
        <Text size="xs" c="dimmed" mt={6}>{item.created_at}</Text>
      </Paper>)}
      {isLoading && <Text size="sm" c="dimmed">正在读取运行历史…</Text>}
      {hasMore && !error && <Button variant="subtle" leftSection={<IconRefresh size={15} />} onClick={onLoadMore} disabled={isLoading}>加载更早记录</Button>}
    </Stack>
  )
}
