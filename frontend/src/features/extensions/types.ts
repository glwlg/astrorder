import type { ComponentType, ReactNode } from 'react'
import type { ArtifactRef } from '../../domain/artifact'
import type { Session } from '../../domain/types'

export const EXTERNAL_PLUGIN_PANEL_VIEWER_ID = 'external-plugin-panel'
export const PENDING_PLUGIN_CONFIG_SESSION_KEY = 'astrorder:pending-plugin-config'

export interface StatusBarSlotContext {
  session: Session
  statusUnavailable: boolean
  statusLoading: boolean
  branch: string | null
}

export interface PluginPanelContext {
  pluginId: string
  panelId: string
  artifact: ArtifactRef
  session?: Session | null
  isActive?: boolean
  surface?: 'desktop' | 'mobile'
  onClose?: () => void
}

export interface BuiltInGitMenuItem {
  id: string
  groupLabel?: string
  label: string
  icon?: ComponentType<{ size?: number; color?: string }>
  iconColor?: string
  requiresWorkspace?: boolean
  onClick: (session: Session) => void
}

export interface BuiltInWelcomeMenuItem {
  id: string
  label: string
  shortcut?: string
  icon?: ComponentType<{ size?: number; color?: string }>
  iconColor?: string
  requiresWorkspace?: boolean
  hideInOfficeMode?: boolean
  onClick: (session: Session) => void
}

export interface BuiltInPanelRegistration {
  title: string
  badgeLabel?: string
  tabColor?: string
  mediaType?: string
  component: ComponentType<PluginPanelContext>
}

export interface BuiltInExtensionRegistration {
  pluginId: string
  settingsTitle?: string
  settingsButtonLabel?: string
  settingsComponent?: ComponentType<{ embedded?: boolean; onClose?: () => void }>
  statusBarComponent?: ComponentType<StatusBarSlotContext>
  gitMenuItems?: BuiltInGitMenuItem[]
  welcomeMenuItems?: BuiltInWelcomeMenuItem[]
  panels?: Record<string, BuiltInPanelRegistration>
}

export interface WelcomeMenuExtraItem {
  id: string
  icon: ReactNode
  label: string
  shortcut?: string
  onClick: () => void
}

export interface OpenPluginPanelEventDetail {
  pluginId: string
  panelId: string
  params?: Record<string, unknown>
  title?: string
  mediaType?: string
  session?: Session | null
}

export interface OpenPluginConfigEventDetail {
  pluginId: string
}

export interface ParsedPluginPanelArtifact {
  pluginId: string | null
  panelId: string | null
  params: Record<string, unknown>
}

export function parsePluginPanelArtifact(artifact: ArtifactRef): ParsedPluginPanelArtifact {
  let pluginId: string | null = null
  let panelId: string | null = null
  const params: Record<string, unknown> = {}

  if (artifact.metadata && typeof artifact.metadata === 'object') {
    if (typeof artifact.metadata.pluginId === 'string' && artifact.metadata.pluginId) {
      pluginId = artifact.metadata.pluginId
    }
    if (typeof artifact.metadata.panelId === 'string' && artifact.metadata.panelId) {
      panelId = artifact.metadata.panelId
    }
    if (artifact.metadata.params && typeof artifact.metadata.params === 'object') {
      Object.assign(params, artifact.metadata.params as Record<string, unknown>)
    }
  }

  if ((!pluginId || !panelId) && artifact.id.startsWith('ext:')) {
    const parts = artifact.id.split(':')
    if (parts.length >= 3) {
      if (!pluginId && parts[1]) pluginId = parts[1]
      if (!panelId && parts[2]) panelId = parts[2]
    }
  }

  const mediaPrefix = 'application/x-astrorder-ext.'
  if ((!pluginId || !panelId) && artifact.mediaType?.startsWith(mediaPrefix)) {
    const rest = artifact.mediaType.slice(mediaPrefix.length)
    const lastDot = rest.lastIndexOf('.')
    if (lastDot > 0) {
      if (!pluginId) pluginId = rest.slice(0, lastDot)
      if (!panelId) panelId = rest.slice(lastDot + 1)
    } else if (!pluginId && rest) {
      pluginId = rest
    }
  }

  if (artifact.readUrl && artifact.readUrl.includes('?')) {
    try {
      const qs = artifact.readUrl.slice(artifact.readUrl.indexOf('?') + 1)
      const search = new URLSearchParams(qs)
      search.forEach((value, key) => {
        if (params[key] === undefined) {
          params[key] = value
        }
      })
    } catch {
      // ignore malformed query strings
    }
  }

  return { pluginId, panelId, params }
}

export interface PermissionMeta {
  id: string
  label: string
  description: string
  risk: 'low' | 'medium' | 'high'
}

export const PERMISSION_CATALOG: Record<string, PermissionMeta> = {
  'network:outbound': {
    id: 'network:outbound',
    label: '外部网络访问',
    description: '允许扩展后端向外部服务或局域网接口发起 HTTP 请求',
    risk: 'high',
  },
  'workspace:git.read': {
    id: 'workspace:git.read',
    label: '读取工作区 Git 信息',
    description: '允许读取当前会话工作区的远程仓库地址、分支名与提交哈希',
    risk: 'medium',
  },
  'storage:kv': {
    id: 'storage:kv',
    label: '插件键值存储',
    description: '允许在扩展专属命名空间下持久化保存配置与状态数据',
    risk: 'low',
  },
  'storage:secrets': {
    id: 'storage:secrets',
    label: '加密凭据存储',
    description: '允许读取与保存扩展自己的加密 Token 或 API 密钥（仅本扩展可访问）',
    risk: 'high',
  },
  'agent:capabilities': {
    id: 'agent:capabilities',
    label: '注册 Agent 能力与技能',
    description: '允许向会话中的 Agent 暴露可调用命令工具与技能文档',
    risk: 'medium',
  },
  'ui:slots': {
    id: 'ui:slots',
    label: '界面扩展槽位',
    description: '允许在底栏状态条、Git 菜单或右侧工作台注册入口与沙箱面板',
    risk: 'low',
  },
  'signals:provide': {
    id: 'signals:provide',
    label: '工作区异常信号上报',
    description: '允许向工作复盘与诊断系统提供失败任务或告警线索',
    risk: 'low',
  },
}

export function getPermissionMeta(permission: string): PermissionMeta {
  return (
    PERMISSION_CATALOG[permission] || {
      id: permission,
      label: permission,
      description: '扩展声明的自定义访问权限',
      risk: 'medium',
    }
  )
}
