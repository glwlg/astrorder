import { Stack, Text, Title } from '@mantine/core'
import { UsageGatewaySettingsCard } from './UsageGatewaySettingsCard'
import { OpenVikingSettingsCard } from './OpenVikingSettingsCard'
import { LlmSettingsCard } from './LlmSettingsCard'
import { SystemOneSettingsCard } from './JevSettingsCard'
import { BrowserEngineSettingsCard } from './BrowserEngineSettingsCard'
import {
  BROWSER_VIEWER_PLUGIN_ID,
  MEMORY_VIEWER_PLUGIN_ID,
  WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

export function ModelGatewaySettingsPage() {
  const analyticsGatewayEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID))
  const memoryEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(MEMORY_VIEWER_PLUGIN_ID))
  const browserEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(BROWSER_VIEWER_PLUGIN_ID))

  return (
    <div className="model-gateway-settings">
      <div className="settings-page-heading"><Title order={2}>模型与网关</Title><Text c="dimmed" size="sm">管理核心模型服务；网关同步、知识记忆与浏览器引擎卡片可在「插件」中按需启停。</Text></div>
      <Stack gap="md">
        {analyticsGatewayEnabled && <UsageGatewaySettingsCard />}
        {memoryEnabled && <OpenVikingSettingsCard />}
        <LlmSettingsCard />
        {browserEnabled && <BrowserEngineSettingsCard />}
        <SystemOneSettingsCard />
      </Stack>
    </div>
  )
}

