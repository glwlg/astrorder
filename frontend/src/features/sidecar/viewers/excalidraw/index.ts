import { IconPencil } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { ExcalidrawViewer } from './ExcalidrawViewer'

export const excalidrawViewer: ArtifactViewer = {
  id: 'excalidraw-viewer',
  title: 'Excalidraw 白板',
  description: '轻量自由手绘白板，适合头脑风暴、逻辑设计与自由拓扑标注',
  version: '1.0.0',
  badgeLabel: '[白板]',
  tabColor: '#f59e0b',
  icon: IconPencil,
  extensions: ['.excalidraw', '.excalidraw.json', '.excalidraw.svg'],
  mimeTypes: ['application/vnd.excalidraw+json'],
  component: ExcalidrawViewer,
  capabilities: {
    canEdit: true,
  },
}
