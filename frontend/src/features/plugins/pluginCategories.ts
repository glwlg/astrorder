import type { ComponentType } from 'react'
import {
  IconCode,
  IconCpu,
  IconLayoutGrid,
  IconPhotoVideo,
  IconSitemap,
} from '@tabler/icons-react'
import type { ArtifactViewer, PluginCategory } from '../sidecar/types'

export type PluginTabId = 'all' | PluginCategory

export interface PluginCategoryMeta {
  id: PluginCategory
  label: string
  description: string
  icon: ComponentType<{ size?: number }>
}

export const PLUGIN_CATEGORIES: PluginCategoryMeta[] = [
  {
    id: 'development',
    label: '开发与数据',
    description: '数据库工具、代码编辑器、文件浏览、交互终端、代码比对与 Git Worktree',
    icon: IconCode,
  },
  {
    id: 'workspace',
    label: '工作台与协作',
    description: '导航扩展模块、侧边外链、黑板总线、知识记忆、并行对话与浏览器自动化',
    icon: IconLayoutGrid,
  },
  {
    id: 'diagram',
    label: '图表与白板',
    description: '矢量流程图、声明式架构图与自由手绘白板',
    icon: IconSitemap,
  },
  {
    id: 'media',
    label: '媒体与预览',
    description: 'HTML 原型、图片、流式视频与 3D/CAD 模型预览',
    icon: IconPhotoVideo,
  },
  {
    id: 'runtime',
    label: '本地运行时',
    description: '语音听写引擎、命令输出压缩等本地算力与工具链加速能力',
    icon: IconCpu,
  },
]

const BUILTIN_VIEWER_CATEGORIES: Record<string, PluginCategory> = {
  'dbx-viewer': 'development',
  'monaco-viewer': 'development',
  'filetree-viewer': 'development',
  'xterm-viewer': 'development',
  'git-diff-viewer': 'development',
  'diff-viewer': 'development',
  'blackboard-viewer': 'workspace',
  'memory-viewer': 'workspace',
  'sidecar-chat-viewer': 'workspace',
  'agent-graph-viewer': 'workspace',
  'browser-mirror-viewer': 'workspace',
  'drawio-viewer': 'diagram',
  'mermaid-viewer': 'diagram',
  'excalidraw-viewer': 'diagram',
  'html-viewer': 'media',
  'image-viewer': 'media',
  'video-viewer': 'media',
  'three-viewer': 'media',
}

export function resolveViewerCategory(viewer: ArtifactViewer): PluginCategory {
  return viewer.category || BUILTIN_VIEWER_CATEGORIES[viewer.id] || 'workspace'
}
