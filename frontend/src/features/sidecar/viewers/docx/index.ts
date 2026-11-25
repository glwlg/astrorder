import { IconFileTypeDocx } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { DocxViewer } from './DocxViewer'

export const docxViewer: ArtifactViewer = {
  id: 'docx-viewer',
  title: 'Word 文档查看与比对器',
  description: '零依赖解析并按 A4 纸张排版预览 .docx 文档，支持左侧目录大纲跳转、划词引用给 Agent 审阅、双版红线 Diff 与打印导出 PDF',
  version: '1.0.0',
  category: 'office',
  badgeLabel: 'Word 文档',
  tabColor: '#2563eb',
  icon: IconFileTypeDocx,
  extensions: ['.docx'],
  mimeTypes: ['application/vnd.openxmlformats-officedocument.wordprocessingml.document'],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.docx')) return 95
    if ((artifact.mediaType || '').toLowerCase().includes('wordprocessingml')) return 90
    return 0
  },
  component: DocxViewer,
  capabilities: { canEdit: false },
}
