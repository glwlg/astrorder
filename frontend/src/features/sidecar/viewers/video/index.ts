import { IconMovie } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { VideoViewer } from './VideoViewer'
import { VIDEO_MIME_TYPES, videoMediaType } from './formats'

export const videoViewer: ArtifactViewer = {
  id: 'video-viewer',
  title: '视频审片台',
  description: '流式播放本地与远程视频，支持截帧引用到对话、审片打点批注、逐帧步进、A-B 循环与同目录字幕联动',
  version: '1.0.0',
  badgeLabel: '[视频]',
  icon: IconMovie,
  extensions: Object.keys(VIDEO_MIME_TYPES),
  supports: artifact => videoMediaType(artifact.name) ? 80 : artifact.mediaType.startsWith('video/') ? 45 : 0,
  component: VideoViewer,
  capabilities: { canEdit: false },
}
