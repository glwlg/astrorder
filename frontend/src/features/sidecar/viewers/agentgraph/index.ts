import { IconBrain } from '@tabler/icons-react'
import type { ArtifactRef } from '../../../../domain/artifact'
import type { ArtifactViewer } from '../../types'
import { AgentGraphViewer } from './AgentGraphViewer'

export const agentGraphViewer: ArtifactViewer = {
  id: 'agent-graph-viewer',
  title: 'Agent 决策状态机',
  description: '全景呈现 Agent 思考链 (CoT)、任务规划堆栈与工具调用 DAG 决策拓扑',
  version: '1.0.0',
  badgeLabel: '[决策图]',
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
