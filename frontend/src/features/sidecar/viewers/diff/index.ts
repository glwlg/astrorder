import { IconGitCompare } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { DiffViewer } from './DiffViewer'

export const diffViewer: ArtifactViewer = {
  id: 'diff-viewer',
  title: '代码变更比对 (Diff)',
  description: '按行呈现 Git Patch / Unified Diff 代码补丁，支持高亮与改动统计',
  version: '1.0.0',
  badgeLabel: '[Diff]',
  tabColor: '#3b82f6',
  icon: IconGitCompare,
  extensions: ['.diff', '.patch'],
  mimeTypes: ['text/x-diff'],
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'text/x-diff' ||
      artifact.id.startsWith('gitdiff:') ||
      artifact.name.endsWith('.diff') ||
      artifact.name.endsWith('.patch')
    ) {
      return 90
    }
    return 0
  },
  component: DiffViewer,
  capabilities: {
    canEdit: false,
  },
}
