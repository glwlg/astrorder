import { Suspense, lazy, type ComponentProps } from 'react'
import { IconMicrophone } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyMeetingsPage = lazy(() =>
  import('../../../../frontend/src/features/meetings/MeetingsPage').then((m) => ({
    default: m.MeetingsPage,
  })),
)

function MeetingsPageRoute(props: ComponentProps<typeof LazyMeetingsPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载会议纪要…</div>}>
      <LazyMeetingsPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'meetings',
    title: '会议纪要与录音',
    navLabel: '会议',
    mobileLabel: '会议',
    mobileHeaderTitle: '会议',
    mobileHeaderAriaLabel: '会议记录，打开会话列表',
    icon: workbenchIcons.MicrophoneIcon,
    mobileIcon: IconMicrophone,
    path: '/meetings',
    mobilePath: '/mobile/meetings',
    order: 100,
    hideInOfficeMode: false,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'office',
    component: MeetingsPageRoute,
    command: {
      id: 'route:meetings',
      title: '会议纪要与录音',
      subtitle: '/meetings',
      to: '/meetings',
      keywords: 'meetings audio minutes 会议 录音 纪要 行动项',
      icon: IconMicrophone,
    },
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
