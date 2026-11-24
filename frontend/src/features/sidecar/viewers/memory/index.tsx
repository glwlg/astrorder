import { IconBrain } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer, ViewerContext } from '../../types'
import { SidecarMemoryPanel } from '../../SidecarMemoryPanel'

export const memoryViewer: ArtifactViewer = {
  id: 'memory-viewer',
  title: '知识记忆',
  description: '跨 Agent 共享的项目级持久记忆与知识库，支持查看知识树与一键下发多环境 Hooks',
  version: '0.1.0',
  category: 'workspace',
  capabilitySlot: 'knowledge-memory',
  capabilityLabel: '知识记忆',
  providerConfigKey: 'provider',
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
  configOptions: [
    {
      key: 'provider',
      label: '工具实现',
      description: '选择跨 Agent 知识库与长期记忆的底层实现，可按需替换或停用',
      type: 'select',
      options: [{ label: 'OpenViking', value: 'openviking' }],
      defaultValue: 'openviking',
    },
  ],
  settingsAction: { tab: 'models', label: '配置记忆服务' },
  component: MemoryViewerComponent,
  documentation: {
    summary: '提供星序的跨 Agent 项目级持久记忆与知识库能力（当前内置 OpenViking 虚拟文件系统实现）。',
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
