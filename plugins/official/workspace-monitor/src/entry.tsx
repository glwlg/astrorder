import { Suspense, lazy, type ComponentProps } from 'react'
import { IconLayoutGrid } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyMonitorPage = lazy(() =>
  import('../../../../frontend/src/features/monitor/MonitorPage').then((m) => ({
    default: m.MonitorPage,
  })),
)

function MonitorPageRoute(props: ComponentProps<typeof LazyMonitorPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载矩阵…</div>}>
      <LazyMonitorPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'monitor',
    title: '矩阵',
    navLabel: '矩阵',
    icon: workbenchIcons.SquaresFourIcon,
    mobileIcon: IconLayoutGrid,
    path: '/monitor',
    order: 40,
    hideInOfficeMode: true,
    hasSecondaryPanel: true,
    showInSidebar: true,
    showInMobileApps: false,
    component: MonitorPageRoute,
    command: {
      id: 'route:matrix',
      title: '矩阵（多会话网格）',
      subtitle: '/monitor',
      to: '/monitor',
      keywords: 'matrix monitor 矩阵 监控室 多窗',
      icon: IconLayoutGrid,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
