import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Autocomplete,
  Button,
  Checkbox,
  Collapse,
  Group,
  Modal,
  PasswordInput,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconArrowRight,
  IconArrowUpRight,
  IconBriefcase,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconCode,
  IconCopy,
  IconDownload,
  IconKey,
  IconLoader2,
  IconRefresh,
  IconRocket,
  IconShieldCheck,
  IconSparkles,
  IconTerminal2,
  IconX,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import {
  api,
  type AiProviderProtocol,
  type OnboardingConfigureResult,
  type ToolchainInstallJob,
  type ToolchainItemStatus,
  type ToolchainOAuthSession,
} from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { BrandMark } from '../../components/BrandMark'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, AgentKind } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { broadcastAiProvidersSync } from '../meetings/meetingSessionStore'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { hasExplicitAppProfile, setAppProfile, type AppProfile } from './appProfile'
import './onboardingWizard.css'

export const OPEN_ONBOARDING_WIZARD_EVENT = 'astrorder:open-onboarding-wizard'

export function openOnboardingWizard(): void {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(OPEN_ONBOARDING_WIZARD_EVENT))
}

export interface OnboardingWizardProps {
  /** Whether backend connection/bootstrap has settled enough to know existing projects/sessions. */
  ready: boolean
  hasExistingData: boolean
  /** Override for tests or explicit preview */
  forceOpen?: boolean
  onSelected?: (profile: AppProfile) => void
}

const OAUTH_SUPPORTED_KINDS = new Set<AgentKind>(['codex', 'antigravity', 'grok'])

function openExternalAuthUrl(url: string) {
  if (!url) return
  const desktop =
    typeof window !== 'undefined'
      ? (window as unknown as { astrorderDesktop?: { openExternal?: (target: string) => Promise<void> } })
          .astrorderDesktop
      : undefined
  if (desktop?.openExternal) {
    void desktop.openExternal(url).catch(() => {
      window.open(url, '_blank', 'noopener,noreferrer')
    })
    return
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}

interface EngineCatalogMeta {
  kind: AgentKind
  name: string
  tagline: string
  recommended?: boolean
  supportsApiKey: boolean
  defaultModels: string[]
}

const ENGINE_META_LIST: EngineCatalogMeta[] = [
  {
    kind: 'codex',
    name: 'OpenAI Codex',
    tagline: '推荐首选 · 生态成熟、上手最快，支持官方账号免 Key 登录或自定义 OpenAI 兼容 API Key',
    recommended: true,
    supportsApiKey: true,
    defaultModels: ['gpt-5.4', 'gpt-5.3-codex', 'o4-mini'],
  },
  {
    kind: 'antigravity',
    name: 'Antigravity',
    tagline: 'Google 多模态 Agent 平台，支持账号直连与长上下文协作',
    supportsApiKey: false,
    defaultModels: [],
  },
  {
    kind: 'hermes',
    name: 'Hermes Agent',
    tagline: '高可定制开源助手，原生适配 OpenAI / DeepSeek / 通义千问等主流模型接口',
    supportsApiKey: true,
    defaultModels: ['deepseek-chat', 'gpt-4.1-mini', 'qwen-plus'],
  },
  {
    kind: 'grok',
    name: 'Grok CLI',
    tagline: 'xAI 终端助手引擎，适合快速问答与结构化分析',
    supportsApiKey: true,
    defaultModels: ['grok-3', 'grok-3-mini'],
  },
]

interface KeyPreset {
  id: string
  label: string
  providerName: string
  protocol: AiProviderProtocol
  baseUrl: string
  models: string[]
}

const KEY_PRESETS: KeyPreset[] = [
  {
    id: 'openai',
    label: 'OpenAI 官方',
    providerName: 'OpenAI',
    protocol: 'openai',
    baseUrl: 'https://api.openai.com/v1',
    models: ['gpt-5.4', 'gpt-4.1-mini', 'gpt-4o-mini'],
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    providerName: 'DeepSeek',
    protocol: 'openai',
    baseUrl: 'https://api.deepseek.com/v1',
    models: ['deepseek-chat', 'deepseek-reasoner'],
  },
  {
    id: 'qwen',
    label: '通义千问',
    providerName: '通义千问',
    protocol: 'openai',
    baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    models: ['qwen-plus', 'qwen-max', 'qwen-turbo'],
  },
  {
    id: 'custom',
    label: '自定义兼容接口',
    providerName: '自定义接口',
    protocol: 'openai',
    baseUrl: '',
    models: [],
  },
]

interface EngineOption {
  kind: AgentKind
  name: string
  tagline: string
  recommended?: boolean
  supportsApiKey: boolean
  installed: boolean
  authConfigured: boolean
  version?: string
  liveAgent?: Agent
  requiresDaemonRestart?: boolean
  authHint?: string
  defaultModels: string[]
}

type StepIndex = 1 | 2 | 3 | 4
type StageStatus = 'pending' | 'running' | 'done' | 'warn'

interface SetupProgress {
  profile: StageStatus
  configure: StageStatus
  capabilities: StageStatus
  session: StageStatus
}

function buildOnboardingAssistantPrompt(
  profile: AppProfile,
  engineName: string,
  result: OnboardingConfigureResult | null,
): string {
  const modeLabel = profile === 'office' ? '办公模式' : '研发模式'
  const skillsText =
    result?.injected_skills && result.injected_skills.length > 0
      ? result.injected_skills.join('、')
      : '星序基础工作区与会话技能'
  const workspaceText = result?.workspace || '默认个人工作空间'
  const llmSummary =
    result?.universal_llm_mode === 'agent'
      ? '通用模型已免 API Key 直连当前引擎'
      : result?.universal_llm_mode === 'api_key'
        ? '通用模型已自动同步你填写的 API 供应商'
        : '通用模型可随时在「设置 → AI 供应商」调整'

  const styleHint =
    profile === 'office'
      ? '请全程使用亲切、自然、完全去技术化的大白话（避免出现 CLI、JSON、Git、Daemon 等程序员术语）。'
      : '请用简洁清晰、工程师友好的语言沟通，同时避免铺陈冗长文档。'

  return [
    '【星序初始化配置助手任务】',
    `用户刚刚通过星序（Astrorder）初始化向导接入了 ${engineName}，选择了「${modeLabel}」。`,
    `当前已自动完成：工作目录 ${workspaceText}；${llmSummary}；已注入星序 CLI 与官方技能（${skillsText}）。`,
    '',
    '请你作为用户的「星序配置助手」，主动完成以下首轮引导：',
    `1. ${styleHint}`,
    '2. 用 3 句以内的话欢迎用户，并简明介绍星序现在已经可以直接帮他做的核心事情（例如对话协作、文档起草与精修、会议纪要、周报复盘、专属小助手等）。',
    '3. 给出 3 个轻松、可直接回答的小问题，帮他一步步完成星序个性化配置：',
    '   - 你的日常岗位/角色和偏好的回复风格（如简洁干练 / 详细结构化 / 温和友好）；',
    '   - 你最常处理的 1-2 类工作或常用文件夹路径（我可以帮你规划工作区或创建专属助手）；',
    '   - 还有哪些想马上试用或调整的星序功能。',
    '4. 回复末尾告诉用户：直接用自然语言回复即可，也可以随时跳过，直接告诉我你今天要处理的第一件事。',
  ].join('\n')
}

export function OnboardingWizard({
  ready,
  hasExistingData,
  forceOpen = false,
  onSelected,
}: OnboardingWizardProps) {
  const navigate = useNavigate()
  const hydrated = usePluginSettingsStore((s) => s.hydrated)
  const agentsMap = useAstrorderStore((s) => s.agents)
  const hydrateBootstrap = useAstrorderStore((s) => s.hydrateBootstrap)

  const [opened, setOpened] = useState(forceOpen)
  const [step, setStep] = useState<StepIndex>(1)
  const [profile, setProfile] = useState<AppProfile>('office')

  // Step 2: Engine detection & installation
  const [loadingToolchain, setLoadingToolchain] = useState(false)
  const [toolchainItems, setToolchainItems] = useState<ToolchainItemStatus[]>([])
  const [selectedKind, setSelectedKind] = useState<AgentKind>('codex')
  const [showMoreEngines, setShowMoreEngines] = useState(false)
  const [installJob, setInstallJob] = useState<ToolchainInstallJob | null>(null)
  const [installing, setInstalling] = useState(false)
  const [activatingDaemon, setActivatingDaemon] = useState(false)
  const installPollRef = useRef<number | null>(null)

  // Step 3: OAuth login, API Key & Model selection
  const [useCustomKey, setUseCustomKey] = useState(false)
  const [skipKeyExplicit, setSkipKeyExplicit] = useState(false)
  const [presetId, setPresetId] = useState('openai')
  const [providerName, setProviderName] = useState('OpenAI')
  const [protocol, setProtocol] = useState<AiProviderProtocol>('openai')
  const [baseUrl, setBaseUrl] = useState('https://api.openai.com/v1')
  const [apiKey, setApiKey] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [loadingModels, setLoadingModels] = useState(false)
  const [oauthSession, setOauthSession] = useState<ToolchainOAuthSession | null>(null)
  const [oauthStarting, setOauthStarting] = useState(false)
  const oauthPollRef = useRef<number | null>(null)
  const activeOAuthIdRef = useRef<string | null>(null)
  const oauthGenRef = useRef(0)

  // Step 4: Configure, inject CLI/Skills & launch assistant session
  const [configuring, setConfiguring] = useState(false)
  const [setupProgress, setSetupProgress] = useState<SetupProgress>({
    profile: 'pending',
    configure: 'pending',
    capabilities: 'pending',
    session: 'pending',
  })
  const [configResult, setConfigResult] = useState<OnboardingConfigureResult | null>(null)
  const [setupError, setSetupError] = useState<string | null>(null)

  // Open automatically on fresh launch
  useEffect(() => {
    if (forceOpen) {
      setOpened(true)
      return
    }
    if (!hydrated || !ready) return
    if (hasExplicitAppProfile()) {
      return
    }
    if (hasExistingData) {
      // Existing local environments silently remain in developer mode without interrupting the user.
      setAppProfile('developer')
      setOpened(false)
      return
    }
    if (import.meta.env.MODE === 'test') {
      return
    }
    setOpened(true)
  }, [forceOpen, hasExistingData, hydrated, ready])

  // Allow reopening anytime from Settings / Welcome / Office Home
  useEffect(() => {
    const onOpenEvent = () => {
      setStep(1)
      setSetupError(null)
      setConfigResult(null)
      setInstallJob(null)
      setOpened(true)
    }
    window.addEventListener(OPEN_ONBOARDING_WIZARD_EVENT, onOpenEvent)
    return () => window.removeEventListener(OPEN_ONBOARDING_WIZARD_EVENT, onOpenEvent)
  }, [])

  const stopOAuthSession = useCallback((cancelRemote: boolean) => {
    oauthGenRef.current += 1
    if (oauthPollRef.current !== null) {
      window.clearInterval(oauthPollRef.current)
      oauthPollRef.current = null
    }
    const activeId = activeOAuthIdRef.current
    activeOAuthIdRef.current = null
    if (cancelRemote && activeId && typeof api.cancelToolchainOAuth === 'function') {
      void api.cancelToolchainOAuth(activeId).catch(() => {})
    }
  }, [])

  // Cleanup install & OAuth polling on unmount
  useEffect(() => {
    return () => {
      if (installPollRef.current !== null) {
        window.clearInterval(installPollRef.current)
        installPollRef.current = null
      }
      stopOAuthSession(true)
    }
  }, [stopOAuthSession])

  // Cancel any in-flight OAuth process when leaving Step 3 or closing wizard
  useEffect(() => {
    if (!opened || step !== 3) {
      stopOAuthSession(true)
      setOauthSession(null)
      setOauthStarting(false)
    }
  }, [opened, step, stopOAuthSession])

  const loadToolchain = useCallback(async () => {
    setLoadingToolchain(true)
    try {
      const report = await api.getToolchainStatus('local')
      const agentItems = (report.items || []).filter((item) => item.kind === 'agent')
      setToolchainItems(agentItems)
    } catch {
      // Fall back gracefully to live agents from store
    } finally {
      setLoadingToolchain(false)
    }
  }, [])

  useEffect(() => {
    if (opened && step === 2) {
      void loadToolchain()
    }
  }, [opened, step, loadToolchain])

  const localLiveAgentsByKind = useMemo(() => {
    const map = new Map<string, Agent>()
    const all = Object.values(agentsMap)
    const sorted = [...all].sort((a, b) => {
      const aLocal = !a.connection_id || a.connection_id === 'local' ? 0 : 1
      const bLocal = !b.connection_id || b.connection_id === 'local' ? 0 : 1
      if (aLocal !== bLocal) return aLocal - bLocal
      const aReady = a.status === 'ready' ? 0 : 1
      const bReady = b.status === 'ready' ? 0 : 1
      return aReady - bReady
    })
    for (const agent of sorted) {
      if (!map.has(agent.kind)) {
        map.set(agent.kind, agent)
      }
    }
    return map
  }, [agentsMap])

  const engineOptions = useMemo<EngineOption[]>(() => {
    const itemByKind = new Map(toolchainItems.map((item) => [item.id, item]))
    return ENGINE_META_LIST.map((meta) => {
      const tc = itemByKind.get(meta.kind)
      const liveAgent = localLiveAgentsByKind.get(meta.kind)
      const installed = Boolean(tc?.installed || liveAgent)
      const authConfigured = Boolean(
        tc?.auth_configured || (liveAgent && liveAgent.status === 'ready' && tc?.auth_configured !== false),
      )
      return {
        kind: meta.kind,
        name: tc?.name || meta.name,
        tagline: meta.tagline,
        recommended: meta.recommended,
        supportsApiKey: meta.supportsApiKey,
        installed,
        authConfigured,
        version: tc?.version,
        liveAgent,
        requiresDaemonRestart: Boolean(tc?.requires_daemon_restart || (installed && !liveAgent)),
        authHint: tc?.auth_hint,
        defaultModels: meta.defaultModels,
      }
    })
  }, [toolchainItems, localLiveAgentsByKind])

  const installedEngines = useMemo(
    () => engineOptions.filter((opt) => opt.installed),
    [engineOptions],
  )
  const uninstalledEngines = useMemo(
    () => engineOptions.filter((opt) => !opt.installed),
    [engineOptions],
  )

  // Auto-select the best detected engine when Step 2 loads
  const autoPickedRef = useRef(false)
  useEffect(() => {
    if (!opened) {
      autoPickedRef.current = false
      return
    }
    if (step !== 2 || loadingToolchain || autoPickedRef.current) return
    autoPickedRef.current = true
    if (installedEngines.length > 0) {
      const best =
        installedEngines.find((e) => e.authConfigured && e.kind === 'codex') ||
        installedEngines.find((e) => e.authConfigured) ||
        installedEngines.find((e) => e.kind === 'codex') ||
        installedEngines[0]
      if (best) setSelectedKind(best.kind)
    } else {
      setSelectedKind('codex')
    }
  }, [opened, step, loadingToolchain, installedEngines])

  const selectedEngine = useMemo(
    () => engineOptions.find((e) => e.kind === selectedKind) || engineOptions[0],
    [engineOptions, selectedKind],
  )

  // Sync Step 3 defaults when entering Step 3
  const fetchAgentNativeModels = useCallback(async (agentId: string, fallbackDefaults: string[]) => {
    setLoadingModels(true)
    try {
      const res = await api.fetchAiProviderModels({ protocol: 'agent', agent_id: agentId })
      const list = Array.isArray(res.models) ? res.models.filter(Boolean) : []
      if (list.length > 0) {
        setModels(list)
        setSelectedModel((prev) => (prev && list.includes(prev) ? prev : list[0]))
        return
      }
    } catch {
      // Try probe endpoint fallback
      try {
        const probed = await api.getSessionModels('__probe__', agentId)
        const list = Array.from(new Set((probed.items || []).map((i) => i.model).filter(Boolean)))
        if (list.length > 0) {
          setModels(list)
          setSelectedModel((prev) => (prev && list.includes(prev) ? prev : list[0]))
          return
        }
      } catch {
        // Ignore probe failures
      }
    } finally {
      setLoadingModels(false)
    }
    setModels(fallbackDefaults)
    if (fallbackDefaults.length > 0) {
      setSelectedModel((prev) => prev || fallbackDefaults[0])
    }
  }, [])

  useEffect(() => {
    if (!opened || step !== 3 || !selectedEngine) return
    const alreadyLoggedIn = selectedEngine.installed && selectedEngine.authConfigured
    setUseCustomKey(!alreadyLoggedIn && selectedEngine.supportsApiKey)
    setSkipKeyExplicit(alreadyLoggedIn || !selectedEngine.supportsApiKey)
    if (selectedEngine.liveAgent?.id) {
      void fetchAgentNativeModels(selectedEngine.liveAgent.id, selectedEngine.defaultModels)
    } else {
      setModels(selectedEngine.defaultModels)
      setSelectedModel((prev) => prev || selectedEngine.defaultModels[0] || '')
    }
  }, [opened, step, selectedEngine?.kind, selectedEngine?.authConfigured, selectedEngine?.liveAgent?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleInstallSelectedEngine = async () => {
    if (!selectedEngine || installing) return
    setInstalling(true)
    setInstallJob(null)
    try {
      const started = await api.startToolchainInstallJob({
        environment_id: 'local',
        tool_ids: [selectedEngine.kind],
        include_deps: true,
        title: `初始化安装 ${selectedEngine.name}`,
      })
      setInstallJob(started)
      if (installPollRef.current !== null) {
        window.clearInterval(installPollRef.current)
      }
      installPollRef.current = window.setInterval(async () => {
        try {
          const latest = await api.getToolchainInstallJob(started.id)
          setInstallJob(latest)
          if (latest.status !== 'running') {
            if (installPollRef.current !== null) {
              window.clearInterval(installPollRef.current)
              installPollRef.current = null
            }
            setInstalling(false)
            if (latest.status === 'completed') {
              notifications.show({
                color: 'teal',
                message: `${selectedEngine.name} 安装完成`,
                icon: <IconCheck size={16} />,
              })
              await loadToolchain()
              try {
                const boot = await api.getBootstrap()
                hydrateBootstrap(boot)
              } catch {
                // ignore
              }
            }
          }
        } catch {
          // keep polling briefly
        }
      }, 1200)
    } catch (error) {
      setInstalling(false)
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '启动安装任务失败',
      })
    }
  }

  const handleActivateDaemon = async () => {
    if (activatingDaemon) return
    setActivatingDaemon(true)
    try {
      await api.createKernelOperation({
        target: 'daemon',
        action: 'restart',
        confirm_active: true,
      })
      notifications.show({
        color: 'teal',
        message: '已触发后台核心刷新，正在检测刚安装的 AI 引擎…',
      })
      window.setTimeout(async () => {
        try {
          const boot = await api.getBootstrap()
          hydrateBootstrap(boot)
          await loadToolchain()
        } catch {
          // ignore
        } finally {
          setActivatingDaemon(false)
        }
      }, 2200)
    } catch (error) {
      setActivatingDaemon(false)
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '刷新后台核心失败，可稍后在系统托盘重启小内核',
      })
    }
  }

  const handleOAuthSucceeded = useCallback(
    async (latest: ToolchainOAuthSession, engineKind: AgentKind, engineName: string) => {
      stopOAuthSession(false)
      setOauthSession(latest)
      setUseCustomKey(false)
      setSkipKeyExplicit(true)
      setToolchainItems((prev) => {
        if (latest.agent_status) {
          const exists = prev.some((item) => item.id === latest.agent_status!.id)
          return exists
            ? prev.map((item) => (item.id === latest.agent_status!.id ? latest.agent_status! : item))
            : [...prev, latest.agent_status]
        }
        return prev.map((item) =>
          item.id === engineKind ? { ...item, installed: true, auth_configured: true } : item,
        )
      })
      notifications.show({
        color: 'teal',
        message: `${engineName} 登录成功，可直接免填 API Key 使用`,
        icon: <IconCheck size={16} />,
      })
      await loadToolchain()
      try {
        const boot = await api.getBootstrap()
        hydrateBootstrap(boot)
      } catch {
        // ignore bootstrap refresh errors
      }
    },
    [hydrateBootstrap, loadToolchain, stopOAuthSession],
  )

  const handleStartOAuth = async (mode: 'browser' | 'device' = 'browser') => {
    if (!selectedEngine || oauthStarting) return
    stopOAuthSession(true)
    const gen = oauthGenRef.current
    setOauthStarting(true)
    setOauthSession(null)
    const targetKind = selectedEngine.kind
    const targetName = selectedEngine.name
    try {
      const started = await api.startToolchainOAuth({
        environment_id: 'local',
        agent_kind: targetKind,
        mode,
        open_browser: true,
      })
      if (oauthGenRef.current !== gen) {
        if (started?.id && typeof api.cancelToolchainOAuth === 'function') {
          void api.cancelToolchainOAuth(started.id).catch(() => {})
        }
        return
      }
      setOauthSession(started)
      if (started.status === 'succeeded' || started.auth_configured) {
        await handleOAuthSucceeded(started, targetKind, targetName)
        return
      }
      if (started.status === 'failed' || started.status === 'cancelled') {
        return
      }
      activeOAuthIdRef.current = started.id
      oauthPollRef.current = window.setInterval(async () => {
        if (oauthGenRef.current !== gen || activeOAuthIdRef.current !== started.id) return
        try {
          const latest = await api.getToolchainOAuthStatus(started.id)
          if (oauthGenRef.current !== gen || activeOAuthIdRef.current !== started.id) return
          setOauthSession(latest)
          if (latest.status === 'succeeded' || latest.auth_configured) {
            await handleOAuthSucceeded(latest, targetKind, targetName)
          } else if (latest.status === 'failed' || latest.status === 'cancelled') {
            stopOAuthSession(false)
          }
        } catch {
          // keep polling transiently in case of brief hiccup
        }
      }, 1500)
    } catch (error) {
      if (oauthGenRef.current !== gen) return
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '启动浏览器一键登录失败',
      })
    } finally {
      if (oauthGenRef.current === gen) {
        setOauthStarting(false)
      }
    }
  }

  const handleCancelOAuth = async () => {
    const id = activeOAuthIdRef.current || oauthSession?.id
    stopOAuthSession(false)
    if (id && typeof api.cancelToolchainOAuth === 'function') {
      try {
        const cancelled = await api.cancelToolchainOAuth(id)
        setOauthSession(cancelled)
        return
      } catch {
        // ignore
      }
    }
    setOauthSession(null)
  }

  const handleSelectPreset = (preset: KeyPreset) => {
    setPresetId(preset.id)
    setProviderName(preset.providerName)
    setProtocol(preset.protocol)
    setBaseUrl(preset.baseUrl)
    if (preset.models.length > 0) {
      setModels(preset.models)
      setSelectedModel(preset.models[0])
    }
  }

  const handleFetchModelsByKey = async () => {
    if (!baseUrl.trim()) {
      notifications.show({ color: 'red', message: '请先填写接口地址 (Base URL)' })
      return
    }
    setLoadingModels(true)
    try {
      const res = await api.fetchAiProviderModels({
        protocol,
        base_url: baseUrl.trim(),
        api_key: apiKey.trim() || undefined,
      })
      const list = Array.isArray(res.models) ? res.models.filter(Boolean) : []
      setModels(list)
      if (list.length > 0) {
        setSelectedModel((prev) => (prev && list.includes(prev) ? prev : list[0]))
        notifications.show({ color: 'teal', message: `成功拉取 ${list.length} 个可用模型` })
      } else {
        notifications.show({ color: 'gray', message: '接口未返回模型列表，可手动输入模型名称' })
      }
    } catch (error) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '拉取模型失败，请检查地址与 API Key',
      })
    } finally {
      setLoadingModels(false)
    }
  }

  const runFinalOnboarding = useCallback(async () => {
    if (!selectedEngine || configuring) return
    setConfiguring(true)
    setSetupError(null)
    setConfigResult(null)
    setSetupProgress({
      profile: 'running',
      configure: 'pending',
      capabilities: 'pending',
      session: 'pending',
    })

    // 1. Apply work profile
    setAppProfile(profile)
    onSelected?.(profile)
    setSetupProgress((p) => ({ ...p, profile: 'done', configure: 'running' }))

    // 2. Call backend onboarding configure (credentials, universal LLM sync, CLI & skills injection)
    const shouldSkipKey = !useCustomKey || skipKeyExplicit || !apiKey.trim()
    let cfgRes: OnboardingConfigureResult | null = null
    try {
      cfgRes = await api.configureOnboarding({
        environment_id: 'local',
        agent_kind: selectedEngine.kind,
        agent_id: selectedEngine.liveAgent?.id || undefined,
        api_key: shouldSkipKey ? undefined : apiKey.trim(),
        base_url: shouldSkipKey ? undefined : baseUrl.trim(),
        provider_name: shouldSkipKey ? undefined : providerName.trim(),
        protocol: shouldSkipKey ? undefined : protocol,
        model: selectedModel.trim() || undefined,
        skip_key: shouldSkipKey,
        inject_capabilities: true,
        sync_universal_llm: true,
      })
      setConfigResult(cfgRes)
      broadcastAiProvidersSync()
      window.dispatchEvent(new CustomEvent('astrorder:gateway-config-changed', { detail: { source: 'onboarding' } }))
      setSetupProgress((p) => ({
        ...p,
        configure: 'done',
        capabilities: cfgRes?.cli_ready || (cfgRes?.injected_skills?.length || 0) > 0 ? 'done' : 'warn',
        session: 'running',
      }))
    } catch (error) {
      setSetupProgress((p) => ({ ...p, configure: 'warn', capabilities: 'warn', session: 'pending' }))
      setSetupError(error instanceof Error ? error.message : '保存初始化配置时出现问题')
      setConfiguring(false)
      return
    }

    // 3. Resolve target live Agent and start the guided "星序初始化配置助手" session
    let targetAgentId = cfgRes.agent_id || selectedEngine.liveAgent?.id || ''
    if (!targetAgentId) {
      try {
        const boot = await api.getBootstrap()
        hydrateBootstrap(boot)
        const matched = (boot.agents || []).find((a) => a.kind === selectedEngine.kind)
        if (matched) targetAgentId = matched.id
      } catch {
        // ignore
      }
    }

    if (!targetAgentId) {
      setSetupProgress((p) => ({ ...p, session: 'warn' }))
      setSetupError(
        cfgRes.requires_daemon_restart
          ? '新安装的引擎尚未注册到后台核心。请点击下方「刷新后台核心」或在系统托盘重启小内核后再发起引导会话，也可以先进入星序工作台。'
          : '当前暂未检测到就绪的在线 Agent 实例，已完成基础配置，你可以先进入工作台。',
      )
      setConfiguring(false)
      return
    }

    try {
      const created = await api.createSession({
        agent_id: targetAgentId,
        workspace: cfgRes.workspace || null,
        title: '星序初始化配置助手',
        project_name: '星序工作区',
      })
      useAstrorderStore.setState((state) => ({
        sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
      }))

      if (selectedModel.trim()) {
        try {
          await api.setSessionModel(created.id, created.agent_id, '', selectedModel.trim())
        } catch {
          // Non-fatal: keep engine's default model
        }
      }

      const promptText = buildOnboardingAssistantPrompt(profile, selectedEngine.name, cfgRes)
      const sent = await api.createCommand({
        id: newCommandId(),
        agent_id: created.agent_id,
        session_id: created.id,
        action: 'send',
        text: promptText,
        attachment_ids: [],
        target_id: null,
      })
      useAstrorderStore.getState().mergeCommands([sent])

      setSetupProgress((p) => ({ ...p, session: 'done' }))
      setConfiguring(false)
      setOpened(false)
      const targetPath = `/chat/${encodeURIComponent(created.id)}?agent_id=${encodeURIComponent(created.agent_id)}`
      const isStandaloneSettings =
        typeof window !== 'undefined' &&
        (window.location.pathname === '/settings' || window.location.pathname.startsWith('/settings/'))
      const focusMain =
        typeof window !== 'undefined'
          ? (window as unknown as { astrorderDesktop?: { focusMain?: (path?: string) => Promise<void> } })
              .astrorderDesktop?.focusMain
          : undefined
      if (isStandaloneSettings && focusMain) {
        void focusMain(targetPath)
      } else {
        navigate(targetPath)
      }
    } catch (error) {
      setSetupProgress((p) => ({ ...p, session: 'warn' }))
      setSetupError(
        error instanceof Error
          ? `配置已保存，但自动启动配置助手会话失败：${error.message}`
          : '配置已保存，但自动启动配置助手会话失败，你可以先进入工作台手动开启对话。',
      )
      setConfiguring(false)
    }
  }, [
    apiKey,
    baseUrl,
    configuring,
    hydrateBootstrap,
    navigate,
    onSelected,
    profile,
    protocol,
    providerName,
    selectedEngine,
    selectedModel,
    skipKeyExplicit,
    useCustomKey,
  ])

  const handleSkipAll = () => {
    stopOAuthSession(true)
    setAppProfile(profile)
    setOpened(false)
    onSelected?.(profile)
  }

  const handleEnterStep4 = () => {
    stopOAuthSession(true)
    setStep(4)
  }

  // Trigger final setup once when entering Step 4
  const step4TriggeredRef = useRef(false)
  useEffect(() => {
    if (step !== 4) {
      step4TriggeredRef.current = false
      return
    }
    if (step4TriggeredRef.current) return
    step4TriggeredRef.current = true
    void runFinalOnboarding()
  }, [step, runFinalOnboarding])

  const stepLabels = [
    { idx: 1 as StepIndex, label: '工作模式' },
    { idx: 2 as StepIndex, label: 'AI 引擎' },
    { idx: 3 as StepIndex, label: '凭据与模型' },
    { idx: 4 as StepIndex, label: '配置助手' },
  ]

  const canProceedStep2 = Boolean(selectedEngine?.installed && !installing)
  const needsKeyInStep3 = Boolean(
    selectedEngine?.supportsApiKey &&
      useCustomKey &&
      !skipKeyExplicit,
  )
  const canProceedStep3 = !needsKeyInStep3 || Boolean(apiKey.trim())

  const renderCheckIcon = (status: StageStatus) => {
    if (status === 'done') {
      return (
        <span className="obw-check-icon is-done">
          <IconCheck size={12} />
        </span>
      )
    }
    if (status === 'running') {
      return (
        <span className="obw-check-icon is-running">
          <IconLoader2 size={12} className="astrorder-spin" />
        </span>
      )
    }
    if (status === 'warn') {
      return <span className="obw-check-icon">!</span>
    }
    return <span className="obw-check-icon">·</span>
  }

  return (
    <Modal
      opened={opened}
      onClose={configuring || installing ? () => {} : handleSkipAll}
      closeOnClickOutside={false}
      closeOnEscape={!configuring && !installing}
      withCloseButton={!configuring && !installing}
      centered
      size={640}
      zIndex={450}
      radius="lg"
      padding={26}
      overlayProps={{ opacity: 0.35, blur: 4 }}
      title={null}
    >
      <div className="obw-root">
        {/* Header */}
        <div className="obw-header">
          <BrandMark size={34} />
          <div className="obw-title">
            {step === 1 && '欢迎使用星序 · 1 分钟完成初始化'}
            {step === 2 && '选择或安装你的 AI 助手引擎'}
            {step === 3 && `配置 ${selectedEngine?.name || 'AI 引擎'} 凭据与默认模型`}
            {step === 4 && '正在注入星序能力并启动配置助手'}
          </div>
          <Text size="xs" c="dimmed" maw={470}>
            {step === 1 && '星序基于你本机成熟的 AI Agent 引擎运行，先选择最适合你的日常界面模式。'}
            {step === 2 && '优先复用本机已安装且已登录的 Agent；如果没有，推荐一键安装 OpenAI Codex。'}
            {step === 3 && '已登录的引擎可直接免填 API Key 并自动同步给周报与会议纪要；也可自由填写 API Key。'}
            {step === 4 && '完成后将自动打开专属引导会话，由你的 AI 助手一步步陪你完成星序个性化设置。'}
          </Text>
        </div>

        {/* 4-Step Progress Bar */}
        <div className="obw-stepper" role="list" aria-label="初始化步骤">
          {stepLabels.map((item) => {
            const isActive = step === item.idx
            const isDone = step > item.idx
            return (
              <div
                key={item.idx}
                role="listitem"
                aria-current={isActive ? 'step' : undefined}
                className={`obw-step${isActive ? ' is-active' : ''}${isDone ? ' is-done' : ''}`}
              >
                <span className="obw-step-dot">
                  {isDone ? <IconCheck size={11} /> : item.idx}
                </span>
                <span className="obw-step-label">{item.label}</span>
              </div>
            )
          })}
        </div>

        {/* STEP 1: Work Mode */}
        {step === 1 && (
          <>
            <div className="obw-card-grid">
              <button
                type="button"
                onClick={() => setProfile('office')}
                aria-pressed={profile === 'office'}
                className={`obw-choice-card${profile === 'office' ? ' is-selected' : ''}`}
              >
                <Group justify="space-between">
                  <span
                    style={{
                      width: 34,
                      height: 34,
                      borderRadius: 8,
                      background: profile === 'office' ? '#18181b' : '#e4e4e7',
                      color: profile === 'office' ? '#ffffff' : '#3f3f46',
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}
                  >
                    <IconBriefcase size={18} />
                  </span>
                  <Group gap={6}>
                    <span className="obw-pill is-recommended">推荐</span>
                    {profile === 'office' && (
                      <span className="obw-step-dot" style={{ background: '#18181b', color: '#fff' }}>
                        <IconCheck size={11} />
                      </span>
                    )}
                  </Group>
                </Group>
                <div>
                  <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--astr-text, #111827)' }}>
                    办公模式
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--astr-muted, #6b7280)', marginTop: 4, lineHeight: 1.55 }}>
                    专注日常对话、文档起草与润色、会议录音纪要、周报复盘与待办清单，自动隐藏终端与代码工具。
                  </div>
                </div>
              </button>

              <button
                type="button"
                onClick={() => setProfile('developer')}
                aria-pressed={profile === 'developer'}
                className={`obw-choice-card${profile === 'developer' ? ' is-selected' : ''}`}
              >
                <Group justify="space-between">
                  <span
                    style={{
                      width: 34,
                      height: 34,
                      borderRadius: 8,
                      background: profile === 'developer' ? '#18181b' : '#e4e4e7',
                      color: profile === 'developer' ? '#ffffff' : '#3f3f46',
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}
                  >
                    <IconCode size={18} />
                  </span>
                  {profile === 'developer' && (
                    <span className="obw-step-dot" style={{ background: '#18181b', color: '#fff' }}>
                      <IconCheck size={11} />
                    </span>
                  )}
                </Group>
                <div>
                  <div style={{ fontSize: 15, fontWeight: 650, color: 'var(--astr-text, #111827)' }}>
                    研发模式
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--astr-muted, #6b7280)', marginTop: 4, lineHeight: 1.55 }}>
                    保留完整工程能力：多会话矩阵、Git 分支与 Worktree、终端、SQL 直连、远端 SSH 与用量统计。
                  </div>
                </div>
              </button>
            </div>

            <div className="obw-footer">
              <button type="button" className="obw-link-btn" onClick={handleSkipAll}>
                稍后再配置，直接进入
              </button>
              <button
                type="button"
                className="obw-primary-btn"
                onClick={() => setStep(2)}
              >
                <span>下一步：选择 AI 引擎</span>
                <IconArrowRight size={15} />
              </button>
            </div>
          </>
        )}

        {/* STEP 2: Detect Installed Agent or Install Recommended Codex */}
        {step === 2 && (
          <>
            {loadingToolchain ? (
              <div className="obw-callout">
                <IconLoader2 size={16} className="astrorder-spin" style={{ marginTop: 2 }} />
                <div>
                  <Text size="sm" fw={550}>正在检测本机已安装的 AI 助手引擎…</Text>
                  <Text size="xs" c="dimmed">自动检查 OpenAI Codex、Antigravity、Hermes 与 Grok 的安装与登录状态</Text>
                </div>
              </div>
            ) : installedEngines.length > 0 ? (
              <Stack gap="sm">
                <Group justify="space-between" align="center">
                  <Text size="xs" fw={600} c="dimmed">
                    检测到本机已安装 {installedEngines.length} 个 AI 助手，直接选用即可
                  </Text>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    leftSection={<IconRefresh size={12} />}
                    onClick={() => void loadToolchain()}
                  >
                    重新检测
                  </Button>
                </Group>

                <div className="obw-engine-list">
                  {installedEngines.map((engine) => {
                    const isSelected = selectedKind === engine.kind
                    return (
                      <button
                        key={engine.kind}
                        type="button"
                        onClick={() => setSelectedKind(engine.kind)}
                        aria-pressed={isSelected}
                        className={`obw-engine-row${isSelected ? ' is-selected' : ''}`}
                      >
                        <div className="obw-engine-left">
                          <span className="obw-engine-icon">
                            <AgentBrandIcon kind={engine.kind} size={20} />
                          </span>
                          <div style={{ minWidth: 0 }}>
                            <div className="obw-engine-title-line">
                              <span className="obw-engine-name">{engine.name}</span>
                              {engine.version && (
                                <span className="obw-pill">v{engine.version}</span>
                              )}
                              {engine.authConfigured ? (
                                <span className="obw-pill is-recommended">
                                  <IconShieldCheck size={11} />
                                  已登录 · 可免填 Key
                                </span>
                              ) : (
                                <span className="obw-pill is-ready">已安装 · 待配置</span>
                              )}
                            </div>
                            <div className="obw-engine-desc">{engine.tagline}</div>
                          </div>
                        </div>
                        {isSelected && (
                          <span className="obw-step-dot" style={{ background: '#18181b', color: '#fff' }}>
                            <IconCheck size={11} />
                          </span>
                        )}
                      </button>
                    )
                  })}
                </div>

                {uninstalledEngines.length > 0 && (
                  <div>
                    <button
                      type="button"
                      className="obw-link-btn"
                      style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}
                      onClick={() => setShowMoreEngines((v) => !v)}
                    >
                      {showMoreEngines ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
                      <span>想安装其他未安装的引擎？（{uninstalledEngines.length} 个可选）</span>
                    </button>
                    <Collapse expanded={showMoreEngines}>
                      <div className="obw-engine-list" style={{ marginTop: 8 }}>
                        {uninstalledEngines.map((engine) => {
                          const isSelected = selectedKind === engine.kind
                          return (
                            <button
                              key={engine.kind}
                              type="button"
                              onClick={() => setSelectedKind(engine.kind)}
                              aria-pressed={isSelected}
                              className={`obw-engine-row${isSelected ? ' is-selected' : ''}`}
                            >
                              <div className="obw-engine-left">
                                <span className="obw-engine-icon">
                                  <AgentBrandIcon kind={engine.kind} size={20} />
                                </span>
                                <div style={{ minWidth: 0 }}>
                                  <div className="obw-engine-title-line">
                                    <span className="obw-engine-name">{engine.name}</span>
                                    {engine.recommended && <span className="obw-pill is-recommended">推荐</span>}
                                    <span className="obw-pill">未安装</span>
                                  </div>
                                  <div className="obw-engine-desc">{engine.tagline}</div>
                                </div>
                              </div>
                            </button>
                          )
                        })}
                      </div>
                    </Collapse>
                  </div>
                )}
              </Stack>
            ) : (
              <Stack gap="sm">
                <div className="obw-callout is-indigo">
                  <IconSparkles size={17} color="#4f46e5" style={{ flexShrink: 0, marginTop: 2 }} />
                  <div>
                    <Text size="sm" fw={600}>本机尚未检测到已安装的 AI 助手引擎</Text>
                    <Text size="xs" c="dimmed" mt={2}>
                      推荐优先一键安装 <b>OpenAI Codex</b>（自动补齐 Node.js 等基础依赖，生态最成熟，安装完成后可立即使用）。
                    </Text>
                  </div>
                </div>

                <div className="obw-engine-list">
                  {engineOptions.map((engine) => {
                    const isSelected = selectedKind === engine.kind
                    return (
                      <button
                        key={engine.kind}
                        type="button"
                        onClick={() => setSelectedKind(engine.kind)}
                        aria-pressed={isSelected}
                        className={`obw-engine-row${isSelected ? ' is-selected' : ''}`}
                      >
                        <div className="obw-engine-left">
                          <span className="obw-engine-icon">
                            <AgentBrandIcon kind={engine.kind} size={20} />
                          </span>
                          <div style={{ minWidth: 0 }}>
                            <div className="obw-engine-title-line">
                              <span className="obw-engine-name">{engine.name}</span>
                              {engine.recommended && (
                                <span className="obw-pill is-recommended">推荐安装</span>
                              )}
                            </div>
                            <div className="obw-engine-desc">{engine.tagline}</div>
                          </div>
                        </div>
                        {isSelected && (
                          <span className="obw-step-dot" style={{ background: '#18181b', color: '#fff' }}>
                            <IconCheck size={11} />
                          </span>
                        )}
                      </button>
                    )
                  })}
                </div>
              </Stack>
            )}

            {/* Install action & live progress when chosen engine is not installed yet */}
            {!loadingToolchain && selectedEngine && !selectedEngine.installed && (
              <Stack gap="xs">
                <Group justify="space-between" align="center">
                  <Text size="xs" c="dimmed">
                    将自动安装 {selectedEngine.name} 及其所需运行环境
                  </Text>
                  <Button
                    size="xs"
                    color="dark"
                    leftSection={installing ? <IconLoader2 size={14} className="astrorder-spin" /> : <IconDownload size={14} />}
                    loading={installing}
                    onClick={() => void handleInstallSelectedEngine()}
                  >
                    {installing ? '正在安装…' : `一键安装 ${selectedEngine.name}`}
                  </Button>
                </Group>
                {installJob && (
                  <Stack gap={6}>
                    <Text size="xs" fw={550} c={installJob.status === 'failed' ? 'red' : 'dimmed'}>
                      {installJob.detail || (installJob.status === 'running' ? '正在执行安装步骤…' : installJob.status)}
                    </Text>
                    {installJob.log && (
                      <div className="obw-install-log">{installJob.log.slice(-1800)}</div>
                    )}
                  </Stack>
                )}
              </Stack>
            )}

            {/* Daemon refresh hint when freshly installed engine needs registration */}
            {!loadingToolchain && selectedEngine?.installed && selectedEngine.requiresDaemonRestart && (
              <div className="obw-callout">
                <IconTerminal2 size={16} style={{ flexShrink: 0, marginTop: 2 }} />
                <div style={{ flex: 1 }}>
                  <Text size="xs" fw={600}>
                    已检测到 {selectedEngine.name}，刷新后台核心后即可直接拉取模型并对话
                  </Text>
                  <Group gap="xs" mt={6}>
                    <Button
                      size="compact-xs"
                      variant="default"
                      loading={activatingDaemon}
                      onClick={() => void handleActivateDaemon()}
                    >
                      刷新后台核心激活引擎
                    </Button>
                  </Group>
                </div>
              </div>
            )}

            <div className="obw-footer">
              <button type="button" className="obw-ghost-btn" onClick={() => setStep(1)} disabled={installing}>
                <IconArrowLeft size={14} />
                <span>上一步</span>
              </button>
              <Group gap="xs">
                <button type="button" className="obw-link-btn" onClick={handleSkipAll} disabled={installing}>
                  稍后再说
                </button>
                <button
                  type="button"
                  className="obw-primary-btn"
                  disabled={!canProceedStep2}
                  onClick={() => setStep(3)}
                >
                  <span>下一步：凭据与模型</span>
                  <IconArrowRight size={15} />
                </button>
              </Group>
            </div>
          </>
        )}

        {/* STEP 3: Optional API Key (skip when already logged in!) + Model Selection */}
        {step === 3 && selectedEngine && (
          <>
            <Stack gap="sm">
              {selectedEngine.authConfigured ? (
                <div className="obw-callout is-indigo">
                  <IconShieldCheck size={18} color="#4f46e5" style={{ flexShrink: 0, marginTop: 2 }} />
                  <div style={{ flex: 1 }}>
                    <Text size="sm" fw={600}>
                      已检测到 {selectedEngine.name} 登录态，无需填写 API Key
                    </Text>
                    <Text size="xs" c="dimmed" mt={2}>
                      将直接复用你的现有登录态，并自动将星序的周报复盘、会议纪要与文档精修直连到该引擎（全程免填 Key）。
                    </Text>
                    {selectedEngine.supportsApiKey && (
                      <Switch
                        mt="xs"
                        size="xs"
                        label="改用自定义 API Key 或第三方模型接口"
                        checked={useCustomKey}
                        onChange={(e) => {
                          const checked = e.currentTarget.checked
                          setUseCustomKey(checked)
                          setSkipKeyExplicit(!checked)
                        }}
                      />
                    )}
                  </div>
                </div>
              ) : (
                <>
                  {OAUTH_SUPPORTED_KINDS.has(selectedEngine.kind) && (
                    <div className="obw-callout is-indigo">
                      <IconShieldCheck size={18} color="#4f46e5" style={{ flexShrink: 0, marginTop: 2 }} />
                      <div style={{ flex: 1 }}>
                        <Group justify="space-between" align="flex-start" gap="xs" wrap="wrap">
                          <div style={{ flex: 1, minWidth: 200 }}>
                            <Text size="sm" fw={600}>
                              浏览器一键登录 {selectedEngine.name}（免填 API Key）
                            </Text>
                            <Text size="xs" c="dimmed" mt={2}>
                              {oauthSession?.status === 'starting' || oauthSession?.status === 'waiting_user'
                                ? oauthSession.detail || '正在等待浏览器完成授权，授权完成后将自动检测并切换为免 Key 模式…'
                                : '自动打开官方授权页面完成登录，无需手动配置命令行或 API Key。'}
                            </Text>
                          </div>
                          {oauthSession?.status === 'starting' || oauthSession?.status === 'waiting_user' ? (
                            <Button
                              size="compact-xs"
                              variant="subtle"
                              color="gray"
                              leftSection={<IconX size={12} />}
                              onClick={() => void handleCancelOAuth()}
                            >
                              取消登录
                            </Button>
                          ) : (
                            <Group gap={6}>
                              <Button
                                size="compact-xs"
                                color="dark"
                                loading={oauthStarting}
                                leftSection={<IconArrowUpRight size={13} />}
                                onClick={() => void handleStartOAuth('browser')}
                              >
                                {oauthSession?.status === 'failed' ? '重试一键登录' : '浏览器一键登录'}
                              </Button>
                              {selectedEngine.kind === 'codex' && (
                                <Button
                                  size="compact-xs"
                                  variant="default"
                                  disabled={oauthStarting}
                                  onClick={() => void handleStartOAuth('device')}
                                >
                                  设备码登录
                                </Button>
                              )}
                            </Group>
                          )}
                        </Group>

                        {(oauthSession?.status === 'starting' || oauthSession?.status === 'waiting_user') && (
                          <Stack gap={6} mt={8}>
                            <Group gap={6} align="center" wrap="wrap">
                              <IconLoader2 size={13} className="astrorder-spin" />
                              <Text size="xs" fw={550}>
                                {oauthSession.status === 'starting' ? '正在启动授权流程…' : '等待你在浏览器中确认授权…'}
                              </Text>
                              {oauthSession.auth_url && (
                                <>
                                  <Button
                                    size="compact-xs"
                                    variant="light"
                                    color="indigo"
                                    rightSection={<IconArrowUpRight size={12} />}
                                    onClick={() => openExternalAuthUrl(oauthSession.auth_url!)}
                                  >
                                    打开授权页
                                  </Button>
                                  <Button
                                    size="compact-xs"
                                    variant="subtle"
                                    color="gray"
                                    leftSection={<IconCopy size={12} />}
                                    onClick={() => {
                                      void navigator.clipboard?.writeText(oauthSession.auth_url!)
                                      notifications.show({ color: 'teal', message: '已复制授权链接' })
                                    }}
                                  >
                                    复制链接
                                  </Button>
                                </>
                              )}
                            </Group>
                            {oauthSession.device_code && (
                              <Group gap={8} align="center">
                                <Text size="xs" c="dimmed">授权验证码：</Text>
                                <code
                                  style={{
                                    padding: '2px 8px',
                                    borderRadius: 6,
                                    background: 'var(--astr-surface, #ffffff)',
                                    border: '1px solid var(--astr-border, #e4e4e7)',
                                    fontWeight: 700,
                                    letterSpacing: '0.08em',
                                    fontSize: 12.5,
                                  }}
                                >
                                  {oauthSession.device_code}
                                </code>
                                <Button
                                  size="compact-xs"
                                  variant="subtle"
                                  color="gray"
                                  leftSection={<IconCopy size={12} />}
                                  onClick={() => {
                                    void navigator.clipboard?.writeText(oauthSession.device_code!)
                                    notifications.show({ color: 'teal', message: '已复制授权验证码' })
                                  }}
                                >
                                  复制验证码
                                </Button>
                              </Group>
                            )}
                          </Stack>
                        )}

                        {oauthSession?.status === 'failed' && (
                          <Text size="xs" c="red" mt={6}>
                            {oauthSession.error || oauthSession.detail || '授权登录未完成，可点击重试或改用下方 API Key。'}
                          </Text>
                        )}
                      </div>
                    </div>
                  )}

                  {selectedEngine.supportsApiKey ? (
                    <div className="obw-callout">
                      <IconKey size={17} style={{ flexShrink: 0, marginTop: 2 }} />
                      <div style={{ flex: 1 }}>
                        <Text size="sm" fw={600}>
                          或填写 API Key（同时自动配好星序通用模型）
                        </Text>
                        <Text size="xs" c="dimmed" mt={2}>
                          没有 API Key 也没关系：可使用上方「浏览器一键登录」或勾选下方「暂不填写 API Key」。
                        </Text>
                      </div>
                    </div>
                  ) : !OAUTH_SUPPORTED_KINDS.has(selectedEngine.kind) ? (
                    <div className="obw-callout is-indigo">
                      <IconShieldCheck size={18} color="#4f46e5" style={{ flexShrink: 0, marginTop: 2 }} />
                      <div>
                        <Text size="sm" fw={600}>{selectedEngine.name} 使用官方账号认证，无需填写 API Key</Text>
                        {selectedEngine.authHint && (
                          <Text size="xs" c="dimmed" mt={2}>{selectedEngine.authHint}</Text>
                        )}
                      </div>
                    </div>
                  ) : null}
                </>
              )}

              {selectedEngine.supportsApiKey && useCustomKey && (
                <Stack gap="xs">
                  {!selectedEngine.authConfigured && (
                    <Checkbox
                      size="xs"
                      label="我暂时没有 API Key（使用官方账号登录，跳过填写 Key）"
                      checked={skipKeyExplicit}
                      onChange={(e) => setSkipKeyExplicit(e.currentTarget.checked)}
                    />
                  )}

                  {!skipKeyExplicit && (
                    <>
                      <div className="obw-preset-chips" aria-label="接口供应商预设">
                        {KEY_PRESETS.map((preset) => (
                          <button
                            key={preset.id}
                            type="button"
                            className={`obw-preset-chip${presetId === preset.id ? ' is-active' : ''}`}
                            onClick={() => handleSelectPreset(preset)}
                          >
                            {preset.label}
                          </button>
                        ))}
                      </div>

                      <Group grow align="flex-start">
                        <PasswordInput
                          size="xs"
                          label="API Key"
                          aria-label="初始化 API Key"
                          placeholder="sk-..."
                          value={apiKey}
                          onChange={(e) => setApiKey(e.currentTarget.value)}
                        />
                        <TextInput
                          size="xs"
                          label="接口地址 (Base URL)"
                          aria-label="初始化接口地址"
                          placeholder="https://api.openai.com/v1"
                          value={baseUrl}
                          onChange={(e) => setBaseUrl(e.currentTarget.value)}
                        />
                      </Group>
                    </>
                  )}
                </Stack>
              )}

              {/* Model selection */}
              <Group align="flex-end" wrap="nowrap">
                <Autocomplete
                  style={{ flex: 1 }}
                  size="sm"
                  label="默认模型"
                  aria-label="初始化默认模型"
                  placeholder={
                    models.length > 0
                      ? '选择或输入默认模型名称'
                      : '留空将使用引擎当前默认模型'
                  }
                  value={selectedModel}
                  data={models}
                  onChange={setSelectedModel}
                />
                <Button
                  size="sm"
                  variant="default"
                  loading={loadingModels}
                  leftSection={<IconRefresh size={14} />}
                  onClick={() => {
                    if (useCustomKey && !skipKeyExplicit && baseUrl.trim()) {
                      void handleFetchModelsByKey()
                    } else if (selectedEngine.liveAgent?.id) {
                      void fetchAgentNativeModels(selectedEngine.liveAgent.id, selectedEngine.defaultModels)
                    }
                  }}
                >
                  拉取模型
                </Button>
              </Group>
            </Stack>

            <div className="obw-footer">
              <button type="button" className="obw-ghost-btn" onClick={() => setStep(2)}>
                <IconArrowLeft size={14} />
                <span>上一步</span>
              </button>
              <button
                type="button"
                className="obw-primary-btn"
                disabled={!canProceedStep3}
                onClick={handleEnterStep4}
              >
                <IconRocket size={15} />
                <span>完成配置并启动配置助手</span>
              </button>
            </div>
          </>
        )}

        {/* STEP 4: Inject CLI + Skills & Launch Guided Configuration Session */}
        {step === 4 && (
          <>
            <div className="obw-checklist" aria-label="初始化执行进度">
              <div className="obw-check-item">
                {renderCheckIcon(setupProgress.profile)}
                <div>
                  <Text size="sm" fw={550}>应用「{profile === 'office' ? '办公模式' : '研发模式'}」界面配置</Text>
                </div>
              </div>

              <div className="obw-check-item">
                {renderCheckIcon(setupProgress.configure)}
                <div>
                  <Text size="sm" fw={550}>同步引擎凭据与星序通用模型</Text>
                  {configResult && (
                    <Text size="xs" c="dimmed">
                      {configResult.universal_llm_mode === 'agent'
                        ? '已将周报、会议纪要与文档精修直连至该引擎（免 API Key）'
                        : configResult.universal_llm_mode === 'api_key'
                          ? '已同步 API 供应商至星序通用模型'
                          : '已完成基础工作空间准备'}
                    </Text>
                  )}
                </div>
              </div>

              <div className="obw-check-item">
                {renderCheckIcon(setupProgress.capabilities)}
                <div>
                  <Text size="sm" fw={550}>注入星序 CLI 与官方技能 (Skills)</Text>
                  {configResult && (
                    <Text size="xs" c="dimmed">
                      {configResult.cli_ready ? 'astrorder-agent CLI 已就绪' : 'CLI 已准备'}
                      {configResult.injected_skills.length > 0
                        ? ` · 已注入 ${configResult.injected_skills.join('、')}`
                        : ''}
                    </Text>
                  )}
                </div>
              </div>

              <div className="obw-check-item">
                {renderCheckIcon(setupProgress.session)}
                <div>
                  <Text size="sm" fw={550}>发起「星序初始化配置助手」引导会话</Text>
                  <Text size="xs" c="dimmed">自动创建专属对话，由 AI 助手协助你完成剩余个性化设置</Text>
                </div>
              </div>
            </div>

            {configResult?.warnings && configResult.warnings.length > 0 && (
              <Alert color="yellow" variant="light" title="提示信息">
                <Stack gap={4}>
                  {configResult.warnings.map((w, idx) => (
                    <Text key={idx} size="xs">{w}</Text>
                  ))}
                </Stack>
              </Alert>
            )}

            {setupError && (
              <Alert color="red" variant="light" title="引导会话未能自动打开">
                <Text size="xs">{setupError}</Text>
              </Alert>
            )}

            <div className="obw-footer">
              <button
                type="button"
                className="obw-ghost-btn"
                disabled={configuring}
                onClick={() => setStep(3)}
              >
                <IconArrowLeft size={14} />
                <span>返回修改</span>
              </button>
              <Group gap="xs">
                {configResult?.requires_daemon_restart && (
                  <Button
                    size="xs"
                    variant="default"
                    loading={activatingDaemon}
                    onClick={() => void handleActivateDaemon()}
                  >
                    刷新后台核心
                  </Button>
                )}
                {setupError && (
                  <Button
                    size="xs"
                    variant="default"
                    loading={configuring}
                    onClick={() => void runFinalOnboarding()}
                  >
                    重试启动助手
                  </Button>
                )}
                <button
                  type="button"
                  className="obw-primary-btn"
                  disabled={configuring}
                  onClick={() => setOpened(false)}
                >
                  <span>先进入星序工作台</span>
                </button>
              </Group>
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}
