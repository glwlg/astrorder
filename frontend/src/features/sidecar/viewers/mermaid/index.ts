import { IconChartDots } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { MermaidViewer } from './MermaidViewer'

export const mermaidViewer: ArtifactViewer = {
  id: 'mermaid-viewer',
  title: 'Mermaid 架构图',
  description: '基于纯文本代码声明式渲染时序图、流程图与甘特图',
  version: '1.0.0',
  badgeLabel: '[架构图]',
  tabColor: '#10b981',
  icon: IconChartDots,
  extensions: ['.mmd', '.mermaid'],
  mimeTypes: ['text/vnd.mermaid'],
  component: MermaidViewer,
  capabilities: {
    canEdit: true,
  },
}
