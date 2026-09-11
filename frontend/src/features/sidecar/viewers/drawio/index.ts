import { IconVectorTriangle } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { DrawioViewer } from './DrawioViewer'

export const drawioViewer: ArtifactViewer = {
  id: 'drawio-viewer',
  title: 'Draw.io 架构与流程图',
  description: '支持交互式查看与全功能编辑矢量架构图、泳道图及流程图（基于 diagrams.net 官方内核）',
  version: '1.0.0',
  badgeLabel: '[流程图]',
  tabColor: 'var(--astr-indigo)',
  icon: IconVectorTriangle,
  extensions: ['.drawio', '.drawio.xml', '.drawio.svg'],
  mimeTypes: ['application/vnd.jgraph.mxfile'],
  component: DrawioViewer,
  capabilities: {
    canEdit: true,
  },
  configOptions: [
    {
      key: 'autosave',
      label: '实时自动暂存修改',
      description: '在画布上微调节点时实时同步至内存缓冲区',
      type: 'boolean',
      defaultValue: true,
    },
    {
      key: 'theme',
      label: '图表界面主题',
      description: 'Draw.io 默认界面配色方案',
      type: 'select',
      options: [
        { label: '自动适应', value: 'auto' },
        { label: '浅色极简', value: 'min' },
        { label: '深色模式', value: 'dark' },
      ],
      defaultValue: 'min',
    },
  ],
}
