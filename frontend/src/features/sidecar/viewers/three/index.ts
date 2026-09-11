import { IconBox } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { ThreeViewer } from './ThreeViewer'

export const threeViewer: ArtifactViewer = {
  id: 'three-viewer',
  title: '3D 模型/CAD 预览',
  description: '基于 Three.js WebGL 视口，支持 3D 打印网格与 CAD 模型 360° 自由旋转预览',
  version: '1.0.0',
  badgeLabel: '[3D]',
  tabColor: '#8b5cf6',
  icon: IconBox,
  extensions: ['.stl', '.obj', '.gltf', '.glb'],
  mimeTypes: ['model/gltf+json', 'model/stl'],
  component: ThreeViewer,
  capabilities: {
    canEdit: false,
  },
}
