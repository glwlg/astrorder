import { IconMovie } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { VideoViewer } from './VideoViewer'
import { VIDEO_MIME_TYPES, videoMediaType } from './formats'

export const videoViewer: ArtifactViewer = {
  id: 'video-viewer',
  title: '视频播放器',
  description: '流式播放本地与远程视频，支持倍速、进度拖拽、画中画、全屏与下载',
  version: '1.0.0',
  badgeLabel: '[视频]',
  icon: IconMovie,
  extensions: Object.keys(VIDEO_MIME_TYPES),
  supports: artifact => videoMediaType(artifact.name) ? 80 : artifact.mediaType.startsWith('video/') ? 45 : 0,
  component: VideoViewer,
  capabilities: { canEdit: false },
}
