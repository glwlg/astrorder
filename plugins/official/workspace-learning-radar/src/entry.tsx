import { Suspense, lazy, type ComponentProps } from 'react'
import { IconCompass } from '@tabler/icons-react'
import { defineAstrorderPlugin, type AstrorderPluginContext } from '@astrorder/sdk'
import { workbenchIcons } from '@astrorder/workspace-bridge'

const LazyLearningRadarPage = lazy(() =>
  import('../../../../frontend/src/features/learning/LearningRadarPage').then((m) => ({
    default: m.LearningRadarPage,
  })),
)

function LearningRadarPageRoute(props: ComponentProps<typeof LazyLearningRadarPage>) {
  return (
    <Suspense fallback={<div style={{ padding: 24, fontSize: 13, opacity: 0.6 }}>正在加载成长雷达…</div>}>
      <LazyLearningRadarPage {...props} />
    </Suspense>
  )
}

export function activate(ctx: AstrorderPluginContext): void {
  ctx.registerPage({
    id: 'learning',
    title: '成长雷达',
    navLabel: '成长',
    mobileLabel: '成长雷达',
    mobileHeaderTitle: '成长雷达',
    mobileHeaderAriaLabel: '成长雷达，打开会话列表',
    icon: workbenchIcons.CompassIcon,
    mobileIcon: IconCompass,
    path: '/learning',
    mobilePath: '/mobile/learning',
    order: 110,
    hideInOfficeMode: true,
    hasSecondaryPanel: false,
    showInSidebar: true,
    showInMobileApps: true,
    mobileLayout: 'custom',
    component: LearningRadarPageRoute,
    command: [
      {
        id: 'route:learning',
        title: '成长雷达（知识盲区与导师伴学）',
        subtitle: '/learning',
        to: '/learning',
        keywords: 'learning radar mentor study 成长 雷达 学习 盲区 导师',
        icon: IconCompass,
      },
      {
        id: 'route:learning-review',
        title: '成长雷达 · 间隔复习队列',
        subtitle: '/learning?tab=review',
        to: '/learning?tab=review',
        keywords: 'learning review leitner 间隔 复习 记忆 刷卡',
        icon: IconCompass,
      },
    ],
  })
}

export function deactivate(): void {}

export default defineAstrorderPlugin({ activate, deactivate })
