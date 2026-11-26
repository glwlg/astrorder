import { IconBrandGitlab } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { GitLabCIViewer } from './GitLabCIViewer'

export const gitlabCIViewer: ArtifactViewer = {
  id: 'gitlab-ci-viewer',
  title: 'GitLab CI 流水线',
  description: '直连自建或云端 GitLab，查看当前仓库提交后的 CI 流水线、阶段作业与实时日志，支持重试与手动触发',
  version: '1.0.0',
  category: 'development',
  badgeLabel: '[GitLab CI]',
  tabColor: '#e24329',
  icon: IconBrandGitlab,
  settingsAction: { tab: 'integrations', label: '配置 GitLab 实例' },
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-gitlab-ci' || artifact.id.startsWith('gitlabci:')) {
      return 100
    }
    return 0
  },
  component: GitLabCIViewer,
  capabilities: {
    canEdit: false,
  },
}
