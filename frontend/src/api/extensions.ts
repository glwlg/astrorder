import { ApiError, authenticatedApiFetch, request } from './client'

export type PluginRuntimeState = 'builtin' | 'ready' | 'stopped' | 'starting' | 'error'
export type PluginSource = 'builtin' | 'official' | 'external'
export type PluginBackendType = 'builtin' | 'process' | 'http' | 'none'
export type PluginFrontendMode = 'esm' | 'iframe' | 'builtin'

export interface FrontendSpec {
  mode?: PluginFrontendMode
  entry?: string
  styles?: string[]
  sdk_version?: string
}

export interface PageContribution {
  id: string
  title: string
  path?: string
  mobile_path?: string
  icon?: string
  category?: string
  badge_label?: string
  order?: number
  hide_in_office_mode?: boolean
}

export interface ViewerContribution {
  id: string
  title: string
  description?: string
  extensions?: string[]
  mime_types?: string[]
  badge_label?: string
  tab_color?: string
  category?: string
  icon?: string
  priority?: number
  hide_in_office_mode?: boolean
}

export interface BackendSpec {
  type: PluginBackendType
  command?: string[]
  args?: string[]
  env?: Record<string, string>
  health?: string
  url?: string
}

export interface SettingOption {
  label: string
  value: string
}

export interface SettingField {
  key: string
  label: string
  description?: string
  type: 'string' | 'secret' | 'boolean' | 'number' | 'select'
  required?: boolean
  placeholder?: string
  default_value?: unknown
  options?: SettingOption[]
  min?: number
  max?: number
  step?: number
}

export interface SettingsSpec {
  mode: 'none' | 'schema' | 'custom_ui'
  schema?: SettingField[]
  ui_entry?: string
  test_endpoint?: string
}

export interface SlotClickAction {
  open_panel?: string
  url?: string
  endpoint?: string
}

export interface UISlotContribution {
  slot: string
  id: string
  renderer?: string
  label?: string
  badge?: string
  icon?: string
  poll_endpoint?: string
  poll_interval_ms?: number
  requires_workspace?: boolean
  on_click?: SlotClickAction
}

export interface SidecarPanelContribution {
  id: string
  title: string
  badge_label?: string
  tab_color?: string
  media_type?: string
  ui_entry?: string
  icon?: string
}

export interface AgentCapabilityContribution {
  id: string
  cli_alias?: string[]
  summary?: string
  endpoint?: string
  input_schema?: Record<string, unknown>
  requires_caller?: boolean
}

export interface SignalProviderContribution {
  kind: string
  endpoint?: string
}

export interface ContributesSpec {
  ui_slots?: UISlotContribution[]
  sidecar_panels?: SidecarPanelContribution[]
  pages?: PageContribution[]
  viewers?: ViewerContribution[]
  agent_capabilities?: AgentCapabilityContribution[]
  agent_skills?: string[]
  signal_providers?: SignalProviderContribution[]
}

export interface DeclarativePillPayload {
  visible: boolean
  label?: string
  sublabel?: string
  tone?: string
  dot_color?: string
  pulsing?: boolean
  tooltip?: string
  icon?: string
  panel_params?: Record<string, unknown>
}

export interface PluginView {
  id: string
  name: string
  version: string
  category: string
  description?: string
  author?: string
  homepage?: string
  icon?: string
  source: PluginSource
  enabled: boolean
  permissions?: string[]
  granted_permissions?: string[]
  pending_permissions?: string[]
  permissions_granted?: boolean
  runtime_state: PluginRuntimeState
  runtime_error?: string
  backend_type: PluginBackendType
  frontend?: FrontendSpec
  content_digest?: string
  signature_verified?: boolean
  default_enabled?: boolean
  settings?: SettingsSpec
  contributes?: ContributesSpec
  ui_base_url?: string
  esm_base_url?: string
  api_base_url?: string
}

export interface ExtensionSecretState {
  set?: boolean
  masked?: string
}

export interface ExtensionConfigResponse {
  plugin_id?: string
  values: Record<string, unknown>
  secrets?: Record<string, string | ExtensionSecretState>
  masked_secrets?: Record<string, string>
  secrets_set?: Record<string, boolean>
}

export interface UpdateExtensionConfigPayload {
  values?: Record<string, unknown>
  secrets?: Record<string, string>
}

export interface SetExtensionEnabledResult {
  item?: PluginView
  requires_permission_grant?: boolean
  pending_permissions?: string[]
  detail?: string
}

function cleanSubpath(endpoint: string): string {
  return endpoint.replace(/^\/+/, '')
}

function buildExtensionUrl(pluginId: string, endpoint: string, query?: Record<string, string | undefined>): string {
  const clean = cleanSubpath(endpoint)
  const search = new URLSearchParams()
  if (query) {
    for (const [key, val] of Object.entries(query)) {
      if (val !== undefined && val !== '') {
        search.set(key, val)
      }
    }
  }
  const qs = search.toString()
  const base = `/ext/${encodeURIComponent(pluginId)}${clean ? `/${clean}` : ''}`
  return qs ? `${base}?${qs}` : base
}

async function parseJsonSafe(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined
  try {
    const raw = await response.text()
    if (!raw) return undefined
    try {
      return JSON.parse(raw) as unknown
    } catch {
      return raw
    }
  } catch {
    return undefined
  }
}

export const extensionsApi = {
  listExtensions: (): Promise<{ items: PluginView[] }> =>
    request<{ items: PluginView[] }>('/extensions'),

  installExtensionFromPath: (sourcePath: string): Promise<{ item: PluginView }> =>
    request<{ item: PluginView }>('/extensions/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source_path: sourcePath }),
    }),

  installExtensionArchive: async (file: File): Promise<{ item: PluginView }> => {
    const form = new FormData()
    const upload =
      typeof window !== 'undefined' && window.location.hostname === 'wails.localhost'
        ? new Blob([await file.arrayBuffer()], { type: file.type })
        : file
    form.append('file', upload, file.name)
    return request<{ item: PluginView }>('/extensions/install', {
      method: 'POST',
      body: form,
    })
  },

  reloadExtensions: (): Promise<{ items: PluginView[] }> =>
    request<{ items: PluginView[] }>('/extensions/reload', {
      method: 'POST',
    }),

  uninstallExtension: (id: string): Promise<{ ok: boolean; id: string }> =>
    request<{ ok: boolean; id: string }>(`/extensions/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),

  setExtensionEnabled: async (
    id: string,
    enabled: boolean,
    grantPermissions?: string[],
  ): Promise<SetExtensionEnabledResult> => {
    const payload: { enabled: boolean; grant_permissions?: string[] } = { enabled }
    if (grantPermissions && grantPermissions.length > 0) {
      payload.grant_permissions = grantPermissions
    }
    const response = await authenticatedApiFetch(`/api/v1/extensions/${encodeURIComponent(id)}/enable`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
    const parsed = (await parseJsonSafe(response)) as Record<string, unknown> | undefined
    if (response.status === 409) {
      const pending = Array.isArray(parsed?.pending_permissions)
        ? parsed.pending_permissions.filter((p): p is string => typeof p === 'string')
        : []
      return {
        requires_permission_grant: true,
        pending_permissions: pending,
        item: parsed?.item as PluginView | undefined,
        detail: typeof parsed?.detail === 'string' ? parsed.detail : '需要授予扩展声明的权限后才能启用',
      }
    }
    if (!response.ok) {
      const detail =
        parsed && typeof parsed.detail === 'string'
          ? parsed.detail
          : `请求失败（${response.status}）`
      throw new ApiError(response.status, detail)
    }
    return (parsed as SetExtensionEnabledResult) || {}
  },

  restartExtension: (id: string): Promise<{ item: PluginView }> =>
    request<{ item: PluginView }>(`/extensions/${encodeURIComponent(id)}/restart`, {
      method: 'POST',
    }),

  getExtensionConfig: (id: string): Promise<ExtensionConfigResponse> =>
    request<ExtensionConfigResponse>(`/extensions/${encodeURIComponent(id)}/config`),

  updateExtensionConfig: (
    id: string,
    payload: UpdateExtensionConfigPayload,
  ): Promise<ExtensionConfigResponse> =>
    request<ExtensionConfigResponse>(`/extensions/${encodeURIComponent(id)}/config`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }),

  fetchExtensionEndpoint: <T = unknown>(
    pluginId: string,
    endpoint: string,
    query?: Record<string, string | undefined>,
    init?: RequestInit,
  ): Promise<T> => request<T>(buildExtensionUrl(pluginId, endpoint, query), init),

  postExtensionEndpoint: <T = unknown>(
    pluginId: string,
    endpoint: string,
    body?: unknown,
    query?: Record<string, string | undefined>,
  ): Promise<T> =>
    request<T>(buildExtensionUrl(pluginId, endpoint, query), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body ?? {}),
    }),
}

export const listExtensions = () => extensionsApi.listExtensions()
export const installExtensionFromPath = (sourcePath: string) => extensionsApi.installExtensionFromPath(sourcePath)
export const installExtensionArchive = (file: File) => extensionsApi.installExtensionArchive(file)
export const reloadExtensions = () => extensionsApi.reloadExtensions()
export const uninstallExtension = (id: string) => extensionsApi.uninstallExtension(id)
export const setExtensionEnabled = (id: string, enabled: boolean, grantPermissions?: string[]) =>
  extensionsApi.setExtensionEnabled(id, enabled, grantPermissions)
export const restartExtension = (id: string) => extensionsApi.restartExtension(id)
export const getExtensionConfig = (id: string) => extensionsApi.getExtensionConfig(id)
export const updateExtensionConfig = (id: string, payload: UpdateExtensionConfigPayload) =>
  extensionsApi.updateExtensionConfig(id, payload)
export const fetchExtensionEndpoint = <T = unknown>(
  pluginId: string,
  endpoint: string,
  query?: Record<string, string | undefined>,
  init?: RequestInit,
) => extensionsApi.fetchExtensionEndpoint<T>(pluginId, endpoint, query, init)
export const postExtensionEndpoint = <T = unknown>(
  pluginId: string,
  endpoint: string,
  body?: unknown,
  query?: Record<string, string | undefined>,
) => extensionsApi.postExtensionEndpoint<T>(pluginId, endpoint, body, query)
