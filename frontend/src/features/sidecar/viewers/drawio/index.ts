import { IconVectorTriangle } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { DrawioViewer } from './DrawioViewer'

export const drawioViewer: ArtifactViewer = {
  id: 'drawio-viewer',
  title: '矢量流程图',
  description: '支持交互式查看与全功能编辑矢量架构图、泳道图及流程图，可切换官方或自托管绘制服务',
  version: '1.0.0',
  category: 'diagram',
  capabilitySlot: 'diagram-flowchart',
  capabilityLabel: '流程图',
  providerConfigKey: 'provider',
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
      key: 'provider',
      label: '工具实现',
      description: '选择流程图绘制服务源，支持官方 diagrams.net 或内网自托管 Draw.io 实例',
      type: 'select',
      options: [
        { label: 'Draw.io (官方)', value: 'diagrams-net' },
        { label: 'Draw.io (自托管)', value: 'self-hosted' },
      ],
      defaultValue: 'diagrams-net',
    },
    {
      key: 'customOrigin',
      label: '自托管服务地址',
      description: '内网自托管 Draw.io / diagrams.net 的 http(s) 地址（如 http://127.0.0.1:8080）',
      type: 'string',
      defaultValue: '',
      visibleWhen: { key: 'provider', value: 'self-hosted' },
    },
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
      description: '流程图编辑器默认界面配色方案',
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
