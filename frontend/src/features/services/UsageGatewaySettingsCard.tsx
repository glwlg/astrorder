import { useEffect, useMemo, useState } from 'react'
import {
  Accordion,
  Badge,
  Button,
  Card,
  Checkbox,
  Code,
  Collapse,
  Group,
  Modal,
  PasswordInput,
  Select,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconCloudDataConnection,
  IconLink,
  IconRefresh,
  IconSend,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

type Target = { id: string; kind: 'local' | 'wsl' | 'ssh'; name: string; state?: string; agents: string[] }
type Selection = Record<string, string[]>
type Preview = Awaited<ReturnType<typeof api.previewModelSync>>
type SyncJob = Awaited<ReturnType<typeof api.getModelSyncJobs>>['items'][number]

interface GatewayFormState {
  managementUrl: string
  inferenceUrl: string
  apiKey: string
  maskedKey: string
  managementKey: string
  maskedManagementKey: string
  overrides: Record<string, string>
}

const DEFAULT_PROFILES: Record<'opencodex' | 'magpie', GatewayFormState> = {
  opencodex: {
    managementUrl: '',
    inferenceUrl: '',
    apiKey: '',
    maskedKey: '',
    managementKey: '',
    maskedManagementKey: '',
    overrides: {},
  },
  magpie: {
    managementUrl: '',
    inferenceUrl: '',
    apiKey: '',
    maskedKey: '',
    managementKey: '',
    maskedManagementKey: '',
    overrides: {},
  },
}

const MANAGED_FILE_LABELS: Record<string, string> = {
  MANAGED_FILE_CODEX_CONFIG: 'Codex 配置',
  MANAGED_FILE_CODEX_AUTH: 'Codex 凭据',
  MANAGED_FILE_CODEX_CATALOG: 'Codex 模型目录',
  MANAGED_FILE_CODEX_MAGPIE_CATALOG: 'Codex 模型目录',
  MANAGED_FILE_CODEX_OPENCODEX_CATALOG: 'Codex 模型目录',
  MANAGED_FILE_GROK_CONFIG: 'Grok 配置',
  MANAGED_FILE_GROK_AUTH: 'Grok 凭据',
  MANAGED_FILE_HERMES_CONFIG: 'Hermes 配置',
  MANAGED_FILE_HERMES_ENV: 'Hermes 环境变量',
  MANAGED_FILE_ANTIGRAVITY_CONFIG: 'Antigravity 配置',
  MANAGED_FILE_AGY_CONFIG: 'Antigravity 配置',
  MANAGED_FILE_AGY_MODELS: 'Antigravity 模型目录',
}

function formatManagedChangedItem(raw: string): string {
  const key = raw.trim()
  if (!key) return ''
  if (MANAGED_FILE_LABELS[key]) return MANAGED_FILE_LABELS[key]
  const upper = key.toUpperCase()
  if (MANAGED_FILE_LABELS[upper]) return MANAGED_FILE_LABELS[upper]
  if (upper.startsWith('MANAGED_FILE_')) {
    const cleaned = upper
      .slice('MANAGED_FILE_'.length)
      .replace(/_/g, ' ')
      .toLowerCase()
      .replace(/\b\w/g, (ch) => ch.toUpperCase())
    return cleaned
  }
  return key
}

function summarizeChangedItems(items?: string[]): string[] {
  if (!items || items.length === 0) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of items) {
    const label = formatManagedChangedItem(item)
    if (label && !seen.has(label)) {
      seen.add(label)
      out.push(label)
    }
  }
  return out
}

function agentDisplayLabel(agent: string): string {
  if (agent === 'codex') return 'Codex'
  if (agent === 'grok') return 'Grok'
  if (agent === 'antigravity') return 'Antigravity (agy)'
  return 'Hermes'
}

export function UsageGatewaySettingsCard() {
  const [gatewayType, setGatewayType] = useState<'opencodex' | 'magpie'>('opencodex')
  const [profiles, setProfiles] = useState<Record<'opencodex' | 'magpie', GatewayFormState>>(DEFAULT_PROFILES)
  const [managementUrl, setManagementUrl] = useState(DEFAULT_PROFILES.opencodex.managementUrl)
  const [inferenceUrl, setInferenceUrl] = useState(DEFAULT_PROFILES.opencodex.inferenceUrl)
  const [apiKey, setApiKey] = useState('')
  const [maskedKey, setMaskedKey] = useState('')
  const [managementKey, setManagementKey] = useState('')
  const [maskedManagementKey, setMaskedManagementKey] = useState('')
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  const [expandedOverrides, setExpandedOverrides] = useState<Record<string, boolean>>({})
  const [rawTargets, setTargets] = useState<Target[]>([])
  const [selection, setSelection] = useState<Selection>({})
  const [preview, setPreview] = useState<Preview | null>(null)
  const [jobs, setJobs] = useState<SyncJob[]>([])
  const [busy, setBusy] = useState<'load' | 'save' | 'preview' | null>('load')
  const enabledPlugins = usePluginSettingsStore((state) => state.enabledPlugins)
  const isAgentEnabled = usePluginSettingsStore((state) => state.isAgentEnabled)
  const addTask = useBackgroundTasks((state) => state.add)
  const completeTask = useBackgroundTasks((state) => state.complete)
  const failTask = useBackgroundTasks((state) => state.fail)

  const targets = useMemo(
    () =>
      rawTargets.map((target) => ({
        ...target,
        agents: target.agents.filter((agent) => isAgentEnabled(agent)),
      })),
    [rawTargets, enabledPlugins, isAgentEnabled],
  )

  const selected = useMemo(
    () =>
      targets.flatMap((target) => {
        const active = (selection[target.id] || []).filter((agent) => target.agents.includes(agent))
        return active.length ? [{ target_id: target.id, agents: active }] : []
      }),
    [selection, targets],
  )

  const totalSelectedAgents = useMemo(
    () => selected.reduce((sum, item) => sum + item.agents.length, 0),
    [selected],
  )

  const load = async () => {
    setBusy('load')
    try {
      const [config, targetResult, jobResult] = await Promise.all([
        api.getAnalyticsConfig(),
        api.getModelSyncTargets(),
        api.getModelSyncJobs(),
      ])
      const currentType = (config.gateway_type as 'opencodex' | 'magpie') || 'opencodex'
      const nextProfiles: Record<'opencodex' | 'magpie', GatewayFormState> = {
        opencodex: { ...DEFAULT_PROFILES.opencodex },
        magpie: { ...DEFAULT_PROFILES.magpie },
      }

      if (config.gateway_profiles?.opencodex) {
        const p = config.gateway_profiles.opencodex
        nextProfiles.opencodex = {
          managementUrl: p.management_url || DEFAULT_PROFILES.opencodex.managementUrl,
          inferenceUrl: p.inference_url || DEFAULT_PROFILES.opencodex.inferenceUrl,
          overrides: p.target_overrides || {},
          maskedKey: p.masked_key || '',
          maskedManagementKey: p.masked_management_key || '',
          apiKey: '',
          managementKey: '',
        }
      }
      if (config.gateway_profiles?.magpie) {
        const p = config.gateway_profiles.magpie
        nextProfiles.magpie = {
          managementUrl: p.management_url || DEFAULT_PROFILES.magpie.managementUrl,
          inferenceUrl: p.inference_url || DEFAULT_PROFILES.magpie.inferenceUrl,
          overrides: p.target_overrides || {},
          maskedKey: p.masked_key || '',
          maskedManagementKey: p.masked_management_key || '',
          apiKey: '',
          managementKey: '',
        }
      }

      nextProfiles[currentType] = {
        managementUrl: config.management_url || nextProfiles[currentType].managementUrl,
        inferenceUrl: config.inference_url || nextProfiles[currentType].inferenceUrl,
        overrides: config.target_overrides || {},
        maskedKey: config.masked_key || '',
        maskedManagementKey: config.masked_management_key || '',
        apiKey: '',
        managementKey: '',
      }

      setProfiles(nextProfiles)
      setGatewayType(currentType)
      setManagementUrl(nextProfiles[currentType].managementUrl)
      setInferenceUrl(nextProfiles[currentType].inferenceUrl)
      setOverrides(nextProfiles[currentType].overrides)
      setMaskedKey(nextProfiles[currentType].maskedKey)
      setMaskedManagementKey(nextProfiles[currentType].maskedManagementKey)
      setApiKey('')
      setManagementKey('')
      setTargets(targetResult.items)
      setJobs(jobResult.items)
      setSelection({})
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '读取 LLM 网关配置失败' })
    } finally {
      setBusy(null)
    }
  }

  useEffect(() => {
    void load()
    const onProviderSynced = (event: Event) => {
      const detail = (event as CustomEvent<{ source?: string }>).detail
      if (detail?.source === 'ai-providers') {
        void load()
      }
    }
    window.addEventListener('astrorder:gateway-config-changed', onProviderSynced)
    return () => window.removeEventListener('astrorder:gateway-config-changed', onProviderSynced)
  }, [])

  const handleGatewayTypeChange = (nextType: 'opencodex' | 'magpie') => {
    if (nextType === gatewayType) return
    setProfiles((prev) => {
      const updated: Record<'opencodex' | 'magpie', GatewayFormState> = {
        ...prev,
        [gatewayType]: {
          managementUrl,
          inferenceUrl,
          apiKey,
          maskedKey,
          managementKey,
          maskedManagementKey,
          overrides,
        },
      }
      const targetState = updated[nextType] || DEFAULT_PROFILES[nextType]
      setGatewayType(nextType)
      setManagementUrl(targetState.managementUrl)
      setInferenceUrl(targetState.inferenceUrl)
      setApiKey(targetState.apiKey)
      setMaskedKey(targetState.maskedKey)
      setManagementKey(targetState.managementKey)
      setMaskedManagementKey(targetState.maskedManagementKey)
      setOverrides(targetState.overrides)
      return updated
    })
  }

  const save = async (clearKey = false, silent = false) => {
    if (!silent) setBusy('save')
    try {
      const currentProfilePayload = {
        gateway_type: gatewayType,
        management_url: managementUrl.trim(),
        inference_url: inferenceUrl.trim(),
        target_overrides: Object.fromEntries(
          Object.entries(overrides)
            .filter(([, value]) => value.trim())
            .map(([key, value]) => [key, value.trim()]),
        ),
        ...(clearKey ? { clear_key: true, api_key: '' } : apiKey.trim() ? { api_key: apiKey.trim() } : {}),
        ...(gatewayType === 'magpie' && managementKey.trim() ? { management_key: managementKey.trim() } : {}),
      }

      const allProfilesPayload: Record<string, any> = {}
      for (const [t, p] of Object.entries(profiles)) {
        if (t === gatewayType) {
          allProfilesPayload[t] = currentProfilePayload
        } else {
          allProfilesPayload[t] = {
            gateway_type: t,
            management_url: p.managementUrl.trim(),
            inference_url: p.inferenceUrl.trim(),
            target_overrides: p.overrides,
            ...(p.apiKey.trim() ? { api_key: p.apiKey.trim() } : {}),
            ...(t === 'magpie' && p.managementKey.trim() ? { management_key: p.managementKey.trim() } : {}),
          }
        }
      }

      const result = await api.updateAnalyticsConfig({
        ...currentProfilePayload,
        gateway_profiles: allProfilesPayload,
      })
      setMaskedKey(result.masked_key)
      setMaskedManagementKey(result.masked_management_key || '')
      setApiKey('')
      setManagementKey('')
      if (result.gateway_profiles) {
        setProfiles((prev) => {
          const next = { ...prev }
          for (const [gt, p] of Object.entries(result.gateway_profiles || {})) {
            if (next[gt as 'opencodex' | 'magpie']) {
              next[gt as 'opencodex' | 'magpie'] = {
                ...next[gt as 'opencodex' | 'magpie'],
                managementUrl: p.management_url,
                inferenceUrl: p.inference_url,
                maskedKey: p.masked_key,
                maskedManagementKey: p.masked_management_key || '',
                managementKey: '',
                overrides: p.target_overrides || {},
              }
            }
          }
          return next
        })
      }
      window.dispatchEvent(new Event('astrorder:gateway-config-changed'))
      if (!silent) {
        notifications.show({ color: 'teal', message: 'LLM 网关配置已保存', icon: <IconCheck size={16} /> })
      }
      return result
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '保存 LLM 网关配置失败' })
      throw error
    } finally {
      if (!silent) setBusy(null)
    }
  }

  const showPreview = async () => {
    setBusy('preview')
    try {
      await save(false, true)
      setPreview(await api.previewModelSync(selected))
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '生成同步预览失败' })
    } finally {
      setBusy(null)
    }
  }

  const startSync = async () => {
    try {
      const operation = await api.startModelSync(selected)
      const taskId = addTask({
        title: '同步 Agent 模型配置',
        detail: '正在读取目录并同步目标',
        cancel: async () => {
          await api.cancelModelSync(operation.id)
        },
      })
      setPreview(null)
      const timer = window.setInterval(async () => {
        try {
          const job = (await api.getModelSyncJobs()).items.find((item) => item.id === operation.id)
          if (!job || job.status === 'running') return
          window.clearInterval(timer)
          if (job.status === 'success') completeTask(taskId, `已完成 ${(job.targets || []).length} 个目标`)
          else failTask(taskId, job.error || (job.status === 'cancelled' ? '同步已取消' : '模型同步失败'))
          void load()
        } catch {
          /* persisted job remains authoritative */
        }
      }, 1200)
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '启动模型同步失败' })
    }
  }

  const selectAll = () => {
    const all: Selection = {}
    targets.forEach((t) => {
      all[t.id] = [...t.agents]
    })
    setSelection(all)
  }

  const syncAll = async () => {
    selectAll()
    const allPayload = targets
      .filter((t) => t.agents.length > 0)
      .map((t) => ({ target_id: t.id, agents: t.agents }))
    if (!allPayload.length) return
    try {
      await save(false, true)
      const operation = await api.startModelSync(allPayload)
      const taskId = addTask({
        title: '全量同步 Agent 模型配置',
        detail: '正在同步全部 ' + targets.length + ' 个目标',
        cancel: async () => {
          await api.cancelModelSync(operation.id)
        },
      })
      const timer = window.setInterval(async () => {
        try {
          const job = (await api.getModelSyncJobs()).items.find((item) => item.id === operation.id)
          if (!job || job.status === 'running') return
          window.clearInterval(timer)
          if (job.status === 'success') completeTask(taskId, '已成功下发至 ' + (job.targets || []).length + ' 个目标')
          else failTask(taskId, job.error || '全量同步失败')
          void load()
        } catch {
          /* authoritative */
        }
      }, 1200)
    } catch (error: any) {
      notifications.show({ color: 'red', message: error.message || '启动全量同步失败' })
    }
  }

  const toggleAgent = (targetId: string, agent: string) =>
    setSelection((current) => ({
      ...current,
      [targetId]: current[targetId]?.includes(agent)
        ? current[targetId].filter((item) => item !== agent)
        : [...(current[targetId] || []), agent],
    }))

  const latestJob = jobs[0] || null

  return (
    <>
      <Card
        className="gateway-settings-card"
        withBorder
        radius="md"
        p="lg"
        style={{ background: 'var(--astr-surface)' }}
      >
        <Stack gap="lg">
          {/* Header */}
          <div className="gw-card-head">
            <div className="gw-card-head-left">
              <span className="gw-card-icon" aria-hidden="true">
                <IconCloudDataConnection size={17} stroke={1.7} />
              </span>
              <div>
                <Text fw={600} size="sm" lh={1.35}>
                  LLM 网关与模型同步
                </Text>
                <Text size="xs" c="dimmed" mt={2}>
                  用量实时读取，并将模型目录同步至本机及远端 Codex、Grok 与 Antigravity
                </Text>
              </div>
            </div>
            <span className="st-status-pill">
              <span className={`st-status-dot${maskedKey ? ' is-ok' : ''}`} />
              <span>
                {gatewayType === 'magpie' ? 'Magpie' : 'OpenCodeX'}
                {maskedKey ? ` · ${maskedKey}` : ' · 未填 Key'}
              </span>
            </span>
          </div>

          {/* Section 1: Balanced 2-Column Connection Form */}
          <div className="gw-form-grid">
            <Select
              label="网关类型"
              value={gatewayType}
              onChange={(v) => handleGatewayTypeChange((v as 'opencodex' | 'magpie') || 'opencodex')}
              data={[
                { value: 'opencodex', label: 'OpenCodeX' },
                { value: 'magpie', label: 'Magpie' },
              ]}
              allowDeselect={false}
            />
            <TextInput
              label="管理地址"
              type="url"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder={gatewayType === 'magpie' ? 'https://magpie.example.com' : 'https://ocx.example.com'}
              value={managementUrl}
              onChange={(event) => setManagementUrl(event.currentTarget.value)}
              disabled={busy === 'load'}
            />
            <TextInput
              label="推理 Base URL"
              type="url"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder="https://llm.example.com/v1"
              value={inferenceUrl}
              onChange={(event) => setInferenceUrl(event.currentTarget.value)}
              disabled={busy === 'load'}
            />
            <PasswordInput
              label="API Key"
              placeholder={maskedKey ? '留空则保留当前 Key' : '可选'}
              value={apiKey}
              onChange={(event) => setApiKey(event.currentTarget.value)}
            />
            {gatewayType === 'magpie' && (
              <PasswordInput
                label="管理端访问参数"
                placeholder={maskedManagementKey ? '留空则保留当前参数' : '管理页链接中的 k'}
                value={managementKey}
                onChange={(event) => setManagementKey(event.currentTarget.value)}
              />
            )}
          </div>

          {/* Section 2: Clean Hairline Sync Targets List */}
          <div className="gw-panel">
            <div className="gw-panel-head">
              <div className="gw-panel-title">
                <span>同步目标</span>
                <span className="st-count-pill">{targets.length} 个环境</span>
                {totalSelectedAgents > 0 && (
                  <span className="gw-selected-hint">已选 {totalSelectedAgents} 项</span>
                )}
              </div>
              <div className="gw-panel-actions">
                <button type="button" className="gw-mini-btn" onClick={selectAll}>
                  全选全部
                </button>
                <button
                  type="button"
                  className="gw-mini-btn is-muted"
                  disabled={totalSelectedAgents === 0}
                  onClick={() => setSelection({})}
                >
                  重置
                </button>
              </div>
            </div>

            {targets.length === 0 ? (
              <div className="gw-panel-empty">暂未检测到可同步的本机或远端环境</div>
            ) : (
              <div className="gw-target-rows">
                {targets.map((target) => {
                  const hasOverride = Boolean((overrides[target.id] || '').trim())
                  const overrideOpen = Boolean(expandedOverrides[target.id] || hasOverride)
                  return (
                    <div key={target.id} className="gw-target-row">
                      <div className="gw-target-row-main">
                        <div className="gw-target-identity">
                          <span className="gw-target-name">{target.name}</span>
                          <span className="gw-env-tag">{target.kind.toUpperCase()}</span>
                        </div>

                        <div className="gw-target-agents">
                          {target.agents.map((agent) => (
                            <Checkbox
                              key={agent}
                              size="xs"
                              label={agentDisplayLabel(agent)}
                              checked={selection[target.id]?.includes(agent) || false}
                              onChange={() => toggleAgent(target.id, agent)}
                            />
                          ))}
                        </div>

                        <button
                          type="button"
                          className={`gw-override-toggle${hasOverride ? ' is-active' : ''}`}
                          onClick={() =>
                            setExpandedOverrides((prev) => ({
                              ...prev,
                              [target.id]: !overrideOpen,
                            }))
                          }
                        >
                          <IconLink size={12} />
                          <span>{hasOverride ? '已覆盖推理地址' : '覆盖地址'}</span>
                        </button>
                      </div>

                      {selection[target.id]?.includes('antigravity') && (
                        <Text size="xs" c="dimmed" mt={4}>
                          Antigravity (agy) 将在新建会话或切换模型后自动采用新网关配置。
                        </Text>
                      )}

                      <Collapse expanded={overrideOpen}>
                        <div className="gw-override-input-wrap">
                          <TextInput
                            size="xs"
                            label="推理地址覆盖（仅对该环境生效，留空则沿用上方默认地址）"
                            placeholder={inferenceUrl || '使用默认推理地址'}
                            value={overrides[target.id] || ''}
                            onChange={(event) => {
                              const val = event.currentTarget.value
                              setOverrides((current) => ({ ...current, [target.id]: val }))
                            }}
                          />
                        </div>
                      </Collapse>
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          {/* Section 3: Compact Human-Readable Recent Sync Summary */}
          {latestJob && (
            <div className="gw-panel">
              <div className="gw-panel-head">
                <div className="gw-panel-title">
                  <span>最近同步</span>
                  <span
                    className={`gw-sync-state-pill ${
                      latestJob.status === 'success'
                        ? 'is-ok'
                        : latestJob.status === 'running'
                          ? 'is-running'
                          : 'is-err'
                    }`}
                  >
                    {latestJob.status === 'success'
                      ? '成功'
                      : latestJob.status === 'running'
                        ? '进行中'
                        : latestJob.status === 'cancelled'
                          ? '已取消'
                          : '失败'}
                  </span>
                  <span className="gw-selected-hint">
                    {(latestJob.targets || []).length} 个目标
                    {latestJob.error ? ` · ${latestJob.error}` : ''}
                  </span>
                </div>
              </div>

              {(latestJob.targets || []).length > 0 && (
                <div className="gw-history-rows">
                  {(latestJob.targets || []).map((tgt) => {
                    const targetName = targets.find((item) => item.id === tgt.target_id)?.name || tgt.target_id
                    const isSuccess = tgt.status === 'success'
                    const changedLabels = summarizeChangedItems(tgt.changed)
                    return (
                      <div key={tgt.target_id} className="gw-history-row">
                        <div className="gw-history-left">
                          <span className={`st-status-dot${isSuccess ? ' is-ok' : ''}`} aria-hidden="true" />
                          <span className="gw-history-name">{targetName}</span>
                          {changedLabels.length > 0 && (
                            <span className="gw-history-changes" title={changedLabels.join('、')}>
                              已更新：{changedLabels.join(' · ')}
                            </span>
                          )}
                        </div>
                        {tgt.error ? (
                          <Text size="xs" c="red" fw={500} title={tgt.error}>
                            {tgt.error}
                          </Text>
                        ) : (
                          <span className={`gw-history-badge${isSuccess ? ' is-ok' : ''}`}>
                            {isSuccess ? '已同步' : tgt.status}
                          </span>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {/* Section 4: Full-Width Aligned Footer Actions */}
          <div className="gateway-settings-actions gw-card-footer">
            <Button
              variant="subtle"
              color="red"
              size="xs"
              disabled={!maskedKey}
              onClick={() => void save(true)}
            >
              清除 Key
            </Button>
            <Group className="gateway-settings-actions-primary" gap="xs">
              <Button
                size="xs"
                variant="default"
                leftSection={<IconSend size={13} />}
                disabled={!targets.length}
                onClick={syncAll}
              >
                一键全量下发
              </Button>
              <Button
                size="xs"
                variant="default"
                loading={busy === 'preview'}
                disabled={!selected.length}
                leftSection={<IconRefresh size={13} />}
                onClick={showPreview}
              >
                预览选中
              </Button>
              <Button
                size="xs"
                disabled={!managementUrl.trim() || !inferenceUrl.trim()}
                loading={busy === 'save'}
                onClick={() => void save()}
              >
                保存配置
              </Button>
            </Group>
          </div>
        </Stack>
      </Card>

      <Modal opened={Boolean(preview)} onClose={() => setPreview(null)} title="模型同步预览" size="lg">
        {preview && (
          <Stack gap="md">
            <Group justify="space-between">
              <Group gap="xs">
                <Badge size="md" color="indigo" variant="light">
                  {preview.model_count} 个网关模型
                </Badge>
                <Text size="xs" c="dimmed">
                  指纹 {preview.catalog_fingerprint.slice(0, 12)}
                </Text>
              </Group>
              <Text size="xs" c="dimmed">
                共 {preview.targets.length} 个目标环境
              </Text>
            </Group>

            <Stack gap="sm">
              {preview.targets.map((target) => {
                const targetName = targets.find((item) => item.id === target.target_id)?.name || target.target_id
                const diff = {
                  added: target.model_diff?.added || [],
                  removed: target.model_diff?.removed || [],
                  kept_count: target.model_diff?.kept_count || 0,
                }
                const targetChanges = target.changes || []
                const hasModelChanges = diff.added.length > 0 || diff.removed.length > 0

                return (
                  <Card
                    key={target.target_id}
                    withBorder
                    radius="md"
                    p="sm"
                    style={{ background: 'var(--astr-surface-muted, #f8fafc)' }}
                  >
                    <Group justify="space-between" mb={6}>
                      <Group gap="xs">
                        <Text fw={650} size="sm">
                          {targetName}
                        </Text>
                        {target.hermes.dynamic && (
                          <Badge size="xs" variant="light">
                            Hermes {target.hermes.status}
                          </Badge>
                        )}
                        {target.reload_pending && (
                          <Badge size="xs" color="yellow">
                            活跃会话稍后重载
                          </Badge>
                        )}
                      </Group>
                      <Badge size="xs" color={hasModelChanges ? 'teal' : 'gray'} variant="light">
                        {hasModelChanges ? '模型有更新' : '模型无变化'}
                      </Badge>
                    </Group>

                    <Stack gap={6} mt={4}>
                      {diff.added.length > 0 && (
                        <Group gap={6} align="flex-start">
                          <Text size="xs" fw={600} c="teal" style={{ flexShrink: 0 }}>
                            新增模型 ({diff.added.length}):
                          </Text>
                          <Group gap={4} wrap="wrap">
                            {diff.added.map((m) => (
                              <Badge key={m} size="xs" color="teal" variant="light">
                                + {m}
                              </Badge>
                            ))}
                          </Group>
                        </Group>
                      )}

                      {diff.removed.length > 0 && (
                        <Group gap={6} align="flex-start">
                          <Text size="xs" fw={600} c="red" style={{ flexShrink: 0 }}>
                            移除模型 ({diff.removed.length}):
                          </Text>
                          <Group gap={4} wrap="wrap">
                            {diff.removed.map((m) => (
                              <Badge key={m} size="xs" color="red" variant="light">
                                - {m}
                              </Badge>
                            ))}
                          </Group>
                        </Group>
                      )}

                      {!hasModelChanges && (
                        <Text size="xs" c="dimmed">
                          {diff.kept_count
                            ? '全部 ' + diff.kept_count + ' 个模型与网关完全一致'
                            : '目标模型目录已是最新的'}
                        </Text>
                      )}

                      {targetChanges.some((c) => c.changed) && (
                        <Accordion variant="subtle" mt={4}>
                          <Accordion.Item value="diff">
                            <Accordion.Control p={0}>
                              <Text size="xs" c="dimmed">
                                查看底层配置文件 Diff 详情（{targetChanges.filter((c) => c.changed).length} 项文件变动）
                              </Text>
                            </Accordion.Control>
                            <Accordion.Panel p={0} pt={4}>
                              <Stack gap="xs">
                                {targetChanges
                                  .filter((c) => c.changed)
                                  .map((change) => (
                                    <div key={change.file}>
                                      <Text size="xs" fw={600}>
                                        {change.file}
                                      </Text>
                                      {change.diff && (
                                        <Code
                                          block
                                          style={{
                                            maxHeight: 180,
                                            overflow: 'auto',
                                            whiteSpace: 'pre',
                                            fontSize: 11,
                                          }}
                                        >
                                          {change.diff}
                                        </Code>
                                      )}
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
              <Button variant="default" onClick={() => setPreview(null)}>
                取消
              </Button>
              <Button color="indigo" onClick={startSync}>
                开始同步
              </Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </>
  )
}
