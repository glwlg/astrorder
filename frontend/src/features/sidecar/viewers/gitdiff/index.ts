import type { ArtifactViewer } from '../../types'
import { IconGitBranch } from '@tabler/icons-react'
import { GitDiffTreeViewer } from './GitDiffTreeViewer'
import type { ArtifactRef } from '../../../../domain/artifact'

export const gitDiffViewer: ArtifactViewer = {
  id: 'git-diff-viewer',
  title: '代码变更',
  description: '浏览工作区未提交的文件变更列表，并查看增删行数及详细 Diff',
  version: '1.0.0',
  badgeLabel: '[Git Diff]',
  tabColor: '#3b82f6',
  icon: IconGitBranch,
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-git-diff-tree' || artifact.id.startsWith('gitdifftree:')) {
      return 100
    }
    return 0
  },
  component: GitDiffTreeViewer,
  capabilities: {
    canEdit: false,
  },
}
