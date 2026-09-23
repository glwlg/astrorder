import { Stack } from '@mantine/core'
import { UsageGatewaySettingsCard } from './UsageGatewaySettingsCard'
import { LlmSettingsCard } from './LlmSettingsCard'
import { JevSettingsCard } from './JevSettingsCard'

export function ModelGatewaySettingsPage() {
  return (
    <div style={{ maxWidth: 840, margin: '0 auto', padding: '4px 0 24px' }}>
      <Stack gap="md">
        <UsageGatewaySettingsCard />
        <LlmSettingsCard />
        <JevSettingsCard />
      </Stack>
    </div>
  )
}
