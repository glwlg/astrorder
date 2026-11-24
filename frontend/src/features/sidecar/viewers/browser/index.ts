import { IconWorld } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { BrowserMirrorViewer } from './BrowserMirrorViewer'

export const browserMirrorViewer: ArtifactViewer = {
  id: 'browser-mirror-viewer',
  title: '浏览器自动化',
  description: '驱动可视浏览器执行网页任务并在右侧实时镜像画面，支持按需切换底层自动化引擎',
  version: '1.0.0',
  category: 'workspace',
  capabilitySlot: 'browser-automation',
  capabilityLabel: '浏览器自动化',
  tabColor: '#3b82f6',
  icon: IconWorld,
  supports: (artifact) => artifact.id.startsWith('browser:') ? 100 : 0,
  component: BrowserMirrorViewer,
  capabilities: { canEdit: false },
}
