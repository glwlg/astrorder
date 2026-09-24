import { useEffect, useMemo, useState } from 'react'
import { Accordion, Badge, Button, Card, Checkbox, Code, Group, Modal, PasswordInput, Select, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconCheck, IconCloudDataConnection, IconRefresh } from '@tabler/icons-react'
import { api } from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'

type Target = { id: string; kind: 'local' | 'wsl' | 'ssh'; name: string; state?: string; agents: string[] }
type Selection = Record<string, string[]>
type Preview = Awaited<ReturnType<typeof api.previewModelSync>>
type SyncJob = Awaited<ReturnType<typeof api.getModelSyncJobs>>['items'][number]

export function UsageGatewaySettingsCard() {
  const [managementUrl, setManagementUrl] = useState('')
  const [inferenceUrl, setInferenceUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [maskedKey, setMaskedKey] = useState('')
  const [targets, setTargets] = useState<Target[]>([])
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const [selection, setSelection] = useState<Selection>({})
  const [preview, setPreview] = useState<Preview | null>(null)
  const [jobs, setJobs] = useState<SyncJob[]>([])
  const [busy, setBusy] = useState<'load' | 'save' | 'preview' | null>('load')
  const addTask = useBackgroundTasks((state) => state.add)
  const completeTask = useBackgroundTasks((state) => state.complete)
  const failTask = useBackgroundTasks((state) => state.fail)

  const selected = useMemo(() => targets.flatMap((target) => selection[target.id]?.length
    ? [{ target_id: target.id, agents: selection[target.id] }]
    : []), [selection, targets])

  const load = async () => {
    setBusy('load')
    try {
      const [config, targetResult, jobResult] = await Promise.all([api.getAnalyticsConfig(), api.getModelSyncTargets(), api.getModelSyncJobs()])
      setManagementUrl(config.management_url)
      setInferenceUrl(config.inference_url)
      setOverrides(config.target_overrides)
      setMaskedKey(config.masked_key)
      setTargets(targetResult.items)
      setJobs(jobResult.items)
      setSelection({})
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '读取 LLM 网关配置失败' })
    } finally { setBusy(null) }
  }

  useEffect(() => { void load() }, [])

  const save = async (clearKey = false) => {
    setBusy('save')
    try {
      const result = await api.updateAnalyticsConfig({
        gateway_type: 'opencodex', management_url: managementUrl.trim(), inference_url: inferenceUrl.trim(),
        target_overrides: Object.fromEntries(Object.entries(overrides).filter(([, value]) => value.trim()).map(([key, value]) => [key, value.trim()])),
        ...(clearKey ? { api_key: '' } : apiKey.trim() ? { api_key: apiKey.trim() } : {}),
      })
      setMaskedKey(result.masked_key)
      setApiKey('')
      notifications.show({ color: 'teal', message: 'LLM 网关配置已保存', icon: <IconCheck size={16} /> })
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '保存 LLM 网关配置失败' })
    } finally { setBusy(null) }
  }

  const showPreview = async () => {
    setBusy('preview')
    try { setPreview(await api.previewModelSync(selected)) }
    catch (error: any) { notifications.show({ color: 'red', message: error.message || '生成同步预览失败' }) }
    finally { setBusy(null) }
  }

  const startSync = async () => {
    try {
      const operation = await api.startModelSync(selected)
      const taskId = addTask({ title: '同步 Agent 模型配置', detail: '正在读取目录并同步目标', cancel: async () => { await api.cancelModelSync(operation.id) } })
      setPreview(null)
      const timer = window.setInterval(async () => {
        try {
          const job = (await api.getModelSyncJobs()).items.find((item) => item.id === operation.id)
          if (!job || job.status === 'running') return
          window.clearInterval(timer)
          if (job.status === 'success') completeTask(taskId, `已完成 ${job.targets.length} 个目标`)
          else failTask(taskId, job.error || (job.status === 'cancelled' ? '同步已取消' : '模型同步失败'))
          void load()
        } catch { /* persisted job remains authoritative */ }
      }, 1200)
    } catch (error: any) { notifications.show({ color: 'red', message: error.message || '启动模型同步失败' }) }
  }

  
  const selectAll = () => {
    const all: Selection = {}
    targets.forEach((t) => { all[t.id] = [...t.agents] })
    setSelection(all)
  }

  const syncAll = async () => {
    selectAll()
    const allPayload = targets.map((t) => ({ target_id: t.id, agents: t.agents }))
    if (!allPayload.length) return
    try {
      const operation = await api.startModelSync(allPayload)
      const taskId = addTask({ title: '全量同步 Agent 模型配置', detail: '正在同步全部 ' + targets.length + ' 个目标', cancel: async () => { await api.cancelModelSync(operation.id) } })
      const timer = window.setInterval(async () => {
        try {
          const job = (await api.getModelSyncJobs()).items.find((item) => item.id === operation.id)
          if (!job || job.status === 'running') return
          window.clearInterval(timer)
          if (job.status === 'success') completeTask(taskId, '已成功下发至 ' + job.targets.length + ' 个目标')
          else failTask(taskId, job.error || '全量同步失败')
          void load()
        } catch { /* authoritative */ }
      }, 1200)
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '启动全量同步失败' })
    }
  }

  const toggleAgent = (targetId: string, agent: string) => setSelection((current) => ({
    ...current,
    [targetId]: current[targetId]?.includes(agent) ? current[targetId].filter((item) => item !== agent) : [...(current[targetId] || []), agent],
  }))

  return <>
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" mb="md">
        <Group gap="xs"><IconCloudDataConnection size={18} color="#2563EB" /><div>
          <Text fw={600} size="sm">LLM 网关与模型同步</Text>
          <Text size="xs" c="dimmed">用量实时读取；Codex 与 Grok 配置按目标同步</Text>
        </div></Group>
        <Badge size="xs" variant="light" color="teal">OpenCodeX{maskedKey ? ` · ${maskedKey}` : ''}</Badge>
      </Group>
      <Stack gap="sm">
        <Select label="网关类型" value="opencodex" data={[{ value: 'opencodex', label: 'OpenCodeX' }]} allowDeselect={false} />
        <TextInput label="管理地址" placeholder="https://ocx.example.com" value={managementUrl} onChange={(event) => setManagementUrl(event.currentTarget.value)} disabled={busy === 'load'} />
        <TextInput label="推理 Base URL" placeholder="https://llm.example.com/v1" value={inferenceUrl} onChange={(event) => setInferenceUrl(event.currentTarget.value)} disabled={busy === 'load'} />
        <PasswordInput label="API Key" placeholder={maskedKey ? '留空则保留当前 Key' : '可选'} value={apiKey} onChange={(event) => setApiKey(event.currentTarget.value)} />
        <Accordion variant="contained">
          <Accordion.Item value="targets">
            <Accordion.Control>
              <Group justify="space-between" pr="md" style={{ width: '100%' }}>
                <Text size="sm" fw={600}>同步目标 · {targets.length} 个环境</Text>
                <Group gap="xs" onClick={(e) => e.stopPropagation()}>
                  <Button size="compact-xs" variant="light" color="indigo" onClick={selectAll}>全选全部</Button>
                  <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSelection({})}>重置</Button>
                </Group>
              </Group>
            </Accordion.Control>
            <Accordion.Panel>
            <Stack gap="md">{targets.map((target) => <Stack key={target.id} gap={6}>
              <Group justify="space-between"><Text size="sm" fw={600}>{target.name}</Text><Badge size="xs" variant="light">{target.kind.toUpperCase()}</Badge></Group>
              <Group gap="md">{['codex', 'grok', 'hermes'].map((agent) => <Checkbox key={agent} size="xs" label={agent === 'codex' ? 'Codex' : agent === 'grok' ? 'Grok' : 'Hermes（动态）'} checked={selection[target.id]?.includes(agent) || false} onChange={() => toggleAgent(target.id, agent)} />)}</Group>
              <TextInput size="xs" label="推理地址覆盖" placeholder={inferenceUrl || '使用默认推理地址'} value={overrides[target.id] || ''} onChange={(event) => setOverrides((current) => ({ ...current, [target.id]: event.currentTarget.value }))} />
            </Stack>)}</Stack>
          </Accordion.Panel></Accordion.Item>
        </Accordion>
        {jobs.length > 0 && (
          <Stack gap={6}>
            <Group gap="xs">
              <Text size="xs" c="dimmed">最近同步</Text>
              <Badge size="xs" color={jobs[0].status === 'success' ? 'teal' : jobs[0].status === 'running' ? 'blue' : 'red'}>
                {jobs[0].status === 'success' ? '成功' : jobs[0].status === 'running' ? '进行中' : jobs[0].status === 'cancelled' ? '已取消' : '失败'}
              </Badge>
              <Text size="xs" c="dimmed">{jobs[0].targets.length} 个目标{jobs[0].error ? ` · ${jobs[0].error}` : ''}</Text>
            </Group>
            {jobs[0].targets.length > 0 && (
              <Stack gap={4} p="xs" style={{ background: 'var(--astr-surface-muted, #f8fafc)', borderRadius: 6, border: '1px solid var(--mantine-color-default-border)' }}>
                {jobs[0].targets.map((tgt) => {
                  const targetName = targets.find((item) => item.id === tgt.target_id)?.name || tgt.target_id
                  const isSuccess = tgt.status === 'success'
                  return (
                    <Group key={tgt.target_id} justify="space-between" align="center">
                      <Group gap={6}>
                        <Badge size="xs" color={isSuccess ? 'teal' : 'red'} variant="dot" p={0} />
                        <Text size="xs" fw={500}>{targetName}</Text>
                        {tgt.changed && tgt.changed.length > 0 && (
                          <Text size="xs" c="dimmed">（更新: {tgt.changed.join(', ')}）</Text>
                        )}
                      </Group>
                      {tgt.error ? (
                        <Text size="xs" c="red" fw={500} title={tgt.error}>{tgt.error}</Text>
                      ) : (
                        <Badge size="xs" color={isSuccess ? 'teal' : 'gray'} variant="light">
                          {isSuccess ? '已同步' : tgt.status}
                        </Badge>
                      )}
                    </Group>
                  )
                })}
              </Stack>
            )}
          </Stack>
        )}
        <Group justify="space-between">
          <Button variant="subtle" color="red" size="xs" disabled={!maskedKey} onClick={() => void save(true)}>清除 Key</Button>
          <Group>
            <Button size="xs" variant="light" color="indigo" disabled={!targets.length} onClick={syncAll}>一键全量下发</Button>
            <Button size="xs" variant="default" loading={busy === 'preview'} disabled={!selected.length} leftSection={<IconRefresh size={15} />} onClick={showPreview}>预览选中</Button>
            <Button size="xs" disabled={!managementUrl.trim() || !inferenceUrl.trim()} loading={busy === 'save'} onClick={() => void save()}>保存配置</Button>
          </Group>
        </Group>
      </Stack>
    </Card>
    <Modal opened={Boolean(preview)} onClose={() => setPreview(null)} title="模型同步预览" size="lg">
      {preview && <Stack gap="md">
        <Group justify="space-between">
          <Group gap="xs">
            <Badge size="md" color="indigo" variant="light">{preview.model_count} 个网关模型</Badge>
            <Text size="xs" c="dimmed">指纹 {preview.catalog_fingerprint.slice(0, 12)}</Text>
          </Group>
          <Text size="xs" c="dimmed">共 {preview.targets.length} 个目标环境</Text>
        </Group>

        <Stack gap="sm">
          {preview.targets.map((target) => {
            const targetName = targets.find((item) => item.id === target.target_id)?.name || target.target_id
            const diff = target.model_diff || { added: [], removed: [], kept_count: 0 }
            const hasModelChanges = diff.added.length > 0 || diff.removed.length > 0

            return (
              <Card key={target.target_id} withBorder radius="md" p="sm" style={{ background: 'var(--astr-surface-muted, #f8fafc)' }}>
                <Group justify="space-between" mb={6}>
                  <Group gap="xs">
                    <Text fw={650} size="sm">{targetName}</Text>
                    {target.hermes.dynamic && <Badge size="xs" variant="light">Hermes {target.hermes.status}</Badge>}
                    {target.reload_pending && <Badge size="xs" color="yellow">活跃会话稍后重载</Badge>}
                  </Group>
                  <Badge size="xs" color={hasModelChanges ? 'teal' : 'gray'} variant="light">
                    {hasModelChanges ? '模型有更新' : '模型无变化'}
                  </Badge>
                </Group>

                <Stack gap={6} mt={4}>
                  {diff.added.length > 0 && (
                    <Group gap={6} align="flex-start">
                      <Text size="xs" fw={600} c="teal" style={{ flexShrink: 0 }}>新增模型 ({diff.added.length}):</Text>
                      <Group gap={4} wrap="wrap">
                        {diff.added.map((m) => (
                          <Badge key={m} size="xs" color="teal" variant="light">+ {m}</Badge>
                        ))}
                      </Group>
                    </Group>
                  )}

                  {diff.removed.length > 0 && (
                    <Group gap={6} align="flex-start">
                      <Text size="xs" fw={600} c="red" style={{ flexShrink: 0 }}>移除模型 ({diff.removed.length}):</Text>
                      <Group gap={4} wrap="wrap">
                        {diff.removed.map((m) => (
                          <Badge key={m} size="xs" color="red" variant="light">- {m}</Badge>
                        ))}
                      </Group>
                    </Group>
                  )}

                  {!hasModelChanges && (
                    <Text size="xs" c="dimmed">
                      {diff.kept_count ? ('全部 ' + diff.kept_count + ' 个模型与网关完全一致') : '目标模型目录已是最新的'}
                    </Text>
                  )}

                  {target.changes.some((c) => c.changed) && (
                    <Accordion variant="subtle" mt={4}>
                      <Accordion.Item value="diff">
                        <Accordion.Control p={0}>
                          <Text size="xs" c="dimmed">查看底层配置文件 Diff 详情（{target.changes.filter(c => c.changed).length} 项文件变动）</Text>
                        </Accordion.Control>
                        <Accordion.Panel p={0} pt={4}>
                          <Stack gap="xs">
                            {target.changes.filter(c => c.changed).map((change) => (
                              <div key={change.file}>
                                <Text size="xs" fw={600}>{change.file}</Text>
                                {change.diff && <Code block style={{ maxHeight: 180, overflow: 'auto', whiteSpace: 'pre', fontSize: 11 }}>{change.diff}</Code>}
                              </div>
                            ))}
                          </Stack>
                        </Accordion.Panel>
                      </Accordion.Item>
                    </Accordion>
                  )}
                </Stack>
              </Card>
            )
          })}
        </Stack>

        <Group justify="flex-end" mt="xs">
          <Button variant="default" onClick={() => setPreview(null)}>取消</Button>
          <Button color="indigo" onClick={startSync}>开始同步</Button>
        </Group>
      </Stack>}
    </Modal>
  </>
}
