import { IconWorld } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { BrowserMirrorViewer } from './BrowserMirrorViewer'

export const browserMirrorViewer: ArtifactViewer = {
  id: 'browser-mirror-viewer',
  title: '真实浏览器镜像',
  description: '查看星序可视浏览器的最新画面',
  version: '1.0.0',
  tabColor: '#3b82f6',
  icon: IconWorld,
  supports: (artifact) => artifact.id.startsWith('browser:') ? 100 : 0,
  component: BrowserMirrorViewer,
  capabilities: { canEdit: false },
}
