import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Autocomplete,
  Button,
  Group,
  PasswordInput,
  SegmentedControl,
  Select,
  Stack,
  Switch,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconCpu,
  IconEdit,
  IconLoader2,
  IconPlus,
  IconRefresh,
  IconServer,
  IconSparkles,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import {
  api,
  type AiProvider,
  type AiProviderProtocol,
  type AiProviderVendor,
  type AiProvidersConfig,
} from '../../api/client'
import {
  AI_PROVIDERS_SYNC_KEY,
  broadcastAiProvidersSync,
} from '../meetings/meetingSessionStore'
import { useSettingsMode } from '../settings/settingsMode'

interface ProviderPreset {
  id: string
  label: string
  name: string
  protocol: AiProviderProtocol
  vendor: AiProviderVendor
  baseUrl: string
  managementUrl?: string
  defaultModels?: string[]
}

const PROVIDER_PRESETS: ProviderPreset[] = [
  {
    id: 'openai',
    label: 'OpenAI',
    name: 'OpenAI',
    protocol: 'openai',
    vendor: '',
    baseUrl: 'https://api.openai.com/v1',
    defaultModels: ['gpt-4.1-mini', 'gpt-4o-mini'],
  },
  {
    id: 'gemini',
    label: 'Google Gemini',
    name: 'Google Gemini',
    protocol: 'gemini',
    vendor: '',
    baseUrl: 'https://generativelanguage.googleapis.com/v1beta',
    defaultModels: ['gemini-2.5-flash', 'gemini-2.5-pro'],
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    name: 'DeepSeek',
    protocol: 'openai',
    vendor: '',
    baseUrl: 'https://api.deepseek.com/v1',
    defaultModels: ['deepseek-chat', 'deepseek-reasoner'],
  },
  {
    id: 'qwen',
    label: '通义千问',
    name: '通义千问',
    protocol: 'openai',
    vendor: '',
    baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    defaultModels: ['qwen-plus', 'qwen-turbo'],
  },
  {
    id: 'openrouter',
    label: 'OpenRouter',
    name: 'OpenRouter',
    protocol: 'openai',
    vendor: '',
    baseUrl: 'https://openrouter.ai/api/v1',
  },
  {
    id: 'magpie',
    label: 'Magpie 网关',
    name: 'Magpie 网关',
    protocol: 'openai',
    vendor: 'magpie',
    baseUrl: '',
    managementUrl: '',
  },
  {
    id: 'opencodex',
    label: 'OpenCodeX 网关',
    name: 'OpenCodeX 网关',
    protocol: 'openai',
    vendor: 'opencodex',
    baseUrl: '',
    managementUrl: '',
  },
  {
    id: 'custom',
    label: '自定义兼容接口',
    name: '',
    protocol: 'openai',
    vendor: '',
    baseUrl: '',
  },
]

interface EditorState {
  id?: string
  name: string
  protocol: AiProviderProtocol
  vendor: AiProviderVendor
  baseUrl: string
  apiKey: string
  apiKeySet: boolean
  maskedKey: string
  managementUrl: string
  managementKey: string
  managementKeySet: boolean
  maskedManagementKey: string
  models: string[]
  enabled: boolean
  testModel: string
}

function emptyEditor(): EditorState {
  return {
    name: '',
    protocol: 'openai',
    vendor: '',
    baseUrl: '',
    apiKey: '',
    apiKeySet: false,
    maskedKey: '',
    managementUrl: '',
    managementKey: '',
    managementKeySet: false,
    maskedManagementKey: '',
    models: [],
    enabled: true,
    testModel: '',
  }
}

function editorFromProvider(p: AiProvider): EditorState {
  const vendor: AiProviderVendor =
    p.vendor === 'magpie' || p.vendor === 'opencodex' ? p.vendor : ''
  return {
    id: p.id,
    name: p.name,
    protocol: p.protocol === 'gemini' ? 'gemini' : 'openai',
    vendor,
    baseUrl: p.base_url,
    apiKey: '',
    apiKeySet: p.api_key_set,
    maskedKey: p.masked_key || '',
    managementUrl: p.management_url || '',
    managementKey: '',
    managementKeySet: Boolean(p.management_key_set),
    maskedManagementKey: p.masked_management_key || '',
    models: Array.isArray(p.models) ? [...p.models] : [],
    enabled: p.enabled,
    testModel: p.models?.[0] || '',
  }
}

function displayHost(rawUrl: string): string {
  try {
    return new URL(rawUrl).host
  } catch {
    return rawUrl || '未填写地址'
  }
}

function protocolBadgeLabel(protocol: string): string {
  return protocol === 'gemini' ? 'Gemini 兼容' : 'OpenAI 兼容'
}

function vendorBadgeLabel(vendor?: string): string {
  if (vendor === 'magpie') return 'Magpie'
  if (vendor === 'opencodex') return 'OpenCodeX'
  return ''
}

export function AiProvidersSettingsSection() {
  const { mode: settingsMode, managedByWorkbench } = useSettingsMode()
  const isAdvancedSettings = !managedByWorkbench || settingsMode === 'advanced'
  const [config, setConfig] = useState<AiProvidersConfig>({
    providers: [],
    default_provider_id: '',
    default_model: '',
    default_reasoning: 'low',
    effective_configured: false,
  })
  const [loading, setLoading] = useState(true)

  // Default universal model bar state
  const [defaultProviderId, setDefaultProviderId] = useState('')
  const [defaultModel, setDefaultModel] = useState('')
  const [defaultReasoning, setDefaultReasoning] = useState('low')
  const [savingDefault, setSavingDefault] = useState(false)
  const [testingDefault, setTestingDefault] = useState(false)

  // Provider editor state (inline create / edit)
  const [editorMode, setEditorMode] = useState<'closed' | 'create' | 'edit'>('closed')
  const [editor, setEditor] = useState<EditorState>(emptyEditor)
  const [manualModelInput, setManualModelInput] = useState('')
  const [modelFilter, setModelFilter] = useState('')
  const [fetchingEditorModels, setFetchingEditorModels] = useState(false)
  const [testingEditor, setTestingEditor] = useState(false)
  const [editorTestFeedback, setEditorTestFeedback] = useState<{ ok: boolean; text: string } | null>(null)
  const [savingProvider, setSavingProvider] = useState(false)

  // Per-row actions
  const [fetchingRowId, setFetchingRowId] = useState<string | null>(null)
  const [togglingRowId, setTogglingRowId] = useState<string | null>(null)
  const [deletingRowId, setDeletingRowId] = useState<string | null>(null)

  const applyConfig = useCallback((next: AiProvidersConfig) => {
    setConfig(next)
    setDefaultProviderId(next.default_provider_id || '')
    setDefaultModel(next.default_model || '')
    setDefaultReasoning(next.default_reasoning || 'low')
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await api.getAiProviders()
      applyConfig(res)
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '读取 AI 供应商配置失败',
      })
    } finally {
      setLoading(false)
    }
  }, [applyConfig])

  useEffect(() => {
    void load()
    const onGatewayChanged = (event: Event) => {
      const detail = (event as CustomEvent<{ source?: string }>).detail
      if (detail?.source === 'ai-providers') return
      void load()
    }
    const onStorage = (event: StorageEvent) => {
      if (event.key === AI_PROVIDERS_SYNC_KEY) {
        void load()
      }
    }
    window.addEventListener('astrorder:gateway-config-changed', onGatewayChanged)
    window.addEventListener('storage', onStorage)
    return () => {
      window.removeEventListener('astrorder:gateway-config-changed', onGatewayChanged)
      window.removeEventListener('storage', onStorage)
    }
  }, [load])

  const enabledProviders = useMemo(
    () => config.providers.filter((p) => p.enabled),
    [config.providers],
  )

  const selectedDefaultProvider = useMemo(
    () =>
      config.providers.find((p) => p.id === defaultProviderId) ||
      enabledProviders[0] ||
      null,
    [config.providers, defaultProviderId, enabledProviders],
  )

  const defaultModelOptions = useMemo(() => {
    const models = selectedDefaultProvider?.models || []
    return Array.from(new Set(models.filter(Boolean)))
  }, [selectedDefaultProvider])

  const openCreateEditor = () => {
    setEditor(emptyEditor())
    setManualModelInput('')
    setModelFilter('')
    setEditorTestFeedback(null)
    setEditorMode('create')
  }

  const openEditEditor = (provider: AiProvider) => {
    setEditor(editorFromProvider(provider))
    setManualModelInput('')
    setModelFilter('')
    setEditorTestFeedback(null)
    setEditorMode('edit')
  }

  const closeEditor = () => {
    setEditorMode('closed')
    setEditorTestFeedback(null)
  }

  const applyPreset = (preset: ProviderPreset) => {
    setEditor((prev) => ({
      ...prev,
      name: preset.name,
      protocol: preset.protocol,
      vendor: preset.vendor,
      baseUrl: preset.baseUrl,
      managementUrl: preset.managementUrl ?? '',
      models: preset.defaultModels ? [...preset.defaultModels] : prev.models,
      testModel: preset.defaultModels?.[0] || prev.testModel,
    }))
    setEditorTestFeedback(null)
  }

  const handleSaveDefault = async () => {
    if (!defaultProviderId || !defaultModel.trim()) return
    setSavingDefault(true)
    try {
      const updated = await api.setDefaultAiProvider({
        provider_id: defaultProviderId,
        model: defaultModel.trim(),
        reasoning: defaultReasoning,
      })
      applyConfig(updated)
      broadcastAiProvidersSync()
      notifications.show({
        color: 'teal',
        message: '默认通用模型已保存',
        icon: <IconCheck size={16} />,
      })
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '保存默认模型失败',
      })
    } finally {
      setSavingDefault(false)
    }
  }

  const handleTestDefault = async () => {
    if (!defaultProviderId || !defaultModel.trim()) return
    setTestingDefault(true)
    try {
      const res = await api.testAiProvider({
        provider_id: defaultProviderId,
        model: defaultModel.trim(),
        reasoning: defaultReasoning,
      })
      notifications.show({
        color: 'teal',
        title: '默认模型连接正常',
        message: `${res.model || defaultModel}${res.latency_ms ? `（${res.latency_ms}ms）` : ''}`,
      })
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        title: '连接测试失败',
        message: error instanceof Error ? error.message : '调用默认模型失败',
      })
    } finally {
      setTestingDefault(false)
    }
  }

  const handleFetchModelsForRow = async (provider: AiProvider) => {
    setFetchingRowId(provider.id)
    try {
      const res = await api.fetchAiProviderModels({ provider_id: provider.id })
      setConfig((prev) => ({
        ...prev,
        providers: prev.providers.map((p) =>
          p.id === provider.id ? { ...p, models: res.models } : p,
        ),
      }))
      if (editorMode === 'edit' && editor.id === provider.id) {
        setEditor((prev) => ({
          ...prev,
          models: res.models,
          testModel: prev.testModel || res.models[0] || '',
        }))
      }
      broadcastAiProvidersSync()
      notifications.show({
        color: 'teal',
        message: `已从「${provider.name}」拉取 ${res.count} 个模型`,
        icon: <IconCheck size={16} />,
      })
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        title: '拉取模型失败',
        message: error instanceof Error ? error.message : '无法获取模型目录',
      })
    } finally {
      setFetchingRowId(null)
    }
  }

  const handleToggleRowEnabled = async (provider: AiProvider, enabled: boolean) => {
    setTogglingRowId(provider.id)
    try {
      const res = await api.saveAiProvider({
        id: provider.id,
        name: provider.name,
        protocol: provider.protocol,
        vendor: provider.vendor || '',
        base_url: provider.base_url,
        management_url: provider.management_url || '',
        models: provider.models,
        enabled,
      })
      applyConfig(res.config)
      broadcastAiProvidersSync()
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '更新供应商状态失败',
      })
    } finally {
      setTogglingRowId(null)
    }
  }

  const handleDeleteRow = async (provider: AiProvider) => {
    setDeletingRowId(provider.id)
    try {
      const updated = await api.deleteAiProvider(provider.id)
      applyConfig(updated)
      if (editorMode === 'edit' && editor.id === provider.id) {
        closeEditor()
      }
      broadcastAiProvidersSync()
      notifications.show({ color: 'teal', message: `已删除供应商「${provider.name}」` })
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '删除供应商失败',
      })
    } finally {
      setDeletingRowId(null)
    }
  }

  const handleFetchModelsInEditor = async () => {
    if (!editor.baseUrl.trim()) {
      notifications.show({ color: 'red', message: '请先填写接口地址 (Base URL)' })
      return
    }
    setFetchingEditorModels(true)
    try {
      const res = await api.fetchAiProviderModels({
        provider_id: editor.id,
        protocol: editor.protocol,
        base_url: editor.baseUrl.trim(),
        api_key: editor.apiKey.trim() || undefined,
      })
      setEditor((prev) => ({
        ...prev,
        models: res.models,
        testModel: prev.testModel && res.models.includes(prev.testModel)
          ? prev.testModel
          : res.models[0] || prev.testModel,
      }))
      if (editor.id) {
        setConfig((prev) => ({
          ...prev,
          providers: prev.providers.map((p) =>
            p.id === editor.id ? { ...p, models: res.models } : p,
          ),
        }))
        broadcastAiProvidersSync()
      }
      notifications.show({
        color: 'teal',
        message: `成功拉取 ${res.count} 个可用模型`,
        icon: <IconCheck size={16} />,
      })
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        title: '拉取模型失败',
        message: error instanceof Error ? error.message : '请检查接口地址与 API Key',
      })
    } finally {
      setFetchingEditorModels(false)
    }
  }

  const handleAddManualModel = () => {
    const id = manualModelInput.trim()
    if (!id) return
    setEditor((prev) => {
      if (prev.models.includes(id)) return prev
      return {
        ...prev,
        models: [...prev.models, id],
        testModel: prev.testModel || id,
      }
    })
    setManualModelInput('')
  }

  const handleRemoveEditorModel = (id: string) => {
    setEditor((prev) => {
      const next = prev.models.filter((m) => m !== id)
      return {
        ...prev,
        models: next,
        testModel: prev.testModel === id ? next[0] || '' : prev.testModel,
      }
    })
  }

  const handleTestInEditor = async () => {
    const modelToTest = editor.testModel.trim() || editor.models[0] || ''
    if (!editor.baseUrl.trim()) {
      setEditorTestFeedback({ ok: false, text: '请先填写接口地址' })
      return
    }
    if (!modelToTest) {
      setEditorTestFeedback({ ok: false, text: '请先拉取或手动添加一个模型' })
      return
    }
    setTestingEditor(true)
    setEditorTestFeedback(null)
    try {
      const res = await api.testAiProvider({
        provider_id: editor.id,
        protocol: editor.protocol,
        base_url: editor.baseUrl.trim(),
        api_key: editor.apiKey.trim() || undefined,
        model: modelToTest,
        reasoning: defaultReasoning,
      })
      setEditorTestFeedback({
        ok: true,
        text: `连接正常 · ${res.model || modelToTest}${res.latency_ms ? ` (${res.latency_ms}ms)` : ''}`,
      })
    } catch (error: unknown) {
      setEditorTestFeedback({
        ok: false,
        text: error instanceof Error ? error.message : '测试失败',
      })
    } finally {
      setTestingEditor(false)
    }
  }

  const handleSaveProvider = async () => {
    if (!editor.baseUrl.trim()) {
      notifications.show({ color: 'red', message: '请填写接口地址 (Base URL)' })
      return
    }
    setSavingProvider(true)
    try {
      const isVendorGateway = editor.protocol === 'openai' && (editor.vendor === 'magpie' || editor.vendor === 'opencodex')
      const res = await api.saveAiProvider({
        id: editor.id,
        name: editor.name.trim(),
        protocol: editor.protocol,
        vendor: isVendorGateway ? editor.vendor : '',
        base_url: editor.baseUrl.trim(),
        ...(editor.apiKey.trim() ? { api_key: editor.apiKey.trim() } : {}),
        management_url: isVendorGateway ? editor.managementUrl.trim() : '',
        ...(isVendorGateway && editor.vendor === 'magpie' && editor.managementKey.trim()
          ? { management_key: editor.managementKey.trim() }
          : {}),
        models: editor.models,
        enabled: editor.enabled,
        set_as_default: config.providers.length === 0,
        default_model: editor.testModel || editor.models[0] || undefined,
      })
      applyConfig(res.config)
      broadcastAiProvidersSync()
      if (isVendorGateway) {
        window.dispatchEvent(new CustomEvent('astrorder:gateway-config-changed', { detail: { source: 'ai-providers' } }))
      }
      notifications.show({
        color: 'teal',
        message: editorMode === 'edit' ? `已更新「${res.provider.name}」` : `已添加「${res.provider.name}」`,
        icon: <IconCheck size={16} />,
      })
      closeEditor()
    } catch (error: unknown) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '保存 AI 供应商失败',
      })
    } finally {
      setSavingProvider(false)
    }
  }

  const filteredEditorModels = useMemo(() => {
    const q = modelFilter.trim().toLowerCase()
    if (!q) return editor.models
    return editor.models.filter((m) => m.toLowerCase().includes(q))
  }, [editor.models, modelFilter])

  const defaultProviderSelectData = useMemo(
    () =>
      enabledProviders.map((p) => {
        const suffix = p.vendor ? vendorBadgeLabel(p.vendor) : protocolBadgeLabel(p.protocol)
        return {
          value: p.id,
          label: `${p.name}（${suffix}）`,
        }
      }),
    [enabledProviders],
  )

  const canSaveDefault = Boolean(defaultProviderId && defaultModel.trim())
  const isGatewayVendor =
    editor.protocol === 'openai' && (editor.vendor === 'magpie' || editor.vendor === 'opencodex')

  return (
    <>
      {/* Section 1: Default Universal Model */}
      <section className="st-section-block" aria-label="默认通用模型">
        <div className="st-section-header">
          <div>
            <div className="st-section-title-row">
              <IconSparkles size={17} stroke={1.7} />
              <span>默认通用模型</span>
            </div>
            <div className="st-section-subtitle">
              用于工作复盘、周报生成、文档精修与自动标题；会议纪要也可直接复用或在会议页单独切换模型
            </div>
          </div>
          <span className="st-status-pill">
            <span className={`st-status-dot${config.effective_configured ? ' is-ok' : ''}`} />
            <span>
              {config.effective_configured
                ? `${config.effective_provider_name || '已配置'} · ${config.effective_model}`
                : '待配置'}
            </span>
          </span>
        </div>

        {enabledProviders.length === 0 ? (
          <div className="st-inline-notice" style={{ marginBottom: 0 }}>
            <span>{loading ? '正在读取 AI 供应商配置…' : '尚未配置可用的 AI 供应商，请先在下方添加。'}</span>
            {!loading && editorMode === 'closed' && (
              <button type="button" className="st-text-link" onClick={openCreateEditor}>
                <IconPlus size={14} />
                <span>立即添加</span>
              </button>
            )}
          </div>
        ) : (
          <Stack gap="sm">
            <Group align="flex-end" wrap="nowrap">
              <Select
                label="默认供应商"
                aria-label="默认 AI 供应商"
                style={{ flex: 1.25, minWidth: 0 }}
                value={defaultProviderId || null}
                data={defaultProviderSelectData}
                allowDeselect={false}
                onChange={(val) => {
                  const nextId = val || ''
                  setDefaultProviderId(nextId)
                  const nextProv = config.providers.find((p) => p.id === nextId)
                  if (nextProv && nextProv.models.length > 0 && !nextProv.models.includes(defaultModel)) {
                    setDefaultModel(nextProv.models[0])
                  }
                }}
              />
              <Autocomplete
                label="默认模型"
                aria-label="默认模型名称"
                style={{ flex: 1.15, minWidth: 0 }}
                placeholder={defaultModelOptions.length > 0 ? '选择或输入模型 ID' : '输入模型 ID 或先拉取模型'}
                value={defaultModel}
                data={defaultModelOptions}
                onChange={setDefaultModel}
                rightSection={
                  selectedDefaultProvider ? (
                    <button
                      type="button"
                      className="st-inline-icon-btn"
                      title="从当前供应商拉取最新模型列表"
                      aria-label="刷新默认供应商模型列表"
                      disabled={fetchingRowId === selectedDefaultProvider.id}
                      onClick={() => void handleFetchModelsForRow(selectedDefaultProvider)}
                    >
                      {fetchingRowId === selectedDefaultProvider.id ? (
                        <IconLoader2 size={13} className="astrorder-spin" />
                      ) : (
                        <IconRefresh size={13} />
                      )}
                    </button>
                  ) : undefined
                }
              />
              <Select
                label="思考程度"
                aria-label="默认模型思考程度"
                w={104}
                style={{ flexShrink: 0 }}
                value={defaultReasoning}
                allowDeselect={false}
                onChange={(val) => setDefaultReasoning(val || 'low')}
                data={[
                  { value: 'none', label: '关闭' },
                  { value: 'low', label: '低' },
                  { value: 'medium', label: '中' },
                  { value: 'high', label: '高' },
                ]}
              />
            </Group>
            <Group justify="flex-end" mt={2}>
              <Button
                variant="default"
                size="xs"
                disabled={!canSaveDefault}
                loading={testingDefault}
                onClick={() => void handleTestDefault()}
              >
                测试连接
              </Button>
              <Button
                size="xs"
                disabled={!canSaveDefault}
                loading={savingDefault}
                onClick={() => void handleSaveDefault()}
              >
                保存默认设置
              </Button>
            </Group>
          </Stack>
        )}
      </section>

      {/* Section 2: Configured AI Providers Directory */}
      <section className="st-section-block" aria-label="AI 供应商列表">
        <div className="st-section-header">
          <div>
            <div className="st-section-title-row">
              <IconServer size={17} stroke={1.7} />
              <span>AI 供应商</span>
              {config.providers.length > 0 && (
                <span className="st-count-pill">{config.providers.length}</span>
              )}
            </div>
            <div className="st-section-subtitle">
              可配置多个官方接口或中转网关（兼容 OpenAI / 兼容 Gemini），支持自动拉取模型并在各场景复用
            </div>
          </div>
          <Button
            size="xs"
            leftSection={<IconPlus size={14} />}
            onClick={openCreateEditor}
            disabled={editorMode === 'create'}
          >
            添加供应商
          </Button>
        </div>

        {/* Inline Create / Edit Provider Form */}
        {editorMode !== 'closed' && (
          <div className="st-provider-editor" role="region" aria-label={editorMode === 'edit' ? '编辑 AI 供应商' : '添加 AI 供应商'}>
            <div className="st-provider-editor-head">
              <span className="st-provider-editor-title">
                {editorMode === 'edit' ? `编辑供应商 · ${editor.name || '未命名'}` : '新增 AI 供应商'}
              </span>
              <button
                type="button"
                className="st-inline-icon-btn"
                aria-label="关闭编辑器"
                onClick={closeEditor}
              >
                <IconX size={15} />
              </button>
            </div>

            {editorMode === 'create' && (
              <div className="st-preset-row" aria-label="快速预设">
                <span className="st-preset-label">快速填充</span>
                <div className="st-preset-chips">
                  {PROVIDER_PRESETS.filter((preset) => isAdvancedSettings || !preset.vendor).map((preset) => (
                    <button
                      key={preset.id}
                      type="button"
                      className="st-preset-chip"
                      onClick={() => applyPreset(preset)}
                    >
                      {preset.label}
                    </button>
                  ))}
                </div>
              </div>
            )}

            <Stack gap="sm">
              <div className="st-editor-protocol-grid">
                <div>
                  <div className="st-field-label">接口协议</div>
                  <SegmentedControl
                    size="xs"
                    fullWidth
                    aria-label="供应商接口协议"
                    value={editor.protocol}
                    onChange={(val) => {
                      const proto: AiProviderProtocol = val === 'gemini' ? 'gemini' : 'openai'
                      setEditor((prev) => ({
                        ...prev,
                        protocol: proto,
                        vendor: proto === 'gemini' ? '' : prev.vendor,
                        baseUrl:
                          !prev.baseUrl.trim() && proto === 'gemini'
                            ? 'https://generativelanguage.googleapis.com/v1beta'
                            : prev.baseUrl,
                      }))
                    }}
                    data={[
                      { value: 'openai', label: '兼容 OpenAI' },
                      { value: 'gemini', label: '兼容 Gemini' },
                    ]}
                  />
                </div>

                {editor.protocol === 'openai' && (isAdvancedSettings || Boolean(editor.vendor)) && (
                  <div>
                    <div className="st-field-label">接口扩展</div>
                    <SegmentedControl
                      size="xs"
                      fullWidth
                      aria-label="OpenAI 网关扩展类型"
                      value={editor.vendor}
                      onChange={(val) => {
                        const vendor: AiProviderVendor =
                          val === 'magpie' || val === 'opencodex' ? val : ''
                        setEditor((prev) => ({
                          ...prev,
                          vendor,
                          name:
                            prev.name ||
                            (vendor === 'magpie'
                              ? 'Magpie 网关'
                              : vendor === 'opencodex'
                                ? 'OpenCodeX 网关'
                                : ''),
                        }))
                      }}
                      data={[
                        { value: '', label: '标准接口' },
                        { value: 'magpie', label: 'Magpie' },
                        { value: 'opencodex', label: 'OpenCodeX' },
                      ]}
                    />
                  </div>
                )}
              </div>

              {isGatewayVendor && (
                <div className="st-vendor-hint">
                  {editor.vendor === 'magpie'
                    ? 'Magpie 基于 OpenAI 兼容协议，补充管理地址与访问参数后可同时用于用量统计、配额查询与 Agent 模型同步。'
                    : 'OpenCodeX 基于 OpenAI 兼容协议，补充管理地址后可同时用于用量统计与 Agent 模型同步。'}
                </div>
              )}

              <Group grow align="flex-start">
                <TextInput
                  label="供应商名称"
                  aria-label="供应商名称"
                  placeholder={editor.protocol === 'gemini' ? '例如：Google Gemini' : '例如：OpenAI / DeepSeek / 公司网关'}
                  value={editor.name}
                  onChange={(e) => {
                    const val = e.currentTarget.value
                    setEditor((prev) => ({ ...prev, name: val }))
                  }}
                />
                <TextInput
                  label="接口地址 (Base URL)"
                  aria-label="供应商接口地址"
                  placeholder={
                    editor.protocol === 'gemini'
                      ? 'https://generativelanguage.googleapis.com/v1beta'
                      : 'https://api.openai.com/v1'
                  }
                  value={editor.baseUrl}
                  onChange={(e) => {
                    const val = e.currentTarget.value
                    setEditor((prev) => ({ ...prev, baseUrl: val }))
                  }}
                />
              </Group>

              <Group grow align="flex-start">
                <PasswordInput
                  label="API Key"
                  aria-label="供应商 API Key"
                  placeholder={
                    editor.apiKeySet
                      ? `${editor.maskedKey || '已保存'}（留空保持不变）`
                      : editor.protocol === 'gemini'
                        ? 'AIza...'
                        : 'sk-...'
                  }
                  value={editor.apiKey}
                  onChange={(e) => {
                    const val = e.currentTarget.value
                    setEditor((prev) => ({ ...prev, apiKey: val }))
                  }}
                />
                {isGatewayVendor && (
                  <TextInput
                    label="管理地址 (Management URL)"
                    aria-label="网关管理地址"
                    placeholder={editor.vendor === 'magpie' ? 'https://magpie.example.com' : 'https://ocx.example.com'}
                    value={editor.managementUrl}
                    onChange={(e) => {
                      const val = e.currentTarget.value
                      setEditor((prev) => ({ ...prev, managementUrl: val }))
                    }}
                  />
                )}
              </Group>

              {isGatewayVendor && editor.vendor === 'magpie' && (
                <PasswordInput
                  label="管理端访问参数 (k)"
                  aria-label="Magpie 管理端访问参数"
                  placeholder={
                    editor.managementKeySet
                      ? `${editor.maskedManagementKey || '已保存'}（留空保持不变）`
                      : 'Magpie 管理页链接中的 k 参数（可选）'
                  }
                  value={editor.managementKey}
                  onChange={(e) => {
                    const val = e.currentTarget.value
                    setEditor((prev) => ({ ...prev, managementKey: val }))
                  }}
                />
              )}

              {/* Models Directory Fetch & Manage */}
              <div className="st-models-panel">
                <div className="st-models-panel-head">
                  <div className="st-models-panel-title">
                    <IconCpu size={14} />
                    <span>可用模型目录 ({editor.models.length})</span>
                  </div>
                  <Group gap={6}>
                    <Button
                      size="compact-xs"
                      variant="default"
                      leftSection={<IconRefresh size={12} />}
                      loading={fetchingEditorModels}
                      disabled={!editor.baseUrl.trim()}
                      onClick={() => void handleFetchModelsInEditor()}
                    >
                      拉取模型
                    </Button>
                  </Group>
                </div>

                <div className="st-models-add-row">
                  <TextInput
                    size="xs"
                    style={{ flex: 1 }}
                    placeholder="手动补充模型 ID（按回车添加，适合不开放 /models 的接口）"
                    value={manualModelInput}
                    onChange={(e) => setManualModelInput(e.currentTarget.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        handleAddManualModel()
                      }
                    }}
                  />
                  <Button
                    size="xs"
                    variant="default"
                    disabled={!manualModelInput.trim()}
                    onClick={handleAddManualModel}
                  >
                    添加
                  </Button>
                </div>

                {editor.models.length > 10 && (
                  <TextInput
                    size="xs"
                    placeholder="筛选已拉取的模型…"
                    value={modelFilter}
                    onChange={(e) => setModelFilter(e.currentTarget.value)}
                  />
                )}

                {editor.models.length > 0 ? (
                  <div className="st-model-chips">
                    {filteredEditorModels.map((modelId) => {
                      const isSelectedForTest = editor.testModel === modelId
                      return (
                        <span
                          key={modelId}
                          className={`st-model-chip${isSelectedForTest ? ' is-selected' : ''}`}
                        >
                          <button
                            type="button"
                            className="st-model-chip-label"
                            title="点击选中用于连接测试"
                            onClick={() => setEditor((prev) => ({ ...prev, testModel: modelId }))}
                          >
                            {modelId}
                          </button>
                          <button
                            type="button"
                            className="st-model-chip-remove"
                            aria-label={`移除模型 ${modelId}`}
                            onClick={() => handleRemoveEditorModel(modelId)}
                          >
                            <IconX size={11} />
                          </button>
                        </span>
                      )
                    })}
                  </div>
                ) : (
                  <div className="st-models-empty">
                    点击右上角「拉取模型」自动获取该接口下的全部模型，或直接手动输入模型 ID。
                  </div>
                )}
              </div>

              <div className="st-provider-editor-footer">
                <div className="st-provider-editor-test">
                  <Button
                    size="xs"
                    variant="default"
                    loading={testingEditor}
                    disabled={!editor.baseUrl.trim() || (!editor.testModel && editor.models.length === 0)}
                    onClick={() => void handleTestInEditor()}
                  >
                    测试连接{editor.testModel ? ` · ${editor.testModel}` : ''}
                  </Button>
                  {editorTestFeedback && (
                    <span className={`st-test-feedback ${editorTestFeedback.ok ? 'is-ok' : 'is-err'}`}>
                      {editorTestFeedback.ok ? <IconCheck size={13} /> : <IconX size={13} />}
                      <span>{editorTestFeedback.text}</span>
                    </span>
                  )}
                </div>
                <Group gap="xs">
                  <Button size="xs" variant="default" onClick={closeEditor}>
                    取消
                  </Button>
                  <Button
                    size="xs"
                    loading={savingProvider}
                    disabled={!editor.baseUrl.trim()}
                    onClick={() => void handleSaveProvider()}
                  >
                    保存供应商
                  </Button>
                </Group>
              </div>
            </Stack>
          </div>
        )}

        {/* Saved Providers Flat Hairline List */}
        {config.providers.length === 0 ? (
          editorMode === 'closed' && (
            <div className="st-empty-providers">
              {isAdvancedSettings
                ? '还没有配置任何 AI 供应商。支持添加 OpenAI、Google Gemini、DeepSeek、OneAPI、Magpie、OpenCodeX 等多个供应商。'
                : '还没有配置任何 AI 供应商。支持添加 OpenAI、Google Gemini、DeepSeek、通义千问等主流接口。'}
            </div>
          )
        ) : (
          <div className="st-provider-list">
            {config.providers.map((provider) => {
              const isDefault = provider.id === config.default_provider_id
              const vendorLabel = vendorBadgeLabel(provider.vendor)
              const isEditing = editorMode === 'edit' && editor.id === provider.id
              return (
                <div
                  key={provider.id}
                  className={`st-provider-row${!provider.enabled ? ' is-disabled' : ''}${isEditing ? ' is-editing' : ''}`}
                >
                  <div className="st-provider-row-main">
                    <div className="st-provider-row-title-line">
                      <span
                        className={`st-status-dot${provider.enabled && provider.api_key_set ? ' is-ok' : ''}`}
                        aria-hidden="true"
                      />
                      <span className="st-provider-row-name">{provider.name}</span>
                      <span className="st-provider-tag">{protocolBadgeLabel(provider.protocol)}</span>
                      {vendorLabel && (
                        <span className="st-provider-tag is-vendor">{vendorLabel}</span>
                      )}
                      {isDefault && <span className="st-provider-tag is-default">默认</span>}
                    </div>
                    <div className="st-provider-row-meta">
                      <span>{displayHost(provider.base_url)}</span>
                      <span>·</span>
                      <span>
                        {provider.models.length > 0
                          ? `${provider.models.length} 个模型`
                          : '尚未拉取模型'}
                      </span>
                      {provider.masked_key && (
                        <>
                          <span>·</span>
                          <span>{provider.masked_key}</span>
                        </>
                      )}
                      {vendorLabel && provider.management_url && (
                        <>
                          <span>·</span>
                          <span>管理端已配置</span>
                        </>
                      )}
                    </div>
                  </div>

                  <div className="st-provider-row-actions">
                    <button
                      type="button"
                      className="st-row-action-btn"
                      disabled={fetchingRowId === provider.id}
                      onClick={() => void handleFetchModelsForRow(provider)}
                    >
                      {fetchingRowId === provider.id ? (
                        <IconLoader2 size={13} className="astrorder-spin" />
                      ) : (
                        <IconRefresh size={13} />
                      )}
                      <span>拉取模型</span>
                    </button>
                    <button
                      type="button"
                      className="st-row-action-btn"
                      onClick={() => openEditEditor(provider)}
                    >
                      <IconEdit size={13} />
                      <span>编辑</span>
                    </button>
                    <Switch
                      size="xs"
                      aria-label={`启用供应商 ${provider.name}`}
                      checked={provider.enabled}
                      disabled={togglingRowId === provider.id}
                      onChange={(e) => void handleToggleRowEnabled(provider, e.currentTarget.checked)}
                    />
                    <button
                      type="button"
                      className="st-inline-icon-btn is-danger"
                      title="删除供应商"
                      aria-label={`删除供应商 ${provider.name}`}
                      disabled={deletingRowId === provider.id}
                      onClick={() => void handleDeleteRow(provider)}
                    >
                      <IconTrash size={14} />
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </section>
    </>
  )
}
