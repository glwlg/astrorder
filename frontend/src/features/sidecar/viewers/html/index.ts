import { IconWorld } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { HtmlViewer } from './HtmlViewer'

export const htmlViewer: ArtifactViewer = {
  id: 'html-viewer',
  title: 'HTML 页面预览',
  description: '在安全隔离的沙箱 Webview 中实时运行并渲染 HTML 交互原型与汇报页面',
  version: '1.0.0',
  badgeLabel: '[页面]',
  tabColor: '#06b6d4',
  icon: IconWorld,
  extensions: ['.html', '.htm'],
  mimeTypes: ['text/html'],
  component: HtmlViewer,
  capabilities: {
    canEdit: false,
  },
}
