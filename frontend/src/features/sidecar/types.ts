import type { ComponentType, ReactNode } from 'react'
import type { ArtifactRef } from '../../domain/artifact'

export interface ViewerContext {
  artifact: ArtifactRef
  onSave?: (content: string) => Promise<boolean>
  onDirtyChange?: (dirty: boolean) => void
  onClose?: () => void
}

export interface ViewerConfigOption {
  key: string
  label: string
  description?: string
  type: 'string' | 'boolean' | 'number' | 'select'
  options?: { label: string; value: string }[]
  defaultValue: unknown
}

export interface ArtifactViewer {
  id: string
  title: string
  description?: string
  version?: string
  badgeLabel?: string // 如 "[流程图]"、"[架构图]"、"[白板]"
  tabColor?: string // 标签页图标主色调
  icon: ComponentType<{ size?: number; className?: string }>
  
  /** 声明该插件支持的文件扩展名列表，如 ['.drawio', '.drawio.xml'] */
  extensions?: string[]
  
  /** 声明该插件支持的 MIME 类型列表，如 ['application/vnd.jgraph.mxfile'] */
  mimeTypes?: string[]

  /** 返回匹配权重，<=0 表示不支持，越高越优先（默认根据 extensions / mimeTypes 自动打分） */
  supports?: (artifact: ArtifactRef) => number
  
  component: ComponentType<ViewerContext>
  renderBadge?: (artifact: ArtifactRef, onOpen: () => void) => ReactNode
  capabilities: {
    canEdit: boolean
  }
  /** 可配置选项声明 */
  configOptions?: ViewerConfigOption[]
}
