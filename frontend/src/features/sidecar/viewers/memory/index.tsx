import { IconBrain } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer, ViewerContext } from '../../types'
import { SidecarMemoryPanel } from '../../SidecarMemoryPanel'

export const memoryViewer: ArtifactViewer = {
  id: 'memory-viewer',
  title: '知识记忆',
  description: 'OpenViking 虚拟文件系统 (viking://) 知识库资源与排障经验看板',
  version: '0.1.0',
  badgeLabel: '[记忆]',
  tabColor: 'var(--astr-indigo, #6366f1)',
  icon: IconBrain,
  extensions: ['.viking', '.ov.md'],
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-openviking-memory' ||
      artifact.id.startsWith('memory:viking')
    ) {
      return 100
    }
    return 0
  },
  capabilities: { canEdit: false },
  component: MemoryViewerComponent,
  documentation: {
    summary: 'OpenViking 为星序提供跨 Agent 共享的项目级持久记忆与知识库支持。',
    sections: [
      {
        title: '虚拟文件系统 (viking://)',
        content: '提供按项目（projects/）、全局用户偏好（user/）和资源（resources/）分类的树状知识与记忆查看能力。',
      },
    ],
  },
}

function MemoryViewerComponent({ isActive }: ViewerContext) {
  if (!isActive) return null
  return <SidecarMemoryPanel />
}
