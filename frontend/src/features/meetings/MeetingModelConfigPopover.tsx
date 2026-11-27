import { useCallback, useEffect, useMemo, useState } from 'react'
import { notifications } from '@mantine/notifications'
import { IconCheck, IconLoader2, IconRefresh, IconX } from '@tabler/icons-react'
import {
  api,
  type AiProvider,
  type LocalVoicePluginStatus,
  type MeetingSettings,
} from '../../api/client'
import { broadcastMeetingSettingsSync } from './meetingSessionStore'

export interface MeetingModelConfigPopoverProps {
  opened: boolean
  settings: MeetingSettings
  r2t2Status: LocalVoicePluginStatus | null
  r2t2Loading: boolean
  onClose: () => void
  onSaved: (next: MeetingSettings) => void
  onToggleR2t2: (enabled: boolean) => Promise<LocalVoicePluginStatus | undefined>
  onRestartR2t2: () => Promise<LocalVoicePluginStatus | undefined>
}

function providerOptionLabel(p: AiProvider): string {
  const tag =
    p.vendor === 'magpie'
      ? 'Magpie'
      : p.vendor === 'opencodex'
        ? 'OpenCodeX'
        : p.protocol === 'gemini'
          ? 'Gemini'
          : 'OpenAI'
  return `${p.name}（${tag}）`
}

export function MeetingModelConfigPopover({
  opened,
  settings,
  r2t2Status,
  r2t2Loading,
  onClose,
  onSaved,
  onToggleR2t2,
  onRestartR2t2,
}: MeetingModelConfigPopoverProps) {
  const [providers, setProviders] = useState<AiProvider[]>([])
  const [providersLoading, setProvidersLoading] = useState(false)
  const [fetchingModels, setFetchingModels] = useState(false)

  const [modelSource, setModelSource] = useState<'global' | 'provider'>(
    settings.model_source === 'provider' ? 'provider' : 'global',
  )
  const [providerId, setProviderId] = useState(settings.provider_id || '')
  const [model, setModel] = useState(settings.model || '')
  const [autoSummarize, setAutoSummarize] = useState(settings.auto_summarize)
  const [intervalSec, setIntervalSec] = useState(settings.auto_summarize_interval_sec || 30)
  const [summaryStyle, setSummaryStyle] = useState(settings.summary_style || 'concise')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null)

  const loadProviders = useCallback(async () => {
    setProvidersLoading(true)
    try {
      const cfg = await api.getAiProviders()
      const enabled = (cfg.providers || []).filter((p) => p.enabled)
      setProviders(enabled)
      setProviderId((prev) => {
        if (prev && enabled.some((p) => p.id === prev)) return prev
        if (settings.provider_id && enabled.some((p) => p.id === settings.provider_id)) {
          return settings.provider_id
        }
        return cfg.default_provider_id || enabled[0]?.id || ''
      })
    } catch {
      setProviders([])
    } finally {
      setProvidersLoading(false)
    }
  }, [settings.provider_id])

  useEffect(() => {
    if (!opened) return
    setModelSource(settings.model_source === 'provider' ? 'provider' : 'global')
    setProviderId(settings.provider_id || '')
    setModel(settings.model || '')
    setAutoSummarize(settings.auto_summarize)
    setIntervalSec(settings.auto_summarize_interval_sec || 30)
    setSummaryStyle(settings.summary_style || 'concise')
    setTestResult(null)
    void loadProviders()
  }, [opened, settings, loadProviders])

  const selectedProvider = useMemo(
    () => providers.find((p) => p.id === providerId) || providers[0] || null,
    [providers, providerId],
  )

  const availableModels = useMemo(() => {
    const list = selectedProvider?.models || []
    return Array.from(new Set(list.filter(Boolean)))
  }, [selectedProvider])

  if (!opened) return null

  const openProviderSettings = () => {
    onClose()
    window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'models' } }))
  }

  const handleRefreshModels = async () => {
    if (!selectedProvider) return
    setFetchingModels(true)
    try {
      const res = await api.fetchAiProviderModels({ provider_id: selectedProvider.id })
      setProviders((prev) =>
        prev.map((p) => (p.id === selectedProvider.id ? { ...p, models: res.models } : p)),
      )
      if (!model.trim() && res.models.length > 0) {
        setModel(res.models[0])
      }
      notifications.show({ color: 'teal', message: `已拉取 ${res.count} 个模型` })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '拉取模型失败',
      })
    } finally {
      setFetchingModels(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testMeetingSettings({
        model_source: modelSource,
        provider_id: modelSource === 'provider' ? selectedProvider?.id || providerId : undefined,
        model: modelSource === 'provider' ? model.trim() : undefined,
      })
      if (res.ok) {
        setTestResult({ ok: true, text: `正常 ${res.latency_ms}ms` })
      } else {
        setTestResult({ ok: false, text: res.error || '连接失败' })
      }
    } catch (cause) {
      setTestResult({ ok: false, text: cause instanceof Error ? cause.message : '测试失败' })
    } finally {
      setTesting(false)
    }
  }

  const handleSave = async () => {
    if (modelSource === 'provider' && (!selectedProvider || !model.trim())) {
      notifications.show({ color: 'red', message: '请先选择 AI 供应商与模型名称' })
      return
    }
    setSaving(true)
    try {
      const saved = await api.saveMeetingSettings({
        model_source: modelSource,
        provider_id: modelSource === 'provider' ? selectedProvider?.id || providerId : '',
        model: modelSource === 'provider' ? model.trim() : settings.model,
        auto_summarize: autoSummarize,
        auto_summarize_interval_sec: intervalSec,
        summary_style: summaryStyle,
      })
      onSaved(saved)
      broadcastMeetingSettingsSync()
      notifications.show({ color: 'teal', message: '会议总结配置已保存' })
      onClose()
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '保存配置失败',
      })
    } finally {
      setSaving(false)
    }
  }

  const r2t2Running = r2t2Status?.state === 'running'
  const r2t2Busy =
    r2t2Loading ||
    r2t2Status?.state === 'starting' ||
    r2t2Status?.state === 'installing' ||
    r2t2Status?.state === 'stopping'

  const r2t2Label =
    r2t2Status?.state === 'running'
      ? `本地语音转写引擎 · ${r2t2Status.device || '本地加速'}`
      : r2t2Status?.state === 'starting'
        ? '本地语音转写引擎正在加载模型…'
        : r2t2Status?.state === 'installing'
          ? `离线语音识别模型正在下载 (${Math.round((r2t2Status.progress || 0) * 100)}%)`
          : r2t2Status?.state === 'error'
            ? r2t2Status.error || '本地语音转写引擎异常'
            : '离线语音识别 · 未启用'

  return (
    <div className="mtg-popover-backdrop" onClick={onClose}>
      <div
        className="mtg-config-popover"
        role="dialog"
        aria-label="会议总结模型与语音转写配置"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mtg-cfg-section">
          <div className="mtg-cfg-head">
            <span className="mtg-cfg-title">总结模型</span>
            <button
              type="button"
              className="mtg-inline-link"
              onClick={openProviderSettings}
            >
              管理 AI 供应商
            </button>
          </div>

          <div className="mtg-seg-control" role="tablist" aria-label="模型来源">
            <button
              type="button"
              role="tab"
              aria-selected={modelSource === 'global'}
              className={`mtg-seg-btn${modelSource === 'global' ? ' is-active' : ''}`}
              onClick={() => {
                setModelSource('global')
                setTestResult(null)
              }}
            >
              默认通用模型
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={modelSource === 'provider'}
              className={`mtg-seg-btn${modelSource === 'provider' ? ' is-active' : ''}`}
              onClick={() => {
                setModelSource('provider')
                setTestResult(null)
                if (!model.trim() && selectedProvider?.models?.[0]) {
                  setModel(selectedProvider.models[0])
                }
              }}
            >
              指定模型
            </button>
          </div>

          {modelSource === 'global' ? (
            <div className="mtg-cfg-global-row">
              <span className={`mtg-dot ${settings.global_configured ? 'is-green' : 'is-muted'}`} />
              <span className="mtg-cfg-global-text">
                {settings.global_configured
                  ? `复用默认：${settings.global_provider_name ? `${settings.global_provider_name} / ` : ''}${settings.global_model || 'deepseek-chat'}`
                  : '尚未配置默认 AI 供应商'}
              </span>
              {settings.global_configured && (
                <button
                  type="button"
                  className="mtg-inline-test-btn"
                  disabled={testing}
                  onClick={() => void handleTest()}
                >
                  {testing ? <IconLoader2 size={12} className="astrorder-spin" /> : '测试'}
                </button>
              )}
            </div>
          ) : providers.length === 0 ? (
            <div className="mtg-cfg-global-row">
              <span className="mtg-dot is-muted" />
              <span className="mtg-cfg-global-text">
                {providersLoading ? '正在加载 AI 供应商…' : '暂无已启用的 AI 供应商'}
              </span>
              <button
                type="button"
                className="mtg-inline-link"
                onClick={openProviderSettings}
              >
                去添加
              </button>
            </div>
          ) : (
            <div className="mtg-cfg-fields">
              <label className="mtg-field-row">
                <span className="mtg-field-prefix">供应商</span>
                <select
                  aria-label="选择 AI 供应商"
                  value={selectedProvider?.id || ''}
                  onChange={(e) => {
                    const nextId = e.currentTarget.value
                    setProviderId(nextId)
                    setTestResult(null)
                    const found = providers.find((p) => p.id === nextId)
                    if (found && found.models.length > 0 && !found.models.includes(model)) {
                      setModel(found.models[0])
                    }
                  }}
                >
                  {providers.map((p) => (
                    <option key={p.id} value={p.id}>
                      {providerOptionLabel(p)}
                    </option>
                  ))}
                </select>
              </label>

              <div className="mtg-field-row">
                <span className="mtg-field-prefix">模型</span>
                <input
                  type="text"
                  list="mtg-provider-models-list"
                  value={model}
                  onChange={(e) => setModel(e.currentTarget.value)}
                  placeholder={availableModels.length > 0 ? '选择或输入模型 ID' : '输入模型或点击右侧拉取'}
                  aria-label="会议总结模型名称"
                />
                <datalist id="mtg-provider-models-list">
                  {availableModels.map((m) => (
                    <option key={m} value={m} />
                  ))}
                </datalist>
                <button
                  type="button"
                  className="mtg-inline-test-btn"
                  title="从该供应商拉取模型列表"
                  aria-label="拉取供应商模型"
                  disabled={fetchingModels || !selectedProvider}
                  onClick={() => void handleRefreshModels()}
                >
                  {fetchingModels ? (
                    <IconLoader2 size={12} className="astrorder-spin" />
                  ) : (
                    <IconRefresh size={12} />
                  )}
                </button>
                <button
                  type="button"
                  className="mtg-inline-test-btn"
                  disabled={testing || !model.trim()}
                  onClick={() => void handleTest()}
                >
                  {testing ? <IconLoader2 size={12} className="astrorder-spin" /> : '测试'}
                </button>
              </div>
            </div>
          )}

          {testResult && (
            <div className={`mtg-test-feedback ${testResult.ok ? 'is-ok' : 'is-err'}`}>
              {testResult.ok ? <IconCheck size={12} /> : <IconX size={12} />}
              <span>{testResult.text}</span>
            </div>
          )}
        </div>

        <div className="mtg-cfg-divider" />

        <div className="mtg-cfg-section">
          <div className="mtg-cfg-head">
            <span className="mtg-cfg-title">边转写边总结</span>
            <button
              type="button"
              role="switch"
              aria-checked={autoSummarize}
              aria-label="开启边转写边总结"
              className={`mtg-toggle${autoSummarize ? ' is-on' : ''}`}
              onClick={() => setAutoSummarize((v) => !v)}
            >
              <span className="mtg-toggle-thumb" />
            </button>
          </div>

          <div className="mtg-cfg-subrow">
            <span className="mtg-cfg-sublabel">增量提炼频率</span>
            <span className="mtg-cfg-meta">{autoSummarize ? `每 ${intervalSec} 秒` : '已关闭'}</span>
          </div>
          <div className="mtg-seg-control is-compact">
            {[20, 30, 60].map((sec) => (
              <button
                key={sec}
                type="button"
                className={`mtg-seg-btn${autoSummarize && intervalSec === sec ? ' is-active' : ''}`}
                onClick={() => {
                  setAutoSummarize(true)
                  setIntervalSec(sec)
                }}
              >
                {sec}s
              </button>
            ))}
            <button
              type="button"
              className={`mtg-seg-btn${!autoSummarize ? ' is-active' : ''}`}
              onClick={() => setAutoSummarize(false)}
            >
              手动
            </button>
          </div>

          <div className="mtg-cfg-subrow" style={{ marginTop: 8 }}>
            <span className="mtg-cfg-sublabel">纪要风格</span>
          </div>
          <div className="mtg-seg-control is-compact">
            {[
              { id: 'concise', label: '简洁汇报' },
              { id: 'detailed', label: '详尽纪要' },
              { id: 'technical', label: '技术评审' },
            ].map((st) => (
              <button
                key={st.id}
                type="button"
                className={`mtg-seg-btn${summaryStyle === st.id ? ' is-active' : ''}`}
                onClick={() => setSummaryStyle(st.id)}
              >
                {st.label}
              </button>
            ))}
          </div>
        </div>

        <div className="mtg-cfg-divider" />

        <div className="mtg-cfg-section">
          <div className="mtg-cfg-head">
            <span className="mtg-cfg-title-dot">
              <span
                className={`mtg-dot ${
                  r2t2Running ? 'is-green' : r2t2Status?.state === 'error' ? 'is-red' : 'is-muted'
                }`}
              />
              <span>本地语音转写引擎</span>
            </span>
            {r2t2Running && (
              <button
                type="button"
                className="mtg-inline-link"
                disabled={r2t2Busy}
                onClick={() => void onRestartR2t2()}
              >
                <IconRefresh size={11} />
                <span>重启</span>
              </button>
            )}
          </div>
          <div className="mtg-cfg-r2t2-row">
            <span className="mtg-cfg-r2t2-name">{r2t2Label}</span>
            <button
              type="button"
              role="switch"
              aria-checked={Boolean(r2t2Status?.enabled)}
              aria-label="启用本地语音转写引擎"
              disabled={r2t2Busy}
              className={`mtg-toggle${r2t2Status?.enabled ? ' is-on' : ''}`}
              onClick={() => void onToggleR2t2(!r2t2Status?.enabled)}
            >
              <span className="mtg-toggle-thumb" />
            </button>
          </div>
        </div>

        <div className="mtg-cfg-footer">
          <button type="button" className="mtg-ghost-btn" onClick={onClose}>
            取消
          </button>
          <button
            type="button"
            className="mtg-obsidian-btn"
            disabled={saving}
            onClick={() => void handleSave()}
          >
            {saving ? '保存中…' : '保存配置'}
          </button>
        </div>
      </div>
    </div>
  )
}
