import { IconMessages } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { SideChatViewer } from './SideChatViewer'

export const sideChatViewer: ArtifactViewer = {
  id: 'sidecar-chat-viewer',
  title: '侧边聊天',
  description: '轻量并行的侧边对话窗口，支持与主会话或随身 AI 并行交互',
  version: '1.0.0',
  badgeLabel: '[侧边聊天]',
  tabColor: '#6366f1',
  icon: IconMessages,
  extensions: ['.sidechat'],
  mimeTypes: ['application/x-astrorder-side-chat'],
  capabilities: {
    canEdit: false,
  },
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-astrorder-side-chat' ||
      artifact.id.startsWith('sidechat:')
    ) {
      return 100
    }
    return 0
  },
  component: SideChatViewer,
}
