import { Component, Suspense, lazy, useEffect, useState, type ReactNode } from 'react'
import { Button, Center, Group, Loader, Paper, Stack, Tabs, Text } from '@mantine/core'
import { IconArrowBackUp, IconPalette, IconPlugConnected } from '@tabler/icons-react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  ChartLineUpIcon,
  CpuIcon,
  DesktopIcon,
  GlobeHemisphereWestIcon,
  KeyboardIcon,
  LightningIcon,
  PuzzlePieceIcon,
  SlidersHorizontalIcon,
} from '../../components/workbenchIcons'
import { SettingsDrawerScope } from '../../components/SettingsDrawer'
import '../../components/settings.css'
import {
  COMMAND_COMPRESSION_PLUGIN_ID,
  SIDEBAR_INTEGRATIONS_PLUGIN_ID,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useIntegrationsStore } from '../integrations/integrationStore'

const AgentsPage = lazy(() => import('../agents/AgentsPage').then((m) => ({ default: m.AgentsPage })))
const ModelGatewaySettingsPage = lazy(() => import('../services/ModelGatewaySettingsPage').then((m) => ({ default: m.ModelGatewaySettingsPage })))
const CapabilitiesSettingsCard = lazy(() => import('../services/CapabilitiesSettingsCard').then((m) => ({ default: m.CapabilitiesSettingsCard })))
const RtkSettingsPage = lazy(() => import('../rtk/RtkSettingsPage').then((m) => ({ default: m.RtkSettingsPage })))
const NetworkSettingsPage = lazy(() => import('../network/NetworkSettingsPage').then((m) => ({ default: m.NetworkSettingsPage })))
const ShortcutSettingsPage = lazy(() => import('../shortcuts/ShortcutSettingsPage').then((m) => ({ default: m.ShortcutSettingsPage })))
const DesktopSettingsPage = lazy(() => import('../services/DesktopSettingsPage').then((m) => ({ default: m.DesktopSettingsPage })))
const AppearanceSettingsPage = lazy(() => import('../appearance/AppearanceSettingsPage').then((m) => ({ default: m.AppearanceSettingsPage })))
const IntegrationsSettingsPage = lazy(() => import('../integrations/IntegrationsSettingsPage').then((m) => ({ default: m.IntegrationsSettingsPage })))
const PluginsPage = lazy(() => import('../plugins/PluginsPage').then((m) => ({ default: m.PluginsPage })))

const VALID_SETTINGS_TABS = new Set([
  'connections',
  'models',
  'skills',
  'rtk',
  'network',
  'shortcuts',
  'appearance',
  'desktop',
  'integrations',
  'plugins',
])

export function normalizeSettingsTab(tab?: string | null): string {
  const clean = (tab || '').trim()
  return VALID_SETTINGS_TABS.has(clean) ? clean : 'connections'
}

export function SettingsWorkbench({
  tab,
  onTabChange,
}: {
  tab: string
  onTabChange: (tab: string) => void
}) {
  const integrationsPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const commandCompressionEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(COMMAND_COMPRESSION_PLUGIN_ID))

  useEffect(() => {
    if (!integrationsPluginEnabled && tab === 'integrations') {
      onTabChange('plugins')
    }
    if (!commandCompressionEnabled && tab === 'rtk') {
      onTabChange('plugins')
    }
  }, [commandCompressionEnabled, integrationsPluginEnabled, onTabChange, tab])

  return (
    <Suspense fallback={<Center style={{ flex: 1 }}><Loader size="sm" color="violet" /></Center>}>
      <Tabs value={tab} onChange={(value) => value && onTabChange(value)} orientation="vertical" keepMounted={true} className="settings-tabs">
        <Tabs.List>
          <Tabs.Tab value="connections" leftSection={<SlidersHorizontalIcon size={16} />}>连接</Tabs.Tab>
          <Tabs.Tab value="models" leftSection={<CpuIcon size={16} />}>模型与网关</Tabs.Tab>
          <Tabs.Tab value="skills" leftSection={<LightningIcon size={16} />}>能力中心</Tabs.Tab>
          {commandCompressionEnabled && (
            <Tabs.Tab value="rtk" leftSection={<ChartLineUpIcon size={16} />}>命令压缩</Tabs.Tab>
          )}
          <Tabs.Tab value="network" leftSection={<GlobeHemisphereWestIcon size={16} />}>网络与移动端</Tabs.Tab>
          <Tabs.Tab value="shortcuts" leftSection={<KeyboardIcon size={16} />}>快捷键</Tabs.Tab>
          <Tabs.Tab value="appearance" leftSection={<IconPalette size={16} />}>外观</Tabs.Tab>
          <Tabs.Tab value="desktop" leftSection={<DesktopIcon size={16} />}>桌面客户端</Tabs.Tab>
          {integrationsPluginEnabled && (
            <Tabs.Tab value="integrations" leftSection={<IconPlugConnected size={16} />}>集成</Tabs.Tab>
          )}
          <Tabs.Tab value="plugins" leftSection={<PuzzlePieceIcon size={16} />}>插件</Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="connections" className="settings-tab-panel"><AgentsPage /></Tabs.Panel>
        <Tabs.Panel value="models" className="settings-tab-panel"><ModelGatewaySettingsPage /></Tabs.Panel>
        <Tabs.Panel value="skills" className="settings-tab-panel" data-tab-panel-skills="true" style={{ overflow: 'hidden' }}>
          <CapabilitiesSettingsCard />
        </Tabs.Panel>
        {commandCompressionEnabled && (
          <Tabs.Panel value="rtk" className="settings-tab-panel" keepMounted={false}><RtkSettingsPage /></Tabs.Panel>
        )}
        <Tabs.Panel value="network" className="settings-tab-panel"><NetworkSettingsPage /></Tabs.Panel>
        <Tabs.Panel value="shortcuts" className="settings-tab-panel" keepMounted={false}><ShortcutSettingsPage /></Tabs.Panel>
        <Tabs.Panel value="appearance" className="settings-tab-panel" keepMounted={false}><AppearanceSettingsPage /></Tabs.Panel>
        <Tabs.Panel value="desktop" className="settings-tab-panel"><DesktopSettingsPage /></Tabs.Panel>
        {integrationsPluginEnabled && (
          <Tabs.Panel value="integrations" className="settings-tab-panel" keepMounted={false}><IntegrationsSettingsPage /></Tabs.Panel>
        )}
        <Tabs.Panel value="plugins" className="settings-tab-panel"><PluginsPage /></Tabs.Panel>
      </Tabs>
    </Suspense>
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
  const initialTab = normalizeSettingsTab(new URLSearchParams(location.search).get('tab'))
  const [tab, setTab] = useState<string>(initialTab)

  useEffect(() => {
    document.title = '星序 · 设置'
    void usePluginSettingsStore.getState().hydrate()
    void useIntegrationsStore.getState().hydrate()
  }, [])

  useEffect(() => {
    const queryTab = new URLSearchParams(location.search).get('tab')
    if (queryTab) {
      setTab(normalizeSettingsTab(queryTab))
    }
  }, [location.search])

  useEffect(() => {
    const onSwitchTab = (event: Event) => {
      const detail = (event as CustomEvent<{ tab?: string }>).detail
      if (detail?.tab) {
        setTab(normalizeSettingsTab(detail.tab))
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
          <header className="settings-standalone-header">
            <Group gap="sm">
              <Text fw={700} size="sm">星序设置</Text>
              <Text size="xs" c="dimmed">独立窗口 · 可随时切回主会话窗口继续处理事务</Text>
            </Group>
            <Button
              size="compact-xs"
              variant="default"
              leftSection={<IconArrowBackUp size={13} />}
              onClick={handleReturnToChat}
            >
              回到会话窗口
            </Button>
          </header>
          <div className="settings-standalone-body">
            <SettingsErrorBoundary resetKey={tab}>
              <SettingsWorkbench tab={tab} onTabChange={setTab} />
            </SettingsErrorBoundary>
          </div>
        </div>
      )}
    </SettingsDrawerScope>
  )
}
