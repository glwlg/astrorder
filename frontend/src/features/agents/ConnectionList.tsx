import { IconAlertTriangle, IconCircleCheck, IconCloud, IconDeviceDesktop, IconPlus, IconSearch } from '@tabler/icons-react'
import { Badge, Button, Group, Paper, Text, Title, UnstyledButton } from '@mantine/core'
import { useMemo, useState } from 'react'
import type { LocalHermesConnection, SshConnection } from '../../domain/types'
import type { CodexConnectionStatus } from '../../api/client'

export type ConnectionListEntry =
  | { kind: 'codex'; id: 'codex'; label: string; connection: CodexConnectionStatus }
  | { kind: 'local'; id: 'local'; label: string; connection: LocalHermesConnection }
  | { kind: 'ssh'; id: string; label: string; connection: SshConnection }

type ConnectionFilter = 'all' | 'connected' | 'attention'

type ConnectionRow = {
  entry: ConnectionListEntry
  method: string
  environment: string
  state: string
  attention: boolean
  connected: boolean
}

export interface ConnectionListProps {
  codex?: CodexConnectionStatus
  local: LocalHermesConnection
  sshItems: SshConnection[]
  onAdd: () => void
  onSelect: (entry: ConnectionListEntry) => void
}

function localState(connection: LocalHermesConnection): { label: string; attention: boolean; connected: boolean } {
  if (connection.state === 'connected') return { label: '已连接', attention: false, connected: true }
  if (connection.state === 'error') return { label: '需处理', attention: true, connected: false }
  if (connection.state === 'connecting') return { label: '连接中', attention: false, connected: false }
  if (connection.state === 'offline') return { label: '未连接', attention: false, connected: false }
  return { label: connection.state === 'installed' ? '已安装' : '已发现', attention: false, connected: false }
}

function sshState(connection: SshConnection): { label: string; attention: boolean; connected: boolean } {
  if (connection.state === 'connected') return { label: '已连接', attention: false, connected: true }
  if (connection.state === 'error') return { label: '需处理', attention: true, connected: false }
  if (connection.state === 'connecting') return { label: '连接中', attention: false, connected: false }
  if (connection.state === 'validated') return { label: '已验证', attention: false, connected: false }
  if (connection.state === 'configured') return { label: '未连接', attention: false, connected: false }
  return { label: '未连接', attention: false, connected: false }
}

function rows(local: LocalHermesConnection, sshItems: SshConnection[], codex?: CodexConnectionStatus): ConnectionRow[] {
  const localStatus = localState(local)
  const localEntry: ConnectionListEntry = { kind: 'local', id: 'local', label: '本机 Hermes', connection: local }
  return [
    {
      entry: localEntry,
      method: '本机',
      environment: local.profile_name ? `profile · ${local.profile_name}` : 'profile 未报告',
      state: localStatus.label,
      attention: localStatus.attention,
      connected: localStatus.connected,
    },
    ...(codex ? [{ entry: { kind: 'codex' as const, id: 'codex' as const, label: '本机 Codex', connection: codex }, method: '本机', environment: 'Codex', state: codex.state === 'connected' ? '已连接' : codex.auth_required ? '需要登录' : codex.state === 'error' ? '需处理' : codex.state === 'connecting' ? '连接中' : '未连接', attention: codex.auth_required || codex.state === 'error', connected: codex.state === 'connected' }] : []),
    ...sshItems.map((connection) => {
      const status = sshState(connection)
      const entry: ConnectionListEntry = { kind: 'ssh', id: connection.id, label: connection.display_name || connection.id, connection }
      const environment = [connection.remote_os || '环境未报告', connection.profile_name || 'profile 未报告'].join(' · ')
      return { entry, method: 'SSH', environment, state: status.label, attention: status.attention, connected: status.connected }
    }),
  ]
}

function stateColor(row: ConnectionRow): string {
  if (row.attention) return 'red'
  if (row.connected) return 'teal'
  return 'gray'
}

export function ConnectionList({ local, sshItems, codex, onAdd, onSelect }: ConnectionListProps) {
  const [filter, setFilter] = useState<ConnectionFilter>('all')
  const [search, setSearch] = useState('')
  const allRows = useMemo(() => rows(local, sshItems, codex), [local, sshItems, codex])
  const visibleRows = useMemo(() => {
    const query = search.trim().toLocaleLowerCase()
    return allRows.filter((row) => {
      if (filter === 'connected' && !row.connected) return false
      if (filter === 'attention' && !row.attention) return false
      if (!query) return true
      return `${row.entry.label} ${row.method} ${row.environment} ${row.state}`.toLocaleLowerCase().includes(query)
    })
  }, [allRows, filter, search])
  const counts = {
    all: allRows.length,
    connected: allRows.filter((row) => row.connected).length,
    attention: allRows.filter((row) => row.attention).length,
  }

  return (
    <section className="connections-list-section" aria-labelledby="connections-list-heading">
      <Group className="route-heading" justify="space-between" align="flex-end" mb="lg" wrap="wrap">
        <div>
          <Text className="eyebrow" size="xs" fw={700}>CONNECTIONS</Text>
          <Title id="connections-list-heading" order={2} size="h2" mt={4}>连接管理</Title>
          <Text c="dimmed" mt={5}>管理本机与远程工作环境；状态和错误只来自服务端连接记录。</Text>
        </div>
        <Button leftSection={<IconPlus size={16} />} onClick={onAdd}>添加连接</Button>
      </Group>
      <Group className="connection-toolbar" justify="space-between" align="center" mb="md" wrap="wrap">
        <div className="connection-filter-group" role="tablist" aria-label="连接状态筛选">
          {([
            ['all', '全部'],
            ['connected', '已连接'],
            ['attention', '需处理'],
          ] as const).map(([value, label]) => (
            <button
              className={`connection-filter ${filter === value ? 'is-active' : ''}`}
              key={value}
              type="button"
              role="tab"
              aria-selected={filter === value}
              onClick={() => setFilter(value)}
            >
              {label} <span>{counts[value]}</span>
            </button>
          ))}
        </div>
        <div className="connection-search-wrap">
          <IconSearch size={16} aria-hidden="true" />
          <input aria-label="搜索连接" placeholder="搜索连接…" value={search} onChange={(event) => setSearch(event.currentTarget.value)} />
        </div>
      </Group>
      <Paper className="connection-table" withBorder radius="lg">
        <div className="connection-table-header" aria-hidden="true">
          <span>名称</span><span>方式</span><span>环境</span><span>状态</span><span>最近活动</span>
        </div>
        <div className="connection-table-body">
          {visibleRows.length === 0 ? (
            <Text className="connection-empty" c="dimmed">没有符合条件的连接</Text>
          ) : visibleRows.map((row) => (
            <UnstyledButton
              className={`connection-row ${row.attention ? 'is-attention' : ''}`}
              key={`${row.entry.kind}:${row.entry.id}`}
              onClick={() => onSelect(row.entry)}
              aria-label={`${row.entry.label}，${row.state}`}
            >
              <div className="connection-name-cell">
                {row.entry.kind !== 'ssh' ? <IconDeviceDesktop size={17} aria-hidden="true" /> : <IconCloud size={17} aria-hidden="true" />}
                <span>{row.entry.label}</span>
              </div>
              <Badge variant="light" color="gray">{row.method}</Badge>
              <Text size="sm" c="dimmed">{row.environment}</Text>
              <div className="connection-state-cell">
                {row.attention ? <IconAlertTriangle size={15} aria-hidden="true" /> : row.connected ? <IconCircleCheck size={15} aria-hidden="true" /> : <span className="connection-state-dot" aria-hidden="true" />}
                <Text size="sm" c={stateColor(row)}>{row.state}</Text>
              </div>
              <Text size="sm" c="dimmed">未报告</Text>
            </UnstyledButton>
          ))}
        </div>
      </Paper>
      <Text className="connection-list-footer" size="sm" c="dimmed">{allRows.length} 个连接 · 历史运行记录位于连接详情</Text>
    </section>
  )
}
