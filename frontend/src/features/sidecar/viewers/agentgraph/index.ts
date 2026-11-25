import { IconBrain } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { AgentGraphViewer } from './AgentGraphViewer'

export const agentGraphViewer: ArtifactViewer = {
  id: 'agent-graph-viewer',
  title: '决策与轨迹',
  description: '按轮次聚合 Agent 阶段决策、文件影响热图、命令验证与报错自愈追踪',
  version: '2.0.0',
  badgeLabel: '[轨迹]',
  tabColor: '#a855f7',
  icon: IconBrain,
  extensions: ['.agentgraph'],
  mimeTypes: ['application/x-agent-graph'],
  capabilities: {
    canEdit: false,
  },
  supports: (artifact: ArtifactRef): number => {
    if (
      artifact.mediaType === 'application/x-agent-graph' ||
      artifact.id.startsWith('agentgraph:')
    ) {
      return 100
    }
    return 0
  },
  component: AgentGraphViewer,
}
