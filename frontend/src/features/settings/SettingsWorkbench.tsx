import { Component, Suspense, lazy, useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Button, Center, Group, Loader, Paper, SegmentedControl, Stack, Tabs, Text } from '@mantine/core'
import {
  IconAdjustmentsHorizontal,
  IconArrowLeft,
  IconChevronRight,
  IconPlugConnected,
} from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  ChartLineUpIcon,
  CpuIcon,
  GlobeHemisphereWestIcon,
  KeyboardIcon,
  LightningIcon,
  PuzzlePieceIcon,
  SlidersHorizontalIcon,
} from '../../components/workbenchIcons'
import { SettingsDrawerScope } from '../../components/SettingsDrawer'
import '../../components/settings.css'
import { OnboardingWizard } from '../office/OnboardingWizard'
import { useIsOfficeProfile } from '../office/appProfile'
import {
  COMMAND_COMPRESSION_PLUGIN_ID,
  SIDEBAR_INTEGRATIONS_PLUGIN_ID,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useIntegrationsStore } from '../integrations/integrationStore'
import { useExtensionStore } from '../extensions/extensionStore'
import {
  PENDING_PLUGIN_CONFIG_SESSION_KEY,
  type OpenPluginConfigEventDetail,
} from '../extensions/types'
import {
  SettingsModeContext,
  isAdvancedSettingsTab,
  readStoredSettingsMode,
  writeStoredSettingsMode,
  type SettingsMode,
} from './settingsMode'

const GeneralSettingsPage = lazy(() => import('./GeneralSettingsPage').then((m) => ({ default: m.GeneralSettingsPage })))
const AgentsPage = lazy(() => import('../agents/AgentsPage').then((m) => ({ default: m.AgentsPage })))
const ModelGatewaySettingsPage = lazy(() => import('../services/ModelGatewaySettingsPage').then((m) => ({ default: m.ModelGatewaySettingsPage })))
const CapabilitiesSettingsCard = lazy(() => import('../services/CapabilitiesSettingsCard').then((m) => ({ default: m.CapabilitiesSettingsCard })))
const RtkSettingsPage = lazy(() => import('../rtk/RtkSettingsPage').then((m) => ({ default: m.RtkSettingsPage })))
const NetworkSettingsPage = lazy(() => import('../network/NetworkSettingsPage').then((m) => ({ default: m.NetworkSettingsPage })))
const ShortcutSettingsPage = lazy(() => import('../shortcuts/ShortcutSettingsPage').then((m) => ({ default: m.ShortcutSettingsPage })))
const IntegrationsSettingsPage = lazy(() => import('../integrations/IntegrationsSettingsPage').then((m) => ({ default: m.IntegrationsSettingsPage })))
const PluginsPage = lazy(() => import('../plugins/PluginsPage').then((m) => ({ default: m.PluginsPage })))

const TAB_ALIASES: Record<string, string> = {
  appearance: 'general',
  desktop: 'general',
  services: 'models',
}

const VALID_SETTINGS_TABS = new Set([
  'general',
  'models',
  'integrations',
  'shortcuts',
  'plugins',
  'connections',
  'skills',
  'rtk',
  'network',
])

export function normalizeSettingsTab(tab?: string | null): string {
  const clean = (tab || '').trim()
  const mapped = TAB_ALIASES[clean] || clean
  return VALID_SETTINGS_TABS.has(mapped) ? mapped : 'general'
}

function forwardPluginConfigOpen(pluginId?: string | null): void {
  const clean = pluginId?.trim()
  if (!clean) return
  try {
    sessionStorage.setItem(PENDING_PLUGIN_CONFIG_SESSION_KEY, clean)
  } catch {
    // ignore storage errors in restricted contexts
  }
  window.dispatchEvent(
    new CustomEvent<OpenPluginConfigEventDetail>('astrorder:open-plugin-config', {
      detail: { pluginId: clean },
    }),
  )
}

export function SettingsWorkbench({
  tab,
  pluginId,
  onTabChange,
  onBackToMain,
  mobile = false,
}: {
  tab: string
  pluginId?: string
  onTabChange: (tab: string) => void
  onBackToMain?: () => void
  mobile?: boolean
}) {
  const normalizedTab = normalizeSettingsTab(tab)
  const navListRef = useRef<HTMLDivElement>(null)
  const isOfficeProfile = useIsOfficeProfile()
  const integrationsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const commandCompressionEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(COMMAND_COMPRESSION_PLUGIN_ID))

  const [mode, setModeState] = useState<SettingsMode>(() =>
    !isOfficeProfile && isAdvancedSettingsTab(normalizedTab) ? 'advanced' : readStoredSettingsMode(),
  )

  const effectiveMode: SettingsMode = isOfficeProfile ? 'basic' : mode
  const effectiveTab = isOfficeProfile && isAdvancedSettingsTab(normalizedTab) ? 'general' : normalizedTab

  const setMode = useCallback(
    (next: SettingsMode) => {
      if (isOfficeProfile) {
        setModeState('basic')
        writeStoredSettingsMode('basic')
        if (isAdvancedSettingsTab(normalizedTab)) {
          onTabChange('general')
        }
        return
      }
      setModeState(next)
      writeStoredSettingsMode(next)
      if (next === 'basic' && isAdvancedSettingsTab(normalizedTab)) {
        onTabChange('general')
      }
    },
    [isOfficeProfile, normalizedTab, onTabChange],
  )

  // Converge settings mode & active tab when global Office Profile is active.
  useEffect(() => {
    if (isOfficeProfile) {
      if (mode !== 'basic') {
        setModeState('basic')
        writeStoredSettingsMode('basic')
      }
      if (isAdvancedSettingsTab(normalizedTab)) {
        onTabChange('general')
      }
      return
    }
    // Deep-linking to any advanced tab in developer profile automatically opens Advanced mode.
    if (isAdvancedSettingsTab(normalizedTab) && mode !== 'advanced') {
      setModeState('advanced')
      writeStoredSettingsMode('advanced')
    }
  }, [isOfficeProfile, normalizedTab, mode, onTabChange])

  useEffect(() => {
    if (pluginId && effectiveTab === 'plugins') {
      forwardPluginConfigOpen(pluginId)
    }
  }, [pluginId, effectiveTab])

  useEffect(() => {
    const onOpenSettings = (event: Event) => {
      const detail = (event as CustomEvent<{ tab?: string; pluginId?: string }>).detail
      if (detail?.pluginId) {
        if (detail.tab) {
          onTabChange(normalizeSettingsTab(detail.tab))
        } else {
          onTabChange('plugins')
        }
        forwardPluginConfigOpen(detail.pluginId)
      }
    }
    window.addEventListener('astrorder:open-settings', onOpenSettings)
    return () => window.removeEventListener('astrorder:open-settings', onOpenSettings)
  }, [onTabChange])

  useEffect(() => {
    if (!integrationsPluginEnabled && effectiveTab === 'integrations') {
      onTabChange('plugins')
    }
    if (!commandCompressionEnabled && effectiveTab === 'rtk') {
      onTabChange('plugins')
    }
  }, [commandCompressionEnabled, integrationsPluginEnabled, onTabChange, effectiveTab])

  useEffect(() => {
    const activeEl = navListRef.current?.querySelector<HTMLElement>('[data-active="true"]')
    activeEl?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
  }, [effectiveTab])

  const showAdvanced = !isOfficeProfile && effectiveMode === 'advanced'
  const advancedTabCount = commandCompressionEnabled ? 4 : 3

  return (
    <SettingsModeContext.Provider value={{ mode: effectiveMode, setMode, officeLocked: isOfficeProfile, managedByWorkbench: true }}>
      <Suspense fallback={<Center style={{ flex: 1 }}><Loader size="sm" color="dark" /></Center>}>
        <Tabs
          value={effectiveTab}
          onChange={(value) => value && onTabChange(value)}
          orientation={mobile ? 'horizontal' : 'vertical'}
          keepMounted={true}
          className={`settings-tabs settings-workbench${mobile ? ' is-mobile' : ''}`}
          data-mobile={mobile ? 'true' : undefined}
        >
          <Tabs.List ref={navListRef} className="settings-nav-list">
            {onBackToMain && (
              <button type="button" className="settings-back-btn" onClick={onBackToMain}>
                <IconArrowLeft size={14} aria-hidden="true" />
                <span>返回主窗口</span>
              </button>
            )}

            {!isOfficeProfile && (
              <div className="settings-mode-switcher">
                <SegmentedControl
                  fullWidth
                  size="xs"
                  aria-label="设置模式"
                  value={effectiveMode}
                  onChange={(val) => setMode(val === 'advanced' ? 'advanced' : 'basic')}
                  data={[
                    { value: 'basic', label: '一般配置' },
                    { value: 'advanced', label: '高级配置' },
                  ]}
                />
              </div>
            )}

            <Tabs.Tab value="general" leftSection={<IconAdjustmentsHorizontal size={16} stroke={1.7} />}>
              常规与外观
            </Tabs.Tab>
            <Tabs.Tab value="models" leftSection={<CpuIcon size={16} />}>
              AI 供应商
            </Tabs.Tab>
            {integrationsPluginEnabled && (
              <Tabs.Tab value="integrations" leftSection={<IconPlugConnected size={16} stroke={1.7} />}>
                {isOfficeProfile ? '常用网站' : '常用集成'}
              </Tabs.Tab>
            )}
            <Tabs.Tab value="shortcuts" leftSection={<KeyboardIcon size={16} />}>
              快捷键
            </Tabs.Tab>
            <Tabs.Tab value="plugins" leftSection={<PuzzlePieceIcon size={16} />}>
              {isOfficeProfile ? '功能开关' : '功能与插件'}
            </Tabs.Tab>

            {!isOfficeProfile && (showAdvanced ? (
              <>
                <div className="settings-nav-group-divider" aria-hidden="true" />
                <div className="settings-nav-group-label">高级与开发配置</div>
                <Tabs.Tab value="connections" leftSection={<SlidersHorizontalIcon size={16} />}>
                  环境与 Agent 连接
                </Tabs.Tab>
                <Tabs.Tab value="skills" leftSection={<LightningIcon size={16} />}>
                  能力中心
                </Tabs.Tab>
                {commandCompressionEnabled && (
                  <Tabs.Tab value="rtk" leftSection={<ChartLineUpIcon size={16} />}>
                    命令输出压缩
                  </Tabs.Tab>
                )}
                <Tabs.Tab value="network" leftSection={<GlobeHemisphereWestIcon size={16} />}>
                  网络与远程访问
                </Tabs.Tab>
              </>
            ) : (
              <button
                type="button"
                className="settings-expand-advanced-btn"
                onClick={() => setMode('advanced')}
              >
                <span>展开高级与开发配置 ({advancedTabCount})</span>
                <IconChevronRight size={14} aria-hidden="true" />
              </button>
            ))}
          </Tabs.List>

          <Tabs.Panel value="general" className="settings-tab-panel" keepMounted={false}>
            <GeneralSettingsPage />
          </Tabs.Panel>
          <Tabs.Panel value="models" className="settings-tab-panel">
            <ModelGatewaySettingsPage />
          </Tabs.Panel>
          {integrationsPluginEnabled && (
            <Tabs.Panel value="integrations" className="settings-tab-panel" keepMounted={false}>
              <IntegrationsSettingsPage />
            </Tabs.Panel>
          )}
          <Tabs.Panel value="shortcuts" className="settings-tab-panel" keepMounted={false}>
            <ShortcutSettingsPage />
          </Tabs.Panel>
          <Tabs.Panel value="plugins" className="settings-tab-panel">
            <PluginsPage />
          </Tabs.Panel>
          {!isOfficeProfile && (
            <>
              <Tabs.Panel value="connections" className="settings-tab-panel">
                <AgentsPage />
              </Tabs.Panel>
              <Tabs.Panel value="skills" className="settings-tab-panel" data-tab-panel-skills="true" style={{ overflow: 'hidden' }}>
                <CapabilitiesSettingsCard />
              </Tabs.Panel>
              {commandCompressionEnabled && (
                <Tabs.Panel value="rtk" className="settings-tab-panel" keepMounted={false}>
                  <RtkSettingsPage />
                </Tabs.Panel>
              )}
              <Tabs.Panel value="network" className="settings-tab-panel">
                <NetworkSettingsPage />
              </Tabs.Panel>
            </>
          )}
        </Tabs>
      </Suspense>
    </SettingsModeContext.Provider>
  )
}

class SettingsErrorBoundary extends Component<
  { children: ReactNode; resetKey: string },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidUpdate(prevProps: { resetKey: string }) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  render() {
    if (this.state.error) {
      return (
        <Center style={{ flex: 1, padding: 24 }}>
          <Paper withBorder radius="md" p="lg" maw={480}>
            <Stack gap="sm">
              <Text fw={700} size="sm">设置页面渲染异常</Text>
              <Text size="xs" c="dimmed" style={{ wordBreak: 'break-word' }}>
                {this.state.error.message || '未知错误'}
              </Text>
              <Group justify="flex-end">
                <Button size="xs" variant="default" onClick={() => this.setState({ error: null })}>
                  重试恢复
                </Button>
              </Group>
            </Stack>
          </Paper>
        </Center>
      )
    }
    return this.props.children
  }
}

export function StandaloneSettingsPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const initialSearch = new URLSearchParams(location.search)
  const initialTab = normalizeSettingsTab(initialSearch.get('tab'))
  const [tab, setTab] = useState<string>(initialTab)
  const [targetPluginId, setTargetPluginId] = useState<string | undefined>(
    () => (initialSearch.get('pluginId') || initialSearch.get('plugin') || '').trim() || undefined,
  )

  useEffect(() => {
    document.title = '星序 · 设置'
    void usePluginSettingsStore.getState().hydrate()
    void useIntegrationsStore.getState().hydrate()
    void useExtensionStore.getState().hydrate()
  }, [])

  useEffect(() => {
    const search = new URLSearchParams(location.search)
    const queryTab = search.get('tab')
    if (queryTab) {
      setTab(normalizeSettingsTab(queryTab))
    }
    const queryPluginId = (search.get('pluginId') || search.get('plugin') || '').trim()
    if (queryPluginId) {
      setTargetPluginId(queryPluginId)
      forwardPluginConfigOpen(queryPluginId)
    }
  }, [location.search])

  useEffect(() => {
    const onSwitchTab = (event: Event) => {
      const detail = (event as CustomEvent<{ tab?: string; pluginId?: string }>).detail
      if (detail?.tab) {
        setTab(normalizeSettingsTab(detail.tab))
      } else if (detail?.pluginId) {
        setTab('plugins')
      }
      if (detail?.pluginId) {
        const clean = detail.pluginId.trim()
        setTargetPluginId(clean || undefined)
        forwardPluginConfigOpen(clean)
      }
    }
    const onNavigateRoute = (event: Event) => {
      const detail = (event as CustomEvent<{ to?: string }>).detail
      const to = detail?.to
      if (!to) return
      const focusMain = (window as unknown as { astrorderDesktop?: { focusMain?: (path?: string) => Promise<void> } }).astrorderDesktop?.focusMain
      if (focusMain) {
        void focusMain(to)
      } else {
        navigate(to)
      }
    }
    window.addEventListener('astrorder:settings-tab', onSwitchTab)
    window.addEventListener('astrorder:open-settings', onSwitchTab)
    window.addEventListener('astrorder:navigate', onNavigateRoute)
    return () => {
      window.removeEventListener('astrorder:settings-tab', onSwitchTab)
      window.removeEventListener('astrorder:open-settings', onSwitchTab)
      window.removeEventListener('astrorder:navigate', onNavigateRoute)
    }
  }, [navigate])

  const handleReturnToChat = () => {
    const focusMain = (window as unknown as { astrorderDesktop?: { focusMain?: (path?: string) => Promise<void> } }).astrorderDesktop?.focusMain
    if (focusMain) {
      void focusMain('')
      return
    }
    navigate('/chat')
  }

  return (
    <SettingsDrawerScope>
      {() => (
        <div className="settings-modal settings-standalone-window" role="main" aria-label="星序设置窗口">
          <div className="settings-standalone-body">
            <SettingsErrorBoundary resetKey={tab}>
              <SettingsWorkbench tab={tab} pluginId={targetPluginId} onTabChange={setTab} onBackToMain={handleReturnToChat} />
            </SettingsErrorBoundary>
          </div>
          <OnboardingWizard ready hasExistingData />
        </div>
      )}
    </SettingsDrawerScope>
  )
}
