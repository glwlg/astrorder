import { IconMusic } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { AudioViewer } from './AudioViewer'
import { AUDIO_MIME_TYPES, audioMediaType } from './formats'

export const audioViewer: ArtifactViewer = {
  id: 'audio-viewer',
  title: '音频试听器',
  description: '流式试听配音、配乐与混音音轨，支持倍速、A-B 循环、同目录切轨与时间点引用',
  version: '1.0.0',
  category: 'media',
  badgeLabel: '[音频]',
  icon: IconMusic,
  extensions: Object.keys(AUDIO_MIME_TYPES),
  supports: (artifact) => (audioMediaType(artifact.name) ? 80 : artifact.mediaType.startsWith('audio/') ? 45 : 0),
  component: AudioViewer,
  capabilities: { canEdit: false },
}
