import { Suspense, lazy, type ComponentProps } from 'react'
import { IconArchive, IconDatabase } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyBackupsPage = lazy(() =>
  import('../../../../frontend/src/features/backups/BackupsPage').then((m) => ({
    default: m.BackupsPage,
  })),
)

function BackupsPageRoute(props: ComponentProps<typeof LazyBackupsPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载备份同步…</div>}>
      <LazyBackupsPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'backups',
    title: '备份同步',
    navLabel: '备份',
    mobileLabel: '备份同步',
    mobileHeaderTitle: '备份同步',
    mobileHeaderAriaLabel: '备份同步，打开会话列表',
    icon: workbenchIcons.HardDrivesIcon,
    mobileIcon: IconArchive,
    path: '/backups',
    mobilePath: '/mobile/backups',
    order: 70,
    hideInOfficeMode: true,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'module',
    component: BackupsPageRoute,
    command: {
      id: 'route:backups',
      title: '文件与目录备份',
      subtitle: '/backups',
      to: '/backups',
      keywords: 'backups 备份 快照',
      icon: IconDatabase,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
