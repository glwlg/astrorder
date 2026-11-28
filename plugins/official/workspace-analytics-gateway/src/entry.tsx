import { Suspense, lazy, type ComponentProps } from 'react'
import { IconChartBar } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyAnalyticsPage = lazy(() =>
  import('../../../../frontend/src/features/analytics/AnalyticsPage').then((m) => ({
    default: m.AnalyticsPage,
  })),
)

function AnalyticsPageRoute(props: ComponentProps<typeof LazyAnalyticsPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载用量统计…</div>}>
      <LazyAnalyticsPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'analytics',
    title: '用量统计',
    navLabel: '统计',
    mobileLabel: '用量统计',
    mobileHeaderTitle: '用量统计',
    mobileHeaderAriaLabel: '用量统计，打开会话列表',
    icon: workbenchIcons.ChartLineUpIcon,
    mobileIcon: IconChartBar,
    path: '/analytics',
    mobilePath: '/mobile/analytics',
    order: 60,
    hideInOfficeMode: true,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'module',
    component: AnalyticsPageRoute,
    command: {
      id: 'route:analytics',
      title: '用量统计',
      subtitle: '/analytics',
      to: '/analytics',
      keywords: 'analytics usage token 统计',
      icon: IconChartBar,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
