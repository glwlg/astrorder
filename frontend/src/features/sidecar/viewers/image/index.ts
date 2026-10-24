import { IconPhoto } from '@tabler/icons-react'
import type { ArtifactViewer } from '../../types'
import { ImageViewer } from './ImageViewer'
import { IMAGE_MIME_TYPES, imageMediaType } from './formats'

export const imageViewer: ArtifactViewer = {
  id: 'image-viewer',
  title: '图片查看器',
  description: '预览本地与远程图片，支持适应窗口、原始尺寸和下载',
  version: '1.0.0',
  badgeLabel: '[图片]',
  icon: IconPhoto,
  extensions: Object.keys(IMAGE_MIME_TYPES),
  supports: artifact => imageMediaType(artifact.name) ? 75 : artifact.mediaType.startsWith('image/') ? 40 : 0,
  component: ImageViewer,
  capabilities: { canEdit: false },
}
