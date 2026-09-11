import { useState } from 'react'
import { Badge, Button, Drawer, Group, Paper, Stack, Text, TextInput, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { SshConnection } from '../../domain/types'

const blank = { display_name: '', host: '', port: '22', user: '', ssh_config_alias: '', identity_file: '' }
export function EnvironmentConnections({ embedded = false }: { embedded?: boolean }) {
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['astrorder', 'environments'], queryFn: api.getEnvironments, retry: false, refetchInterval: 10000 })
  const connections = useQuery({ queryKey: ['astrorder', 'connections'], queryFn: api.getConnections })
  const [busy, setBusy] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState(blank)
  const run = async (key: string, action: () => Promise<unknown>) => {
    setBusy(key)
    try { await action(); await client.invalidateQueries({ queryKey: ['astrorder'] }) }
    catch (error) { notifications.show({ color: 'red', message: error instanceof Error ? error.message : '操作未确认' }) }
    finally { setBusy(null) }
  }
  const edit = (row?: SshConnection) => {
    setEditing(row?.id || 'new')
    setForm({ display_name: row?.display_name || '', host: row?.settings?.host || '', port: String(row?.settings?.port || 22), user: row?.settings?.user || '', ssh_config_alias: row?.settings?.ssh_config_alias || '', identity_file: row?.settings?.identity_file || '' })
  }
  const save = async () => {
    const previous = connections.data?.ssh.items.find(row => row.id === editing)?.settings
    const payload = { ...previous, ...form, hermes_path: previous?.hermes_path || null, workspace: previous?.workspace || null, port: Number(form.port), profile_name: previous?.profile_name || 'default', connection_id: editing === 'new' ? null : editing }
    const saved = editing === 'new' ? await api.saveSshConnection(payload) : await api.updateSshConnection(editing!, payload)
    setEditing(null)
    await api.discoverEnvironment(saved.id)
  }
  return <section className="environment-connections">
    <Group justify="space-between" mb="lg"><div>{!embedded && <Title order={2}>连接管理</Title>}<Text c="dimmed">先配置 SSH，再发现并选择接入 Agent；本机自动发现。</Text></div><Button onClick={() => edit()}>添加 SSH 连接</Button></Group>
    {query.isLoading && <Text>正在发现本机 Agent…</Text>}
    {query.isError && <Button variant="subtle" onClick={() => void query.refetch()}>重新读取连接环境</Button>}
    <Stack gap="md">{query.data?.items.map(environment => <Paper key={environment.id} className="environment-row" withBorder radius="lg" p="md">
      <Group justify="space-between"><div><Title order={3} size="h4">{environment.name}</Title><Text size="xs" c="dimmed">{environment.method === 'local' ? '本机自动发现' : `SSH · ${environment.os || '尚未探测系统'}`}</Text></div><Group gap="xs">
        {environment.method === 'ssh' && <Button variant="subtle" onClick={() => edit(connections.data?.ssh.items.find(row => row.id === environment.id))}>配置 SSH</Button>}
        <Button variant="default" loading={busy === environment.id} disabled={!!busy} onClick={() => void run(environment.id, () => api.discoverEnvironment(environment.id))}>{environment.discovered ? '重新发现' : '验证 SSH 并发现 Agent'}</Button>
      </Group></Group>
      <Stack gap="xs" mt="md">{environment.agents.map(agent => <Group className="environment-agent" key={agent.kind} justify="space-between" wrap="nowrap">
        <div><Group gap="xs"><Text fw={600}>{agent.kind === 'hermes' ? 'Hermes' : 'Codex'}</Text><Badge color={agent.state === 'connected' ? 'teal' : agent.state === 'error' ? 'red' : 'gray'}>{agent.state === 'connected' ? '已接入' : agent.state === 'error' ? '连接失败' : agent.state === 'authentication_required' ? '需要登录' : agent.available ? '已发现' : environment.discovered ? '未安装' : '待发现'}</Badge></Group>{agent.detail && <Text size="xs" c={agent.state === 'error' ? 'red' : 'dimmed'} style={{ overflowWrap: 'anywhere' }}>{agent.detail}</Text>}{agent.executable && <Text size="xs" c="dimmed" style={{ overflowWrap: 'anywhere' }}>{agent.executable}</Text>}</div>
        <Button size="xs" variant={agent.state === 'connected' ? 'default' : 'filled'} loading={busy === `${environment.id}:${agent.kind}`} disabled={!!busy || (!agent.available && agent.state !== 'connected')} onClick={() => void run(`${environment.id}:${agent.kind}`, () => api.changeEnvironmentAgent(environment.id, agent.kind, agent.state !== 'connected'))}>{agent.state === 'connected' ? '断开' : '接入'}</Button>
      </Group>)}</Stack>
    </Paper>)}</Stack>
    <Drawer opened={editing !== null} onClose={() => setEditing(null)} title={editing === 'new' ? '配置 SSH 连接' : '修改 SSH 连接'} position="right" size="md">
      <Stack>{([['display_name', '连接名称'], ['ssh_config_alias', 'SSH 配置别名'], ['host', '主机'], ['port', '端口'], ['user', '用户'], ['identity_file', '私钥文件引用（不读取或上传）']] as const).map(([field, label]) => <TextInput key={field} label={label} value={form[field]} onChange={event => { const value = event.currentTarget.value; setForm(current => ({ ...current, [field]: value })) }} />)}
        <Text size="sm" c="dimmed">使用系统 SSH 和已审核的主机指纹；保存后自动发现 Agent，不会自动启动或接入它们。</Text>
        <Button loading={busy === 'save'} onClick={() => void run('save', save)}>保存并发现 Agent</Button>
      </Stack>
    </Drawer>
  </section>
}
