import type { ComponentType, LazyExoticComponent, ReactNode } from 'react'

export declare const SDK_VERSION: 1

export interface Disposable {
  dispose(): void
}

export type AppProfile = 'office' | 'developer'
export type ThemeMode = 'light' | 'dark'

export type PluginCategory =
  | 'agent'
  | 'development'
  | 'office'
  | 'workspace'
  | 'diagram'
  | 'media'
  | 'runtime'

export type ArtifactKind = 'workspace_file' | 'attachment' | 'remote_url'

export interface ArtifactRef {
  id: string
  sessionId: string
  agentId: string
  connectionId?: string
  name: string
  kind: ArtifactKind
  path?: string
  readUrl: string
  mediaType: string
  writable: boolean
  metadata?: Record<string, unknown>
}

export interface Session {
  id: string
  agent_id: string
  title: string
  workspace: string | null
  status: 'idle' | 'running' | 'waiting_approval' | 'error'
  updated_at: string
  last_user_at?: string
  live?: boolean
  source_id?: string
  connection_id?: string | null
  source_session_id?: string
  project_id?: string | null
  project_name?: string | null
  [key: string]: unknown
}

export interface OpenSqlFileParams {
  path: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  content?: string
  line?: number
  forceNew?: boolean
}

export interface OpenSqlSnippetParams {
  sql: string
  filename?: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  forceNew?: boolean
}

export interface DatabaseToolProvider {
  id: string
  implementationName: string
  mediaType: string
  openWorkbench: (sessionId?: string, agentId?: string, connectionId?: string) => void
  openSqlFile: (params: OpenSqlFileParams) => Promise<void>
  openSqlSnippet: (params: OpenSqlSnippetParams) => void
}

export interface OpenPluginPanelOptions {
  title?: string
  mediaType?: string
  viewerId?: string
  sessionId?: string
  agentId?: string
  workspacePath?: string
  connectionId?: string
  params?: Record<string, unknown>
}

export interface PluginOpenSettingsOptions {
  tab?: string
  pluginId?: string
}

export type ComposerQuoteTarget =
  | Pick<Session, 'id' | 'agent_id'>
  | { sessionId?: string; agentId?: string }

export interface ViewerContext {
  artifact: ArtifactRef
  session?: Session | null
  isActive?: boolean
  surface?: 'desktop' | 'mobile'
  toolbarAction?: ReactNode
  onSave?: (content: string) => Promise<boolean>
  onDirtyChange?: (dirty: boolean) => void
  onClose?: () => void
}

export type ArtifactViewerComponent =
  | ComponentType<ViewerContext>
  | LazyExoticComponent<ComponentType<ViewerContext>>

export interface ViewerConfigOption {
  key: string
  label: string
  description?: string
  type: 'string' | 'boolean' | 'number' | 'select'
  options?: { label: string; value: string }[]
  defaultValue: unknown
  min?: number
  max?: number
  step?: number
  visibleWhen?: { key: string; value: string | boolean }
}

export type ViewerIconComponent = ComponentType<{ size?: number; className?: string }>

export interface PluginViewerContribution {
  id?: string
  title: string
  description?: string
  version?: string
  category?: PluginCategory
  capabilitySlot?: string
  capabilityLabel?: string
  providerConfigKey?: string
  implementationName?: string
  badgeLabel?: string
  tabColor?: string
  icon?: ViewerIconComponent
  extensions?: string[]
  mimeTypes?: string[]
  supports?: (artifact: ArtifactRef) => number
  component: ArtifactViewerComponent
  renderBadge?: (artifact: ArtifactRef, onOpen: () => void) => ReactNode
  capabilities?: {
    canEdit: boolean
  }
  configOptions?: ViewerConfigOption[]
  settingsAction?: { tab: string; label: string }
  documentation?: {
    summary?: string
    sections?: Array<{ title: string; content: string }>
    supportedComponents?: Array<{
      name: string
      label: string
      description: string
      triggerKeys?: string[]
    }>
  }
}

export type ExtensionPageIcon = ComponentType<{
  size?: number
  weight?: 'thin' | 'light' | 'regular' | 'bold' | 'fill' | 'duotone'
  className?: string
  color?: string
}>

export interface PluginPageProps {
  pluginId?: string
  pageId?: string
  surface?: 'desktop' | 'mobile'
}

export type ExtensionPageComponent =
  | ComponentType<PluginPageProps>
  | LazyExoticComponent<ComponentType<PluginPageProps>>

export interface ExtensionPageCommandSpec {
  id?: string
  title?: string
  subtitle?: string
  to?: string
  keywords?: string
  icon?: ComponentType<{ size?: number; color?: string; className?: string }>
  hideInOfficeMode?: boolean
}

export interface PluginPageContribution {
  id: string
  title: string
  navLabel?: string
  mobileLabel?: string
  mobileHeaderTitle?: string
  mobileHeaderAriaLabel?: string
  icon?: ExtensionPageIcon
  mobileIcon?: ComponentType<{ size?: number; color?: string; className?: string }>
  path?: string
  routePaths?: string[]
  mobilePath?: string
  order?: number
  hideInOfficeMode?: boolean
  hasSecondaryPanel?: boolean
  showInSidebar?: boolean
  showInMobileApps?: boolean
  component: ExtensionPageComponent
  mobileComponent?: ExtensionPageComponent
  mobileLayout?: 'office' | 'module' | 'custom'
  command?: ExtensionPageCommandSpec | ExtensionPageCommandSpec[] | false
}

export interface PluginCommandContribution {
  id: string
  title: string
  subtitle?: string
  keywords?: string | string[]
  badge?: string
  category?: 'route' | 'action'
  icon?: ComponentType<{ size?: number; color?: string; className?: string }>
  hideInOfficeMode?: boolean
  order?: number
  run: () => void | Promise<void>
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

export interface PluginSlotContribution {
  settingsTitle?: string
  settingsButtonLabel?: string
  settingsComponent?: ComponentType<{ embedded?: boolean; onClose?: () => void }>
  statusBarComponent?: ComponentType<StatusBarSlotContext>
  gitMenuItems?: Array<{
    id: string
    groupLabel?: string
    label: string
    icon?: ComponentType<{ size?: number; color?: string }>
    iconColor?: string
    requiresWorkspace?: boolean
    onClick: (session: Session) => void
  }>
  welcomeMenuItems?: Array<{
    id: string
    label: string
    shortcut?: string
    icon?: ComponentType<{ size?: number; color?: string }>
    iconColor?: string
    requiresWorkspace?: boolean
    hideInOfficeMode?: boolean
    onClick: (session: Session) => void
  }>
  panels?: Record<
    string,
    {
      title: string
      badgeLabel?: string
      tabColor?: string
      mediaType?: string
      component: ComponentType<PluginPanelContext>
    }
  >
}

export interface PluginNotifyOptions {
  message: string
  title?: string
  color?: string
  autoClose?: number | boolean
}

export interface AstrorderHostRuntime {
  readonly apiVersion: 1
  readonly react: Record<string, unknown>
  readonly reactDom: Record<string, unknown>
  readonly reactDomClient: Record<string, unknown>
  readonly jsxRuntime: Record<string, unknown>
  readonly mantineCore: Record<string, unknown>
  readonly mantineNotifications: Record<string, unknown>
  readonly sdk: Record<string, unknown>
}

export interface AstrorderPluginApi {
  fetch(path: string, init?: RequestInit): Promise<Response>
  fetchExt<T = unknown>(subpath: string, init?: RequestInit): Promise<T>
  notify(opts: PluginNotifyOptions): void
  navigate(path: string): void
  openArtifact(artifact: ArtifactRef, viewerId?: string): void
  openPluginPanel(panelId: string, opts?: OpenPluginPanelOptions): void
  openDatabaseWorkbench(
    mediaType: string,
    viewerId?: string,
    sessionId?: string,
    agentId?: string,
    connectionId?: string,
  ): void
  openSettings(opts?: PluginOpenSettingsOptions): void
  quoteToComposer(session: ComposerQuoteTarget, text: string): void
  openExternal(url: string): void
  downloadFile(url: string, filename: string): Promise<void>
  getConfig<T = Record<string, unknown>>(): T
  onConfigChange(cb: (cfg: Record<string, unknown>) => void): Disposable
  usePluginConfig<T = unknown>(key: string, fallback: T): T
  getActiveSession(): Session | null
  getSessions(): Session[]
  useAppProfile(): AppProfile
  useCurrentSession(): Session | null
  useThemeMode(): ThemeMode
}

export interface AstrorderPluginContext {
  readonly pluginId: string
  readonly version: string
  readonly subscriptions: Disposable[]
  registerPage(spec: PluginPageContribution): Disposable
  registerViewer(spec: PluginViewerContribution): Disposable
  registerSlot(spec: PluginSlotContribution): Disposable
  registerCommand(spec: PluginCommandContribution): Disposable
  registerDatabaseTool(provider: DatabaseToolProvider): Disposable
  readonly api: AstrorderPluginApi
}

export interface AstrorderPluginModule {
  activate(ctx: AstrorderPluginContext): void | Promise<void>
  deactivate?(): void | Promise<void>
}

export declare function defineAstrorderPlugin<T extends AstrorderPluginModule>(mod: T): T
export declare function downloadFile(url: string, filename: string): Promise<void>
export declare function getActiveSession(): Session | null
export declare function getHostRuntime(): AstrorderHostRuntime | undefined
export declare function getSessions(): Session[]
export declare function navigate(path: string): void
export declare function notify(opts: PluginNotifyOptions): void
export declare function openArtifact(artifact: ArtifactRef, viewerId?: string): void
export declare function openDatabaseWorkbench(
  mediaType: string,
  viewerId: string,
  sessionId?: string,
  agentId?: string,
  connectionId?: string,
): void
export declare function openExternal(url: string): void
export declare function openPluginPanel(
  pluginId: string,
  panelId: string,
  opts?: OpenPluginPanelOptions,
): void
export declare function openSettings(opts?: PluginOpenSettingsOptions): void
export declare function quoteToComposer(session: ComposerQuoteTarget, text: string): void
export declare function useAppProfile(): AppProfile
export declare function useCurrentSession(): Session | null
export declare function usePluginConfig<T = unknown>(
  pluginId: string,
  key: string,
  fallback: T,
): T
export declare function useThemeMode(): ThemeMode
