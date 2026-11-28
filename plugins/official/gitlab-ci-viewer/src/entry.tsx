import { Suspense, lazy } from 'react'
import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type PluginPanelContext,
  type StatusBarSlotContext,
  type ViewerContext,
} from '@astrorder/sdk'
import { createGitLabApi } from './api'
import {
  GITLAB_CI_PANEL_ID,
  GITLAB_CI_PLUGIN_ID,
  GitLabComposerStatusPill,
  openGitLabCIPanel,
} from './gitlabExtension'
import { GitlabIcon } from './icons'
import './gitlabci.css'

const LazyGitLabCIViewer = lazy(() =>
  import('./GitLabCIViewer').then((mod) => ({ default: mod.GitLabCIViewer })),
)

const LazyGitLabSettingsSection = lazy(() =>
  import('./GitLabSettingsSection').then((mod) => ({ default: mod.GitLabSettingsSection })),
)

export function activate(ctx: AstrorderPluginContext): void {
  const api = createGitLabApi(ctx.api.fetch)

  const BoundGitLabCIViewer = (props: ViewerContext) => (
    <Suspense fallback={<div style={{ padding: 16, fontSize: 13, opacity: 0.7 }}>正在加载 GitLab CI 视图…</div>}>
      <LazyGitLabCIViewer {...props} ctx={ctx} api={api} />
    </Suspense>
  )

  const BoundGitLabCIPanelAdapter = ({
    artifact,
    session,
    isActive,
    surface,
    onClose,
  }: PluginPanelContext) => (
    <Suspense fallback={<div style={{ padding: 16, fontSize: 13, opacity: 0.7 }}>正在加载 GitLab CI 视图…</div>}>
      <LazyGitLabCIViewer
        ctx={ctx}
        api={api}
        artifact={artifact}
        session={session}
        isActive={isActive}
        surface={surface}
        onClose={onClose}
      />
    </Suspense>
  )

  const BoundGitLabSettingsSection = (props: { embedded?: boolean; onClose?: () => void }) => (
    <Suspense fallback={<div style={{ padding: 12, fontSize: 13, opacity: 0.7 }}>正在加载 GitLab 配置…</div>}>
      <LazyGitLabSettingsSection {...props} ctx={ctx} api={api} />
    </Suspense>
  )

  const BoundStatusBarPill = (props: StatusBarSlotContext) => (
    <GitLabComposerStatusPill {...props} ctx={ctx} api={api} />
  )

  ctx.registerViewer({
    id: GITLAB_CI_PLUGIN_ID,
    title: 'GitLab CI/CD 流水线',
    description: '直连自建或云端 GitLab，查看当前仓库提交后的 CI 流水线、阶段作业与实时日志，支持重试与手动触发',
    version: ctx.version,
    category: 'development',
    badgeLabel: '[GitLab CI]',
    tabColor: '#e24329',
    icon: GitlabIcon,
    mimeTypes: ['application/x-gitlab-ci'],
    supports: (artifact) =>
      artifact.mediaType === 'application/x-gitlab-ci' || artifact.id.startsWith('gitlabci:')
        ? 100
        : 0,
    component: BoundGitLabCIViewer,
    capabilities: {
      canEdit: false,
    },
  })

  ctx.registerSlot({
    settingsTitle: 'GitLab CI / CD 实例配置',
    settingsButtonLabel: '配置实例',
    settingsComponent: BoundGitLabSettingsSection,
    statusBarComponent: BoundStatusBarPill,
    gitMenuItems: [
      {
        id: 'open-gitlab-ci',
        groupLabel: 'CI / CD',
        label: '查看 GitLab CI 流水线...',
        icon: GitlabIcon,
        iconColor: '#e24329',
        requiresWorkspace: true,
        onClick: (session) => openGitLabCIPanel(ctx, session),
      },
    ],
    welcomeMenuItems: [
      {
        id: 'gitlab-ci',
        label: 'GitLab CI 流水线',
        shortcut: 'CI / CD',
        icon: GitlabIcon,
        iconColor: '#e24329',
        requiresWorkspace: true,
        hideInOfficeMode: true,
        onClick: (session) => openGitLabCIPanel(ctx, session),
      },
    ],
    panels: {
      [GITLAB_CI_PANEL_ID]: {
        title: 'GitLab CI',
        badgeLabel: '[GitLab CI]',
        tabColor: '#e24329',
        mediaType: 'application/x-gitlab-ci',
        component: BoundGitLabCIPanelAdapter,
      },
    },
  })
}

export function deactivate(): void {
  // All viewer and slot registrations are automatically released via ctx.subscriptions.
}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
