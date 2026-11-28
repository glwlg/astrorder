import { Suspense, lazy, type ComponentProps } from 'react'
import { IconNotebook } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyDigestPage = lazy(() =>
  import('../../../../frontend/src/features/digest/DigestPage').then((m) => ({
    default: m.DigestPage,
  })),
)

function DigestPageRoute(props: ComponentProps<typeof LazyDigestPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载工作复盘…</div>}>
      <LazyDigestPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'digest',
    title: '工作复盘与周报',
    navLabel: '复盘',
    mobileLabel: '复盘',
    mobileHeaderTitle: '复盘',
    mobileHeaderAriaLabel: '工作复盘，打开会话列表',
    icon: workbenchIcons.NotebookIcon,
    mobileIcon: IconNotebook,
    path: '/digest',
    mobilePath: '/mobile/digest',
    order: 90,
    hideInOfficeMode: false,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'office',
    component: DigestPageRoute,
    command: {
      id: 'route:digest',
      title: '工作复盘与周报',
      subtitle: '/digest',
      to: '/digest',
      keywords: 'digest report weekly 复盘 周报 日报 总结',
      icon: IconNotebook,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
