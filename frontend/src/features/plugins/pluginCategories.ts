import type { ComponentType } from 'react'
import {
  IconBriefcase,
  IconCode,
  IconCpu,
  IconLayoutGrid,
  IconPhotoVideo,
  IconRobot,
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
    id: 'agent',
    label: 'Agent 引擎',
    description: '按需启用 Antigravity、Codex、Hermes、Grok 等原生 Agent；启用时自动体检、引导安装或指定已有路径',
    icon: IconRobot,
  },
  {
    id: 'development',
    label: '开发与数据',
    description: '数据库工具、代码编辑器、文件浏览、交互终端、代码比对与 Git Worktree',
    icon: IconCode,
  },
  {
    id: 'office',
    label: '办公与文档',
    description: 'PDF / Word / Excel / PPT 预览导出、长文档分段精修、智能工作复盘与会议纪要闭环',
    icon: IconBriefcase,
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
    label: '本地运行时与工具链',
    description: '常用工具（uv、Node.js/npm、Git、ripgrep）辅助安装、命令输出压缩与本地语音听写引擎',
    icon: IconCpu,
  },
]

const BUILTIN_VIEWER_CATEGORIES: Record<string, PluginCategory> = {
  'dbx-viewer': 'development',
  'monaco-viewer': 'development',
  'filetree-viewer': 'development',
  'xterm-viewer': 'development',
  'git-diff-viewer': 'development',
  'git-worktrees-viewer': 'development',
  'gitlab-ci-viewer': 'development',
  'diff-viewer': 'development',
  'pdf-viewer': 'office',
  'docx-viewer': 'office',
  'sheet-viewer': 'office',
  'pptx-viewer': 'office',
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
  'audio-viewer': 'media',
  'three-viewer': 'media',
}

export function resolveViewerCategory(viewer: ArtifactViewer): PluginCategory {
  return viewer.category || BUILTIN_VIEWER_CATEGORIES[viewer.id] || 'workspace'
}
