import { IconPresentation } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { PptxViewer } from './PptxViewer'

export const pptxViewer: ArtifactViewer = {
  id: 'pptx-viewer',
  title: 'PPT 演示预览',
  description: '预览 PowerPoint (.pptx) 幻灯片页面、表格、插图与演讲备注，支持一键引用到对话',
  version: '1.0.0',
  category: 'office',
  badgeLabel: 'PPT 演示',
  tabColor: '#ea580c',
  icon: IconPresentation,
  extensions: ['.pptx'],
  mimeTypes: ['application/vnd.openxmlformats-officedocument.presentationml.presentation'],
  supports: (artifact) => {
    const name = (artifact.name || artifact.path || '').toLowerCase()
    const mime = (artifact.mediaType || '').toLowerCase()
    if (name.endsWith('.pptx')) return 95
    if (mime === 'application/vnd.openxmlformats-officedocument.presentationml.presentation') return 90
    return 0
  },
  component: PptxViewer,
  capabilities: { canEdit: false },
}
