import { IconFolder } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { FileTreeViewer } from './FileTreeViewer'

export const fileTreeViewer: ArtifactViewer = {
  id: 'filetree-viewer',
  title: '工作区文件浏览器',
  description: '直观浏览当前会话与工作区的目录树结构，点击文件即可在侧边栏直接打开与预览',
  version: '1.0.0',
  badgeLabel: '[目录树]',
  tabColor: '#f59e0b',
  icon: IconFolder,
  supports: (artifact: ArtifactRef): number => {
    if (artifact.mediaType === 'application/x-directory' || artifact.id.startsWith('filetree:')) {
      return 100
    }
    return 0
  },
  component: FileTreeViewer,
  capabilities: {
    canEdit: false,
  },
}
