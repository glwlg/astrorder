import { Divider, Stack, Text, Title } from '@mantine/core'
import { IconChevronRight, IconInfoCircle } from '@tabler/icons-react'
import { AiProvidersSettingsSection } from './AiProvidersSettingsSection'
import { UsageGatewaySettingsCard } from './UsageGatewaySettingsCard'
import { OpenVikingSettingsCard } from './OpenVikingSettingsCard'
import { SystemOneSettingsCard } from './JevSettingsCard'
import { BrowserEngineSettingsCard } from './BrowserEngineSettingsCard'
import {
  BROWSER_VIEWER_PLUGIN_ID,
  MEMORY_VIEWER_PLUGIN_ID,
  WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useSettingsMode } from '../settings/settingsMode'

export function ModelGatewaySettingsPage() {
  const analyticsGatewayEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID))
  const memoryEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(MEMORY_VIEWER_PLUGIN_ID))
  const browserEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(BROWSER_VIEWER_PLUGIN_ID))
  const { mode, setMode, managedByWorkbench } = useSettingsMode()

  const showAdvancedCards = !managedByWorkbench || mode === 'advanced'

  return (
    <div className="model-gateway-settings">
      <div className="settings-page-heading">
        <Title order={2}>AI 供应商</Title>
        <Text c="dimmed" size="sm">
          {showAdvancedCards
            ? '统一管理 Agent 免 Key 直连、多个 AI 供应商（兼容 OpenAI、兼容 Gemini 及 Magpie / OpenCodeX 网关）与决策模型等扩展引擎'
            : '可直接对接已接入的 AI 助手引擎（免 API Key）或添加多个 AI 供应商，供复盘周报、文档精修、自动标题与会议纪要共同复用'}
        </Text>
      </div>

      <AiProvidersSettingsSection />

      {showAdvancedCards ? (
        <Stack gap="md" mt="md">
          <Divider
            label={
              <Text size="xs" fw={600} c="dimmed">
                高级配置 · Agent 模型同步与扩展引擎
              </Text>
            }
            labelPosition="left"
          />
          {analyticsGatewayEnabled && <UsageGatewaySettingsCard />}
          {memoryEnabled && <OpenVikingSettingsCard />}
          {browserEnabled && <BrowserEngineSettingsCard />}
          <SystemOneSettingsCard />
        </Stack>
      ) : (
        <div className="st-advanced-footer-bar">
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--st-muted)' }}>
            <IconInfoCircle size={14} aria-hidden="true" />
            <span>Agent 模型下发同步、知识记忆、决策模型与浏览器引擎属于高级配置</span>
          </span>
          <button
            type="button"
            className="st-text-link"
            onClick={() => setMode('advanced')}
          >
            <span>切换到高级配置</span>
            <IconChevronRight size={14} aria-hidden="true" />
          </button>
        </div>
      )}
    </div>
  )
}
