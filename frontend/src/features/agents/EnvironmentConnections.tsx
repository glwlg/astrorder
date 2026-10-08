import { useState, useRef, useMemo, type ReactNode } from 'react'
import { ActionIcon, Badge, Button, Checkbox, Group, Loader, Menu, Modal, Paper, Progress, Stack, Table, Text, TextInput, Title } from '@mantine/core'
import { IconArrowUpCircle, IconDotsVertical, IconPlayerPlay, IconPlayerStop, IconRefresh, IconTerminal } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { SettingsDrawer } from '../../components/SettingsDrawer'
import { api } from '../../api/client'
import type { SshConnection } from '../../domain/types'
import { useBackgroundTasks } from '../../state/backgroundTasks'

const blank = { display_name: '', host: '', port: '22', user: '', ssh_config_alias: '', identity_file: '' }
export function EnvironmentConnections({ embedded = false, renderAgentConfiguration }: { embedded?: boolean; renderAgentConfiguration?: (environmentId: string, kind: string) => ReactNode }) {
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['astrorder', 'environments'], queryFn: api.getEnvironments, retry: false, refetchInterval: 10000 })
  const connections = useQuery({ queryKey: ['astrorder', 'connections'], queryFn: api.getConnections })
  const [busy, setBusy] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [editingOpened, setEditingOpened] = useState(false)
  const [form, setForm] = useState(blank)
  const [selectedId, setSelectedId] = useState('local')
  const [configuration, setConfiguration] = useState<{ environmentId: string; kind: string } | null>(null)
  const [configurationOpened, setConfigurationOpened] = useState(false)
  const items = query.data?.items ?? []
  const selected = items.find(item => item.id === selectedId) ?? items[0]

  // 一键升级与批量升级状态
  const [batchUpgrading, setBatchUpgrading] = useState(false)
  const [batchModalOpen, setBatchModalOpen] = useState(false)
  const [batchMode, setBatchMode] = useState<'node' | 'all'>('all')
  const [batchProgress, setBatchProgress] = useState<{ current: number; total: number; currentName: string }>({ current: 0, total: 0, currentName: '' })
  const [batchLogs, setBatchLogs] = useState<string>('')
  const [selectedAgentIds, setSelectedAgentIds] = useState<Record<string, boolean>>({})
  const [targetsLoading, setTargetsLoading] = useState(false)
  const [upgradeTargets, setUpgradeTargets] = useState<Array<{
    id: string
    name: string
    kind: string
    status: string
    connection_id: string
    current_version: string
    latest_version: string
    has_update: boolean
  }>>([])
  const batchAbortRef = useRef<AbortController | null>(null)
  const batchLogPreRef = useRef<HTMLPreElement | null>(null)

  const addBackgroundTask = useBackgroundTasks((state) => state.add)
  const completeBackgroundTask = useBackgroundTasks((state) => state.complete)
  const failBackgroundTask = useBackgroundTasks((state) => state.fail)

  // 打开批量升级前预先读取当前和最新版本号列表
  const openBatchUpgradeDialog = async (mode: 'node' | 'all') => {
    setBatchMode(mode)
    setBatchModalOpen(true)
    setTargetsLoading(true)
    try {
      const res = await api.getUpgradeTargets()
      const list = res.items || []
      setUpgradeTargets(list)
      // 默认全选该模式下的 Agent
      const filtered = list.filter((a) => {
        if (mode === 'node') {
          if (!selected) return false
          return selected.id === 'local'
            ? !a.connection_id || a.connection_id === 'local'
            : a.connection_id === selected.id
        }
        return true
      })
      // 默认仅选中可以升级的 Agent（若全部都已最新则不勾选或全不选）
      const initialSelected: Record<string, boolean> = {}
      for (const t of filtered) {
        if (t.has_update) {
          initialSelected[t.id] = true
        }
      }
      setSelectedAgentIds(initialSelected)
    } catch (err: unknown) {
      notifications.show({ color: 'red', message: (err as Error)?.message || '读取 Agent 目标列表失败' })
    } finally {
      setTargetsLoading(false)
    }
  }

  // 待升级的目标列表（根据当前打开的模式 node / all 过滤）
  const displayedTargets = useMemo(() => {
    return upgradeTargets.filter((a) => {
      if (batchMode === 'node') {
        if (!selected) return false
        return selected.id === 'local'
          ? !a.connection_id || a.connection_id === 'local'
          : a.connection_id === selected.id
      }
      return true
    })
  }, [upgradeTargets, batchMode, selected])

  // 执行勾选的 Agent 批量升级
  const startSelectedUpgrade = async () => {
    if (batchUpgrading) return
    const targets = displayedTargets.filter((t) => selectedAgentIds[t.id])
    if (targets.length === 0) {
      notifications.show({ color: 'yellow', message: '请至少勾选一个要升级的 Agent。' })
      return
    }

    const abortCtrl = new AbortController()
    batchAbortRef.current = abortCtrl
    setBatchUpgrading(true)
    setBatchLogs('')
    setBatchProgress({ current: 0, total: targets.length, currentName: targets[0].name })

    const title = batchMode === 'node'
      ? `批量升级 [${selected?.name}] Agent (${targets.length})`
      : `批量升级 Agent (${targets.length})`
    const taskId = addBackgroundTask({
      title,
      detail: '正在批量执行版本更新…',
      actionLabel: '查看日志',
      action: () => setBatchModalOpen(true),
      cancel: () => {
        if (batchAbortRef.current) {
          batchAbortRef.current.abort()
        }
      },
    })

    const appendLog = (line: string) => {
      setBatchLogs((prev) => {
        const next = prev + line
        if (batchLogPreRef.current) {
          batchLogPreRef.current.scrollTop = batchLogPreRef.current.scrollHeight
        }
        return next
      })
    }

    appendLog(`[INIT] 开始任务：${title}\n目标列表: ${targets.map((t) => `${t.name} (${t.kind})`).join(', ')}\n\n`)

    let successCount = 0
    let failureCount = 0

    try {
      for (let i = 0; i < targets.length; i++) {
        if (abortCtrl.signal.aborted) {
          appendLog(`\n[ABORT] 用户已中止批量升级任务。\n`)
          break
        }
        const target = targets[i]
        setBatchProgress({ current: i + 1, total: targets.length, currentName: target.name })
        appendLog(`----------------------------------------\n[${i + 1}/${targets.length}] 正在升级: ${target.name} (${target.kind})...\n`)

        try {
          const res = await api.upgradeAgentStream(
            target.id,
            (chunk) => appendLog(chunk),
            (cmd) => appendLog(`[EXEC] ${cmd}\n`),
            abortCtrl.signal
          )
          if (res.ok) {
            successCount++
            appendLog(`[DONE] ${target.name} 升级成功。\n\n`)
          } else {
            failureCount++
            appendLog(`[FAIL] ${target.name} 升级退出码异常: ${res.exit_code}\n\n`)
          }
        } catch (err: unknown) {
          if ((err as Error)?.name === 'AbortError') {
            appendLog(`[ABORT] ${target.name} 任务被手动取消。\n`)
            break
          }
          failureCount++
          appendLog(`[ERROR] ${target.name} 升级出错: ${(err as Error)?.message || String(err)}\n\n`)
        }
      }

      if (abortCtrl.signal.aborted) {
        failBackgroundTask(taskId, '批量升级被用户取消')
        notifications.show({ color: 'gray', title: '批量升级已取消', message: '已停止后续 Agent 的升级。' })
      } else if (failureCount === 0) {
        completeBackgroundTask(taskId, `全部 ${successCount} 个 Agent 已顺利完成升级`)
        notifications.show({ color: 'teal', title: '批量升级完成', message: `共 ${successCount} 个 Agent 已成功升级至最新版本！` })
      } else {
        failBackgroundTask(taskId, `完成 ${successCount} 个，失败 ${failureCount} 个`)
        notifications.show({ color: 'yellow', title: '批量升级部分完成', message: `成功: ${successCount}，失败: ${failureCount}。详情请查看日志。` })
      }
    } finally {
      setBatchUpgrading(false)
      batchAbortRef.current = null
      void client.invalidateQueries({ queryKey: ['astrorder'] })
    }
  }
  const run = async (key: string, action: () => Promise<unknown>) => {
    setBusy(key)
    try { await action(); await client.invalidateQueries({ queryKey: ['astrorder'] }) }
    catch (error) { notifications.show({ color: 'red', message: error instanceof Error ? error.message : '操作未确认' }) }
    finally { setBusy(null) }
  }
  const edit = (row?: SshConnection) => {
    setEditing(row?.id || 'new')
    setEditingOpened(true)
    setForm({ display_name: row?.display_name || '', host: row?.settings?.host || '', port: String(row?.settings?.port || 22), user: row?.settings?.user || '', ssh_config_alias: row?.settings?.ssh_config_alias || '', identity_file: row?.settings?.identity_file || '' })
  }
  const save = async () => {
    const previous = connections.data?.ssh.items.find(row => row.id === editing)?.settings
    const payload = { ...previous, ...form, hermes_path: previous?.hermes_path || null, workspace: previous?.workspace || null, port: Number(form.port), profile_name: previous?.profile_name || 'default', connection_id: editing === 'new' ? null : editing }
    const saved = editing === 'new' ? await api.saveSshConnection(payload) : await api.updateSshConnection(editing!, payload)
    setEditingOpened(false)
    await api.discoverEnvironment(saved.id)
  }
  const hostAddress = (id: string) => {
    const settings = connections.data?.ssh.items.find(row => row.id === id)?.settings
    return id === 'local' ? '本机自动发现' : settings?.host ? `${settings.user ? `${settings.user}@` : ''}${settings.host}:${settings.port || 22}` : settings?.ssh_config_alias || 'SSH'
  }
  return <section className={`environment-connections${embedded ? ' is-embedded' : ''}`}>
    <div className="connection-hosts-layout">
      <nav className="connection-hosts" aria-label="运行环境">
        <div className="connection-hosts-heading"><Text fw={600} size="xs">主机节点 ({items.length})</Text><Text size="xs" c="dimmed">选择环境</Text></div>
        {query.isLoading && <Text size="xs" role="status">正在发现本机 Agent…</Text>}
        {query.isError && <Button variant="subtle" onClick={() => void query.refetch()}>重新读取连接环境</Button>}
        <div className="connection-host-list">{items.map(item => <button className="connection-host" type="button" key={item.id} aria-pressed={selected?.id === item.id} onClick={() => setSelectedId(item.id)}>
          <span className="connection-host-name">{item.name}<small>{item.agents.some(agent => agent.state === 'error') ? '异常' : item.agents.some(agent => agent.state === 'connected') ? '已连接' : item.discovered ? '已发现' : '待发现'}</small></span>
          <span className="connection-host-address">{hostAddress(item.id)}</span>
          <span className="connection-host-meta">{item.agents.filter(agent => agent.state === 'connected').length} 接入 Agent{item.os ? ` · ${item.os}` : ''}</span>
        </button>)}</div>
        <Stack gap={6} mt="auto">
          <Button variant="default" size="xs" className="connection-add-host" onClick={() => edit()}>添加 SSH 连接</Button>
          <Button
            variant="light"
            color="indigo"
            size="xs"
            leftSection={<IconArrowUpCircle size={14} />}
            loading={batchUpgrading}
            onClick={() => void openBatchUpgradeDialog('all')}
            title="一键选择升级全部环境节点下的所有 Agent"
          >
            全节点升级
          </Button>
        </Stack>
      </nav>
      <div className="connection-host-detail">
        {selected ? <>
          <header className="connection-detail-header"><div><Group gap="sm"><Title order={3} size="h4">{selected.name}</Title><Text className="connection-address" size="xs">{hostAddress(selected.id)}</Text></Group><Text size="xs" c="dimmed" mt={6}>{selected.method === 'local' ? '本机' : 'SSH'} · {selected.agents.filter(agent => agent.state === 'connected').length} 个 Agent 已接入</Text></div><Group gap="xs">
            <Button
              size="xs"
              variant="light"
              color="indigo"
              leftSection={<IconArrowUpCircle size={14} />}
              loading={batchUpgrading}
              onClick={() => void openBatchUpgradeDialog('node')}
              title={`一键按序升级当前主机 [${selected.name}] 下的所有 Agent`}
            >
              升级本节点 Agent
            </Button>
            <Button size="xs" variant="default" loading={busy === selected.id} disabled={!!busy} onClick={() => void run(selected.id, () => api.discoverEnvironment(selected.id))}>{selected.discovered ? '重新发现' : '验证 SSH 并发现 Agent'}</Button>
            {selected.method === 'ssh' && <Button size="xs" variant="default" onClick={() => edit(connections.data?.ssh.items.find(row => row.id === selected.id))}>配置 SSH</Button>}
          </Group></header>
          <div className="connection-agent-list"><Group justify="space-between" mb="sm"><Text size="xs" fw={600}>托管 Agent 实例与执行通道</Text><Text size="xs" c="dimmed">运行配置与状态</Text></Group>
            {selected.agents.map(agent => <article className="connection-agent-card" key={agent.kind}>
              <span className="connection-agent-icon"><AgentBrandIcon kind={agent.kind} size={22} /></span>
              <div className="connection-agent-description"><Group gap="xs"><Text fw={600} size="sm">{agentKindLabel(agent.kind)}</Text>{agent.daemon_mode && <Text size="xs" c="dimmed" data-testid={`environment-daemon-mode-${selected.id}-${agent.kind}`}>守护进程托管</Text>}</Group>
                <Text size="xs" c={agent.state === 'error' ? 'red' : 'dimmed'} className="connection-agent-path">{agent.executable || agent.detail || '尚未发现执行路径'}</Text>
                {agent.executable && agent.state === 'error' && <Text size="xs" c="red">{agent.detail}</Text>}
              </div>
              <Text size="xs" className="connection-agent-state" c={agent.state === 'error' ? 'red' : 'dimmed'}>{agent.state === 'connected' ? '已接入' : agent.state === 'error' ? '连接失败' : agent.state === 'authentication_required' ? '需要登录' : agent.available ? '已发现' : selected.discovered ? '未安装' : '待发现'}</Text>
              <Group gap="xs" wrap="nowrap">
                {renderAgentConfiguration && <Button size="compact-xs" variant="default" onClick={() => { setConfiguration({ environmentId: selected.id, kind: agent.kind }); setConfigurationOpened(true) }} aria-label={`配置 ${agentKindLabel(agent.kind)}`}>配置</Button>}
                {agent.state !== 'connected' && <Button size="compact-xs" disabled={!!busy || !agent.available} loading={busy === `${selected.id}:${agent.kind}`} onClick={() => void run(`${selected.id}:${agent.kind}`, () => api.changeEnvironmentAgent(selected.id, agent.kind, true))}>接入</Button>}
                <Menu position="bottom-end" withinPortal><Menu.Target><ActionIcon variant="default" size="sm" aria-label={`${agentKindLabel(agent.kind)} 更多操作`}><IconDotsVertical size={15} /></ActionIcon></Menu.Target><Menu.Dropdown>
                  <Menu.Item disabled={!!busy || agent.state !== 'connected'} onClick={() => void run(`${selected.id}:${agent.kind}`, () => api.changeEnvironmentAgent(selected.id, agent.kind, false))}>断开</Menu.Item>
                  <Menu.Item color="red" disabled={!!busy || (!agent.available && agent.state !== 'connected')} onClick={() => void run(`restart:${selected.id}:${agent.kind}`, () => api.restartEnvironmentAgent(selected.id, agent.kind))}>强制重启</Menu.Item>
                </Menu.Dropdown></Menu>
              </Group>
            </article>)}
            {!selected.agents.length && <Text size="sm" c="dimmed" py="lg">尚未发现 Agent，请先验证环境。</Text>}
          </div>
          <footer className="connection-detail-footer"><Text size="xs" c="dimmed">{hostAddress(selected.id)}</Text>{selected.method === 'ssh' && <Button size="compact-xs" variant="subtle" disabled={!!busy || !selected.agents.some(agent => agent.state === 'connected')} onClick={() => void run(`disconnect:${selected.id}`, async () => { for (const agent of selected.agents) { if (agent.state === 'connected') await api.changeEnvironmentAgent(selected.id, agent.kind, false) } })}>断开环境</Button>}</footer>
        </> : <Text c="dimmed" p="lg">{!embedded && '连接管理 · '}选择或添加一个运行环境</Text>}
      </div>
    </div>
    <SettingsDrawer closeButtonProps={{ 'aria-label': '关闭运行配置' }} opened={configurationOpened} onClose={() => setConfigurationOpened(false)} onExitTransitionEnd={() => setConfiguration(null)} title={configuration ? `${agentKindLabel(configuration.kind)} · 运行配置` : '运行配置'}>{configuration && renderAgentConfiguration?.(configuration.environmentId, configuration.kind)}</SettingsDrawer>
    <SettingsDrawer closeButtonProps={{ 'aria-label': '关闭 SSH 配置' }} opened={editingOpened} onClose={() => setEditingOpened(false)} onExitTransitionEnd={() => setEditing(null)} title={editing === 'new' ? '配置 SSH 连接' : '修改 SSH 连接'}>
      <Stack>{([['display_name', '连接名称'], ['ssh_config_alias', 'SSH 配置别名'], ['host', '主机'], ['port', '端口'], ['user', '用户'], ['identity_file', '私钥文件引用（不读取或上传）']] as const).map(([field, label]) => <TextInput key={field} label={label} value={form[field]} onChange={event => { const value = event.currentTarget.value; setForm(current => ({ ...current, [field]: value })) }} />)}
        <Text size="sm" c="dimmed">使用系统 SSH 和已审核的主机指纹；保存后自动发现 Agent，不会自动启动或接入它们。</Text>
        <Button loading={busy === 'save'} onClick={() => void run('save', save)}>保存并发现 Agent</Button>
      </Stack>
    </SettingsDrawer>

    {/* 批量升级进度与版本列表弹窗 */}
    <Modal
      opened={batchModalOpen}
      onClose={() => setBatchModalOpen(false)}
      title={
        <Group gap={8}>
          <IconTerminal size={18} color="var(--astr-indigo)" />
          <Text fw={600} size="sm">
            {batchUpgrading ? 'Agent 批量升级中心 · 正在执行' : 'Agent 批量版本检测与升级'}
          </Text>
          {batchUpgrading && <Badge size="xs" color="indigo" variant="light">升级进行中…</Badge>}
        </Group>
      }
      size="xl"
      radius="md"
    >
      <Stack gap="sm">
        {targetsLoading ? (
          <Group justify="center" p="xl">
            <Loader size="sm" />
            <Text size="sm" c="dimmed">正在探测各环境节点的 Agent 当前版本与最新发布版本…</Text>
          </Group>
        ) : !batchUpgrading && !batchLogs ? (
          <>
            <Paper p="xs" withBorder radius="sm" style={{ background: 'var(--astr-surface-muted)' }}>
              <Group justify="space-between">
                <Group gap="xs">
                  <Checkbox
                    size="xs"
                    label="全选所有 Agent"
                    checked={displayedTargets.length > 0 && displayedTargets.every(t => selectedAgentIds[t.id])}
                    indeterminate={displayedTargets.some(t => selectedAgentIds[t.id]) && !displayedTargets.every(t => selectedAgentIds[t.id])}
                    onChange={(e) => {
                      const checked = e.currentTarget.checked
                      const next: Record<string, boolean> = { ...selectedAgentIds }
                      for (const t of displayedTargets) {
                        next[t.id] = checked
                      }
                      setSelectedAgentIds(next)
                    }}
                  />
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    onClick={() => {
                      const next: Record<string, boolean> = {}
                      for (const t of displayedTargets) {
                        if (t.has_update) next[t.id] = true
                      }
                      setSelectedAgentIds(next)
                    }}
                  >
                    仅选有新版本 ({displayedTargets.filter(t => t.has_update).length})
                  </Button>
                </Group>
                <Text size="xs" c="dimmed">
                  已选中 {displayedTargets.filter(t => selectedAgentIds[t.id]).length} / {displayedTargets.length} 个 Agent
                </Text>
              </Group>
            </Paper>

            <Paper withBorder radius="sm" style={{ maxHeight: '360px', overflowY: 'auto' }}>
              <Table verticalSpacing="xs" striped highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th style={{ width: 40 }} />
                    <Table.Th>Agent 名称</Table.Th>
                    <Table.Th>所属环境</Table.Th>
                    <Table.Th>当前版本</Table.Th>
                    <Table.Th>官方最新版本</Table.Th>
                    <Table.Th style={{ textAlign: 'right' }}>版本状态</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {displayedTargets.map((target) => (
                    <Table.Tr key={target.id}>
                      <Table.Td>
                        <Checkbox
                          size="xs"
                          checked={Boolean(selectedAgentIds[target.id])}
                          onChange={(e) => {
                            const checked = e.currentTarget.checked
                            setSelectedAgentIds((prev) => ({ ...prev, [target.id]: checked }))
                          }}
                        />
                      </Table.Td>
                      <Table.Td>
                        <Group gap={6}>
                          <AgentBrandIcon kind={target.kind as any} size={16} />
                          <Text size="xs" fw={600}>{target.name}</Text>
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Text size="xs" c="dimmed">
                          {target.connection_id === 'local' ? '本机' : (items.find(i => i.id === target.connection_id)?.name || target.connection_id)}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="xs" ff="monospace">{target.current_version}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Text size="xs" ff="monospace" c={target.has_update ? 'teal' : undefined}>
                          {target.latest_version}
                        </Text>
                      </Table.Td>
                      <Table.Td style={{ textAlign: 'right' }}>
                        {target.has_update ? (
                          <Badge size="xs" color="teal" variant="light">可升级</Badge>
                        ) : target.current_version === '未知' ? (
                          <Badge size="xs" color="gray" variant="outline">版本未知</Badge>
                        ) : (
                          <Badge size="xs" color="gray" variant="light">已是最新</Badge>
                        )}
                      </Table.Td>
                    </Table.Tr>
                  ))}
                  {displayedTargets.length === 0 && (
                    <Table.Tr>
                      <Table.Td colSpan={6} style={{ textAlign: 'center', color: 'var(--astr-text-muted)' }}>
                        未发现符合条件的 Agent
                      </Table.Td>
                    </Table.Tr>
                  )}
                </Table.Tbody>
              </Table>
            </Paper>

            <Group justify="space-between" mt="xs">
              <Button
                variant="subtle"
                size="xs"
                leftSection={<IconRefresh size={14} />}
                onClick={() => void openBatchUpgradeDialog(batchMode)}
              >
                重新检测
              </Button>
              <Group gap="xs">
                <Button variant="default" size="xs" onClick={() => setBatchModalOpen(false)}>取消</Button>
                <Button
                  color="indigo"
                  size="xs"
                  leftSection={<IconPlayerPlay size={14} />}
                  onClick={() => void startSelectedUpgrade()}
                  disabled={displayedTargets.filter(t => selectedAgentIds[t.id]).length === 0}
                >
                  开始升级选中 Agent ({displayedTargets.filter(t => selectedAgentIds[t.id]).length})
                </Button>
              </Group>
            </Group>
          </>
        ) : (
          <>
            <Paper p="xs" withBorder radius="sm" style={{ background: 'var(--astr-surface-muted)' }}>
              <Group justify="space-between" mb={6}>
                <Text size="xs" fw={600}>
                  总进度：{batchProgress.current} / {batchProgress.total}
                </Text>
                <Text size="xs" c="dimmed">
                  {batchUpgrading ? `当前处理: ${batchProgress.currentName}` : '升级任务已执行完毕'}
                </Text>
              </Group>
              <Progress
                value={batchProgress.total ? (batchProgress.current / batchProgress.total) * 100 : 0}
                size="sm"
                radius="xl"
                animated={batchUpgrading}
                color="indigo"
              />
            </Paper>

            <Paper p="sm" withBorder radius="sm" style={{ background: '#090d16', maxHeight: '380px', overflowY: 'auto' }}>
              <pre ref={batchLogPreRef} style={{ margin: 0, fontSize: '11px', fontFamily: 'monospace', color: '#86efac', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                {batchLogs || (batchUpgrading ? '正在初始化批量升级任务…' : '任务尚未开始。')}
              </pre>
            </Paper>

            <Group justify="space-between" align="center">
              <Text size="xs" c="dimmed">
                {batchUpgrading ? '关闭此弹窗将自动放入后台任务继续执行。' : ''}
              </Text>
              <Group gap="xs">
                {batchUpgrading && (
                  <Button
                    size="xs"
                    color="red"
                    variant="subtle"
                    leftSection={<IconPlayerStop size={14} />}
                    onClick={() => {
                      if (batchAbortRef.current) {
                        batchAbortRef.current.abort()
                      }
                    }}
                  >
                    中止升级
                  </Button>
                )}
                {!batchUpgrading && (
                  <Button
                    size="xs"
                    variant="light"
                    onClick={() => {
                      setBatchLogs('')
                      void openBatchUpgradeDialog(batchMode)
                    }}
                  >
                    返回列表
                  </Button>
                )}
                <Button size="xs" variant="default" onClick={() => setBatchModalOpen(false)}>
                  {batchUpgrading ? '放入后台运行' : '关闭'}
                </Button>
              </Group>
            </Group>
          </>
        )}
      </Stack>
    </Modal>
  </section>
}
