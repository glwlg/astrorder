import { useState } from 'react'
import { Tabs } from '@mantine/core'
import { GatewayStatisticsView } from './GatewayStatisticsView'
import { DirectTokenUsageView } from './DirectTokenUsageView'
import { AgentUsageView } from './AgentUsageView'

export function AnalyticsPage() {
  const [view, setView] = useState<string | null>('gateway')
  return <div className="route-page analytics-page" style={{ maxWidth: 'none', margin: 0, minHeight: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
    <Tabs value={view} onChange={setView} px="sm" style={{ flexShrink: 0 }}>
      <Tabs.List>
        <Tabs.Tab value="gateway">网关统计</Tabs.Tab>
        <Tabs.Tab value="direct">会话 Token</Tabs.Tab>
        <Tabs.Tab value="agent">CLI 使用</Tabs.Tab>
      </Tabs.List>
    </Tabs>
    {view === 'direct' ? <DirectTokenUsageView /> : view === 'agent' ? <AgentUsageView /> : <GatewayStatisticsView />}
  </div>
}
