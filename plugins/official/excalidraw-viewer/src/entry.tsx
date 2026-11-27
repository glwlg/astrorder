import {
  defineAstrorderPlugin,
  type ArtifactRef,
  type AstrorderPluginContext,
  type ViewerContext,
} from '@astrorder/sdk'
import { ExcalidrawViewer } from './ExcalidrawViewer'
import { PencilIcon } from './icons'
import './excalidrawViewer.css'

const EXCALIDRAW_EXTENSIONS = ['.excalidraw', '.excalidraw.json', '.excalidraw.svg']
const EXCALIDRAW_MIME_TYPES = ['application/vnd.excalidraw+json']

function supportsExcalidraw(artifact: ArtifactRef): number {
  const lower = (artifact.name || artifact.path || '').toLowerCase().split(/[?#]/)[0]
  if (lower.endsWith('.excalidraw.json') || lower.endsWith('.excalidraw.svg')) {
    return 96
  }
  if (lower.endsWith('.excalidraw')) {
    return 95
  }
  const mime = (artifact.mediaType || '').toLowerCase()
  if (EXCALIDRAW_MIME_TYPES.includes(mime)) {
    return 90
  }
  return 0
}

export function activate(ctx: AstrorderPluginContext): void {
  const BoundExcalidrawViewer = (props: ViewerContext) => (
    <ExcalidrawViewer {...props} ctx={ctx} />
  )

  ctx.registerViewer({
    id: 'excalidraw-viewer',
    title: '手绘白板',
    description: '轻量自由手绘白板，适合头脑风暴、逻辑设计与自由拓扑标注',
    version: ctx.version,
    category: 'diagram',
    capabilitySlot: 'whiteboard',
    capabilityLabel: '白板',
    implementationName: 'Excalidraw',
    badgeLabel: '[白板]',
    tabColor: '#f59e0b',
    icon: PencilIcon,
    extensions: EXCALIDRAW_EXTENSIONS,
    mimeTypes: EXCALIDRAW_MIME_TYPES,
    supports: supportsExcalidraw,
    component: BoundExcalidrawViewer,
    capabilities: {
      canEdit: true,
    },
  })
}

export function deactivate(): void {
  // All viewer registrations are automatically released via ctx.subscriptions.
}

export default defineAstrorderPlugin({
  activate,
  deactivate,
})
