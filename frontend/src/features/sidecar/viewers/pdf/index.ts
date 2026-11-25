import { IconFileTypePdf } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { PdfViewer } from './PdfViewer'

export const pdfViewer: ArtifactViewer = {
  id: 'pdf-viewer',
  title: 'PDF 阅读器',
  description: '调用原生渲染引擎无损预览工作区及远程 PDF 文档，支持目录缩略图、全文搜索、打印与一键引用到当前对话',
  version: '1.0.0',
  category: 'office',
  badgeLabel: 'PDF 文档',
  tabColor: '#dc2626',
  icon: IconFileTypePdf,
  extensions: ['.pdf'],
  mimeTypes: ['application/pdf'],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.pdf')) return 95
    if ((artifact.mediaType || '').toLowerCase() === 'application/pdf') return 90
    return 0
  },
  component: PdfViewer,
  capabilities: { canEdit: false },
}
