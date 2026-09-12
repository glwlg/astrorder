import { useState } from 'react'
import { Badge, Button, Group, Paper, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { api } from '../../api/client'

export function CodexConnectionCard() {
  const client = useQueryClient()
  const key = ['astrorder', 'codex-connection']
  const query = useQuery({ queryKey: key, queryFn: api.getCodexConnection, retry: false, staleTime: 5000 })
  const [busy, setBusy] = useState(false)
  const connected = query.data?.state === 'connected'
  const change = async () => {
    setBusy(true)
    try {
      await client.cancelQueries({ queryKey: key })
      await (connected ? api.disconnectCodex() : api.connectCodex())
      const actual = await api.getCodexConnection()
      client.setQueryData(key, actual)
      await client.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
      await client.invalidateQueries({ queryKey: ['astrorder', 'open-sessions'] })
      if (actual.auth_required) notifications.show({ message: actual.detail, color: 'yellow' })
    } catch (error) {
      notifications.show({ message: error instanceof Error ? error.message : 'Codex 连接未确认', color: 'red' })
      void query.refetch()
    } finally { setBusy(false) }
  }
  return <Paper withBorder radius="lg" p="md" mt="md" aria-label="本机 Codex 连接">
    <Stack gap="sm">
      <Group justify="space-between"><Group gap="xs"><AgentBrandIcon kind="codex" size={21} /><Text fw={700}>本机 Codex</Text></Group><Badge color={connected ? 'teal' : 'gray'}>{connected ? '已连接' : query.data?.auth_required ? '需要登录' : '未连接'}</Badge></Group>
      <Text size="sm" c="dimmed">{query.data?.detail || '通过原生 app-server 接管 Codex 会话，保留原生 thread ID。'}</Text>
      {query.data?.daemon_mode && <Badge data-testid="codex-daemon-mode" variant="light" color="indigo">守护进程托管</Badge>}
      {query.data && <Text size="xs">{query.data.session_count} 个原生会话</Text>}
      <Group><Button loading={busy} onClick={() => void change()}>{connected ? '断开 Codex' : '连接 Codex'}</Button><Button variant="subtle" onClick={() => void query.refetch()}>刷新状态</Button></Group>
    </Stack>
  </Paper>
}
