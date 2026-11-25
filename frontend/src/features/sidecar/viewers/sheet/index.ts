import { IconFileSpreadsheet } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { SheetViewer } from './SheetViewer'

export const sheetViewer: ArtifactViewer = {
  id: 'sheet-viewer',
  title: 'Excel / CSV 表格查看器',
  description: '零依赖解析 .xlsx 多工作表与 UTF-8/GBK 编码 .csv/.tsv，支持搜索筛选、点击列头排序、数值汇总统计及选中行发给对话分析',
  version: '1.0.0',
  category: 'office',
  badgeLabel: '电子表格',
  tabColor: '#15803d',
  icon: IconFileSpreadsheet,
  extensions: ['.xlsx', '.csv', '.tsv'],
  mimeTypes: [
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    'text/csv',
    'text/tab-separated-values',
  ],
  supports: (artifact) => {
    const lower = (artifact.name || '').toLowerCase()
    if (lower.endsWith('.xlsx')) return 95
    if (lower.endsWith('.csv') || lower.endsWith('.tsv')) return 92
    const mime = (artifact.mediaType || '').toLowerCase()
    if (mime.includes('spreadsheetml') || mime === 'text/csv') return 88
    return 0
  },
  component: SheetViewer,
  capabilities: { canEdit: false },
}
