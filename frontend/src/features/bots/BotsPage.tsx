import {
  IconAdjustmentsHorizontal,
  IconArrowRight,
  IconBookmark,
  IconBriefcase,
  IconChartBar,
  IconCheck,
  IconCircleCheck,
  IconCircleDashed,
  IconCode,
  IconCopy,
  IconDeviceFloppy,
  IconFile,
  IconFileText,
  IconInfoCircle,
  IconLayersLinked,
  IconMovie,
  IconPalette,
  IconPaperclip,
  IconPlus,
  IconRobot,
  IconSearch,
  IconServer,
  IconSparkles,
  IconTransfer,
  IconTrash,
  IconUpload,
  IconWand,
  IconX,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
  type KeyboardEvent,
} from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import {
  api,
  type BotApprovalMode,
  type BotPreset,
  type BotSummary,
  type InvokeBotRequest,
  type UploadedBotInboxFile,
} from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { selectSessions, useAstrorderStore } from '../../state/store'
import { connectionName } from '../../components/AgentSessionFilter'
import { displaySessionTitle, formatRelativeTime, sessionActivityStatus } from '../../components/sessionRailModel'
import { setProjectOfficeMode } from '../office/officeWorkspaceMode'
import { BotCreateModal, type BotModalMode } from './BotCreateModal'
import {
  approvalModeDescription,
  approvalModeShortLabel,
  BOT_APPROVAL_MODE_OPTIONS,
  effortOptionsForBotSelection,
  formatBotModelSummary,
  normalizeBotApprovalMode,
  resolveBotModelSelection,
  splitBotModelSelection,
  useBotModelCatalog,
} from './useBotModelCatalog'
import {
  buildBotEnvironmentOptions,
  consumePendingDistillIntent,
  consumePendingHandoffIntent,
  normalizeBotConnection,
  normalizeBotWorkspace,
  useBots,
  useInvalidateBots,
  type DistillBotIntent,
  type HandoffBotIntent,
} from './useBots'
import './bots.css'

type WorkbenchTab = 'dispatch' | 'agents' | 'skills' | 'settings'

const TAB_ITEMS: Array<{ id: WorkbenchTab; label: string }> = [
  { id: 'dispatch', label: '总览与派单' },
  { id: 'agents', label: '人设 AGENTS.md' },
  { id: 'skills', label: '技能库 Skills' },
  { id: 'settings', label: '配置与协作' },
]

const QUICK_AI_TWEAKS = [
  { label: '+ 强化交付标准', prompt: '强化交付物验收标准与质量自检条目，要求明确文件路径与验证结论。' },
  { label: '+ 补充异常处理', prompt: '补充素材缺失、命令失败或环境异常时的沟通与降级处理约定。' },
  { label: '+ 限制输出格式', prompt: '明确最终回复的结构化格式，避免冗长过程说明，突出核心交付结果。' },
  { label: '+ 提炼精简版', prompt: '保持所有关键规则与约束完整，精简措辞使其更紧凑易读。' },
]

function emblemToneForIcon(icon?: string): string {
  switch (icon) {
    case 'video':
    case 'spark':
      return 'tone-indigo'
    case 'doc':
    case 'data':
      return 'tone-blue'
    case 'ops':
    case 'code':
      return 'tone-amber'
    case 'briefcase':
    case 'design':
      return 'tone-blue'
    default:
      return 'tone-neutral'
  }
}

export function BotGlyph({ icon, size = 16 }: { icon?: string; size?: number }) {
  switch (icon) {
    case 'video':
      return <IconMovie size={size} aria-hidden="true" />
    case 'doc':
      return <IconFileText size={size} aria-hidden="true" />
    case 'code':
      return <IconCode size={size} aria-hidden="true" />
    case 'design':
      return <IconPalette size={size} aria-hidden="true" />
    case 'data':
      return <IconChartBar size={size} aria-hidden="true" />
    case 'ops':
      return <IconServer size={size} aria-hidden="true" />
    case 'briefcase':
      return <IconBriefcase size={size} aria-hidden="true" />
    case 'spark':
      return <IconSparkles size={size} aria-hidden="true" />
    default:
      return <IconRobot size={size} aria-hidden="true" />
  }
}

function formatBotEnvLabel(connectionId: string | undefined, agent: Agent | undefined): string {
  if (agent) return connectionName(agent)
  if (!connectionId || connectionId === 'local') return '本机'
  if (connectionId.startsWith('wsl:')) return connectionId.slice(4) || 'WSL'
  if (connectionId.startsWith('ssh-')) return 'SSH'
  return connectionId
}

function formatBotAgentLabel(agentId: string, agent: Agent | undefined): string {
  const kindMap: Record<string, string> = {
    antigravity: 'Antigravity',
    'antigravity-ssh': 'Antigravity',
    codex: 'Codex',
    'codex-ssh': 'Codex',
    hermes: 'Hermes',
    ssh: 'Hermes',
    grok: 'Grok',
    'grok-ssh': 'Grok',
  }
  if (agent) return kindMap[agent.kind] || agent.name || agent.id
  const low = agentId.toLowerCase()
  if (low.includes('antigravity')) return 'Antigravity'
  if (low.includes('codex')) return 'Codex'
  if (low.includes('hermes')) return 'Hermes'
  if (low.includes('grok')) return 'Grok'
  return agentId
}

function analyzeAgentsCompleteness(content: string) {
  const text = content || ''
  return [
    { label: '角色定位与目标', ok: /角色|定位|职责|能力|目标|persona|role/i.test(text) },
    { label: '输入与跨会话协作', ok: /inbox|输入|素材|派单|协作|回溯|上下文/i.test(text) },
    { label: '交付与自检标准', ok: /交付|验收|自检|标准|质量|产物|deliver/i.test(text) },
    { label: '安全与操作边界', ok: /边界|禁止|安全|权限|严禁|constraint|boundary/i.test(text) },
  ]
}

function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function BotsPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const queryClient = useQueryClient()
  const invalidateBots = useInvalidateBots()
  const baseTabId = useId()
  const searchInputId = useId()

  const botsQuery = useBots(true)
  const bots = useMemo(() => botsQuery.data?.items || [], [botsQuery.data?.items])
  const allSessions = useAstrorderStore(useShallow(selectSessions))
  const agentsMap = useAstrorderStore((s) => s.agents)
  const agents = useMemo(() => Object.values(agentsMap), [agentsMap])

  const [search, setSearch] = useState('')
  const [selectedBotId, setSelectedBotId] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState<WorkbenchTab>('dispatch')

  // Create / Distill / Feedback modal state
  const [modalOpen, setModalOpen] = useState(false)
  const [modalMode, setModalMode] = useState<BotModalMode>('create')
  const [modalDistill, setModalDistill] = useState<DistillBotIntent | null>(null)
  const [feedbackBot, setFeedbackBot] = useState<BotSummary | null>(null)

  // Spotlight Dispatch Composer state
  const [showTitleInput, setShowTitleInput] = useState(false)
  const [showAttachInput, setShowAttachInput] = useState(false)
  const [showDispatchOverrides, setShowDispatchOverrides] = useState(false)
  const [jobTitle, setJobTitle] = useState('')
  const [jobPrompt, setJobPrompt] = useState('')
  const [jobAttachPaths, setJobAttachPaths] = useState('')
  const [uploadedFiles, setUploadedFiles] = useState<UploadedBotInboxFile[]>([])
  const [uploadingFiles, setUploadingFiles] = useState(false)
  const [composerDragOver, setComposerDragOver] = useState(false)
  const [handoffContext, setHandoffContext] = useState<HandoffBotIntent | null>(null)
  const [notifyCallerOnDispatch, setNotifyCallerOnDispatch] = useState(true)
  const [overrideApprovalMode, setOverrideApprovalMode] = useState<'' | BotApprovalMode>('')
  const [overrideModelKey, setOverrideModelKey] = useState('')
  const [overrideEffort, setOverrideEffort] = useState('')
  const [savingPresetOpen, setSavingPresetOpen] = useState(false)
  const [presetLabelInput, setPresetLabelInput] = useState('')
  const [savingPreset, setSavingPreset] = useState(false)
  const [openAfterDispatch, setOpenAfterDispatch] = useState(true)
  const [dispatching, setDispatching] = useState(false)

  // AGENTS.md editor state
  const [agentsViewMode, setAgentsViewMode] = useState<'edit' | 'preview'>('edit')
  const [agentsDraft, setAgentsDraft] = useState('')
  const [agentsSavedSnapshot, setAgentsSavedSnapshot] = useState('')
  const [agentsTunePrompt, setAgentsTunePrompt] = useState('')
  const [agentsOptimizing, setAgentsOptimizing] = useState(false)
  const [agentsSaving, setAgentsSaving] = useState(false)

  // Skills editor state
  const [skillViewMode, setSkillViewMode] = useState<'edit' | 'preview'>('edit')
  const [selectedSkillSlug, setSelectedSkillSlug] = useState<string>('')
  const [skillDraft, setSkillDraft] = useState('')
  const [newSkillSlug, setNewSkillSlug] = useState('')
  const [creatingSkill, setCreatingSkill] = useState(false)
  const [skillSaving, setSkillSaving] = useState(false)

  // Settings form state
  const [cfgName, setCfgName] = useState('')
  const [cfgSlug, setCfgSlug] = useState('')
  const [cfgDesc, setCfgDesc] = useState('')
  const [cfgIcon, setCfgIcon] = useState('robot')
  const [cfgWorkspace, setCfgWorkspace] = useState('')
  const [cfgConn, setCfgConn] = useState('')
  const [cfgAgentId, setCfgAgentId] = useState('')
  const [cfgModelKey, setCfgModelKey] = useState('')
  const [cfgEffort, setCfgEffort] = useState('')
  const [cfgApprovalMode, setCfgApprovalMode] = useState<BotApprovalMode>('full_access')
  const [cfgNotifyCaller, setCfgNotifyCaller] = useState(true)
  const [cfgPresets, setCfgPresets] = useState<BotPreset[]>([])
  const [newPresetLabel, setNewPresetLabel] = useState('')
  const [newPresetTitle, setNewPresetTitle] = useState('')
  const [newPresetPrompt, setNewPresetPrompt] = useState('')
  const [cfgOfficeMode, setCfgOfficeMode] = useState(false)
  const [cfgTags, setCfgTags] = useState('')
  const [cfgSaving, setCfgSaving] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const cfgModelDirtyRef = useRef(false)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const promptTextareaRef = useRef<HTMLTextAreaElement | null>(null)
  const pendingHandoffSourceRef = useRef<string | null>(null)

  const tabButtonRefs = useRef<Record<WorkbenchTab, HTMLButtonElement | null>>({
    dispatch: null,
    agents: null,
    skills: null,
    settings: null,
  })

  // Check if user navigated here via SessionRail "沉淀为 Bot" or "接力派给 Bot"
  useEffect(() => {
    const distillIntent = consumePendingDistillIntent()
    if (distillIntent) {
      setFeedbackBot(null)
      setModalDistill(distillIntent)
      setModalMode('distill')
      setModalOpen(true)
      return
    }
    const handoffIntent = consumePendingHandoffIntent()
    if (handoffIntent) {
      setHandoffContext(handoffIntent)
      setActiveTab('dispatch')
      if (handoffIntent.prompt) setJobPrompt(handoffIntent.prompt)
      if (handoffIntent.targetBotId) {
        setSelectedBotId(handoffIntent.targetBotId)
      } else if (handoffIntent.sourceBotId) {
        pendingHandoffSourceRef.current = handoffIntent.sourceBotId
      }
    }
  }, [])

  // Support deep linking via /bots?bot=<id-or-slug>
  useEffect(() => {
    const botParam = searchParams.get('bot')?.trim()
    if (!botParam || bots.length === 0) return
    const matched = bots.find((b) => b.id === botParam || b.slug === botParam)
    if (matched) {
      setSelectedBotId(matched.id)
    }
    const nextParams = new URLSearchParams(searchParams)
    nextParams.delete('bot')
    setSearchParams(nextParams, { replace: true })
  }, [bots, searchParams, setSearchParams])

  // When handing off from another Bot without an explicit target, default to a different Bot if available.
  useEffect(() => {
    const sourceId = pendingHandoffSourceRef.current
    if (!sourceId || bots.length === 0) return
    pendingHandoffSourceRef.current = null
    const other = bots.find((b) => b.id !== sourceId)
    if (other) {
      setSelectedBotId(other.id)
    }
  }, [bots])

  const filteredBots = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return bots
    return bots.filter(
      (b) =>
        b.name.toLowerCase().includes(q) ||
        b.slug.toLowerCase().includes(q) ||
        b.description.toLowerCase().includes(q) ||
        b.workspace.toLowerCase().includes(q) ||
        (b.tags || []).some((t) => t.toLowerCase().includes(q)),
    )
  }, [bots, search])

  const selectedBot = useMemo(
    () => bots.find((b) => b.id === selectedBotId) || filteredBots[0] || bots[0] || null,
    [bots, filteredBots, selectedBotId],
  )

  useEffect(() => {
    if (selectedBot && selectedBot.id !== selectedBotId) {
      setSelectedBotId(selectedBot.id)
    }
  }, [selectedBot, selectedBotId])

  const docsQuery = useQuery({
    queryKey: ['astrorder', 'bot-docs', selectedBot?.id],
    queryFn: () => (selectedBot ? api.getBotDocs(selectedBot.id) : Promise.reject(new Error('no bot'))),
    enabled: Boolean(selectedBot?.id),
    staleTime: 10_000,
    retry: false,
  })

  const cfgEffectiveAgentId = cfgAgentId || selectedBot?.default_agent_id || ''
  const settingsModelCatalog = useBotModelCatalog(
    cfgEffectiveAgentId,
    agentsMap[cfgEffectiveAgentId],
    allSessions,
    Boolean(selectedBot),
  )
  const selectedBotModelCatalog = useBotModelCatalog(
    selectedBot?.default_agent_id || '',
    selectedBot ? agentsMap[selectedBot.default_agent_id] : undefined,
    allSessions,
    Boolean(selectedBot),
  )

  useEffect(() => {
    if (!selectedBot) return
    setCfgName(selectedBot.name)
    setCfgSlug(selectedBot.slug)
    setCfgDesc(selectedBot.description || '')
    setCfgIcon(selectedBot.icon || 'robot')
    setCfgWorkspace(selectedBot.workspace)
    setCfgConn(selectedBot.connection_id || '')
    setCfgAgentId(selectedBot.default_agent_id)
    setCfgApprovalMode(normalizeBotApprovalMode(selectedBot.default_approval_mode))
    setCfgNotifyCaller(selectedBot.default_notify_caller ?? true)
    setCfgPresets(selectedBot.presets || [])
    setCfgOfficeMode(Boolean(selectedBot.office_mode))
    setCfgTags((selectedBot.tags || []).join(', '))
    setConfirmDelete(false)
    setCreatingSkill(false)
    setNotifyCallerOnDispatch(selectedBot.default_notify_caller ?? true)
    setUploadedFiles([])
    setShowDispatchOverrides(false)
    setOverrideApprovalMode('')
    setOverrideModelKey('')
    setOverrideEffort('')
    setSavingPresetOpen(false)
    setPresetLabelInput('')
    setNewPresetLabel('')
    setNewPresetTitle('')
    setNewPresetPrompt('')
    cfgModelDirtyRef.current = false
  }, [selectedBot])

  // Sync saved (provider, model, effort) into grouped dropdown once model catalog loads or Bot switches.
  useEffect(() => {
    if (!selectedBot || cfgModelDirtyRef.current) return
    if (cfgAgentId && cfgAgentId !== selectedBot.default_agent_id) return
    const split = splitBotModelSelection(
      settingsModelCatalog.groups,
      settingsModelCatalog.isAntigravity,
      selectedBot.default_provider,
      selectedBot.default_model,
      selectedBot.default_effort,
    )
    setCfgModelKey(split.groupKey)
    setCfgEffort(split.effort)
  }, [
    selectedBot,
    cfgAgentId,
    settingsModelCatalog.groups,
    settingsModelCatalog.isAntigravity,
  ])

  useEffect(() => {
    if (!docsQuery.data) return
    const md = docsQuery.data.agents_md || ''
    setAgentsDraft(md)
    setAgentsSavedSnapshot(md)
    const skills = docsQuery.data.skills || []
    if (skills.length > 0) {
      const keep = skills.find((s) => s.slug === selectedSkillSlug) || skills[0]
      setSelectedSkillSlug(keep.slug)
      setSkillDraft(keep.content || '')
    } else {
      setSelectedSkillSlug('')
      setSkillDraft('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [docsQuery.data?.bot.id, docsQuery.dataUpdatedAt])

  const botSessions = useMemo(() => {
    if (!selectedBot) return []
    const wsKey = normalizeBotWorkspace(selectedBot.workspace)
    const conn = normalizeBotConnection(selectedBot.connection_id)
    return allSessions
      .filter(
        (s) =>
          normalizeBotWorkspace(s.workspace) === wsKey &&
          normalizeBotConnection(s.connection_id) === conn,
      )
      .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
  }, [allSessions, selectedBot])

  const agentsDirty = agentsDraft !== agentsSavedSnapshot
  const completenessChecks = useMemo(() => analyzeAgentsCompleteness(agentsDraft), [agentsDraft])

  const selectWorkbenchTab = (next: WorkbenchTab) => {
    setActiveTab(next)
    const btn = tabButtonRefs.current[next]
    if (btn && typeof btn.scrollIntoView === 'function') {
      btn.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    }
  }

  const handleTabKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const idx = TAB_ITEMS.findIndex((t) => t.id === activeTab)
    let nextIdx = idx
    if (event.key === 'ArrowRight') nextIdx = (idx + 1) % TAB_ITEMS.length
    else if (event.key === 'ArrowLeft') nextIdx = (idx - 1 + TAB_ITEMS.length) % TAB_ITEMS.length
    else if (event.key === 'Home') nextIdx = 0
    else if (event.key === 'End') nextIdx = TAB_ITEMS.length - 1
    else return

    event.preventDefault()
    const nextTab = TAB_ITEMS[nextIdx].id
    selectWorkbenchTab(nextTab)
    tabButtonRefs.current[nextTab]?.focus()
  }

  const openCreateModal = () => {
    setFeedbackBot(null)
    setModalDistill(null)
    setModalMode('create')
    setModalOpen(true)
  }

  const openDistillModal = () => {
    setFeedbackBot(null)
    setModalDistill(null)
    setModalMode('distill')
    setModalOpen(true)
  }

  const openFeedbackModal = (bot: BotSummary, sessionKey?: string) => {
    setFeedbackBot(bot)
    setModalDistill(sessionKey ? { sessionKey } : null)
    setModalMode('feedback')
    setModalOpen(true)
  }

  const handleUploadInboxFiles = async (fileList: FileList | File[]) => {
    if (!selectedBot) return
    const files = Array.from(fileList).filter((f) => f && f.size > 0)
    if (files.length === 0) return
    setUploadingFiles(true)
    try {
      const res = await api.uploadBotInboxFiles(selectedBot.id, files)
      if (res.files?.length) {
        setUploadedFiles((prev) => [...prev, ...res.files])
        notifications.show({
          color: 'teal',
          message: `已上传 ${res.files.length} 个素材到「${selectedBot.name}」工作区 inbox`,
        })
      }
    } catch (err) {
      notifications.show({
        color: 'red',
        title: '上传素材失败',
        message: err instanceof Error ? err.message : '无法上传文件到工作区 inbox',
      })
    } finally {
      setUploadingFiles(false)
    }
  }

  const handleComposerDrop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    setComposerDragOver(false)
    if (event.dataTransfer?.files?.length) {
      void handleUploadInboxFiles(event.dataTransfer.files)
    }
  }

  const handleComposerPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const dt = event.clipboardData
    if (!dt) return
    const pasted: File[] = []
    if (dt.files && dt.files.length > 0) {
      for (const f of Array.from(dt.files)) {
        if (f.size > 0) pasted.push(f)
      }
    } else if (dt.items) {
      for (const item of Array.from(dt.items)) {
        if (item.kind === 'file') {
          const f = item.getAsFile()
          if (f && f.size > 0) {
            const named =
              f.name && f.name !== 'image.png'
                ? f
                : new File([f], `screenshot-${Date.now()}.${f.type.split('/')[1] || 'png'}`, { type: f.type })
            pasted.push(named)
          }
        }
      }
    }
    if (pasted.length > 0) {
      event.preventDefault()
      void handleUploadInboxFiles(pasted)
    }
  }

  const handleApplyPreset = (preset: BotPreset) => {
    setJobPrompt(preset.prompt)
    if (preset.title) {
      setJobTitle(preset.title)
      setShowTitleInput(true)
    }
    promptTextareaRef.current?.focus()
  }

  const handleSavePresetFromComposer = async () => {
    if (!selectedBot) return
    const label = presetLabelInput.trim()
    const prompt = jobPrompt.trim()
    if (!label || !prompt) {
      notifications.show({ color: 'yellow', message: '请填写模板名称与任务简报' })
      return
    }
    setSavingPreset(true)
    try {
      const nextPresets: BotPreset[] = [
        ...(selectedBot.presets || []),
        {
          id: '',
          label,
          title: jobTitle.trim() || undefined,
          prompt,
        },
      ]
      await api.updateBot(selectedBot.id, { presets: nextPresets })
      await invalidateBots()
      setSavingPresetOpen(false)
      setPresetLabelInput('')
      notifications.show({ color: 'teal', message: `已保存常用派单模板「${label}」` })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存模板失败',
      })
    } finally {
      setSavingPreset(false)
    }
  }

  const handleStartHandoffFromSession = (session: Session) => {
    if (!selectedBot) return
    const intent: HandoffBotIntent = {
      callerSessionKey: scopeKey(session.agent_id, session.id),
      callerTitle: displaySessionTitle(session),
      callerWorkspace: session.workspace || selectedBot.workspace,
      callerConnectionId: session.connection_id || selectedBot.connection_id,
      sourceBotId: selectedBot.id,
    }
    setHandoffContext(intent)
    setActiveTab('dispatch')
    const otherBot = bots.find((b) => b.id !== selectedBot.id)
    if (otherBot) {
      setSelectedBotId(otherBot.id)
      notifications.show({
        color: 'teal',
        message: `已携带「${intent.callerTitle}」上下文切换至「${otherBot.name}」，也可在左侧选择其他目标 Bot`,
      })
    } else {
      notifications.show({
        color: 'teal',
        message: `已携带「${intent.callerTitle}」上下文，输入任务说明即可接力派单`,
      })
    }
    setTimeout(() => promptTextareaRef.current?.focus(), 0)
  }

  const handleDispatchJob = async () => {
    if (!selectedBot) return
    if (!jobPrompt.trim()) {
      notifications.show({ color: 'yellow', message: '请先输入任务简报' })
      return
    }
    setDispatching(true)
    try {
      const manualAttachPaths = jobAttachPaths
        .split(/[\n,]/)
        .map((s) => s.trim())
        .filter(Boolean)
      const attachPaths = [...uploadedFiles.map((f) => f.path), ...manualAttachPaths]
      const payload: InvokeBotRequest = {
        title: jobTitle.trim() || undefined,
        prompt: jobPrompt.trim(),
        attach_paths: attachPaths.length ? attachPaths : undefined,
      }
      if (handoffContext) {
        payload.caller_session_key = handoffContext.callerSessionKey
        if (handoffContext.callerWorkspace) payload.caller_workspace = handoffContext.callerWorkspace
        if (handoffContext.callerConnectionId) payload.caller_connection_id = handoffContext.callerConnectionId
        payload.notify_caller = notifyCallerOnDispatch
      }
      if (overrideApprovalMode) {
        payload.approval_mode = overrideApprovalMode
      }
      if (overrideModelKey || overrideEffort) {
        const resolved = resolveBotModelSelection(
          selectedBotModelCatalog.groups,
          selectedBotModelCatalog.isAntigravity,
          overrideModelKey,
          overrideEffort,
        )
        if (resolved.default_provider) payload.provider = resolved.default_provider
        if (resolved.default_model) payload.model = resolved.default_model
        if (resolved.default_effort) payload.effort = resolved.default_effort
      }
      const res = await api.invokeBot(selectedBot.id, payload)
      setJobTitle('')
      setJobPrompt('')
      setJobAttachPaths('')
      setUploadedFiles([])
      setHandoffContext(null)
      setShowDispatchOverrides(false)
      setOverrideApprovalMode('')
      setOverrideModelKey('')
      setOverrideEffort('')
      setSavingPresetOpen(false)
      setPresetLabelInput('')
      await invalidateBots()
      notifications.show({
        color: 'teal',
        title: `已派单给「${selectedBot.name}」`,
        message: res.staged_files?.length
          ? `已派生干净会话并推送 ${res.staged_files.length} 个附件到 inbox`
          : '已派生干净新会话开始执行',
      })
      const createdId = res.session?.id || ''
      const createdAgent = res.session?.agent_id || selectedBot.default_agent_id
      if (openAfterDispatch && createdId) {
        navigate(`/chat/${encodeURIComponent(createdId)}?agent_id=${encodeURIComponent(createdAgent)}`)
      }
    } catch (err) {
      notifications.show({
        color: 'red',
        title: '派单失败',
        message: err instanceof Error ? err.message : '无法派发 Bot 任务',
      })
    } finally {
      setDispatching(false)
    }
  }

  const handleOptimizeAgentsMd = async (customInstruction?: string) => {
    if (!selectedBot) return
    setAgentsOptimizing(true)
    try {
      const res = await api.optimizeBotConfig({
        name: selectedBot.name,
        description: selectedBot.description,
        instructions: customInstruction?.trim() || agentsTunePrompt.trim() || undefined,
        workspace: selectedBot.workspace,
        default_agent_id: selectedBot.default_agent_id,
        existing_agents_md: agentsDraft,
      })
      setAgentsDraft(res.agents_md)
      setAgentsTunePrompt('')
      notifications.show({
        color: 'teal',
        message:
          res.message ||
          (res.mode === 'llm' ? '已优化 AGENTS.md，审阅后点击保存生效' : '已按标准结构补全 AGENTS.md'),
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '优化失败',
      })
    } finally {
      setAgentsOptimizing(false)
    }
  }

  const handleSaveAgentsMd = async () => {
    if (!selectedBot) return
    setAgentsSaving(true)
    try {
      await api.saveBotAgentsMd(selectedBot.id, agentsDraft)
      setAgentsSavedSnapshot(agentsDraft)
      await Promise.all([
        invalidateBots(),
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'bot-docs', selectedBot.id] }),
      ])
      notifications.show({ color: 'teal', message: 'AGENTS.md 已保存到工作区' })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存 AGENTS.md 失败',
      })
    } finally {
      setAgentsSaving(false)
    }
  }

  const handleSelectSkill = (slug: string) => {
    const found = docsQuery.data?.skills.find((s) => s.slug === slug)
    setSelectedSkillSlug(slug)
    setSkillDraft(found?.content || '')
    setCreatingSkill(false)
  }

  const handleStartNewSkill = () => {
    setCreatingSkill(true)
    setNewSkillSlug('')
    setSelectedSkillSlug('')
    setSkillViewMode('edit')
    setSkillDraft(
      `---\nname: my-workflow\ndescription: Describe when and how this Bot should apply this SOP.\n---\n\n# 标准作业流程\n\n## 1. 输入检查\n\n## 2. 执行步骤\n\n## 3. 验收标准\n`,
    )
    selectWorkbenchTab('skills')
  }

  const handleSaveSkill = async () => {
    if (!selectedBot) return
    const targetSlug = creatingSkill ? newSkillSlug.trim() : selectedSkillSlug
    if (!targetSlug) {
      notifications.show({ color: 'yellow', message: '请填写英文 Skill 标识（如 promo-video-production）' })
      return
    }
    setSkillSaving(true)
    try {
      const res = await api.saveBotSkill(selectedBot.id, targetSlug, skillDraft)
      setCreatingSkill(false)
      setSelectedSkillSlug(res.skill.slug)
      await Promise.all([
        invalidateBots(),
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'bot-docs', selectedBot.id] }),
      ])
      notifications.show({ color: 'teal', message: `Skill「${res.skill.slug}」已保存到 .agents/skills/` })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存 Skill 失败',
      })
    } finally {
      setSkillSaving(false)
    }
  }

  const handleSaveSettings = async () => {
    if (!selectedBot) return
    setCfgSaving(true)
    try {
      const tags = cfgTags
        .split(/[,，、]/)
        .map((s) => s.trim())
        .filter(Boolean)
      const resolvedModel = resolveBotModelSelection(
        settingsModelCatalog.groups,
        settingsModelCatalog.isAntigravity,
        cfgModelKey,
        cfgEffort,
      )
      await api.updateBot(selectedBot.id, {
        name: cfgName.trim(),
        slug: cfgSlug.trim(),
        description: cfgDesc.trim(),
        icon: cfgIcon,
        workspace: cfgWorkspace.trim(),
        connection_id: cfgConn.trim(),
        default_agent_id: cfgAgentId.trim(),
        default_provider: resolvedModel.default_provider,
        default_model: resolvedModel.default_model,
        default_effort: resolvedModel.default_effort,
        default_approval_mode: cfgApprovalMode,
        default_notify_caller: cfgNotifyCaller,
        presets: cfgPresets,
        office_mode: cfgOfficeMode,
        tags,
      })
      cfgModelDirtyRef.current = false
      if (cfgWorkspace.trim()) {
        setProjectOfficeMode({ projectKey: `bot:${selectedBot.slug}`, workspace: cfgWorkspace.trim() }, cfgOfficeMode)
      }
      await invalidateBots()
      notifications.show({ color: 'teal', message: 'Bot 配置已保存' })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存配置失败',
      })
    } finally {
      setCfgSaving(false)
    }
  }

  const handleDeleteBot = async () => {
    if (!selectedBot) return
    setDeleting(true)
    try {
      const res = await api.deleteBot(selectedBot.id)
      setSelectedBotId(null)
      setConfirmDelete(false)
      await invalidateBots()
      notifications.show({
        color: 'teal',
        message: res.message || '已取消 Bot 注册（工作区文件已保留）',
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '取消注册失败',
      })
    } finally {
      setDeleting(false)
    }
  }

  const copyMentionTag = (bot: BotSummary) => {
    void navigator.clipboard?.writeText(`@Bot:${bot.slug} `)
    notifications.show({ color: 'teal', message: `已复制 @Bot:${bot.slug}，可在任意会话中直接调用` })
  }

  const copyCliExample = (bot: BotSummary) => {
    const cmd = `astrorder-agent bots invoke --bot ${bot.slug} --operation-id op-${bot.slug}-1 --title "新任务" --prompt "请完成任务描述"`
    void navigator.clipboard?.writeText(cmd)
    notifications.show({ color: 'teal', message: '已复制 CLI 调用命令' })
  }

  const selectedAgent = selectedBot ? agentsMap[selectedBot.default_agent_id] : undefined
  const selectedEnvLabel = selectedBot ? formatBotEnvLabel(selectedBot.connection_id, selectedAgent) : ''
  const selectedAgentLabel = selectedBot ? formatBotAgentLabel(selectedBot.default_agent_id, selectedAgent) : ''
  const cfgEnvOptions = useMemo(() => buildBotEnvironmentOptions(agents, cfgConn), [agents, cfgConn])
  const selectedSkillDoc = useMemo(
    () => (!creatingSkill ? docsQuery.data?.skills.find((s) => s.slug === selectedSkillSlug) : undefined),
    [creatingSkill, docsQuery.data?.skills, selectedSkillSlug],
  )
  const hasActiveDispatchOverride = Boolean(overrideApprovalMode || overrideModelKey || overrideEffort)

  const handlePersistPresets = async (nextPresets: BotPreset[], message: string) => {
    if (!selectedBot) return
    setSavingPreset(true)
    try {
      await api.updateBot(selectedBot.id, { presets: nextPresets })
      setCfgPresets(nextPresets)
      await invalidateBots()
      notifications.show({ color: 'teal', message })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '更新派单模板失败',
      })
    } finally {
      setSavingPreset(false)
    }
  }

  return (
    <div className="bot-studio">
      {/* 1. Left Master Rail (268px, #F8F8FA) */}
      <aside className="bot-rail" aria-label="Bot 目录">
        <div className="bot-rail-header">
          <div className="bot-rail-title-wrap">
            <h1 className="bot-rail-heading">Bot</h1>
            <span className="bot-count-pill">{bots.length}</span>
          </div>
          <div className="bot-rail-icon-actions">
            <button
              type="button"
              className="bot-icon-btn"
              title="从会话孵化 Bot"
              aria-label="从会话孵化 Bot"
              onClick={openDistillModal}
            >
              <IconSparkles size={15} aria-hidden="true" />
            </button>
            <button
              type="button"
              className="bot-icon-btn"
              title="新建 Bot"
              aria-label="新建 Bot"
              onClick={openCreateModal}
            >
              <IconPlus size={16} aria-hidden="true" />
            </button>
          </div>
        </div>

        <div className="bot-rail-search">
          <label htmlFor={searchInputId} className="bot-sr-only">
            搜索 Bot
          </label>
          <div className="bot-search-wrap">
            <IconSearch size={14} className="bot-search-icon" aria-hidden="true" />
            <input
              id={searchInputId}
              className="bot-search-input"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索 Bot…"
            />
          </div>
        </div>

        {botsQuery.isLoading ? (
          <div className="bot-list-empty">正在加载 Bot 列表…</div>
        ) : filteredBots.length === 0 ? (
          <div className="bot-list-empty">
            {bots.length === 0
              ? '还没有 Bot。可以直接新建，或从已有会话沉淀一个 Bot。'
              : '没有匹配的 Bot。'}
          </div>
        ) : (
          <ul className="bot-list">
            {filteredBots.map((bot, idx) => {
              const isSelected = selectedBot?.id === bot.id
              const botAgent = agentsMap[bot.default_agent_id]
              const envLabel = formatBotEnvLabel(bot.connection_id, botAgent)
              const metaParts = [
                envLabel,
                `${bot.skills.length} ${bot.skills.length === 1 ? 'Skill' : 'Skills'}`,
                ...(bot.has_agents_md ? ['AGENTS.md'] : []),
              ]
              return (
                <li key={bot.id}>
                  <button
                    type="button"
                    className="bot-list-btn"
                    aria-current={isSelected ? 'true' : undefined}
                    onClick={() => setSelectedBotId(bot.id)}
                  >
                    <div className="bot-list-top">
                      <span className={`bot-emblem is-sm ${emblemToneForIcon(bot.icon)}`}>
                        <BotGlyph icon={bot.icon} size={15} />
                      </span>
                      <div className="bot-list-copy">
                        <div className="bot-list-name-row">
                          <span className="bot-list-name">{bot.name}</span>
                          {bot.running_count > 0 ? (
                            <span className="bot-running-tag">
                              <span className="bot-status-dot is-running" aria-hidden="true" />
                              运行中
                            </span>
                          ) : isSelected ? (
                            <span className="bot-status-dot is-active" aria-hidden="true" />
                          ) : null}
                        </div>
                        <div className="bot-list-desc">
                          {bot.description || bot.agents_md_excerpt || bot.workspace}
                        </div>
                      </div>
                    </div>
                    <div className="bot-list-foot">
                      <span className="bot-list-meta-line">{metaParts.join(' · ')}</span>
                      <span className="bot-list-index">#{String(idx + 1).padStart(2, '0')}</span>
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        )}

        <div className="bot-rail-footer">
          <button type="button" className="bot-rail-incubate-btn" onClick={openDistillModal}>
            <IconSparkles size={14} aria-hidden="true" />
            <span>从已有会话孵化 Bot</span>
          </button>
        </div>
      </aside>

      {/* 2. Right Studio Canvas */}
      <main className="bot-main">
        {!selectedBot ? (
          <div className="bot-empty">
            <div className="bot-empty-icon">
              <IconRobot size={26} aria-hidden="true" />
            </div>
            <h2>把你的专属工作区打造成可随时调用的 Bot</h2>
            <p>
              每个 Bot 对应一个独立项目目录，原生沉淀 <code>AGENTS.md</code> 角色宪章与{' '}
              <code>.agents/skills/</code> 技能库。每次派单都会派生干净新会话，也可在其他会话通过{' '}
              <code>@Bot</code> 或 <code>astrorder-agent bots invoke</code> 直接调用。
            </p>
            <div className="bot-empty-actions">
              <button type="button" className="bot-btn-primary" onClick={openDistillModal}>
                <IconSparkles size={15} aria-hidden="true" />
                从已有会话孵化 Bot
              </button>
              <button type="button" className="bot-btn-secondary" onClick={openCreateModal}>
                <IconPlus size={15} aria-hidden="true" />
                直接新建 Bot
              </button>
            </div>
          </div>
        ) : (
          <>
            {/* Unified Editorial Top Header */}
            <header className="bot-header">
              <div className="bot-header-row">
                <div className="bot-identity">
                  <span className={`bot-emblem is-lg ${emblemToneForIcon(selectedBot.icon)}`}>
                    <BotGlyph icon={selectedBot.icon} size={21} />
                  </span>
                  <div className="bot-identity-text">
                    <div className="bot-title-line">
                      <h2 className="bot-title">{selectedBot.name}</h2>
                      <span className="bot-slug-pill">@{selectedBot.slug}</span>
                      {selectedBot.office_mode && <span className="bot-badge is-blue">办公</span>}
                      <span className={`bot-ready-state${selectedBot.running_count > 0 ? ' is-running' : ''}`}>
                        <span
                          className={`bot-status-dot${selectedBot.running_count > 0 ? ' is-running' : ''}`}
                          aria-hidden="true"
                        />
                        {selectedBot.running_count > 0 ? `运行中 (${selectedBot.running_count})` : '已就绪'}
                      </span>
                    </div>
                    <p className="bot-meta-line" title={selectedBot.workspace}>
                      <span className="bot-meta-env">{selectedEnvLabel}</span>
                      <span className="bot-meta-sep" aria-hidden="true">·</span>
                      <span className="bot-meta-path">{selectedBot.workspace}</span>
                      <span className="bot-meta-sep" aria-hidden="true">·</span>
                      <span>{selectedAgentLabel}</span>
                    </p>
                  </div>
                </div>

                <div className="bot-header-actions">
                  <button
                    type="button"
                    className="bot-btn-secondary"
                    onClick={() =>
                      openFeedbackModal(
                        selectedBot,
                        botSessions[0] ? scopeKey(botSessions[0].agent_id, botSessions[0].id) : undefined,
                      )
                    }
                  >
                    <IconUpload size={14} aria-hidden="true" />
                    <span>反哺经验</span>
                  </button>
                  <button
                    type="button"
                    className="bot-btn-ghost"
                    onClick={() => copyMentionTag(selectedBot)}
                  >
                    <IconCopy size={14} aria-hidden="true" />
                    <span>复制 @调用</span>
                  </button>
                  <div className="bot-vsep" aria-hidden="true" />
                  <button type="button" className="bot-btn-primary" onClick={openCreateModal}>
                    <IconPlus size={14} aria-hidden="true" />
                    <span>新建 Bot</span>
                  </button>
                </div>
              </div>

              {/* Clean Single-Row Accessible Tablist */}
              <div
                className="bot-tablist"
                role="tablist"
                aria-label="Bot 工作台分区"
                onKeyDown={handleTabKeyDown}
                {...({ focusgroup: 'tablist nomemory' } as Record<string, string>)}
              >
                {TAB_ITEMS.map((tab) => {
                  const selected = activeTab === tab.id
                  return (
                    <button
                      key={tab.id}
                      ref={(el) => {
                        tabButtonRefs.current[tab.id] = el
                      }}
                      type="button"
                      id={`${baseTabId}-tab-${tab.id}`}
                      role="tab"
                      className="bot-tab"
                      aria-selected={selected}
                      {...(selected ? ({ focusgroupstart: '' } as Record<string, string>) : {})}
                      onClick={() => selectWorkbenchTab(tab.id)}
                    >
                      <span>{tab.label}</span>
                      {tab.id === 'skills' && (
                        <span className="bot-count-pill">{selectedBot.skills.length}</span>
                      )}
                    </button>
                  )
                })}
              </div>
            </header>

            <div className="bot-panels">
              {/* Panel 1: Overview & Spotlight Dispatch (2-Column Stitch Split) */}
              <div
                className="bot-tabpanel"
                role="tabpanel"
                aria-labelledby={`${baseTabId}-tab-dispatch`}
                hidden={activeTab !== 'dispatch'}
              >
                <div className="bot-overview-split">
                  {/* Left Execution Stream */}
                  <div className="bot-exec-col">
                    {(selectedBot.presets || []).length > 0 && (
                      <div className="bot-presets-bar" aria-label="常用派单模板">
                        <span className="bot-presets-label">快捷模板</span>
                        <div className="bot-presets-list">
                          {(selectedBot.presets || []).map((preset) => (
                            <button
                              key={preset.id || preset.label}
                              type="button"
                              className="bot-preset-chip"
                              title={preset.prompt}
                              onClick={() => handleApplyPreset(preset)}
                            >
                              <IconBookmark size={12} aria-hidden="true" />
                              <span>{preset.label}</span>
                            </button>
                          ))}
                        </div>
                      </div>
                    )}

                    <div
                      className={`bot-composer${composerDragOver ? ' is-drag-over' : ''}`}
                      onDragOver={(e) => {
                        e.preventDefault()
                        if (!composerDragOver) setComposerDragOver(true)
                      }}
                      onDragLeave={(e) => {
                        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
                          setComposerDragOver(false)
                        }
                      }}
                      onDrop={handleComposerDrop}
                    >
                      <input
                        ref={fileInputRef}
                        type="file"
                        multiple
                        className="bot-sr-only"
                        aria-label="上传素材文件"
                        onChange={(e) => {
                          if (e.target.files?.length) {
                            void handleUploadInboxFiles(e.target.files)
                          }
                          e.target.value = ''
                        }}
                      />

                      {handoffContext && (
                        <div className="bot-handoff-banner" role="status">
                          <div className="bot-handoff-banner-main">
                            <IconTransfer size={13} aria-hidden="true" />
                            <span className="bot-handoff-banner-text">
                              接力来源：<strong>{handoffContext.callerTitle || handoffContext.callerSessionKey}</strong>
                            </span>
                          </div>
                          <div className="bot-handoff-banner-actions">
                            <label className="bot-checkbox-label">
                              <input
                                type="checkbox"
                                checked={notifyCallerOnDispatch}
                                onChange={(e) => setNotifyCallerOnDispatch(e.target.checked)}
                              />
                              <span>完成后回告发起会话</span>
                            </label>
                            <button
                              type="button"
                              className="bot-icon-btn"
                              aria-label="取消接力关联"
                              title="取消接力关联"
                              onClick={() => setHandoffContext(null)}
                            >
                              <IconX size={13} aria-hidden="true" />
                            </button>
                          </div>
                        </div>
                      )}

                      <div className="bot-composer-top">
                        <label htmlFor="bot-job-prompt" className="bot-sr-only">
                          任务简报（Prompt）
                        </label>
                        <textarea
                          ref={promptTextareaRef}
                          id="bot-job-prompt"
                          className="bot-composer-textarea"
                          rows={3}
                          value={jobPrompt}
                          onChange={(e) => setJobPrompt(e.target.value)}
                          onPaste={handleComposerPaste}
                          onKeyDown={(e) => {
                            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && jobPrompt.trim() && !dispatching) {
                              e.preventDefault()
                              void handleDispatchJob()
                            }
                          }}
                          placeholder={`向「${selectedBot.name}」派发新任务… 支持拖拽或粘贴截图上传素材（Ctrl+Enter 派单）`}
                        />
                      </div>

                      {uploadedFiles.length > 0 && (
                        <div className="bot-upload-chips" aria-label="已上传素材">
                          {uploadedFiles.map((file) => (
                            <span key={file.path} className="bot-upload-chip" title={file.path}>
                              <IconFile size={12} aria-hidden="true" />
                              <span className="bot-upload-chip-name">{file.name}</span>
                              <span className="bot-upload-chip-size">{formatFileSize(file.size)}</span>
                              <button
                                type="button"
                                className="bot-upload-chip-remove"
                                aria-label={`移除 ${file.name}`}
                                onClick={() =>
                                  setUploadedFiles((prev) => prev.filter((item) => item.path !== file.path))
                                }
                              >
                                <IconX size={11} aria-hidden="true" />
                              </button>
                            </span>
                          ))}
                        </div>
                      )}

                      {(showTitleInput ||
                        showAttachInput ||
                        showDispatchOverrides ||
                        savingPresetOpen ||
                        jobTitle ||
                        jobAttachPaths ||
                        hasActiveDispatchOverride) && (
                        <div className="bot-composer-inline-fields">
                          {(showTitleInput || jobTitle) && (
                            <div className="bot-composer-inline-row">
                              <label htmlFor="bot-job-title" className="bot-composer-inline-label">
                                任务标题
                              </label>
                              <input
                                id="bot-job-title"
                                className="bot-composer-inline-input"
                                type="text"
                                value={jobTitle}
                                onChange={(e) => setJobTitle(e.target.value)}
                                placeholder="例如：Q4 发布会 90 秒 2K 宣传片（留空自动生成）"
                              />
                            </div>
                          )}
                          {(showAttachInput || jobAttachPaths) && (
                            <div className="bot-composer-inline-row">
                              <label htmlFor="bot-job-attach" className="bot-composer-inline-label">
                                路径附件
                              </label>
                              <input
                                id="bot-job-attach"
                                className="bot-composer-inline-input"
                                type="text"
                                value={jobAttachPaths}
                                onChange={(e) => setJobAttachPaths(e.target.value)}
                                placeholder="逗号分隔文件路径，自动复制到工作区 inbox/<job-id>/"
                              />
                            </div>
                          )}
                          {(showDispatchOverrides || hasActiveDispatchOverride) &&
                            (() => {
                              const overrideEffortMeta = effortOptionsForBotSelection(
                                selectedBotModelCatalog.groups,
                                selectedBotModelCatalog.isAntigravity,
                                overrideModelKey,
                              )
                              return (
                                <div className="bot-composer-override-row">
                                  <div className="bot-composer-override-field">
                                    <label htmlFor="bot-override-approval" className="bot-composer-override-label">
                                      本次权限
                                    </label>
                                    <select
                                      id="bot-override-approval"
                                      className="bot-composer-inline-select"
                                      value={overrideApprovalMode}
                                      onChange={(e) =>
                                        setOverrideApprovalMode(e.target.value as '' | BotApprovalMode)
                                      }
                                    >
                                      <option value="">
                                        沿用默认（{approvalModeShortLabel(selectedBot.default_approval_mode)}）
                                      </option>
                                      {BOT_APPROVAL_MODE_OPTIONS.map((opt) => (
                                        <option key={opt.value} value={opt.value}>
                                          {opt.shortLabel}
                                        </option>
                                      ))}
                                    </select>
                                  </div>
                                  <div className="bot-composer-override-field">
                                    <label htmlFor="bot-override-model" className="bot-composer-override-label">
                                      本次模型
                                    </label>
                                    <select
                                      id="bot-override-model"
                                      className="bot-composer-inline-select"
                                      value={overrideModelKey}
                                      onChange={(e) => {
                                        const nextKey = e.target.value
                                        setOverrideModelKey(nextKey)
                                        const nextMeta = effortOptionsForBotSelection(
                                          selectedBotModelCatalog.groups,
                                          selectedBotModelCatalog.isAntigravity,
                                          nextKey,
                                        )
                                        if (nextMeta.disabled) {
                                          setOverrideEffort(nextMeta.options[0]?.value || '')
                                        } else if (
                                          overrideEffort &&
                                          !nextMeta.options.some((opt) => opt.value === overrideEffort)
                                        ) {
                                          setOverrideEffort('')
                                        }
                                      }}
                                    >
                                      <option value="">沿用 Bot 默认模型</option>
                                      {selectedBotModelCatalog.groups.map((g) => (
                                        <option key={g.key} value={g.key}>
                                          {g.label}
                                        </option>
                                      ))}
                                    </select>
                                  </div>
                                  <div className="bot-composer-override-field">
                                    <label htmlFor="bot-override-effort" className="bot-composer-override-label">
                                      本次思考
                                    </label>
                                    <select
                                      id="bot-override-effort"
                                      className="bot-composer-inline-select"
                                      value={
                                        overrideEffortMeta.disabled
                                          ? overrideEffortMeta.options[0]?.value || ''
                                          : overrideEffort
                                      }
                                      disabled={overrideEffortMeta.disabled}
                                      onChange={(e) => setOverrideEffort(e.target.value)}
                                    >
                                      <option value="">{overrideEffortMeta.placeholder}</option>
                                      {overrideEffortMeta.options.map((opt) => (
                                        <option key={opt.value} value={opt.value}>
                                          {opt.label}
                                        </option>
                                      ))}
                                    </select>
                                  </div>
                                </div>
                              )
                            })()}
                          {savingPresetOpen && (
                            <div className="bot-composer-inline-row">
                              <label htmlFor="bot-preset-label" className="bot-composer-inline-label">
                                模板名称
                              </label>
                              <input
                                id="bot-preset-label"
                                className="bot-composer-inline-input"
                                type="text"
                                value={presetLabelInput}
                                onChange={(e) => setPresetLabelInput(e.target.value)}
                                placeholder="例如：90 秒 2K 宣传片"
                              />
                              <button
                                type="button"
                                className="bot-btn-secondary bot-btn-sm"
                                disabled={savingPreset || !presetLabelInput.trim() || !jobPrompt.trim()}
                                onClick={() => void handleSavePresetFromComposer()}
                              >
                                {savingPreset ? '保存中…' : '保存模板'}
                              </button>
                              <button
                                type="button"
                                className="bot-btn-ghost bot-btn-sm"
                                onClick={() => {
                                  setSavingPresetOpen(false)
                                  setPresetLabelInput('')
                                }}
                              >
                                取消
                              </button>
                            </div>
                          )}
                        </div>
                      )}

                      <div className="bot-composer-bar">
                        <div className="bot-composer-chips">
                          <button
                            type="button"
                            className="bot-chip-toggle"
                            aria-pressed={showTitleInput || Boolean(jobTitle)}
                            onClick={() => setShowTitleInput((v) => !v)}
                          >
                            <IconPlus size={12} aria-hidden="true" />
                            <span>任务标题</span>
                          </button>
                          <button
                            type="button"
                            className="bot-chip-toggle"
                            disabled={uploadingFiles}
                            onClick={() => fileInputRef.current?.click()}
                          >
                            <IconUpload size={12} aria-hidden="true" />
                            <span>{uploadingFiles ? '上传中…' : '上传素材'}</span>
                          </button>
                          <button
                            type="button"
                            className="bot-chip-toggle"
                            aria-pressed={showAttachInput || Boolean(jobAttachPaths)}
                            onClick={() => setShowAttachInput((v) => !v)}
                          >
                            <IconPaperclip size={12} aria-hidden="true" />
                            <span>路径附件</span>
                          </button>
                          <button
                            type="button"
                            className="bot-chip-toggle"
                            aria-pressed={showDispatchOverrides || hasActiveDispatchOverride}
                            onClick={() => setShowDispatchOverrides((v) => !v)}
                          >
                            <IconAdjustmentsHorizontal size={12} aria-hidden="true" />
                            <span>{hasActiveDispatchOverride ? '本次策略 · 已改写' : '本次策略'}</span>
                          </button>
                          {jobPrompt.trim() && (
                            <button
                              type="button"
                              className="bot-chip-toggle"
                              aria-pressed={savingPresetOpen}
                              onClick={() => setSavingPresetOpen((v) => !v)}
                            >
                              <IconBookmark size={12} aria-hidden="true" />
                              <span>存为模板</span>
                            </button>
                          )}
                        </div>

                        <div className="bot-composer-right">
                          <label className="bot-checkbox-label">
                            <input
                              type="checkbox"
                              checked={openAfterDispatch}
                              onChange={(e) => setOpenAfterDispatch(e.target.checked)}
                            />
                            <span>派单后打开</span>
                          </label>
                          <button
                            type="button"
                            className="bot-btn-primary bot-btn-sm"
                            disabled={dispatching || uploadingFiles || !jobPrompt.trim()}
                            onClick={() => void handleDispatchJob()}
                          >
                            <span>{dispatching ? '派单中…' : '派生会话执行'}</span>
                            <span className="bot-kbd-hint" aria-hidden="true">↵</span>
                          </button>
                        </div>
                      </div>
                    </div>

                    {/* Task Session History */}
                    <section className="bot-stream-section" aria-label="任务会话记录">
                      <div className="bot-stream-header">
                        <div className="bot-stream-title-wrap">
                          <h3 className="bot-section-title">任务会话记录</h3>
                          <span className="bot-count-pill">{botSessions.length}</span>
                        </div>
                        <span className="bot-stream-note">按最近活跃倒序</span>
                      </div>

                      {botSessions.length === 0 ? (
                        <div style={{ padding: '24px 0', fontSize: 12.5, color: 'var(--bot-muted)' }}>
                          暂无任务会话。在上方输入任务说明并点击「派生会话执行」开始第一单。
                        </div>
                      ) : (
                        <ul className="bot-session-list">
                          {botSessions.map((session) => {
                            const key = scopeKey(session.agent_id, session.id)
                            const status = sessionActivityStatus(session)
                            const sessAgent = agentsMap[session.agent_id]
                            const agentLabel = formatBotAgentLabel(session.agent_id, sessAgent)
                            const envLabel = formatBotEnvLabel(session.connection_id || undefined, sessAgent)
                            return (
                              <li key={key} className="bot-session-row">
                                <button
                                  type="button"
                                  className="bot-session-main"
                                  onClick={() =>
                                    navigate(
                                      `/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`,
                                    )
                                  }
                                >
                                  <span
                                    className={`bot-session-dot${status === 'running' ? ' is-running' : status === 'waiting_approval' ? ' is-waiting' : ''}`}
                                    aria-hidden="true"
                                  />
                                  <div className="bot-session-info">
                                    <div className="bot-session-name">{displaySessionTitle(session)}</div>
                                    <div className="bot-session-meta">
                                      <span>{agentLabel}</span>
                                      <span>·</span>
                                      <span>{envLabel}</span>
                                      <span>·</span>
                                      <span>{formatRelativeTime(session.updated_at)}</span>
                                      {status === 'running' && <span className="bot-badge is-accent">运行中</span>}
                                      {status === 'waiting_approval' && <span className="bot-badge is-amber">待确认</span>}
                                    </div>
                                  </div>
                                </button>
                                <div className="bot-session-actions">
                                  <button
                                    type="button"
                                    className="bot-btn-ghost bot-btn-sm"
                                    title="携带此任务会话上下文接力派给其他 Bot"
                                    onClick={() => handleStartHandoffFromSession(session)}
                                  >
                                    <IconTransfer size={13} aria-hidden="true" />
                                    <span>接力</span>
                                  </button>
                                  <button
                                    type="button"
                                    className="bot-btn-ghost bot-btn-sm"
                                    onClick={() => openFeedbackModal(selectedBot, key)}
                                  >
                                    <IconUpload size={13} aria-hidden="true" />
                                    <span>反哺经验</span>
                                  </button>
                                  <button
                                    type="button"
                                    className="bot-btn-ghost bot-btn-sm"
                                    onClick={() =>
                                      navigate(
                                        `/chat/${encodeURIComponent(session.id)}?agent_id=${encodeURIComponent(session.agent_id)}`,
                                      )
                                    }
                                  >
                                    <span>打开会话</span>
                                    <IconArrowRight size={13} aria-hidden="true" />
                                  </button>
                                </div>
                              </li>
                            )
                          })}
                        </ul>
                      )}

                      <div className="bot-stream-footer">
                        <IconInfoCircle size={13} aria-hidden="true" />
                        <span>每次派单都会在当前工作区派生干净隔离的新会话，自动加载 AGENTS.md 与技能库。</span>
                      </div>
                    </section>
                  </div>

                  {/* Right Capability Inspector (#FAFAFC) */}
                  <aside className="bot-inspector-col" aria-label="Bot 能力概览">
                    <div className="bot-insp-block">
                      <div className="bot-insp-head">
                        <span className="bot-eyebrow">执行策略 · 权限与模型</span>
                        <button
                          type="button"
                          className="bot-link-btn"
                          onClick={() => selectWorkbenchTab('settings')}
                        >
                          配置 →
                        </button>
                      </div>
                      <div className="bot-insp-card bot-policy-summary">
                        <div className="bot-policy-row">
                          <span className="bot-policy-key">默认权限</span>
                          <span className="bot-policy-val">
                            {approvalModeShortLabel(selectedBot.default_approval_mode)}
                          </span>
                        </div>
                        <div className="bot-policy-row">
                          <span className="bot-policy-key">模型与思考</span>
                          <span className="bot-policy-val">
                            {formatBotModelSummary(
                              selectedBot,
                              selectedBotModelCatalog.groups,
                              selectedBotModelCatalog.isAntigravity,
                            )}
                          </span>
                        </div>
                        <div className="bot-policy-row">
                          <span className="bot-policy-key">完成回告</span>
                          <span className="bot-policy-val">
                            {(selectedBot.default_notify_caller ?? true) ? '自动回告发起会话' : '静默执行'}
                          </span>
                        </div>
                      </div>
                    </div>

                    <div className="bot-insp-divider" aria-hidden="true" />

                    <div className="bot-insp-block">
                      <div className="bot-insp-head">
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          <span className="bot-eyebrow">角色宪章 · AGENTS.md</span>
                          <span className={`bot-status-caption${selectedBot.has_agents_md ? '' : ' is-warn'}`}>
                            · {selectedBot.has_agents_md ? '已生效' : '未创建'}
                          </span>
                        </div>
                        <button
                          type="button"
                          className="bot-link-btn"
                          onClick={() => selectWorkbenchTab('agents')}
                        >
                          编辑 →
                        </button>
                      </div>
                      <div className="bot-insp-card">
                        {selectedBot.agents_md_excerpt ||
                          (selectedBot.has_agents_md
                            ? '已配置工作区 AGENTS.md，点击右上角「编辑 →」查看或优化完整内容。'
                            : '尚未创建 AGENTS.md。前往「人设 AGENTS.md」标签即可一键用 AI 生成并保存。')}
                      </div>
                    </div>

                    <div className="bot-insp-divider" aria-hidden="true" />

                    <div className="bot-insp-block">
                      <div className="bot-insp-head">
                        <span className="bot-eyebrow">
                          已装载技能库 · SKILLS ({selectedBot.skills.length})
                        </span>
                        <button
                          type="button"
                          className="bot-link-btn"
                          onClick={handleStartNewSkill}
                        >
                          + 新建
                        </button>
                      </div>
                      {selectedBot.skills.length === 0 ? (
                        <div className="bot-insp-card" style={{ color: 'var(--bot-muted)' }}>
                          暂无工作区 Skill，可点击「+ 新建」或通过「反哺经验」自动沉淀。
                        </div>
                      ) : (
                        <div className="bot-insp-skill-list">
                          {selectedBot.skills.map((sk) => (
                            <button
                              key={sk.slug}
                              type="button"
                              className="bot-insp-skill-btn"
                              onClick={() => {
                                handleSelectSkill(sk.slug)
                                selectWorkbenchTab('skills')
                              }}
                            >
                              <span className="bot-insp-skill-slug">
                                <IconLayersLinked size={13} aria-hidden="true" />
                                <span>{sk.slug}</span>
                              </span>
                              {sk.description && <span className="bot-insp-skill-desc">{sk.description}</span>}
                            </button>
                          ))}
                        </div>
                      )}
                    </div>

                    <div className="bot-insp-divider" aria-hidden="true" />

                    <div className="bot-insp-block">
                      <span className="bot-eyebrow">跨会话快捷调用</span>
                      <div className="bot-field">
                        <span className="bot-field-hint">在任意会话输入框中键入 @ 呼出派工：</span>
                        <div className="bot-copy-box">
                          <code>@Bot:{selectedBot.slug}</code>
                          <button
                            type="button"
                            className="bot-icon-btn"
                            aria-label="复制 @Bot 提及"
                            onClick={() => copyMentionTag(selectedBot)}
                          >
                            <IconCopy size={13} aria-hidden="true" />
                          </button>
                        </div>
                      </div>
                      <div className="bot-field">
                        <span className="bot-field-hint">或通过 CLI 自动化触发：</span>
                        <div className="bot-copy-box is-dark">
                          <code>astrorder-agent bots invoke --bot {selectedBot.slug} …</code>
                          <button
                            type="button"
                            className="bot-icon-btn"
                            style={{ color: '#a1a1aa' }}
                            aria-label="复制 CLI 命令"
                            onClick={() => copyCliExample(selectedBot)}
                          >
                            <IconCopy size={13} aria-hidden="true" />
                          </button>
                        </div>
                      </div>
                    </div>
                  </aside>
                </div>
              </div>

              {/* Panel 2: AGENTS.md Full-Height Editor + AI Compliance Assistant (Stitch Screen #2) */}
              <div
                className="bot-tabpanel"
                role="tabpanel"
                aria-labelledby={`${baseTabId}-tab-agents`}
                hidden={activeTab !== 'agents'}
              >
                <div className="bot-subtoolbar">
                  <div className="bot-subtoolbar-left">
                    <IconFileText size={14} style={{ color: 'var(--bot-muted)', flex: 'none' }} aria-hidden="true" />
                    <span className="bot-filepath" title={docsQuery.data?.agents_md_path || `${selectedBot.workspace}/AGENTS.md`}>
                      {docsQuery.data?.agents_md_path || `${selectedBot.workspace}/AGENTS.md`}
                    </span>
                    <span className={`bot-status-caption${agentsDirty ? ' is-warn' : ''}`}>
                      · {agentsDirty ? '未保存修改' : docsQuery.data?.agents_md_exists ? '已同步' : '新建草案'}
                    </span>
                  </div>
                  <div className="bot-subtoolbar-right">
                    <div className="bot-segmented" role="group" aria-label="编辑与预览切换">
                      <button
                        type="button"
                        className="bot-segmented-btn"
                        aria-pressed={agentsViewMode === 'edit'}
                        onClick={() => setAgentsViewMode('edit')}
                      >
                        编辑
                      </button>
                      <button
                        type="button"
                        className="bot-segmented-btn"
                        aria-pressed={agentsViewMode === 'preview'}
                        onClick={() => setAgentsViewMode('preview')}
                      >
                        预览
                      </button>
                    </div>
                    <button
                      type="button"
                      className="bot-btn-secondary bot-btn-sm"
                      disabled={agentsOptimizing || agentsSaving}
                      onClick={() => void handleOptimizeAgentsMd()}
                    >
                      <IconWand size={14} aria-hidden="true" />
                      <span>{agentsOptimizing ? '优化中…' : 'AI 智能优化'}</span>
                    </button>
                    <button
                      type="button"
                      className="bot-btn-primary bot-btn-sm"
                      disabled={agentsSaving}
                      onClick={() => void handleSaveAgentsMd()}
                    >
                      <IconDeviceFloppy size={14} aria-hidden="true" />
                      <span>{agentsSaving ? '保存中…' : '保存 AGENTS.md'}</span>
                    </button>
                  </div>
                </div>

                <div className="bot-editor-split">
                  <div className="bot-editor-canvas">
                    {agentsViewMode === 'edit' ? (
                      <>
                        <label htmlFor="bot-agents-editor" className="bot-sr-only">
                          AGENTS.md 内容编辑器
                        </label>
                        <textarea
                          id="bot-agents-editor"
                          className="bot-editor-textarea"
                          value={agentsDraft}
                          onChange={(e) => setAgentsDraft(e.target.value)}
                          onKeyDown={(e) => {
                            if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
                              e.preventDefault()
                              void handleSaveAgentsMd()
                            }
                          }}
                          placeholder="在此编写 Bot 的角色定位、输入协作约定、交付自检标准与安全边界；也可以在右侧输入指令让 AI 优化。"
                        />
                      </>
                    ) : (
                      <div className="bot-md-preview">
                        {agentsDraft.trim() ? (
                          <ReactMarkdown remarkPlugins={[remarkGfm]}>{agentsDraft}</ReactMarkdown>
                        ) : (
                          <p style={{ color: 'var(--bot-muted)' }}>当前尚未编写 AGENTS.md 内容。</p>
                        )}
                      </div>
                    )}
                  </div>

                  <aside className="bot-assistant-col" aria-label="AI 规范助手">
                    <div className="bot-insp-head">
                      <span className="bot-section-title">AI 规范检查助手</span>
                      <span className="bot-count-pill">
                        {completenessChecks.filter((c) => c.ok).length}/{completenessChecks.length}
                      </span>
                    </div>

                    <div className="bot-insp-card">
                      <span className="bot-eyebrow" style={{ display: 'block', marginBottom: 10 }}>
                        结构完整性检查
                      </span>
                      <ul className="bot-check-list">
                        {completenessChecks.map((item) => (
                          <li key={item.label} className="bot-check-item">
                            <span className="bot-check-left">
                              {item.ok ? (
                                <IconCircleCheck size={15} className="bot-check-ok" aria-hidden="true" />
                              ) : (
                                <IconCircleDashed size={15} className="bot-check-miss" aria-hidden="true" />
                              )}
                              <span>{item.label}</span>
                            </span>
                          </li>
                        ))}
                      </ul>
                    </div>

                    <div className="bot-field">
                      <span className="bot-eyebrow">快捷 AI 调优指令</span>
                      <div className="bot-quick-grid">
                        {QUICK_AI_TWEAKS.map((tweak) => (
                          <button
                            key={tweak.label}
                            type="button"
                            className="bot-quick-chip"
                            onClick={() =>
                              setAgentsTunePrompt((prev) => (prev.trim() ? `${prev.trim()}\n${tweak.prompt}` : tweak.prompt))
                            }
                          >
                            {tweak.label}
                          </button>
                        ))}
                      </div>
                    </div>

                    <div className="bot-field">
                      <label htmlFor="bot-agents-tune-input" className="bot-sr-only">
                        输入 AI 规范调优指令
                      </label>
                      <textarea
                        id="bot-agents-tune-input"
                        className="bot-textarea"
                        rows={4}
                        value={agentsTunePrompt}
                        onChange={(e) => setAgentsTunePrompt(e.target.value)}
                        placeholder="告诉 AI 如何调整这份规范…（如：让交付验收更严格、补充输出命名规则）"
                      />
                      <button
                        type="button"
                        className="bot-btn-primary"
                        disabled={agentsOptimizing || agentsSaving}
                        onClick={() => void handleOptimizeAgentsMd(agentsTunePrompt)}
                      >
                        <IconSparkles size={14} aria-hidden="true" />
                        <span>{agentsOptimizing ? '正在应用优化…' : '应用优化'}</span>
                      </button>
                    </div>
                  </aside>
                </div>
              </div>

              {/* Panel 3: .agents/skills/ Full-Height Master-Detail Editor */}
              <div
                className="bot-tabpanel"
                role="tabpanel"
                aria-labelledby={`${baseTabId}-tab-skills`}
                hidden={activeTab !== 'skills'}
              >
                <div className="bot-editor-split">
                  <div className="bot-skills-sidebar">
                    <div className="bot-insp-head">
                      <span className="bot-eyebrow">SKILLS ({docsQuery.data?.skills.length || 0})</span>
                      <button type="button" className="bot-link-btn" onClick={handleStartNewSkill}>
                        + 新建 Skill
                      </button>
                    </div>

                    {(docsQuery.data?.skills || []).length === 0 && !creatingSkill ? (
                      <p className="bot-field-hint">
                        当前工作区尚无 Skill，点击上方「+ 新建 Skill」或通过「反哺经验」自动沉淀。
                      </p>
                    ) : (
                      <ul className="bot-skill-list">
                        {(docsQuery.data?.skills || []).map((sk) => (
                          <li key={sk.slug}>
                            <button
                              type="button"
                              className="bot-skill-btn"
                              aria-current={!creatingSkill && selectedSkillSlug === sk.slug ? 'true' : undefined}
                              onClick={() => handleSelectSkill(sk.slug)}
                            >
                              <span className="bot-skill-name">{sk.slug}</span>
                              {sk.description && <span className="bot-skill-desc">{sk.description}</span>}
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}

                    {(docsQuery.data?.referenced_skills || []).length > 0 && (
                      <div className="bot-ref-skills-block">
                        <span className="bot-eyebrow">引用的外部 / 全局技能</span>
                        <ul className="bot-ref-skill-list">
                          {(docsQuery.data?.referenced_skills || []).map((refSlug) => (
                            <li
                              key={refSlug}
                              className="bot-ref-skill-item"
                              title="在 AGENTS.md 中引用，位于全局或系统技能目录"
                            >
                              <IconLayersLinked size={12} aria-hidden="true" />
                              <span>{refSlug}</span>
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                  </div>

                  <div className="bot-editor-canvas">
                    {creatingSkill || selectedSkillSlug ? (
                      <>
                        <div className="bot-subtoolbar">
                          <div className="bot-subtoolbar-left">
                            {creatingSkill ? (
                              <>
                                <label htmlFor="bot-new-skill-slug" className="bot-sr-only">
                                  新建 Skill 目录标识
                                </label>
                                <input
                                  id="bot-new-skill-slug"
                                  className="bot-composer-inline-input"
                                  style={{ width: 240 }}
                                  type="text"
                                  value={newSkillSlug}
                                  onChange={(e) => setNewSkillSlug(e.target.value)}
                                  placeholder="英文目录标识，如 promo-video-production"
                                />
                              </>
                            ) : (
                              <span className="bot-filepath">
                                .agents/skills/{selectedSkillSlug}/SKILL.md
                              </span>
                            )}
                          </div>
                          <div className="bot-subtoolbar-right">
                            <div className="bot-segmented" role="group" aria-label="Skill 编辑与预览切换">
                              <button
                                type="button"
                                className="bot-segmented-btn"
                                aria-pressed={skillViewMode === 'edit'}
                                onClick={() => setSkillViewMode('edit')}
                              >
                                编辑
                              </button>
                              <button
                                type="button"
                                className="bot-segmented-btn"
                                aria-pressed={skillViewMode === 'preview'}
                                onClick={() => setSkillViewMode('preview')}
                              >
                                预览
                              </button>
                            </div>
                            <button
                              type="button"
                              className="bot-btn-primary bot-btn-sm"
                              disabled={skillSaving}
                              onClick={() => void handleSaveSkill()}
                            >
                              <IconDeviceFloppy size={14} aria-hidden="true" />
                              <span>{skillSaving ? '保存中…' : '保存 Skill'}</span>
                            </button>
                          </div>
                        </div>

                        {selectedSkillDoc?.files && selectedSkillDoc.files.length > 0 && (
                          <div className="bot-skill-files-bar" aria-label="Skill 关联文件">
                            <span className="bot-skill-files-label">关联文件</span>
                            <div className="bot-skill-files-list">
                              {selectedSkillDoc.files.map((relPath) => (
                                <span key={relPath} className="bot-skill-file-pill" title={relPath}>
                                  <IconFile size={11} aria-hidden="true" />
                                  <span>{relPath}</span>
                                </span>
                              ))}
                            </div>
                          </div>
                        )}

                        {skillViewMode === 'edit' ? (
                          <>
                            <label htmlFor="bot-skill-editor" className="bot-sr-only">
                              SKILL.md 内容编辑器
                            </label>
                            <textarea
                              id="bot-skill-editor"
                              className="bot-editor-textarea"
                              value={skillDraft}
                              onChange={(e) => setSkillDraft(e.target.value)}
                            />
                          </>
                        ) : (
                          <div className="bot-md-preview">
                            <ReactMarkdown remarkPlugins={[remarkGfm]}>{skillDraft}</ReactMarkdown>
                          </div>
                        )}
                      </>
                    ) : (
                      <div className="bot-empty">
                        <p>选择左侧已有 Skill 查看编辑，或点击「+ 新建 Skill」沉淀一份标准作业流程。</p>
                      </div>
                    )}
                  </div>
                </div>
              </div>

              {/* Panel 4: Settings & Cross-Session Usage */}
              <div
                className="bot-tabpanel"
                role="tabpanel"
                aria-labelledby={`${baseTabId}-tab-settings`}
                hidden={activeTab !== 'settings'}
              >
                <div className="bot-settings-scroll">
                  <div className="bot-settings-inner">
                    <section className="bot-settings-section" aria-label="基础配置">
                      <div>
                        <h3 className="bot-settings-heading">基础配置</h3>
                        <p className="bot-settings-sub">管理 Bot 名称、调用标识、绑定工作区与默认执行引擎。</p>
                      </div>

                      <div className="bot-field-row">
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-name" className="bot-label">
                            Bot 名称
                          </label>
                          <input
                            id="cfg-bot-name"
                            className="bot-input"
                            value={cfgName}
                            onChange={(e) => setCfgName(e.target.value)}
                          />
                        </div>
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-slug" className="bot-label">
                            调用标识（Slug）
                          </label>
                          <input
                            id="cfg-bot-slug"
                            className="bot-input"
                            value={cfgSlug}
                            onChange={(e) => setCfgSlug(e.target.value)}
                          />
                        </div>
                      </div>

                      <div className="bot-field">
                        <label htmlFor="cfg-bot-desc" className="bot-label">
                          一句话简介
                        </label>
                        <input
                          id="cfg-bot-desc"
                          className="bot-input"
                          value={cfgDesc}
                          onChange={(e) => setCfgDesc(e.target.value)}
                        />
                      </div>

                      <div className="bot-field-row">
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-ws" className="bot-label">
                            绑定工作区
                          </label>
                          <input
                            id="cfg-bot-ws"
                            className="bot-input"
                            value={cfgWorkspace}
                            onChange={(e) => setCfgWorkspace(e.target.value)}
                          />
                        </div>
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-conn" className="bot-label">
                            运行环境
                          </label>
                          <select
                            id="cfg-bot-conn"
                            className="bot-select"
                            value={normalizeBotConnection(cfgConn)}
                            onChange={(e) => setCfgConn(e.target.value)}
                          >
                            {cfgEnvOptions.map((opt) => (
                              <option key={opt.value || 'local'} value={opt.value}>
                                {opt.label}
                              </option>
                            ))}
                          </select>
                        </div>
                      </div>

                      <div className="bot-field-row">
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-agent" className="bot-label">
                            默认执行 Agent
                          </label>
                          <select
                            id="cfg-bot-agent"
                            className="bot-select"
                            value={cfgAgentId}
                            onChange={(e) => {
                              cfgModelDirtyRef.current = true
                              setCfgAgentId(e.target.value)
                              setCfgModelKey('')
                              setCfgEffort('')
                            }}
                          >
                            {agents.map((a) => {
                              const agentLabel = formatBotAgentLabel(a.id, a)
                              const envLabel = formatBotEnvLabel(a.connection_id || undefined, a)
                              const display = a.name || `${agentLabel} · ${envLabel}`
                              const suffix = `${agentLabel} · ${envLabel}`
                              const redundant =
                                display.toLowerCase().includes(agentLabel.toLowerCase()) &&
                                display.toLowerCase().includes(envLabel.toLowerCase())
                              return (
                                <option key={a.id} value={a.id}>
                                  {redundant ? display : `${display} (${suffix})`}
                                </option>
                              )
                            })}
                            {!agents.some((a) => a.id === cfgAgentId) && cfgAgentId && (
                              <option value={cfgAgentId}>{formatBotAgentLabel(cfgAgentId, undefined)}</option>
                            )}
                          </select>
                        </div>
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-approval" className="bot-label">
                            默认执行权限
                          </label>
                          <select
                            id="cfg-bot-approval"
                            className="bot-select"
                            value={cfgApprovalMode}
                            onChange={(e) => setCfgApprovalMode(normalizeBotApprovalMode(e.target.value))}
                          >
                            {BOT_APPROVAL_MODE_OPTIONS.map((opt) => (
                              <option key={opt.value} value={opt.value}>
                                {opt.label}
                              </option>
                            ))}
                          </select>
                          <span className="bot-field-hint">{approvalModeDescription(cfgApprovalMode)}</span>
                        </div>
                      </div>

                      {(() => {
                        const effortMeta = effortOptionsForBotSelection(
                          settingsModelCatalog.groups,
                          settingsModelCatalog.isAntigravity,
                          cfgModelKey,
                        )
                        const hasSelectedInCatalog =
                          !cfgModelKey || settingsModelCatalog.groups.some((g) => g.key === cfgModelKey)
                        const fallbackModelLabel = (() => {
                          if (!cfgModelKey) return ''
                          try {
                            const parsed = JSON.parse(cfgModelKey) as [string, string]
                            if (Array.isArray(parsed)) return parsed[1] || cfgModelKey
                          } catch {
                            // ignore
                          }
                          return cfgModelKey
                        })()
                        return (
                          <div className="bot-field-row">
                            <div className="bot-field">
                              <label htmlFor="cfg-bot-model" className="bot-label">
                                默认模型
                              </label>
                              <select
                                id="cfg-bot-model"
                                className="bot-select"
                                value={cfgModelKey}
                                onChange={(e) => {
                                  const nextKey = e.target.value
                                  cfgModelDirtyRef.current = true
                                  setCfgModelKey(nextKey)
                                  const nextMeta = effortOptionsForBotSelection(
                                    settingsModelCatalog.groups,
                                    settingsModelCatalog.isAntigravity,
                                    nextKey,
                                  )
                                  if (nextMeta.disabled) {
                                    setCfgEffort(nextMeta.options[0]?.value || '')
                                  } else if (
                                    cfgEffort &&
                                    !nextMeta.options.some((opt) => opt.value === cfgEffort)
                                  ) {
                                    setCfgEffort('')
                                  }
                                }}
                              >
                                <option value="">
                                  {settingsModelCatalog.isLoading ? '正在加载模型列表…' : '沿用 Agent 默认模型'}
                                </option>
                                {!hasSelectedInCatalog && cfgModelKey && (
                                  <option value={cfgModelKey}>{fallbackModelLabel}</option>
                                )}
                                {settingsModelCatalog.groups.map((g) => (
                                  <option key={g.key} value={g.key}>
                                    {g.label}
                                  </option>
                                ))}
                              </select>
                            </div>
                            <div className="bot-field">
                              <label htmlFor="cfg-bot-effort" className="bot-label">
                                默认思考强度
                              </label>
                              <select
                                id="cfg-bot-effort"
                                className="bot-select"
                                value={effortMeta.disabled ? (effortMeta.options[0]?.value || '') : cfgEffort}
                                disabled={effortMeta.disabled}
                                onChange={(e) => {
                                  cfgModelDirtyRef.current = true
                                  setCfgEffort(e.target.value)
                                }}
                              >
                                <option value="">{effortMeta.placeholder}</option>
                                {effortMeta.options.map((opt) => (
                                  <option key={opt.value} value={opt.value}>
                                    {opt.label}
                                  </option>
                                ))}
                              </select>
                            </div>
                          </div>
                        )
                      })()}

                      <div className="bot-field-row">
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-icon" className="bot-label">
                            图标类型
                          </label>
                          <select
                            id="cfg-bot-icon"
                            className="bot-select"
                            value={cfgIcon}
                            onChange={(e) => setCfgIcon(e.target.value)}
                          >
                            <option value="robot">通用 Bot</option>
                            <option value="video">视频制作</option>
                            <option value="doc">文档写作</option>
                            <option value="code">工程研发</option>
                            <option value="design">视觉设计</option>
                            <option value="data">数据分析</option>
                            <option value="ops">运维巡检</option>
                            <option value="briefcase">办公助理</option>
                            <option value="spark">创意策划</option>
                          </select>
                        </div>
                        <div className="bot-field">
                          <label htmlFor="cfg-bot-tags" className="bot-label">
                            标签（逗号分隔）
                          </label>
                          <input
                            id="cfg-bot-tags"
                            className="bot-input"
                            value={cfgTags}
                            onChange={(e) => setCfgTags(e.target.value)}
                          />
                        </div>
                      </div>

                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        <label className="bot-checkbox-label">
                          <input
                            type="checkbox"
                            checked={cfgNotifyCaller}
                            onChange={(e) => setCfgNotifyCaller(e.target.checked)}
                          />
                          <span>跨会话派单完成后自动回告发起会话（附带产物路径与交付摘要）</span>
                        </label>
                        <label className="bot-checkbox-label">
                          <input
                            type="checkbox"
                            checked={cfgOfficeMode}
                            onChange={(e) => setCfgOfficeMode(e.target.checked)}
                          />
                          <span>启用办公空间模式</span>
                        </label>
                      </div>

                      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                        <button
                          type="button"
                          className="bot-btn-primary"
                          disabled={cfgSaving}
                          onClick={() => void handleSaveSettings()}
                        >
                          <IconCheck size={15} aria-hidden="true" />
                          <span>{cfgSaving ? '保存中…' : '保存配置'}</span>
                        </button>
                      </div>
                    </section>

                    <section className="bot-settings-section" aria-label="常用派单模板">
                      <div>
                        <h3 className="bot-settings-heading">常用派单模板</h3>
                        <p className="bot-settings-sub">
                          将高频任务保存为快捷按钮，展示在「总览与派单」输入框上方，一键填入任务标题与简报。
                        </p>
                      </div>

                      {cfgPresets.length > 0 && (
                        <ul className="bot-preset-manage-list">
                          {cfgPresets.map((preset) => (
                            <li key={preset.id || preset.label} className="bot-preset-manage-item">
                              <div className="bot-preset-manage-copy">
                                <div className="bot-preset-manage-head">
                                  <span className="bot-preset-manage-name">{preset.label}</span>
                                  {preset.title && (
                                    <span className="bot-preset-manage-title">标题：{preset.title}</span>
                                  )}
                                </div>
                                <div className="bot-preset-manage-prompt">{preset.prompt}</div>
                              </div>
                              <button
                                type="button"
                                className="bot-btn-ghost bot-btn-sm"
                                disabled={savingPreset}
                                aria-label={`删除模板 ${preset.label}`}
                                onClick={() =>
                                  void handlePersistPresets(
                                    cfgPresets.filter((item) =>
                                      preset.id ? item.id !== preset.id : item.label !== preset.label,
                                    ),
                                    `已移除派单模板「${preset.label}」`,
                                  )
                                }
                              >
                                <IconTrash size={13} aria-hidden="true" />
                                <span>移除</span>
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}

                      <div className="bot-preset-add-card">
                        <div className="bot-field-row">
                          <div className="bot-field">
                            <label htmlFor="cfg-new-preset-label" className="bot-label">
                              模板名称
                            </label>
                            <input
                              id="cfg-new-preset-label"
                              className="bot-input"
                              value={newPresetLabel}
                              onChange={(e) => setNewPresetLabel(e.target.value)}
                              placeholder="例如：90 秒 2K 宣传片"
                            />
                          </div>
                          <div className="bot-field">
                            <label htmlFor="cfg-new-preset-title" className="bot-label">
                              默认任务标题（可选）
                            </label>
                            <input
                              id="cfg-new-preset-title"
                              className="bot-input"
                              value={newPresetTitle}
                              onChange={(e) => setNewPresetTitle(e.target.value)}
                              placeholder="例如：2K 产品宣传片制作"
                            />
                          </div>
                        </div>
                        <div className="bot-field">
                          <label htmlFor="cfg-new-preset-prompt" className="bot-label">
                            模板任务简报
                          </label>
                          <textarea
                            id="cfg-new-preset-prompt"
                            className="bot-textarea"
                            rows={2}
                            value={newPresetPrompt}
                            onChange={(e) => setNewPresetPrompt(e.target.value)}
                            placeholder="填入点击后自动填充的任务说明、交付要求或验收标准"
                          />
                        </div>
                        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                          <button
                            type="button"
                            className="bot-btn-secondary bot-btn-sm"
                            disabled={savingPreset || !newPresetLabel.trim() || !newPresetPrompt.trim()}
                            onClick={() => {
                              const label = newPresetLabel.trim()
                              const next: BotPreset[] = [
                                ...cfgPresets,
                                {
                                  id: '',
                                  label,
                                  title: newPresetTitle.trim() || undefined,
                                  prompt: newPresetPrompt.trim(),
                                },
                              ]
                              setNewPresetLabel('')
                              setNewPresetTitle('')
                              setNewPresetPrompt('')
                              void handlePersistPresets(next, `已添加派单模板「${label}」`)
                            }}
                          >
                            <IconPlus size={13} aria-hidden="true" />
                            <span>添加模板</span>
                          </button>
                        </div>
                      </div>
                    </section>

                    <section className="bot-settings-section" aria-label="跨会话调用示例">
                      <div>
                        <h3 className="bot-settings-heading">跨会话调用示例</h3>
                        <p className="bot-settings-sub">
                          在任意会话中输入 <code>@Bot:{selectedBot.slug}</code> 即可派工，或由 Agent 直接执行：
                        </p>
                      </div>
                      <pre className="bot-cli-snippet">
                        {`astrorder-agent bots invoke \\\n  --bot ${selectedBot.slug} \\\n  --operation-id op-${selectedBot.slug}-1 \\\n  --title "新任务标题" \\\n  --prompt "自包含的任务说明与交付标准" \\\n  --attach "/path/to/reference.md"`}
                      </pre>
                    </section>

                    <section className="bot-settings-section" aria-label="取消 Bot 注册">
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap' }}>
                        <div>
                          <h3 className="bot-settings-heading">取消 Bot 注册</h3>
                          <p className="bot-settings-sub">
                            仅从 Bot 列表移除登记，<strong>不会删除</strong>工作区中的 <code>AGENTS.md</code>、
                            <code>.agents/skills/</code> 或历史会话。
                          </p>
                        </div>
                        {!confirmDelete ? (
                          <button
                            type="button"
                            className="bot-btn-danger"
                            onClick={() => setConfirmDelete(true)}
                          >
                            <IconTrash size={14} aria-hidden="true" />
                            <span>取消注册</span>
                          </button>
                        ) : (
                          <div style={{ display: 'flex', gap: 8 }}>
                            <button
                              type="button"
                              className="bot-btn-danger"
                              disabled={deleting}
                              onClick={() => void handleDeleteBot()}
                            >
                              {deleting ? '移除中…' : '确认移除'}
                            </button>
                            <button
                              type="button"
                              className="bot-btn-ghost"
                              onClick={() => setConfirmDelete(false)}
                            >
                              保留
                            </button>
                          </div>
                        )}
                      </div>
                    </section>
                  </div>
                </div>
              </div>
            </div>
          </>
        )}
      </main>

      <BotCreateModal
        opened={modalOpen}
        initialMode={modalMode}
        initialDistill={modalDistill}
        feedbackBot={feedbackBot}
        onClose={() => setModalOpen(false)}
        onSaved={(botId) => {
          setSelectedBotId(botId)
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'bot-docs', botId] })
        }}
      />
    </div>
  )
}
