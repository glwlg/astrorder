import { Stack, Text, Title } from '@mantine/core'
import { UsageGatewaySettingsCard } from './UsageGatewaySettingsCard'
import { OpenVikingSettingsCard } from './OpenVikingSettingsCard'
import { LlmSettingsCard } from './LlmSettingsCard'
import { JevSettingsCard } from './JevSettingsCard'

export function ModelGatewaySettingsPage() {
  return (
    <div className="model-gateway-settings">
      <div className="settings-page-heading"><Title order={2}>模型与网关</Title><Text c="dimmed" size="sm">管理模型服务、同步目标与跨环境记忆配置。</Text></div>
      <Stack gap="md">
        <UsageGatewaySettingsCard />
        <OpenVikingSettingsCard />
        <LlmSettingsCard />
        <JevSettingsCard />
      </Stack>
    </div>
  )
}

