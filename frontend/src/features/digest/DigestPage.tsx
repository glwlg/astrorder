import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Divider,
  Drawer,
  Group,
  MultiSelect,
  Popover,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArchive,
  IconChevronDown,
  IconChevronRight,
  IconClipboardText,
  IconHistory,
  IconInfoCircle,
  IconLoader2,
  IconMessageForward,
  IconNotebook,
  IconRefresh,
  IconSearch,
  IconSettings,
  IconSparkles,
  IconTrash,
  IconWand,
  IconX,
} from '@tabler/icons-react'
import {
  api,
  type AiProvider,
  type AiProvidersConfig,
  type DigestBundle,
  type DigestHistoryRecord,
  type DigestItem,
  type DigestTemplate,
} from '../../api/client'
import { MarkdownContent } from '../../components/MarkdownContent'
import { useAstrorderStore } from '../../state/store'
import { AI_PROVIDERS_SYNC_KEY } from '../meetings/meetingSessionStore'
import { useIsOfficeProfile } from '../office/appProfile'
import { ExportMenu } from '../office/export/ExportMenu'
import { DigestTemplateModal } from './DigestTemplateModal'
import './digest.css'

type DatePreset = 'today' | 'this_week' | 'last_week' | 'this_month' | 'custom'

const DIGEST_MODEL_PREF_KEY = 'astrorder:digest:model:v1'

interface DigestModelPref {
  mode: 'default' | 'custom'
  providerId: string
  model: string
}

function readDigestModelPref(): DigestModelPref {
  try {
    const raw = localStorage.getItem(DIGEST_MODEL_PREF_KEY)
    if (!raw) return { mode: 'default', providerId: '', model: '' }
    const parsed = JSON.parse(raw) as Partial<DigestModelPref>
    return {
      mode: parsed.mode === 'custom' ? 'custom' : 'default',
      providerId: typeof parsed.providerId === 'string' ? parsed.providerId : '',
      model: typeof parsed.model === 'string' ? parsed.model : '',
    }
  } catch {
    return { mode: 'default', providerId: '', model: '' }
  }
}

function writeDigestModelPref(pref: DigestModelPref) {
  try {
    localStorage.setItem(DIGEST_MODEL_PREF_KEY, JSON.stringify(pref))
  } catch {
    // ignore storage quota failures
  }
}

function toDateInputValue(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function computePresetRange(preset: DatePreset): { from: string; to: string } {
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  if (preset === 'today') {
    const s = toDateInputValue(today)
    return { from: s, to: s }
  }
  if (preset === 'this_week') {
    const dayOfWeek = today.getDay() || 7
    const monday = new Date(today)
    monday.setDate(today.getDate() - (dayOfWeek - 1))
    return { from: toDateInputValue(monday), to: toDateInputValue(today) }
  }
  if (preset === 'last_week') {
    const dayOfWeek = today.getDay() || 7
    const thisMonday = new Date(today)
    thisMonday.setDate(today.getDate() - (dayOfWeek - 1))
    const lastMonday = new Date(thisMonday)
    lastMonday.setDate(thisMonday.getDate() - 7)
    const lastSunday = new Date(thisMonday)
    lastSunday.setDate(thisMonday.getDate() - 1)
    return { from: toDateInputValue(lastMonday), to: toDateInputValue(lastSunday) }
  }
  const firstDay = new Date(today.getFullYear(), today.getMonth(), 1)
  return { from: toDateInputValue(firstDay), to: toDateInputValue(today) }
}

function formatItemTimestamp(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mi = String(d.getMinutes()).padStart(2, '0')
  return `${mm}-${dd} ${hh}:${mi}`
}

function kindMeta(kind: string): { label: string; color: string } {
  switch (kind) {
    case 'session':
      return { label: '对话', color: 'gray' }
    case 'office_doc':
      return { label: '文档', color: 'blue' }
    case 'meeting':
      return { label: '会议', color: 'cyan' }
    case 'git_commit':
      return { label: 'Git', color: 'teal' }
    case 'task':
      return { label: '任务', color: 'indigo' }
    case 'checkpoint':
      return { label: '代码', color: 'violet' }
    case 'sql_audit':
      return { label: 'SQL', color: 'orange' }
    default:
      return { label: '记录', color: 'gray' }
  }
}

const REFINE_PRESETS = [
  { label: '更精炼', instruction: '请压缩篇幅 35%，保留最关键成果与量化数据，去除冗余过程描述。' },
  { label: '偏管理视角', instruction: '请改写为面向管理层的汇报口吻：突出业务价值、交付里程碑、风险与资源诉求。' },
  { label: '对外脱敏', instruction: '请严格隐去所有内网 IP、本机路径、数据库名/表名、账号与内部环境标识，改写为适合发给外部客户的进展汇报。' },
  { label: '补齐总览表', instruction: '请在开篇补充一张按项目汇总的 Markdown 进度总览表（含项目、核心交付、当前状态）。' },
]

export function DigestPage() {
  const navigate = useNavigate()
  const isOfficeProfile = useIsOfficeProfile()
  const sessions = useAstrorderStore((s) => s.sessions)

  const initialRange = useMemo(() => computePresetRange('this_week'), [])
  const [preset, setPreset] = useState<DatePreset>('this_week')
  const [fromDate, setFromDate] = useState(initialRange.from)
  const [toDate, setToDate] = useState(initialRange.to)
  const [selectedProjects, setSelectedProjects] = useState<string[]>([])

  const [includeSessions, setIncludeSessions] = useState(true)
  const [includeOfficeDocs, setIncludeOfficeDocs] = useState(true)
  const [includeMeetings, setIncludeMeetings] = useState(true)
  const [includeGit, setIncludeGit] = useState(!isOfficeProfile)
  const [includeCheckpoints, setIncludeCheckpoints] = useState(!isOfficeProfile)
  const [includeSQLAudit, setIncludeSQLAudit] = useState(!isOfficeProfile)
  const [showDevSources, setShowDevSources] = useState(false)

  // AI provider/model selection synced with Settings -> AI 供应商
  const [aiConfig, setAiConfig] = useState<AiProvidersConfig | null>(null)
  const [modelPref, setModelPref] = useState<DigestModelPref>(() => readDigestModelPref())
  const [modelPopoverOpen, setModelPopoverOpen] = useState(false)
  const [fetchingModels, setFetchingModels] = useState(false)

  const [bundle, setBundle] = useState<DigestBundle | null>(null)
  const [itemSelection, setItemSelection] = useState<Record<string, boolean>>({})
  const [collapsedProjects, setCollapsedProjects] = useState<Record<string, boolean>>({})
  const [collecting, setCollecting] = useState(false)

  const [templates, setTemplates] = useState<DigestTemplate[]>([])
  const [templateId, setTemplateId] = useState(isOfficeProfile ? 'office_weekly' : 'weekly_exec')
  const userPickedTemplateRef = useRef(false)
  const [templateModalOpen, setTemplateModalOpen] = useState(false)
  const [manualNotes, setManualNotes] = useState('')

  const [reportTitle, setReportTitle] = useState('')
  const [reportMarkdown, setReportMarkdown] = useState('')
  const [reportModel, setReportModel] = useState('')
  const [reportNotice, setReportNotice] = useState('')
  const [activeRecordId, setActiveRecordId] = useState('')
  const [viewMode, setViewMode] = useState<'preview' | 'edit'>('preview')
  const [generating, setGenerating] = useState(false)
  const [refining, setRefining] = useState(false)
  const [customRefine, setCustomRefine] = useState('')
  const [savingHistory, setSavingHistory] = useState(false)

  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyQuery, setHistoryQuery] = useState('')
  const [historyItems, setHistoryItems] = useState<DigestHistoryRecord[]>([])

  const loadAiProviders = useCallback(async () => {
    if (typeof api.getAiProviders !== 'function') return
    try {
      const cfg = await api.getAiProviders()
      setAiConfig(cfg)
    } catch {
      // gracefully ignore when providers endpoint isn't mocked or configured
    }
  }, [])

  useEffect(() => {
    void loadAiProviders()
    const onStorage = (e: StorageEvent) => {
      if (e.key === AI_PROVIDERS_SYNC_KEY) {
        void loadAiProviders()
      }
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [loadAiProviders])

  const enabledProviders = useMemo<AiProvider[]>(
    () => (aiConfig?.providers || []).filter((p) => p.enabled !== false),
    [aiConfig],
  )

  const activeCustomProvider = useMemo(
    () => enabledProviders.find((p) => p.id === modelPref.providerId) || enabledProviders[0] || null,
    [enabledProviders, modelPref.providerId],
  )

  const updateModelPref = (patch: Partial<DigestModelPref>) => {
    setModelPref((prev) => {
      const next = { ...prev, ...patch }
      writeDigestModelPref(next)
      return next
    })
  }

  const effectiveRequestModel = useMemo(() => {
    if (modelPref.mode === 'custom' && activeCustomProvider) {
      return {
        provider_id: activeCustomProvider.id,
        model: modelPref.model.trim() || activeCustomProvider.models?.[0] || undefined,
      }
    }
    return { provider_id: undefined, model: undefined }
  }, [activeCustomProvider, modelPref.mode, modelPref.model])

  const llmConfigured = Boolean(
    (modelPref.mode === 'custom' && activeCustomProvider) ||
      aiConfig?.effective_configured ||
      bundle?.llm_configured,
  )

  const activeModelDisplay = useMemo(() => {
    if (modelPref.mode === 'custom' && activeCustomProvider) {
      const m = modelPref.model.trim() || activeCustomProvider.models?.[0] || '默认模型'
      return `${activeCustomProvider.name} · ${m}`
    }
    if (aiConfig?.effective_configured) {
      const pname = aiConfig.effective_provider_name
      const m = aiConfig.effective_model || bundle?.llm_model || '默认模型'
      return pname ? `${pname} · ${m}` : m
    }
    if (bundle?.llm_configured) {
      return bundle.llm_model || '默认模型'
    }
    return '未配置 AI 模型'
  }, [activeCustomProvider, aiConfig, bundle?.llm_configured, bundle?.llm_model, modelPref.mode, modelPref.model])

  const runCollect = useCallback(
    async (overrides?: { from?: string; to?: string; projectKeys?: string[] }) => {
      setCollecting(true)
      try {
        const resp = await api.collectDigest({
          from: overrides?.from ?? fromDate,
          to: overrides?.to ?? toDate,
          project_keys: overrides?.projectKeys ?? selectedProjects,
          include_sessions: includeSessions,
          include_office_docs: includeOfficeDocs,
          include_git: includeGit,
          include_tasks: false,
          include_checkpoints: includeCheckpoints,
          include_sql_audit: includeSQLAudit,
          include_meetings: includeMeetings,
          local_only: isOfficeProfile,
        })
        setBundle(resp)
        const nextSel: Record<string, boolean> = {}
        for (const proj of resp.projects) {
          for (const item of proj.items) {
            nextSel[item.item_id] = item.selected
          }
        }
        setItemSelection(nextSel)
      } catch (cause) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '采集工作素材失败',
        })
      } finally {
        setCollecting(false)
      }
    },
    [
      fromDate,
      toDate,
      selectedProjects,
      includeSessions,
      includeOfficeDocs,
      includeGit,
      includeCheckpoints,
      includeSQLAudit,
      includeMeetings,
      isOfficeProfile,
    ],
  )

  const handlePresetChange = (next: string) => {
    const p = next as DatePreset
    setPreset(p)
    if (p !== 'custom') {
      const range = computePresetRange(p)
      setFromDate(range.from)
      setToDate(range.to)
      void runCollect({ from: range.from, to: range.to })
    }
  }

  const loadTemplatesAndHistory = useCallback(async () => {
    try {
      const [tplRes, histRes] = await Promise.all([api.listDigestTemplates(), api.listDigestHistory()])
      const loaded = tplRes.items || []
      setTemplates(loaded)
      if (!userPickedTemplateRef.current && loaded.length > 0) {
        const preferred = isOfficeProfile ? 'office_weekly' : 'weekly_exec'
        if (loaded.some((t) => t.id === preferred)) {
          setTemplateId(preferred)
        } else if (!loaded.some((t) => t.id === templateId)) {
          setTemplateId(loaded[0].id)
        }
      }
      setHistoryItems(histRes.items || [])
    } catch {
      // ignore initial load errors gracefully
    }
  }, [isOfficeProfile, templateId])

  useEffect(() => {
    void loadTemplatesAndHistory()
    void runCollect({ from: initialRange.from, to: initialRange.to, projectKeys: [] })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const allItems = useMemo<DigestItem[]>(() => {
    if (!bundle) return []
    const list: DigestItem[] = []
    for (const proj of bundle.projects) {
      for (const item of proj.items) {
        list.push({ ...item, selected: itemSelection[item.item_id] ?? item.selected })
      }
    }
    return list
  }, [bundle, itemSelection])

  const selectedItems = useMemo(() => allItems.filter((it) => it.selected), [allItems])
  const selectedTemplate = useMemo(
    () => templates.find((t) => t.id === templateId) || templates[0],
    [templateId, templates],
  )

  const exportTargetWorkspace = useMemo(() => {
    if (!bundle) return undefined
    const selectedKeys = new Set(selectedItems.map((it) => it.project_key))
    const matched = bundle.projects.find((p) => p.workspace && (selectedKeys.size === 0 || selectedKeys.has(p.project_key)))
    return matched?.workspace || bundle.projects.find((p) => p.workspace)?.workspace || undefined
  }, [bundle, selectedItems])

  const toggleProjectItems = (projectKey: string, checked: boolean) => {
    if (!bundle) return
    const proj = bundle.projects.find((p) => p.project_key === projectKey)
    if (!proj) return
    setItemSelection((prev) => {
      const next = { ...prev }
      for (const it of proj.items) {
        next[it.item_id] = checked
      }
      return next
    })
  }

  const toggleAllItems = (checked: boolean) => {
    const next: Record<string, boolean> = {}
    for (const it of allItems) {
      next[it.item_id] = checked
    }
    setItemSelection(next)
  }

  const handleRefreshProviderModels = async () => {
    if (!activeCustomProvider) return
    setFetchingModels(true)
    try {
      const res = await api.fetchAiProviderModels({ provider_id: activeCustomProvider.id })
      setAiConfig((prev) =>
        prev
          ? {
              ...prev,
              providers: prev.providers.map((p) =>
                p.id === activeCustomProvider.id ? { ...p, models: res.models } : p,
              ),
            }
          : prev,
      )
      if (!modelPref.model.trim() && res.models.length > 0) {
        updateModelPref({ model: res.models[0] })
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

  const handleGenerate = async (mode: 'llm' | 'prompt_only' = 'llm') => {
    if (selectedItems.length === 0 && !manualNotes.trim()) {
      notifications.show({ color: 'yellow', message: '请至少勾选一条工作素材，或填写手动补充事项' })
      return
    }
    setGenerating(true)
    setReportNotice('')
    try {
      const resp = await api.generateDigest({
        from: fromDate,
        to: toDate,
        template_id: templateId,
        title: reportTitle.trim() || undefined,
        items: selectedItems,
        manual_notes: manualNotes.trim() || undefined,
        mode,
        ...(effectiveRequestModel.provider_id ? { provider_id: effectiveRequestModel.provider_id } : {}),
        ...(effectiveRequestModel.model ? { model: effectiveRequestModel.model } : {}),
      })
      setReportTitle(resp.title)
      setReportMarkdown(resp.markdown)
      setReportModel(resp.mode === 'llm' ? resp.model || '' : '')
      setActiveRecordId('')
      if (resp.message) {
        setReportNotice(resp.message)
      }
      if (mode === 'prompt_only') {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(resp.assembled_prompt)
          notifications.show({ color: 'teal', message: '已复制完整素材提示词，并生成离线草稿' })
        }
      } else if (resp.mode === 'llm') {
        notifications.show({ color: 'teal', message: `已使用 ${resp.model || 'AI 模型'} 生成报告` })
      }
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '生成报告失败',
      })
    } finally {
      setGenerating(false)
    }
  }

  const handleSendPromptToAgent = async () => {
    if (selectedItems.length === 0 && !manualNotes.trim()) {
      notifications.show({ color: 'yellow', message: '请先采集并勾选工作素材' })
      return
    }
    setGenerating(true)
    try {
      const resp = await api.generateDigest({
        from: fromDate,
        to: toDate,
        template_id: templateId,
        title: reportTitle.trim() || undefined,
        items: selectedItems,
        manual_notes: manualNotes.trim() || undefined,
        mode: 'prompt_only',
      })
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(resp.assembled_prompt).catch(() => {})
      }
      const targetSession = sessions[0]
      if (targetSession) {
        navigate(`/chat/${encodeURIComponent(targetSession.id)}?agent_id=${encodeURIComponent(targetSession.agent_id)}`)
        window.setTimeout(() => {
          window.dispatchEvent(
            new CustomEvent('astrorder:quote-to-composer', {
              detail: {
                agentId: targetSession.agent_id,
                sessionId: targetSession.id,
                text: resp.assembled_prompt,
              },
            }),
          )
        }, 220)
        notifications.show({ color: 'teal', message: '已将完整素材提示词填入对话输入框（同时已复制备用）' })
      } else {
        navigate('/chat')
        notifications.show({ color: 'teal', message: '已复制完整素材提示词，新建对话后直接粘贴即可生成' })
      }
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '组装提示词失败' })
    } finally {
      setGenerating(false)
    }
  }

  const handleRefine = async (instruction: string) => {
    const trimmed = instruction.trim()
    if (!trimmed || !reportMarkdown.trim()) return
    setRefining(true)
    try {
      const resp = await api.generateDigest({
        from: fromDate,
        to: toDate,
        template_id: templateId,
        title: reportTitle.trim() || undefined,
        items: selectedItems,
        manual_notes: manualNotes.trim() || undefined,
        refine_instruction: trimmed,
        existing_markdown: reportMarkdown,
        mode: 'llm',
        ...(effectiveRequestModel.provider_id ? { provider_id: effectiveRequestModel.provider_id } : {}),
        ...(effectiveRequestModel.model ? { model: effectiveRequestModel.model } : {}),
      })
      if (resp.mode === 'llm') {
        setReportMarkdown(resp.markdown)
        setReportModel(resp.model || reportModel)
        setCustomRefine('')
        notifications.show({ color: 'teal', message: '已完成报告润色' })
      } else {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(resp.assembled_prompt)
        }
        notifications.show({
          color: 'yellow',
          message: '未配置 AI 供应商模型，已将润色提示词复制到剪贴板',
        })
      }
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '润色失败',
      })
    } finally {
      setRefining(false)
    }
  }

  const handleSaveHistory = async () => {
    if (!reportMarkdown.trim()) {
      notifications.show({ color: 'yellow', message: '请先生成或编写报告内容' })
      return
    }
    setSavingHistory(true)
    try {
      const projectNames = Array.from(new Set(selectedItems.map((it) => it.project_name)))
      const saved = await api.saveDigestHistory({
        id: activeRecordId || undefined,
        title: reportTitle.trim() || `${selectedTemplate?.name || '工作周报'} (${fromDate} ~ ${toDate})`,
        template_id: templateId,
        template_name: selectedTemplate?.name || templateId,
        from: fromDate,
        to: toDate,
        markdown: reportMarkdown,
        model: reportModel,
        project_names: projectNames,
        item_count: selectedItems.length,
      })
      setActiveRecordId(saved.id)
      const listRes = await api.listDigestHistory()
      setHistoryItems(listRes.items || [])
      notifications.show({ color: 'teal', message: '已归档保存到历史记录' })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '保存归档失败',
      })
    } finally {
      setSavingHistory(false)
    }
  }

  const handleDeleteHistory = async (id: string) => {
    try {
      await api.deleteDigestHistory(id)
      setHistoryItems((prev) => prev.filter((item) => item.id !== id))
      if (activeRecordId === id) setActiveRecordId('')
      notifications.show({ color: 'teal', message: '已删除该历史记录' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '删除失败' })
    }
  }

  const handleLoadHistory = (rec: DigestHistoryRecord) => {
    setActiveRecordId(rec.id)
    setReportTitle(rec.title)
    setReportMarkdown(rec.markdown)
    setReportModel(rec.model || '')
    if (rec.template_id) {
      userPickedTemplateRef.current = true
      setTemplateId(rec.template_id)
    }
    setReportNotice('')
    setHistoryOpen(false)
  }

  const projectOptions = useMemo(
    () =>
      (bundle?.available_projects || []).map((p) => ({
        value: p.project_key,
        label: p.project_name,
      })),
    [bundle?.available_projects],
  )

  const filteredHistory = useMemo(() => {
    const q = historyQuery.trim().toLowerCase()
    if (!q) return historyItems
    return historyItems.filter(
      (rec) =>
        rec.title.toLowerCase().includes(q) ||
        (rec.template_name || '').toLowerCase().includes(q) ||
        rec.markdown.toLowerCase().includes(q),
    )
  }, [historyItems, historyQuery])

  const kpiCards = useMemo(() => {
    if (!bundle) return []
    if (isOfficeProfile) {
      return [
        { label: '工作空间', value: bundle.stats.project_count },
        { label: '会议纪要', value: bundle.stats.meeting_count ?? 0 },
        { label: '文档产出', value: bundle.stats.office_doc_count ?? 0 },
        { label: 'AI 对话', value: bundle.stats.session_count },
      ]
    }
    return [
      { label: '活跃项目', value: bundle.stats.project_count },
      { label: '会话结论', value: bundle.stats.session_count },
      { label: 'Git 提交', value: bundle.stats.commit_count },
      { label: '改动文件', value: bundle.stats.files_changed },
    ]
  }, [bundle, isOfficeProfile])

  return (
    <div className="route-page digest-page-shell">
      <header className="digest-page-header">
        <div className="digest-header-left">
          <IconNotebook size={18} className="digest-header-icon" />
          <span className="digest-header-title">
            {isOfficeProfile ? '工作周报与复盘' : '工作复盘与周报'}
          </span>
          {isOfficeProfile && (
            <span className="digest-header-sub">仅汇总本机工作空间、文档与会议</span>
          )}
        </div>

        <div className="digest-header-right">
          <Popover
            opened={modelPopoverOpen}
            onChange={setModelPopoverOpen}
            position="bottom-end"
            width={340}
            shadow="md"
            withinPortal
          >
            <Popover.Target>
              <button
                type="button"
                className={`digest-model-trigger${modelPopoverOpen ? ' is-open' : ''}`}
                aria-label="选择周报生成模型"
                onClick={() => setModelPopoverOpen((v) => !v)}
              >
                <span className={`digest-status-dot${llmConfigured ? ' is-ready' : ''}`} />
                <span className="digest-model-trigger-label">{activeModelDisplay}</span>
                <IconChevronDown size={13} />
              </button>
            </Popover.Target>
            <Popover.Dropdown>
              <Stack gap="sm">
                <Group justify="space-between">
                  <Text size="xs" fw={600}>周报生成模型</Text>
                  <button
                    type="button"
                    className="digest-link-btn"
                    onClick={() => {
                      setModelPopoverOpen(false)
                      window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'models' } }))
                    }}
                  >
                    <IconSettings size={12} />
                    <span>管理 AI 供应商</span>
                  </button>
                </Group>

                <SegmentedControl
                  size="xs"
                  fullWidth
                  value={modelPref.mode}
                  onChange={(val) => updateModelPref({ mode: val as 'default' | 'custom' })}
                  data={[
                    { value: 'default', label: '默认通用模型' },
                    { value: 'custom', label: '指定供应商' },
                  ]}
                />

                {modelPref.mode === 'default' ? (
                  <Text size="xs" c="dimmed">
                    {aiConfig?.effective_configured
                      ? `当前默认：${aiConfig.effective_provider_name ? `${aiConfig.effective_provider_name} / ` : ''}${aiConfig.effective_model || '默认模型'}`
                      : bundle?.llm_configured
                        ? `当前默认：${bundle.llm_model || '已配置'}`
                        : '尚未配置默认 AI 供应商，可前往设置添加。'}
                  </Text>
                ) : enabledProviders.length === 0 ? (
                  <Text size="xs" c="dimmed">暂无可用 AI 供应商，请先在设置中添加。</Text>
                ) : (
                  <Stack gap="xs">
                    <Select
                      size="xs"
                      label="AI 供应商"
                      aria-label="周报 AI 供应商"
                      value={activeCustomProvider?.id || ''}
                      onChange={(val) => {
                        if (!val) return
                        const found = enabledProviders.find((p) => p.id === val)
                        updateModelPref({
                          providerId: val,
                          model:
                            found && found.models.length > 0 && !found.models.includes(modelPref.model)
                              ? found.models[0]
                              : modelPref.model,
                        })
                      }}
                      data={enabledProviders.map((p) => ({
                        value: p.id,
                        label: `${p.name}（${p.protocol === 'gemini' ? 'Gemini' : 'OpenAI'}）`,
                      }))}
                    />
                    <Group gap={6} align="flex-end" wrap="nowrap">
                      <TextInput
                        size="xs"
                        style={{ flex: 1 }}
                        label="模型名称"
                        aria-label="周报模型名称"
                        list="digest-provider-models"
                        placeholder="选择或输入模型 ID"
                        value={modelPref.model}
                        onChange={(e) => updateModelPref({ model: e.currentTarget.value })}
                      />
                      <Tooltip label="从该供应商拉取模型列表">
                        <ActionIcon
                          size="sm"
                          variant="light"
                          color="gray"
                          mb={2}
                          aria-label="拉取供应商模型"
                          disabled={fetchingModels || !activeCustomProvider}
                          onClick={() => void handleRefreshProviderModels()}
                        >
                          {fetchingModels ? <IconLoader2 size={13} className="astrorder-spin" /> : <IconRefresh size={13} />}
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                    <datalist id="digest-provider-models">
                      {(activeCustomProvider?.models || []).map((m) => (
                        <option key={m} value={m} />
                      ))}
                    </datalist>
                  </Stack>
                )}
              </Stack>
            </Popover.Dropdown>
          </Popover>

          <button
            type="button"
            className="digest-ghost-btn"
            onClick={() => setHistoryOpen(true)}
          >
            <IconHistory size={14} />
            <span>历史归档 ({historyItems.length})</span>
          </button>
        </div>
      </header>

      <div className="digest-workspace-grid">
        {/* Left pane: Time range, source filters, selectable evidence tree, template & actions */}
        <section className="digest-left-pane" aria-label="素材采集与汇报配置">
          <div className="digest-section-block">
            <div className="digest-section-label">1. 时间跨度</div>
            <SegmentedControl
              size="xs"
              fullWidth
              value={preset}
              onChange={handlePresetChange}
              data={[
                { value: 'today', label: '今日' },
                { value: 'this_week', label: '本周' },
                { value: 'last_week', label: '上周' },
                { value: 'this_month', label: '本月' },
                { value: 'custom', label: '自定义' },
              ]}
            />
            {preset === 'custom' && (
              <Group grow gap="xs" mt={6}>
                <TextInput
                  type="date"
                  size="xs"
                  label="开始日期"
                  value={fromDate}
                  onChange={(e) => setFromDate(e.currentTarget.value)}
                />
                <TextInput
                  type="date"
                  size="xs"
                  label="结束日期"
                  value={toDate}
                  onChange={(e) => setToDate(e.currentTarget.value)}
                />
              </Group>
            )}
          </div>

          <div className="digest-section-block">
            <MultiSelect
              size="xs"
              label={isOfficeProfile ? '工作空间范围' : '项目范围'}
              placeholder={isOfficeProfile ? '全部本机工作空间' : '全部活跃项目'}
              data={projectOptions}
              value={selectedProjects}
              onChange={setSelectedProjects}
              searchable
              clearable
            />

            <Group gap="sm" wrap="wrap" mt={8}>
              <Checkbox size="xs" label="会议纪要" checked={includeMeetings} onChange={(e) => setIncludeMeetings(e.currentTarget.checked)} />
              <Checkbox size="xs" label="办公文档" checked={includeOfficeDocs} onChange={(e) => setIncludeOfficeDocs(e.currentTarget.checked)} />
              <Checkbox size="xs" label={isOfficeProfile ? '对话结论' : '会话结论'} checked={includeSessions} onChange={(e) => setIncludeSessions(e.currentTarget.checked)} />
              {!isOfficeProfile && (
                <>
                  <Checkbox size="xs" label="Git 提交" checked={includeGit} onChange={(e) => setIncludeGit(e.currentTarget.checked)} />
                  <Checkbox size="xs" label="代码统计" checked={includeCheckpoints} onChange={(e) => setIncludeCheckpoints(e.currentTarget.checked)} />
                  <Checkbox size="xs" label="SQL 变更" checked={includeSQLAudit} onChange={(e) => setIncludeSQLAudit(e.currentTarget.checked)} />
                </>
              )}
            </Group>

            {isOfficeProfile && (
              <div style={{ marginTop: 6 }}>
                <button
                  type="button"
                  className="digest-link-btn"
                  onClick={() => setShowDevSources((v) => !v)}
                >
                  {showDevSources ? <IconChevronDown size={12} /> : <IconChevronRight size={12} />}
                  <span>研发数据源（Git / 代码 / SQL）</span>
                </button>
                {showDevSources && (
                  <Group gap="sm" wrap="wrap" mt={6}>
                    <Checkbox size="xs" label="Git 提交" checked={includeGit} onChange={(e) => setIncludeGit(e.currentTarget.checked)} />
                    <Checkbox size="xs" label="代码统计" checked={includeCheckpoints} onChange={(e) => setIncludeCheckpoints(e.currentTarget.checked)} />
                    <Checkbox size="xs" label="SQL 变更" checked={includeSQLAudit} onChange={(e) => setIncludeSQLAudit(e.currentTarget.checked)} />
                  </Group>
                )}
              </div>
            )}

            <button
              type="button"
              className="digest-secondary-btn"
              style={{ marginTop: 10, width: '100%' }}
              disabled={collecting}
              onClick={() => void runCollect()}
            >
              {collecting ? <IconLoader2 size={14} className="astrorder-spin" /> : <IconRefresh size={14} />}
              <span>采集真实素材（{fromDate} ~ {toDate}）</span>
            </button>
          </div>

          {bundle && (
            <div>
              <div className="digest-kpi-strip" aria-label="素材统计概览">
                {kpiCards.map((card) => (
                  <div key={card.label} className="digest-kpi-card">
                    <div className="digest-kpi-label">{card.label}</div>
                    <div className="digest-kpi-value">{card.value}</div>
                  </div>
                ))}
              </div>
              {!isOfficeProfile && ((bundle.stats.meeting_count ?? 0) > 0 || (bundle.stats.office_doc_count ?? 0) > 0) && (
                <Text size="11px" c="dimmed" mt={5}>
                  另含 {bundle.stats.meeting_count ?? 0} 场会议纪要 · {bundle.stats.office_doc_count ?? 0} 份办公文档
                </Text>
              )}
            </div>
          )}

          <div className="digest-section-block">
            <Group justify="space-between" mb={6}>
              <div className="digest-section-label" style={{ marginBottom: 0 }}>
                2. 勾选素材（{selectedItems.length} / {allItems.length}）
              </div>
              {allItems.length > 0 && (
                <Group gap={6}>
                  <button type="button" className="digest-link-btn" onClick={() => toggleAllItems(true)}>全选</button>
                  <button type="button" className="digest-link-btn" onClick={() => toggleAllItems(false)}>清空</button>
                </Group>
              )}
            </Group>

            {bundle && bundle.projects.length === 0 ? (
              <div className="digest-empty-box">
                该时间段内暂无采集到的工作记录；可调整时间范围或在下方手动补充工作事项。
              </div>
            ) : (
              <div className="digest-material-tree">
                {(bundle?.projects || []).map((proj) => {
                  const isCollapsed = Boolean(collapsedProjects[proj.project_key])
                  const projCheckedCount = proj.items.filter((it) => itemSelection[it.item_id] ?? it.selected).length
                  const allChecked = proj.items.length > 0 && projCheckedCount === proj.items.length
                  const indeterminate = projCheckedCount > 0 && projCheckedCount < proj.items.length
                  return (
                    <div key={proj.project_key} className="digest-project-card">
                      <div className="digest-project-header">
                        <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                          <Checkbox
                            size="xs"
                            checked={allChecked}
                            indeterminate={indeterminate}
                            onChange={(e) => toggleProjectItems(proj.project_key, e.currentTarget.checked)}
                            aria-label={`选择项目 ${proj.project_name} 的全部素材`}
                          />
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            onClick={() =>
                              setCollapsedProjects((prev) => ({
                                ...prev,
                                [proj.project_key]: !prev[proj.project_key],
                              }))
                            }
                            aria-label={isCollapsed ? '展开项目素材' : '收起项目素材'}
                          >
                            {isCollapsed ? <IconChevronRight size={13} /> : <IconChevronDown size={13} />}
                          </ActionIcon>
                          <Text size="xs" fw={600} truncate title={proj.workspace || proj.project_name}>
                            {proj.project_name}
                          </Text>
                        </Group>
                        <span className="digest-count-pill">
                          {projCheckedCount}/{proj.items.length}
                        </span>
                      </div>

                      {!isCollapsed && (
                        <div className="digest-project-items">
                          {proj.items.map((item) => {
                            const checked = itemSelection[item.item_id] ?? item.selected
                            const meta = kindMeta(item.kind)
                            return (
                              <div key={item.item_id} className="digest-item-row">
                                <Checkbox
                                  size="xs"
                                  mt={2}
                                  checked={checked}
                                  onChange={(e) =>
                                    setItemSelection((prev) => ({
                                      ...prev,
                                      [item.item_id]: e.currentTarget.checked,
                                    }))
                                  }
                                />
                                <div style={{ minWidth: 0, flex: 1 }}>
                                  <Group gap={6} wrap="nowrap" justify="space-between" mb={2}>
                                    <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                                      <Badge size="xs" variant="light" color={meta.color} style={{ flexShrink: 0 }}>
                                        {meta.label}
                                      </Badge>
                                      <Text size="xs" fw={550} truncate title={item.title}>
                                        {item.title}
                                      </Text>
                                    </Group>
                                    {item.timestamp && (
                                      <Text size="11px" c="dimmed" style={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums' }}>
                                        {formatItemTimestamp(item.timestamp)}
                                      </Text>
                                    )}
                                  </Group>
                                  {item.summary && <div className="digest-item-summary">{item.summary}</div>}
                                </div>
                              </div>
                            )
                          })}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          <Textarea
            size="xs"
            label="3. 补充事项"
            placeholder="线下沟通、电话确认或其他需纳入汇报的重点事项…"
            minRows={2}
            autosize
            value={manualNotes}
            onChange={(e) => setManualNotes(e.currentTarget.value)}
          />

          <Divider color="var(--dg-hairline)" />

          <Stack gap="xs">
            <Group justify="space-between" align="flex-end" gap="xs">
              <Select
                size="xs"
                style={{ flex: 1 }}
                label="4. 汇报模板"
                value={templateId}
                onChange={(val) => {
                  if (val) {
                    userPickedTemplateRef.current = true
                    setTemplateId(val)
                  }
                }}
                data={templates.map((t) => ({
                  value: t.id,
                  label: `${t.name}${t.built_in ? '' : '（自定义）'}`,
                }))}
              />
              <button type="button" className="digest-ghost-btn" onClick={() => setTemplateModalOpen(true)}>
                管理模板
              </button>
            </Group>
            {selectedTemplate?.description && (
              <Text size="xs" c="dimmed">{selectedTemplate.description}</Text>
            )}

            <button
              type="button"
              className="digest-obsidian-btn"
              disabled={generating}
              onClick={() => void handleGenerate('llm')}
            >
              {generating ? <IconLoader2 size={15} className="astrorder-spin" /> : <IconSparkles size={15} />}
              <span>{llmConfigured ? '一键生成报告' : '生成素材草稿与提示词'}</span>
            </button>

            <div className="digest-sub-actions">
              <button
                type="button"
                className="digest-secondary-btn"
                disabled={generating}
                onClick={() => void handleGenerate('prompt_only')}
              >
                <IconClipboardText size={13} />
                <span>复制提示词</span>
              </button>
              <button
                type="button"
                className="digest-secondary-btn"
                disabled={generating}
                onClick={() => void handleSendPromptToAgent()}
              >
                <IconMessageForward size={13} />
                <span>{isOfficeProfile ? '交给 AI 助手生成' : '交给会话 Agent 生成'}</span>
              </button>
            </div>
          </Stack>
        </section>

        {/* Right pane: Pure editorial paper preview / Markdown edit / Quick refine / ExportMenu */}
        <section className="digest-right-pane" aria-label="报告审阅与成品导出">
          <div className="digest-report-toolbar">
            <Group justify="space-between" gap="xs" wrap="wrap">
              <input
                type="text"
                className="digest-report-title-input"
                placeholder="输入报告标题…"
                value={reportTitle}
                onChange={(e) => setReportTitle(e.currentTarget.value)}
              />
              <Group gap="xs" wrap="nowrap">
                <SegmentedControl
                  size="xs"
                  value={viewMode}
                  onChange={(v) => setViewMode(v as 'preview' | 'edit')}
                  data={[
                    { value: 'preview', label: '排版预览' },
                    { value: 'edit', label: 'Markdown 编辑' },
                  ]}
                />
                <Button
                  size="compact-xs"
                  variant="default"
                  loading={savingHistory}
                  disabled={!reportMarkdown.trim()}
                  leftSection={<IconArchive size={13} />}
                  onClick={() => void handleSaveHistory()}
                >
                  保存归档
                </Button>
                <ExportMenu
                  markdown={reportMarkdown}
                  title={reportTitle || selectedTemplate?.name || '工作周报'}
                  fileName={reportTitle || `工作周报_${fromDate}_${toDate}`}
                  workspace={exportTargetWorkspace}
                  variant="button"
                  buttonLabel="导出"
                />
              </Group>
            </Group>

            {reportMarkdown.trim() && (
              <Group gap={6} wrap="wrap">
                <Text size="xs" c="dimmed">快捷润色：</Text>
                {REFINE_PRESETS.map((p) => (
                  <button
                    key={p.label}
                    type="button"
                    className="digest-refine-chip"
                    disabled={refining}
                    onClick={() => void handleRefine(p.instruction)}
                  >
                    {p.label}
                  </button>
                ))}
                <TextInput
                  size="xs"
                  w={200}
                  placeholder="自定义润色要求…"
                  value={customRefine}
                  onChange={(e) => setCustomRefine(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && customRefine.trim()) {
                      void handleRefine(customRefine)
                    }
                  }}
                />
                <Tooltip label="按自定义要求二次润色当前报告">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="gray"
                    loading={refining}
                    disabled={!customRefine.trim()}
                    leftSection={<IconWand size={12} />}
                    onClick={() => void handleRefine(customRefine)}
                  >
                    润色
                  </Button>
                </Tooltip>
              </Group>
            )}
          </div>

          <div className="digest-report-scroll">
            {reportNotice && (
              <Alert
                mb="md"
                maw={760}
                mx="auto"
                color="gray"
                variant="light"
                icon={<IconInfoCircle size={16} />}
                withCloseButton
                onClose={() => setReportNotice('')}
              >
                {reportNotice}
              </Alert>
            )}

            {!reportMarkdown.trim() ? (
              <div className="digest-empty-stage">
                <div className="digest-empty-icon">
                  <IconNotebook size={28} stroke={1.6} />
                </div>
                <h3>准备生成你的工作周报</h3>
                <p>
                  左侧已按时间范围聚合你的会议纪要、办公文档与对话产出。勾选需要汇报的条目并选择模板后点击「一键生成报告」，成稿后可直接导出 Word、PPT、PDF 或保存到当前工作区。
                </p>
              </div>
            ) : viewMode === 'edit' ? (
              <Textarea
                className="digest-editor-textarea"
                value={reportMarkdown}
                onChange={(e) => setReportMarkdown(e.currentTarget.value)}
                autosize
                minRows={24}
                aria-label="编辑报告 Markdown 正文"
              />
            ) : (
              <article className="digest-report-paper" aria-label="报告排版预览">
                <MarkdownContent value={reportMarkdown} />
              </article>
            )}
          </div>
        </section>
      </div>

      <DigestTemplateModal
        opened={templateModalOpen}
        onClose={() => setTemplateModalOpen(false)}
        templates={templates}
        onTemplatesChange={setTemplates}
      />

      <Drawer
        opened={historyOpen}
        onClose={() => setHistoryOpen(false)}
        title="历史周报与复盘归档"
        position="right"
        size="md"
        withinPortal
      >
        <Stack gap="sm">
          {historyItems.length > 0 && (
            <TextInput
              size="xs"
              leftSection={<IconSearch size={13} />}
              placeholder="搜索标题、模板或正文关键词…"
              value={historyQuery}
              onChange={(e) => setHistoryQuery(e.currentTarget.value)}
              rightSection={
                historyQuery ? (
                  <ActionIcon size="xs" variant="subtle" aria-label="清空搜索" onClick={() => setHistoryQuery('')}>
                    <IconX size={12} />
                  </ActionIcon>
                ) : null
              }
            />
          )}

          {filteredHistory.length === 0 ? (
            <Text size="sm" c="dimmed" ta="center" mt="xl">
              {historyItems.length === 0
                ? '暂无已保存的历史记录；生成报告后点击「保存归档」即可保留最近 30 份报告。'
                : '没有匹配的历史归档。'}
            </Text>
          ) : (
            <Stack gap="xs">
              {filteredHistory.map((rec) => (
                <div key={rec.id} className={`digest-history-card${activeRecordId === rec.id ? ' is-active' : ''}`}>
                  <div
                    style={{ minWidth: 0, flex: 1, cursor: 'pointer' }}
                    onClick={() => handleLoadHistory(rec)}
                  >
                    <Text size="sm" fw={600} truncate>{rec.title}</Text>
                    <Group gap={6} mt={4}>
                      <Badge size="xs" variant="light" color="gray">{rec.template_name || rec.template_id}</Badge>
                      <Text size="xs" c="dimmed">{rec.from} ~ {rec.to}</Text>
                      <Text size="xs" c="dimmed">{rec.item_count} 条素材</Text>
                    </Group>
                  </div>
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    color="red"
                    aria-label={`删除历史记录 ${rec.title}`}
                    onClick={() => void handleDeleteHistory(rec.id)}
                  >
                    <IconTrash size={14} />
                  </ActionIcon>
                </div>
              ))}
            </Stack>
          )}
        </Stack>
      </Drawer>
    </div>
  )
}
