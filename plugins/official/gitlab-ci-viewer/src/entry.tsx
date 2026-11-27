import {
  defineAstrorderPlugin,
  type AstrorderPluginContext,
  type PluginPanelContext,
  type StatusBarSlotContext,
  type ViewerContext,
} from '@astrorder/sdk'
import { createGitLabApi } from './api'
import { GitLabCIViewer } from './GitLabCIViewer'
import {
  GITLAB_CI_PANEL_ID,
  GITLAB_CI_PLUGIN_ID,
  GitLabComposerStatusPill,
  openGitLabCIPanel,
} from './gitlabExtension'
import { GitLabSettingsSection } from './GitLabSettingsSection'
import { GitlabIcon } from './icons'
import './gitlabci.css'

export function activate(ctx: AstrorderPluginContext): void {
  const api = createGitLabApi(ctx.api.fetch)

  const BoundGitLabCIViewer = (props: ViewerContext) => (
    <GitLabCIViewer {...props} ctx={ctx} api={api} />
  )

  const BoundGitLabCIPanelAdapter = ({
    artifact,
    session,
    isActive,
    surface,
    onClose,
  }: PluginPanelContext) => (
    <GitLabCIViewer
      ctx={ctx}
      api={api}
      artifact={artifact}
      session={session}
      isActive={isActive}
      surface={surface}
      onClose={onClose}
    />
  )

  const BoundGitLabSettingsSection = (props: { embedded?: boolean; onClose?: () => void }) => (
    <GitLabSettingsSection {...props} ctx={ctx} api={api} />
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
