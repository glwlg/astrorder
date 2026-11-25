import type { ArtifactViewer } from '../../types'
import { IconGitBranch } from '@tabler/icons-react'
import { GitWorktreesViewer } from './GitWorktreesViewer'
import type { ArtifactRef } from '../../../../domain/artifact'

export const gitWorktreesViewer: ArtifactViewer = {
  id: 'git-worktrees-viewer',
  title: 'Worktree 工作区',
  description: '管理当前 Git 仓库的全部 Worktree 分支、环境配置同步、主分支 Diff 对比、安全合并与失效清理',
  version: '1.0.0',
  badgeLabel: '[Worktrees]',
  tabColor: '#0ea5e9',
  icon: IconGitBranch,
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-git-worktrees' || artifact.id.startsWith('gitworktrees:')) {
      return 100
    }
    return 0
  },
  component: GitWorktreesViewer,
  capabilities: {
    canEdit: false,
  },
}
