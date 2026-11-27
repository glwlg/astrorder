import {
  defineAstrorderPlugin,
  type ArtifactRef,
  type AstrorderPluginContext,
  type ViewerConfigOption,
  type ViewerContext,
} from '@astrorder/sdk'
import { DrawioViewer } from './DrawioViewer'
import { VectorTriangleIcon } from './icons'
import './drawioViewer.css'

const DRAWIO_EXTENSIONS = ['.drawio', '.drawio.xml', '.drawio.svg', '.drawio.png', '.dio']
const DRAWIO_MIME_TYPES = ['application/vnd.jgraph.mxfile', 'application/x-drawio']

const DRAWIO_CONFIG_OPTIONS: ViewerConfigOption[] = [
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
]

function supportsDrawio(artifact: ArtifactRef): number {
  const lower = (artifact.name || artifact.path || '').toLowerCase().split(/[?#]/)[0]
  if (
    lower.endsWith('.drawio.xml') ||
    lower.endsWith('.drawio.svg') ||
    lower.endsWith('.drawio.png')
  ) {
    return 96
  }
  if (lower.endsWith('.drawio') || lower.endsWith('.dio')) {
    return 95
  }
  const mime = (artifact.mediaType || '').toLowerCase()
  if (DRAWIO_MIME_TYPES.includes(mime)) {
    return 90
  }
  return 0
}

export function activate(ctx: AstrorderPluginContext): void {
  const BoundDrawioViewer = (props: ViewerContext) => (
    <DrawioViewer {...props} ctx={ctx} />
  )

  ctx.registerViewer({
    id: 'drawio-viewer',
    title: '流程图与架构图编辑器',
    description: '支持交互式查看与全功能编辑矢量架构图、泳道图及流程图，可切换官方或自托管绘制服务',
    version: ctx.version,
    category: 'diagram',
    capabilitySlot: 'diagram-flowchart',
    capabilityLabel: '流程图',
    providerConfigKey: 'provider',
    badgeLabel: '[流程图]',
    tabColor: 'var(--astr-indigo)',
    icon: VectorTriangleIcon,
    extensions: DRAWIO_EXTENSIONS,
    mimeTypes: DRAWIO_MIME_TYPES,
    supports: supportsDrawio,
    component: BoundDrawioViewer,
    capabilities: {
      canEdit: true,
    },
    configOptions: DRAWIO_CONFIG_OPTIONS,
  })
}

export function deactivate(): void {
  // All viewer registrations are automatically released via ctx.subscriptions.
}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
