import { defineAstrorderPlugin, type AstrorderPluginContext, type ViewerContext } from '@astrorder/sdk'
import { BoxIcon } from './icons'
import { ThreeViewer } from './ThreeViewer'
import './threeViewer.css'

export function activate(ctx: AstrorderPluginContext): void {
  const BoundThreeViewer = (props: ViewerContext) => <ThreeViewer {...props} ctx={ctx} />

  ctx.registerViewer({
    id: 'three-viewer',
    title: '3D 模型/CAD 预览',
    description: '基于 Three.js WebGL 视口，支持 STL、OBJ、GLTF、GLB 模型 360° 轨道旋转缩放与线框检视',
    version: ctx.version,
    category: 'media',
    badgeLabel: '[3D]',
    tabColor: '#8b5cf6',
    icon: BoxIcon,
    extensions: ['.stl', '.obj', '.gltf', '.glb'],
    mimeTypes: [
      'model/stl',
      'model/obj',
      'model/gltf+json',
      'model/gltf-binary',
      'application/sla',
    ],
    component: BoundThreeViewer,
    capabilities: {
      canEdit: false,
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
