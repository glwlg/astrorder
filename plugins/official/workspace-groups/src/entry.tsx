import { Suspense, lazy, type ComponentProps } from 'react'
import { IconUsers } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyGroupsPage = lazy(() =>
  import('../../../../frontend/src/features/chat/GroupsPage').then((m) => ({
    default: m.GroupsPage,
  })),
)

function GroupsPageRoute(props: ComponentProps<typeof LazyGroupsPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载群聊协作…</div>}>
      <LazyGroupsPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'groups',
    title: '群聊协作',
    navLabel: '群聊',
    mobileLabel: '群聊',
    mobileHeaderTitle: '群聊',
    mobileHeaderAriaLabel: '群聊列表',
    icon: workbenchIcons.UsersThreeIcon,
    mobileIcon: IconUsers,
    path: '/groups',
    routePaths: ['/groups', '/groups/:groupId'],
    mobilePath: '/mobile/groups',
    order: 50,
    hideInOfficeMode: true,
    hasSecondaryPanel: true,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'custom',
    component: GroupsPageRoute,
    command: {
      id: 'route:groups',
      title: '群聊协作',
      subtitle: '/groups',
      to: '/groups',
      keywords: 'groups chat 群聊 接力',
      icon: IconUsers,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
