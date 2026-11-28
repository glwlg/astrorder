import type { ComponentType, LazyExoticComponent, ReactNode } from 'react'
import type { ArtifactRef } from '../../domain/artifact'
import type { Message, Session } from '../../domain/types'
import type { AppProfile } from '../office/appProfile'

export const EXTERNAL_PLUGIN_PANEL_VIEWER_ID = 'external-plugin-panel'
export const PENDING_PLUGIN_CONFIG_SESSION_KEY = 'astrorder:pending-plugin-config'

export interface Disposable {
  dispose(): void
}

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

export type ExtensionPageIcon = ComponentType<{
  size?: number
  weight?: 'thin' | 'light' | 'regular' | 'bold' | 'fill' | 'duotone'
  className?: string
  color?: string
}>

export interface ExtensionPageComponentProps {
  pluginId?: string
  pageId?: string
  surface?: 'desktop' | 'mobile'
  [key: string]: unknown
}

export type ExtensionPageComponent =
  | ComponentType<ExtensionPageComponentProps>
  | LazyExoticComponent<ComponentType<ExtensionPageComponentProps>>

export interface ExtensionPageCommandSpec {
  id?: string
  title?: string
  subtitle?: string
  to?: string
  keywords?: string
  icon?: ComponentType<{ size?: number; color?: string; className?: string }>
  hideInOfficeMode?: boolean
}

export interface ExtensionPageRegistration {
  /** 所属插件 ID（内置模块或官方/第三方 ESM 插件 ID） */
  pluginId: string
  /** 页面 ID（插件内唯一；用于路由与主导航顺序识别） */
  id: string
  /** 页面主标题 */
  title: string
  /** 桌面侧边栏紧凑导航文案（缺省使用 title） */
  navLabel?: string
  /** 移动端工作台抽屉卡片文案（缺省使用 title） */
  mobileLabel?: string
  /** 移动端顶栏标题（缺省使用 mobileLabel ?? title） */
  mobileHeaderTitle?: string
  /** 移动端顶栏无障碍标签 */
  mobileHeaderAriaLabel?: string
  /** 桌面导航图标（兼容 Phosphor / Tabler / 内联 SVG 组件） */
  icon?: ExtensionPageIcon
  /** 移动端导航图标（缺省复用 icon） */
  mobileIcon?: ComponentType<{ size?: number; color?: string; className?: string }>
  /** 桌面路由路径（内置如 '/meetings'；动态 ESM 页面缺省为 /ext-page/:pluginId/:pageId） */
  path?: string
  /** 附加桌面路由模式（如 '/groups/:groupId'） */
  routePaths?: string[]
  /** 移动端路由路径（内置如 '/mobile/meetings'；动态 ESM 页面缺省为 /mobile/ext-page/:pluginId/:pageId） */
  mobilePath?: string
  /** 排序权重，越小越靠前（默认 200） */
  order?: number
  /** 办公模式下是否隐藏并拦截访问 */
  hideInOfficeMode?: boolean
  /** 桌面端该页面是否保留次级侧栏（如会话/群聊列表） */
  hasSecondaryPanel?: boolean
  /** 是否在桌面左侧主导航展示（默认 true） */
  showInSidebar?: boolean
  /** 是否在移动端工作台导航抽屉展示（默认 true） */
  showInMobileApps?: boolean
  /** 桌面/通用页面组件（支持 React.lazy） */
  component: ExtensionPageComponent
  /** 移动端专用组件（缺省复用 component） */
  mobileComponent?: ExtensionPageComponent
  /** 移动端页面容器布局风格：'office' | 'module' | 'custom' */
  mobileLayout?: 'office' | 'module' | 'custom'
  /** 命令面板路由项配置；false 表示不生成，缺省自动生成一条跳转项 */
  command?: ExtensionPageCommandSpec | ExtensionPageCommandSpec[] | false
  /** 注册来源 */
  source?: 'builtin' | 'esm'
}

export interface ExtensionCommandRegistration {
  /** 所属插件 ID */
  pluginId: string
  /** 命令 ID（插件内唯一） */
  id: string
  /** 命令面板展示标题 */
  title: string
  /** 副标题或快捷键提示 */
  subtitle?: string
  /** 搜索匹配关键词 */
  keywords?: string
  /** 角标文案 */
  badge?: string
  /** 命令分类，默认 'action' */
  category?: 'route' | 'action'
  /** 图标组件 */
  icon?: ComponentType<{ size?: number; color?: string; className?: string }>
  /** 办公模式下是否隐藏 */
  hideInOfficeMode?: boolean
  /** 排序权重，越小越靠前（默认 200） */
  order?: number
  /** 触发执行回调 */
  run: () => void | Promise<void>
  /** 注册来源 */
  source?: 'builtin' | 'esm'
}

export interface MessageSendContext {
  session: Session
  text: string
  attachments: File[]
  metadata: Record<string, unknown>
}

export interface MessageInterceptorResult {
  text?: string
  cancel?: boolean
  reason?: string
  metadata?: Record<string, unknown>
}

export interface MessageInterceptorRegistration {
  pluginId: string
  id: string
  order?: number
  beforeSend: (
    ctx: MessageSendContext,
  ) => void | MessageInterceptorResult | Promise<void | MessageInterceptorResult>
}

export interface MarkdownBlockRendererProps {
  code: string
  language: string
  session?: Session | null
}

export interface MarkdownBlockRendererRegistration {
  pluginId: string
  id: string
  /** Lowercase language identifiers, e.g. ['plantuml', 'puml', 'vega-lite'] */
  languages: string[]
  priority?: number
  title?: string
  component: ComponentType<MarkdownBlockRendererProps>
}

export interface MessageDecoratorProps {
  message: Message
  session?: Session | null
  isLatest?: boolean
  isOfficeMode?: boolean
}

export interface MessageDecoratorRegistration {
  pluginId: string
  id: string
  position: 'header' | 'footer'
  order?: number
  match?: (message: Message, session?: Session | null) => boolean
  component: ComponentType<MessageDecoratorProps>
}

export interface ExtensionQueryOptions {
  profile?: AppProfile
  isOfficeMode?: boolean
  enabledPlugins?: Record<string, boolean>
  includeDisabled?: boolean
}

export function extensionPageKey(pluginId: string, pageId: string): string {
  return `${pluginId}:${pageId}`
}

export function resolveExtensionPagePath(
  page: Pick<ExtensionPageRegistration, 'pluginId' | 'id' | 'path'>,
): string {
  if (page.path) return page.path
  return `/ext-page/${encodeURIComponent(page.pluginId)}/${encodeURIComponent(page.id)}`
}

export function resolveExtensionPageMobilePath(
  page: Pick<ExtensionPageRegistration, 'pluginId' | 'id' | 'mobilePath'>,
): string {
  if (page.mobilePath) return page.mobilePath
  return `/mobile/ext-page/${encodeURIComponent(page.pluginId)}/${encodeURIComponent(page.id)}`
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
  'workspace:files.read': {
    id: 'workspace:files.read',
    label: '读取工作区文件',
    description: '允许通过宿主安全沙箱浏览、搜索与读取当前会话工作区内的文件（限工作区目录内）',
    risk: 'medium',
  },
  'workspace:files.write': {
    id: 'workspace:files.write',
    label: '写入工作区文件',
    description: '允许通过宿主安全沙箱在当前会话工作区内创建目录或原子写入文件（记录审计日志）',
    risk: 'high',
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
  'desktop:control': {
    id: 'desktop:control',
    label: '宿主机桌面控制',
    description: '允许查看宿主机桌面屏幕与 UI 控件树，并在交互模式下模拟鼠标点击、键盘输入、窗口切换与剪贴板操作',
    risk: 'high',
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
